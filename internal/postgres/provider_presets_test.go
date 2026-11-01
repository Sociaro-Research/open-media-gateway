package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

func postgresPresetContext(f *providerResourceFixture, actor, revision string) context.Context {
	ctx := identity.WithUser(f.ctx, identity.User{ID: actor})
	ctx = identity.WithOrganizationRole(ctx, identity.OwnerID(f.ctx), "member")
	return core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: f.job.ConnectionID, OwnerID: identity.ProviderOwnerID(ctx), Revision: revision})
}
func postgresPresetBinding(ctx context.Context) providerresources.Binding {
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	return providerresources.Binding{ID: uuid.NewString(), OwnerID: identity.OwnerID(ctx), ActorID: identity.ActorID(ctx), ProviderOwnerID: identity.ProviderOwnerID(ctx), ConnectionID: scope.ID, ProviderID: "actual-kling-connection", Revision: scope.Revision, RemoteID: "official-large-id-900719925474099312345", Kind: "element", Subtype: "multi_image", Name: "Official fixed element", Status: providerresources.Succeeded, IsPreset: true}
}
func TestProviderPresetConcurrentImportSharesOnlyOfficialBindings(t *testing.T) {
	f := newProviderResourceFixture(t)
	actorA := identity.ActorID(f.ctx)
	a := postgresPresetContext(f, actorA, f.job.Revision)
	b := postgresPresetContext(f, uuid.NewString(), f.job.Revision)
	const count = 8
	ids := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := a
			if i%2 != 0 {
				ctx = b
			}
			saved, err := f.repo.ImportPreset(ctx, postgresPresetBinding(ctx))
			if err != nil {
				errs <- err
				return
			}
			ids <- saved.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	id := ""
	for v := range ids {
		if id != "" && id != v {
			t.Fatal("duplicate imported binding")
		}
		id = v
	}
	if id == "" {
		t.Fatal("missing binding")
	}
	for _, ctx := range []context.Context{a, b} {
		got, err := f.repo.GetBinding(ctx, id)
		if err != nil || !got.IsPreset {
			t.Fatal(got, err)
		}
		tx, err := f.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = resourceBindingTx(ctx, tx, id, " FOR SHARE")
		tx.Rollback()
		if err != nil {
			t.Fatal("shared preset transaction lookup denied", err)
		}
		service := providerresources.New(f.repo, func(context.Context, string) (core.ProviderConnectionScope, error) {
			return core.ProviderConnectionScope{ID: f.job.ConnectionID, OwnerID: identity.ProviderOwnerID(ctx), Revision: f.job.Revision}, nil
		})
		refs, err := service.ResolveResourceReferences(ctx, f.job.ProviderID, []core.ResourceReference{{BindingID: id, ReferenceID: "element_1"}})
		if err != nil || len(refs) != 1 || !refs[0].IsPreset {
			t.Fatal(refs, err)
		}
	}
	custom := f.complete(t, "custom-private")
	if _, err := f.repo.GetBinding(b, custom.ID); !errors.Is(err, providerresources.ErrNotFound) {
		t.Fatal("custom binding shared", err)
	}
	rows, err := f.repo.ListBindings(b, f.job.ProviderID, 100, 0)
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatal(rows, err)
	}
	var jid string
	if err = f.db.QueryRow(`SELECT id::text FROM provider_resource_jobs WHERE owner_id=$1 LIMIT 1`, identity.OwnerID(f.ctx)).Scan(&jid); err != nil {
		t.Fatal(err)
	}
	if _, err = f.repo.GetJob(b, jid); !errors.Is(err, providerresources.ErrNotFound) {
		t.Fatal("custom job shared", err)
	}
	if _, _, err = f.repo.BeginDelete(a, id, "forbidden-preset-delete"); !errors.Is(err, providerresources.ErrConflict) {
		t.Fatal("preset mutated", err)
	}
	var jobCount int
	if err = f.db.QueryRow(`SELECT count(*) FROM provider_resource_jobs WHERE owner_id=$1`, identity.OwnerID(f.ctx)).Scan(&jobCount); err != nil || jobCount != 1 {
		t.Fatal("import/delete produced job", jobCount, err)
	}
	other := identity.WithOwnerID(context.Background(), uuid.NewString())
	other = identity.WithResourceOwners(other, identity.ProviderOwnerID(f.ctx), "")
	if _, err = f.repo.GetBinding(other, id); !errors.Is(err, providerresources.ErrNotFound) {
		t.Fatal("foreign owner saw preset", err)
	}
	rotated := postgresPresetContext(f, actorA, "other-revision")
	again, err := f.repo.ImportPreset(rotated, postgresPresetBinding(rotated))
	if err != nil || again.ID == id {
		t.Fatal("revision identity conflated", again, err)
	}
	same := postgresPresetBinding(a)
	same.Name = "Caller rename"
	if _, err = f.repo.ImportPreset(a, same); !errors.Is(err, providerresources.ErrConflict) {
		t.Fatal("official label changed", err)
	}
}
func TestProviderPresetImportRejectsCustomIdentityCollisionAndForgedScope(t *testing.T) {
	f := newProviderResourceFixture(t)
	custom := f.complete(t, "collision")
	ctx := postgresPresetContext(f, identity.ActorID(f.ctx), f.job.Revision)
	binding := postgresPresetBinding(ctx)
	binding.RemoteID = custom.RemoteID
	if _, err := f.repo.ImportPreset(ctx, binding); !errors.Is(err, providerresources.ErrConflict) {
		t.Fatal("custom converted to preset", err)
	}
	saved, err := f.repo.GetBinding(f.ctx, custom.ID)
	if err != nil || saved.IsPreset || saved.Name != custom.Name {
		t.Fatal("custom metadata mutated", saved, err)
	}
	binding = postgresPresetBinding(ctx)
	binding.ConnectionID = "another-connection"
	if _, err = f.repo.ImportPreset(ctx, binding); !errors.Is(err, providerresources.ErrInvalid) {
		t.Fatal("foreign connection accepted", err)
	}
	binding = postgresPresetBinding(ctx)
	binding.Kind = "element"
	binding.Subtype = "video_character"
	if _, err = f.repo.ImportPreset(ctx, binding); !errors.Is(err, providerresources.ErrInvalid) {
		t.Fatal("unknown video evidence accepted", err)
	}
}

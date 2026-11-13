package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

// A prepared request is not yet admitted. These changes occur after inspection
// and before the actual production task/outbox transaction; no storage is used.
func TestGenerationAdmissionRechecksPreparedReference(t *testing.T) {
	for _, change := range []string{"deleted", "deleting", "checksum", "size"} {
		t.Run(change, func(t *testing.T) {
			db := auditDatabase(t, true)
			owner := uuid.NewString()
			ctx := identity.WithOwnerID(context.Background(), owner)
			ref := references.Image{ID: uuid.NewString(), Name: "Synthetic", ContentType: "image/png", SizeBytes: 12, StorageKey: owner + "/references/fixture/image.png", SHA256: strings.Repeat("a", 64), CreatedAt: time.Now()}
			if e := NewReferenceRepository(db).Insert(ctx, ref); e != nil {
				t.Fatal(e)
			}
			raw := "https://fixture.invalid/reference/" + ref.ID
			task := core.Task{ID: uuid.NewString(), OwnerID: owner, ActorID: owner, ProviderOwnerID: owner, StorageOwnerID: owner, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Synthetic admission", Status: core.TaskQueued, CreatedAt: time.Now(), UpdatedAt: time.Now(), Options: core.MediaOptions{ReferenceSources: []core.ReferenceBinding{{OwnerID: owner, ReferenceID: ref.ID, StorageOwnerID: owner, StorageKey: ref.StorageKey, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes}}, ImageURLs: []string{raw}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{raw: {ReferenceID: ref.ID, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes, Type: "image"}}}}
			var sql string
			switch change {
			case "deleted":
				sql = `DELETE FROM reference_images WHERE id=$1`
			case "deleting":
				sql = `UPDATE reference_images SET deleting=true WHERE id=$1`
			case "checksum":
				sql = `UPDATE reference_images SET content_sha256=repeat('b',64) WHERE id=$1`
			case "size":
				sql = `UPDATE reference_images SET size_bytes=13 WHERE id=$1`
			}
			if _, e := db.Exec(sql, ref.ID); e != nil {
				t.Fatal(e)
			}
			_, _, e := NewTaskRepository(db).CreateOrGet(ctx, task, "fresh-"+change, "synthetic-hash")
			if e == nil {
				t.Error("prepared reference changed before admission but request was accepted")
			}
			for _, table := range []string{"tasks", "task_queue_outbox"} {
				var n int
				if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
					t.Fatal(e)
				}
				if n != 0 {
					t.Errorf("rejected new request left %s=%d", table, n)
				}
			}
		})
	}
}

func TestGenerationAdmissionRechecksDisabledProvider(t *testing.T) {
	db := auditDatabase(t, true)
	owner := uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), owner)
	if _, e := db.Exec(`INSERT INTO provider_settings(owner_id,provider_id,enabled,credential_source) VALUES($1,'mock',false,'none')`, owner); e != nil {
		t.Fatal(e)
	}
	task := core.Task{ID: uuid.NewString(), OwnerID: owner, ActorID: owner, ProviderOwnerID: owner, StorageOwnerID: owner, Provider: "mock", ProviderIdentity: "prepared-revision", Model: "mock-video-v1", MediaType: "video", Prompt: "Synthetic admission", Status: core.TaskQueued, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, _, e := NewTaskRepository(db).CreateOrGet(ctx, task, "disabled-after-prepare", "synthetic"); e == nil {
		t.Error("disabled provider accepted after preparation")
	}
	for _, table := range []string{"tasks", "task_queue_outbox"} {
		var n int
		if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil {
			t.Fatal(e)
		}
		if n != 0 {
			t.Errorf("rejected new request left %s=%d", table, n)
		}
	}
}

func admissionTask(ctx context.Context) core.Task {
	return core.Task{ID: uuid.NewString(), OwnerID: identity.OwnerID(ctx), ActorID: identity.ActorID(ctx), OrganizationID: identity.OrganizationFromContext(ctx).ID, APIKeyID: identity.APIKeyID(ctx), ProviderOwnerID: identity.ProviderOwnerID(ctx), StorageOwnerID: identity.StorageOwnerID(ctx), Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Synthetic admission", Status: core.TaskQueued, CreatedAt: time.Now(), UpdatedAt: time.Now()}
}
func assertNoAdmission(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"tasks", "task_queue_outbox"} {
		var n int
		if e := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); e != nil || n != 0 {
			t.Fatal("rejected admission persisted", table, n, e)
		}
	}
}

func TestGenerationAdmissionProviderAndCatalogRevisionFence(t *testing.T) {
	for _, change := range []string{"provider-version", "provider-archived", "catalog-model", "catalog-ABA", "catalog-first-row"} {
		t.Run(change, func(t *testing.T) {
			db := auditDatabase(t, true)
			ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
			task := admissionTask(ctx)
			if _, e := db.Exec(`INSERT INTO provider_settings(owner_id,provider_id,enabled,credential_source) VALUES($1,'mock',true,'none')`, identity.ProviderOwnerID(ctx)); e != nil {
				t.Fatal(e)
			}
			if e := db.QueryRow(`SELECT version FROM provider_settings WHERE owner_id=$1 AND provider_id='mock'`, identity.ProviderOwnerID(ctx)).Scan(&task.ProviderSettingsVersion); e != nil {
				t.Fatal(e)
			}
			before := catalogsync.State{Version: 1, Models: map[string]catalogsync.Entry{}}
			after := before
			switch change {
			case "provider-version":
				if _, e := db.Exec(`UPDATE provider_settings SET version=version+1`); e != nil {
					t.Fatal(e)
				}
			case "provider-archived":
				if _, e := db.Exec(`UPDATE provider_settings SET archived=true`); e != nil {
					t.Fatal(e)
				}
			case "catalog-model":
				after.Models = map[string]catalogsync.Entry{"new": {ID: "new"}}
				after.Version = 2
			case "catalog-ABA":
				after.Version = 3
			case "catalog-first-row":
				before.Version = 0
			}
			if strings.HasPrefix(change, "catalog-") {
				task.ProviderCatalogFingerprint = catalogsync.AdmissionFingerprint(before)
				raw, _ := json.Marshal(after)
				if _, e := db.Exec(`INSERT INTO catalog_sync_state(provider,payload) VALUES('mock',$1)`, raw); e != nil {
					t.Fatal(e)
				}
			}
			if _, _, e := NewTaskRepository(db).CreateOrGet(ctx, task, change, "fixture"); e == nil {
				t.Fatal("changed prepared contract accepted")
			}
			assertNoAdmission(t, db)
		})
	}
}

func admissionKeyFixture(t *testing.T) (*sql.DB, context.Context, apikey.Key) {
	t.Helper()
	db := auditDatabase(t, true)
	ctx := context.Background()
	u, e := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Admission fixture", "")
	if e != nil {
		t.Fatal(e)
	}
	_ = budgetOrganization(t, db, u.ID, 1000000)
	key, secret, e := apikey.Generate(apikey.CreateInput{Name: "Synthetic", OrganizationID: u.ID, ResourceMode: "shared", StorageMode: "shared"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = NewAPIKeyRepository(db).Create(ctx, u.ID, key, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	ctx = identity.WithResourceOwners(identity.WithOrganization(identity.WithAPIKeyID(identity.WithUser(ctx, u), key.ID), u.ID, true), identity.SharedOwnerID, identity.SharedOwnerID)
	return db, ctx, key
}
func TestGenerationAdmissionCurrentKeyAndMemberAuthority(t *testing.T) {
	for _, change := range []string{"valid", "revoked", "expired", "scope", "member-removed", "member-readonly", "sales-role", "provider-mode"} {
		t.Run(change, func(t *testing.T) {
			db, ctx, key := admissionKeyFixture(t)
			query := ""
			switch change {
			case "revoked":
				query = `UPDATE user_api_keys SET revoked_at=now() WHERE id=$1`
			case "expired":
				query = `UPDATE user_api_keys SET expires_at=now()-interval '1 minute' WHERE id=$1`
			case "scope":
				query = `UPDATE user_api_keys SET scopes='["history:read"]'::jsonb WHERE id=$1`
			case "provider-mode":
				query = `UPDATE user_api_keys SET resource_mode='personal' WHERE id=$1`
			case "member-removed":
				_, e := db.Exec(`UPDATE organization_members SET removed_at=now() WHERE user_id=$1`, identity.ActorID(ctx))
				if e != nil {
					t.Fatal(e)
				}
			case "member-readonly":
				_, e := db.Exec(`UPDATE organization_members SET role='content_officer' WHERE user_id=$1`, identity.ActorID(ctx))
				if e != nil {
					t.Fatal(e)
				}
			case "sales-role":
				_, e := db.Exec(`UPDATE users SET is_client_manager=true WHERE id=$1`, identity.ActorID(ctx))
				if e != nil {
					t.Fatal(e)
				}
			}
			if query != "" {
				if _, e := db.Exec(query, key.ID); e != nil {
					t.Fatal(e)
				}
			}
			_, _, e := NewTaskRepository(db).CreateOrGet(ctx, admissionTask(ctx), "new", "fixture")
			if change == "valid" {
				if e != nil {
					t.Fatal(e)
				}
				return
			}
			if !errors.Is(e, core.ErrKeyUnavailable) {
				t.Fatal("changed authority accepted", e)
			}
			assertNoAdmission(t, db)
		})
	}
}
func TestGenerationAdmissionWaitsForKeyRevocationCommit(t *testing.T) {
	db, ctx, key := admissionKeyFixture(t)
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`UPDATE user_api_keys SET revoked_at=now() WHERE id=$1`, key.ID); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, _, e := NewTaskRepository(db).CreateOrGet(ctx, admissionTask(ctx), "blocked", "fixture")
		done <- e
	}()
	select {
	case e := <-done:
		t.Fatal("admission passed uncommitted revocation", e)
	case <-time.After(100 * time.Millisecond):
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, core.ErrKeyUnavailable) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admission did not resume")
	}
	assertNoAdmission(t, db)
}
func TestGenerationAdmissionExactRecoveryPreservesAcceptedSnapshot(t *testing.T) {
	db, ctx, key := admissionKeyFixture(t)
	repo := NewTaskRepository(db)
	task := admissionTask(ctx)
	task.AdmissionIntent = "server-original-intent"
	saved, _, e := repo.CreateOrGet(ctx, task, "accepted", "normalized")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO provider_settings(owner_id,provider_id,enabled,credential_source) VALUES($1,'mock',false,'none')`, identity.ProviderOwnerID(ctx)); e != nil {
		t.Fatal(e)
	}
	got, found, e := repo.RecoverAcceptedIntent(ctx, "accepted", task.AdmissionIntent)
	if e != nil || !found || got.ID != saved.ID {
		t.Fatal("accepted snapshot re-admitted", e, found)
	}
	if _, found, e = repo.RecoverAcceptedIntent(ctx, "accepted", "changed"); e != nil || found {
		t.Fatal("different payload recovered", e, found)
	}
	if _, e = db.Exec(`UPDATE user_api_keys SET revoked_at=now() WHERE id=$1`, key.ID); e != nil {
		t.Fatal(e)
	}
	if _, found, e = repo.RecoverAcceptedIntent(ctx, "accepted", task.AdmissionIntent); !errors.Is(e, core.ErrKeyUnavailable) || found {
		t.Fatal("revoked recipient recovered", e, found)
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}

func TestGenerationAdmissionEstimateProjectScopeAndAcceptedRecovery(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTaskRepository(db)
	q := &quantityQueue{}
	svc := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": mock.New(time.Millisecond, "")}, map[string]string{"mock": "mock-video-v1"}, "mock")
	svc.EnableAccounting()
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	project, e := repo.CreateProject(ctx, "Synthetic project")
	if e != nil {
		t.Fatal(e)
	}
	input := core.CreateMediaInput{Prompt: "Original request", ProjectID: project.ID}
	if _, e = svc.Estimate(ctx, input); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Estimate(identity.WithOwnerID(context.Background(), uuid.NewString()), input); !errors.Is(e, core.ErrProjectInvalid) {
		t.Fatal("foreign estimate", e)
	}
	assertNoAdmission(t, db)
	if q.enqueued != 0 {
		t.Fatal("estimate queued work")
	}
	accepted, e := svc.CreateMediaRequest(ctx, input, "original")
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.DeleteProject(ctx, project.ID, project.Version); e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Estimate(ctx, input); !errors.Is(e, core.ErrProjectInvalid) {
		t.Fatal("deleted project estimate", e)
	}
	if _, e = svc.CreateMediaRequest(ctx, input, "new"); !errors.Is(e, core.ErrProjectInvalid) || e.Error() != core.ErrProjectInvalid.Error() {
		t.Fatal("new request accepted deleted project or exposed internal error context", e)
	}
	recovered, e := svc.CreateMediaRequest(ctx, input, "original")
	if e != nil || recovered.ID != accepted.ID {
		t.Fatal("accepted recovery lost after project deletion", e)
	}
	if recovered.ProjectID != "" {
		t.Fatal("recovery restored deleted project")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM tasks`).Scan(&n)
	if n != 1 {
		t.Fatal(n)
	}
}
func TestGenerationAdmissionReferenceTombstoneCommitOrdersAcceptance(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	ref := references.Image{ID: uuid.NewString(), Name: "Temporary metadata", StorageKey: "fixture/immutable", SHA256: "a", SizeBytes: 1, ContentType: "image/png", CreatedAt: time.Now()}
	if e := NewReferenceRepository(db).Insert(ctx, ref); e != nil {
		t.Fatal(e)
	}
	task := admissionTask(ctx)
	task.Options.ReferenceSources = []core.ReferenceBinding{{OwnerID: identity.OwnerID(ctx), ReferenceID: ref.ID, StorageOwnerID: identity.OwnerID(ctx), StorageKey: ref.StorageKey, SHA256: ref.SHA256, SizeBytes: ref.SizeBytes}}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if e = sourceAssetLock(ctx, tx, identity.OwnerID(ctx), ref.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(`UPDATE reference_images SET deleting=true WHERE id=$1`, ref.ID); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, _, e := NewTaskRepository(db).CreateOrGet(ctx, task, "changed-reference", "fixture")
		done <- e
	}()
	select {
	case e := <-done:
		t.Fatal("uncommitted source change ignored", e)
	case <-time.After(100 * time.Millisecond):
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if !errors.Is(e, core.ErrOptionsInvalid) {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("source admission did not resume")
	}
	assertNoAdmission(t, db)
}

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerassets"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"testing"
	"time"
)

func TestCompanyMutationDeletingSourceCannotBeReused(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewReferenceRepository(db)
	owner := uuid.NewString()
	ctx := identity.WithOwnerID(context.Background(), owner)
	item := references.Image{ID: uuid.NewString(), Name: "legacy", ContentType: "image/png", StorageKey: owner + "/references/original/file.png", SizeBytes: 512, CreatedAt: time.Now()}
	if e := repo.Insert(ctx, item); e != nil {
		t.Fatal(e)
	}
	if e := repo.DeleteWithGuard(ctx, item.ID, func() error { return errors.New("cleanup pending") }); e == nil {
		t.Fatal("fixture expected pending cleanup")
	}
	if e := repo.SetHash(ctx, item.ID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatal("deleting hash accepted", e)
	}
	items, e := repo.UnhashedBySize(ctx, item.SizeBytes)
	if e != nil || len(items) != 0 {
		t.Fatal("deleting unhashed candidate returned", e)
	}
	assets := NewProviderAssetRepository(db)
	_, created, e := assets.Reserve(ctx, providerassets.Record{ID: uuid.NewString(), Provider: "fixture", Scope: "connection", SourceID: item.ID, Asset: providerassets.Asset{Status: "Creating"}})
	if !errors.Is(e, providerassets.ErrNotFound) || created {
		t.Fatal("deleting source newly reserved", e)
	}
}

type deletionDuringOpen struct {
	*directReferenceStore
	once   bool
	remove func()
}

func (s *deletionDuringOpen) Open(ctx context.Context, key string) (storage.Object, error) {
	o, e := s.directReferenceStore.Open(ctx, key)
	if e == nil && !s.once {
		s.once = true
		s.remove()
	}
	return o, e
}
func TestCompanyMutationDirectDedupRechecksSourceAtCommit(t *testing.T) {
	db, library, store, ctx := uploadFixture(t)
	input := uploadInput("dedup-delete.png")
	plan, e := library.BeginUpload(ctx, input)
	if e != nil {
		t.Fatal(e)
	}
	source, e := library.FinishUpload(ctx, plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	repo := NewReferenceRepository(db)
	wrapped := &deletionDuringOpen{directReferenceStore: store, remove: func() {
		if e := repo.DeleteWithGuard(ctx, source.ID, func() error { return errors.New("keep deleting custody") }); e == nil {
			t.Fatal("expected pending cleanup")
		}
	}}
	raced := references.New(repo, wrapped)
	if _, e = raced.BeginUpload(ctx, input); !errors.Is(e, storage.ErrObjectNotFound) {
		t.Fatal("deleting duplicate committed", e)
	}
	rows, e := db.Query(`SELECT state FROM reference_uploads WHERE owner_id=$1 AND id<>$2`, identity.OwnerID(ctx), plan.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var raw []byte
		if e = rows.Scan(&raw); e != nil {
			t.Fatal(e)
		}
		var s references.UploadSession
		if e = json.Unmarshal(raw, &s); e != nil || s.ResultID != "" {
			t.Fatal("late duplicate result persisted", e)
		}
		count++
	}
	if count != 1 {
		t.Fatal("real second upload custody absent", count)
	}
}
func TestCompanyMutationAdmissionMigrationPreservesPopulatedCustody(t *testing.T) {
	db := auditDatabase(t, true)
	owner, actor, grant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	id := uuid.NewString()
	if _, e := db.Exec(`INSERT INTO provider_asset_group_admissions(id,owner_id,actor_id,provider_owner_id,provider,library_scope,name,grant_id) VALUES($1,$2,$3,$2,'fixture','old-connection','pending',$4)`, id, owner, actor, grant); e != nil {
		t.Fatal(e)
	}
	repo := NewReferenceRepository(db)
	ctx := identity.WithOwnerID(context.Background(), owner)
	item := references.Image{ID: uuid.NewString(), Name: "pending", ContentType: "image/png", StorageKey: "s3:" + uuid.NewString() + "/" + owner + "/references/fixed/file.png", SizeBytes: 12, CreatedAt: time.Now()}
	if e := repo.Insert(ctx, item); e != nil {
		t.Fatal(e)
	}
	if e := repo.DeleteWithGuard(ctx, item.ID, func() error { return errors.New("pending cleanup") }); e == nil {
		t.Fatal("expected pending")
	}
	projection := newResourceLegacyProjection(t, db, "provider_asset_group_admissions", "reference_deletion_admissions", "reference_images")
	before := projection.snapshot(t, db, "before92rollback")
	if e := goose.DownTo(db, "migrations", 91); e != nil {
		t.Fatal(e)
	}
	if before != projection.snapshot(t, db, "after92rollback") {
		t.Fatal("rollback erased custody")
	}
	if e := Migrate(db); e != nil {
		t.Fatal(e)
	}
	if before != projection.snapshot(t, db, "after92reupgrade") {
		t.Fatal("reupgrade changed custody")
	}
	if _, e := db.Exec(`UPDATE reference_deletion_admissions SET storage_key='different'`); e == nil {
		t.Fatal("cleanup destination mutable")
	}
	if _, e := db.Exec(`UPDATE provider_asset_group_admissions SET library_scope='new-connection'`); e == nil {
		t.Fatal("group destination mutable")
	}
}

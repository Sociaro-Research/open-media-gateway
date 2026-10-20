package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/references"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/google/uuid"
)

type directReferenceStore struct {
	accounting   *StorageSettingsRepository
	entered      chan struct{}
	release      chan struct{}
	completed    atomic.Int32
	aborted      atomic.Int32
	failComplete atomic.Bool
}

func (s *directReferenceStore) PlanUpload(ctx context.Context, id, kind, digest string, size int64) (*storage.PendingUpload, error) {
	return &storage.PendingUpload{Key: "s3:" + uuid.NewString() + "/" + identity.OwnerID(ctx) + "/references/" + id + "/file.png", StorageOwner: identity.SharedOwnerID, Size: size, ContentType: kind, SHA256: digest}, nil
}
func (s *directReferenceStore) StartUpload(_ context.Context, p *storage.PendingUpload) (storage.UploadTarget, error) {
	p.UploadID = "fixture"
	return storage.UploadTarget{URL: "https://storage.example/upload"}, nil
}
func (s *directReferenceStore) CompleteUpload(ctx context.Context, p storage.PendingUpload) (core.StoredMedia, error) {
	if s.failComplete.Load() {
		return core.StoredMedia{}, errors.New("temporary S3 metadata failure")
	}
	if s.entered != nil {
		select {
		case s.entered <- struct{}{}:
		case <-ctx.Done():
			return core.StoredMedia{}, ctx.Err()
		}
		select {
		case <-s.release:
		case <-ctx.Done():
			return core.StoredMedia{}, ctx.Err()
		}
	}
	s.completed.Add(1)
	if err := s.accounting.RecordObject(ctx, p.Key, "", p.Size); err != nil {
		return core.StoredMedia{}, err
	}
	return core.StoredMedia{StorageKey: p.Key, SizeBytes: p.Size, SHA256: p.SHA256, ContentType: p.ContentType}, nil
}
func (s *directReferenceStore) AbortUpload(ctx context.Context, p storage.PendingUpload, remove bool) error {
	s.aborted.Add(1)
	if remove {
		return s.accounting.RemoveObject(ctx, p.Key)
	}
	return nil
}
func (*directReferenceStore) StoreReference(context.Context, string, []byte, string) (core.StoredMedia, error) {
	panic("must not proxy bytes")
}
func (*directReferenceStore) DeleteReference(context.Context, string) error { return nil }
func (*directReferenceStore) Open(context.Context, string) (storage.Object, error) {
	panic("must not download bytes")
}
func uploadFixture(t *testing.T) (*sql.DB, *references.Library, *directReferenceStore, context.Context) {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"reference_uploads", "reference_images", "stored_objects"} {
			_, _ = db.ExecContext(context.Background(), "DELETE FROM "+table+" WHERE owner_id=$1", identity.OwnerID(ctx))
		}
		db.Close()
	})
	store := &directReferenceStore{accounting: NewStorageSettingsRepository(db)}
	return db, references.New(NewReferenceRepository(db), store), store, ctx
}
func uploadInput(name string) references.UploadInput {
	sum := sha256.Sum256([]byte(name))
	return references.UploadInput{Name: name, ContentType: "image/png", SizeBytes: 512, SHA256: hex.EncodeToString(sum[:])}
}
func TestDirectUploadPersistenceRetryIsolationAndCleanup(t *testing.T) {
	db, library, store, ctx := uploadFixture(t)
	// This catches nested pool acquisition during atomic object accounting.
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	plan, err := library.BeginUpload(ctx, uploadInput("first.png"))
	if err != nil {
		t.Fatal(err)
	}
	other := identity.WithOwnerID(ctx, uuid.NewString())
	if _, err = library.FinishUpload(other, plan.ID); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatal("other owner accessed upload", err)
	}
	store.failComplete.Store(true)
	if _, err = library.FinishUpload(ctx, plan.ID); err == nil {
		t.Fatal("failed upload accepted")
	}
	store.failComplete.Store(false)
	item, err := library.FinishUpload(ctx, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	again, err := library.FinishUpload(ctx, plan.ID)
	if err != nil || again.ID != item.ID || store.completed.Load() != 1 {
		t.Fatal("retry repeated storage operation", again, err)
	}
	duplicate, err := library.BeginUpload(ctx, uploadInput("first.png"))
	if err != nil || duplicate.Existing == nil || duplicate.Existing.ID != item.ID {
		t.Fatal("duplicate not reused", duplicate, err)
	}
	abandoned, err := library.BeginUpload(ctx, uploadInput("abandoned.png"))
	if err != nil {
		t.Fatal(err)
	}
	expireUploads(t, db, identity.OwnerID(ctx))
	if _, err = library.FinishUpload(ctx, abandoned.ID); !errors.Is(err, references.ErrUploadExpired) {
		t.Fatal("expired upload accepted", err)
	}
	if err = library.CleanupUploads(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = library.Get(ctx, item.ID); err != nil {
		t.Fatal("cleanup deleted saved reference", err)
	}
	var sessions int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM reference_uploads WHERE owner_id=$1`, identity.OwnerID(ctx)).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("sessions not reclaimed", sessions, err)
	}
}
func expireUploads(t *testing.T, db *sql.DB, owner string) {
	t.Helper()
	expired := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	_, err := db.Exec(`UPDATE reference_uploads SET expires_at=$2::text::timestamptz,state=jsonb_set(state,'{expires_at}',to_jsonb($2::text)) WHERE owner_id=$1`, owner, expired)
	if err != nil {
		t.Fatal(err)
	}
}
func TestTwentyDirectUploadsCompleteConcurrently(t *testing.T) {
	db, library, store, ctx := uploadFixture(t)
	db.SetMaxOpenConns(20)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	plans := make([]references.UploadPlan, 20)
	for i := range plans {
		var err error
		plans[i], err = library.BeginUpload(ctx, uploadInput(fmt.Sprintf("file-%d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	store.entered = make(chan struct{}, 20)
	store.release = make(chan struct{})
	errs := make(chan error, 20)
	for _, plan := range plans {
		go func(id string) { _, err := library.FinishUpload(ctx, id); errs <- err }(plan.ID)
	}
	for range plans {
		select {
		case <-store.entered:
		case <-ctx.Done():
			t.Fatal("uploads waited for each other instead of running concurrently")
		}
	}
	close(store.release)
	for range plans {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if store.completed.Load() != 20 {
		t.Fatal("missing completion")
	}
}
func TestConcurrentIdenticalUploadsDeduplicate(t *testing.T) {
	_, library, store, ctx := uploadFixture(t)
	first, err := library.BeginUpload(ctx, uploadInput("same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := library.BeginUpload(ctx, uploadInput("same"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var results [2]references.Image
	var failures [2]error
	for i, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(i int, id string) { defer wg.Done(); results[i], failures[i] = library.FinishUpload(ctx, id) }(i, id)
	}
	wg.Wait()
	if failures[0] != nil || failures[1] != nil || results[0].ID != results[1].ID {
		t.Fatal("duplicate race", results, failures)
	}
	if store.aborted.Load() != 1 {
		t.Fatal("duplicate object not removed")
	}
}

package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestSharedStorageLifecycleAndResultDeletion(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	user, e := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), "storage-usage@example.test", "Storage", "")
	if e != nil {
		t.Fatal(e)
	}
	var oldLimit *int64
	if e = db.QueryRow(`SELECT shared_storage_limit_bytes FROM system_settings`).Scan(&oldLimit); e != nil {
		t.Fatal(e)
	}
	defer func() {
		db.Exec(`UPDATE system_settings SET shared_storage_limit_bytes=$1`, oldLimit)
		db.Exec(`DELETE FROM stored_objects WHERE owner_id=$1`, user.ID)
		db.Exec(`DELETE FROM tasks WHERE owner_id=$1`, user.ID)
		db.Exec(`DELETE FROM users WHERE id=$1`, user.ID)
	}()
	if _, e = db.Exec(`UPDATE system_settings SET shared_storage_limit_bytes=10`); e != nil {
		t.Fatal(e)
	}
	personal := identity.WithUser(ctx, user)
	shared := identity.WithResourceOwners(personal, identity.SharedOwnerID, identity.SharedOwnerID)
	settings := NewStorageSettingsRepository(db)
	cipher, _ := providerconfig.NewCipher("test-accounting-cipher")
	manager, e := storage.NewManager(settings, cipher, t.TempDir(), http.DefaultClient, 1024)
	if e != nil {
		t.Fatal(e)
	}
	usage := func(want int64, exceeded bool) {
		t.Helper()
		u, e := manager.SharedUsage(shared)
		if e != nil || u.UsedBytes != want || u.Exceeded != exceeded || u.UnknownObjects != 0 {
			t.Fatalf("usage want %d/%t: %+v %v", want, exceeded, u, e)
		}
	}
	usage(0, false)
	ref, e := manager.StoreReference(shared, uuid.NewString(), []byte("1234567890"), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	usage(10, false)
	if _, e = manager.StoreReference(personal, uuid.NewString(), []byte("personal"), "image/png"); e != nil {
		t.Fatal(e)
	}
	usage(10, false)
	// The next admitted upload can cross the threshold; no incoming-size reservation.
	extra, e := manager.StoreReference(shared, uuid.NewString(), []byte("X"), "image/png")
	if e != nil {
		t.Fatal(e)
	}
	usage(11, true)
	repo := NewTaskRepository(db)
	task := core.Task{ID: uuid.NewString(), OwnerID: user.ID, ProviderOwnerID: identity.SharedOwnerID, StorageOwnerID: identity.SharedOwnerID, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Keep history", CreatedAt: time.Now(), UpdatedAt: time.Now(), Cost: &core.TaskCost{ExpectedUSD: 40000}}
	if _, _, e = repo.CreateOrGet(shared, task, task.ID, task.ID); e != nil {
		t.Fatal(e)
	}
	if e = repo.MarkRunning(shared, task.ID); e != nil {
		t.Fatal(e)
	}
	service := core.NewTaskService(repo, nil, "mock", "mock-video-v1")
	if e = service.DeleteResult(personal, task.ID, manager.DeleteResult); !errors.Is(e, core.ErrTaskActive) {
		t.Fatalf("active task deletion: %v", e)
	}
	source := "data:video/mp4;base64,c21hbGwtdmlkZW8="
	// Accepted generation results and last frames archive even though usage is already over.
	media, e := manager.Archive(shared, task.ID, source)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = manager.Archive(shared, task.ID, source); e != nil {
		t.Fatal(e)
	}
	usage(22, true) // overwrite does not double-count
	frame, e := manager.Archive(shared, task.ID+"/last-frame", source)
	if e != nil {
		t.Fatal(e)
	}
	usage(33, true)
	media.LastFrameStorageKey = frame.StorageKey
	media.SourceURL = "https://provider.example/result.mp4"
	if e = repo.MarkSucceeded(shared, task.ID, media, "/v1/generations/"+task.ID+"/last-frame"); e != nil {
		t.Fatal(e)
	}
	if e = service.DeleteResult(identity.WithOwnerID(ctx, uuid.NewString()), task.ID, manager.DeleteResult); !errors.Is(e, core.ErrTaskNotFound) {
		t.Fatalf("foreign deletion: %v", e)
	}
	calls := 0
	if e = service.DeleteResult(personal, task.ID, func(c context.Context, id, key string) error {
		calls++
		if calls == 2 {
			return errors.New("temporary storage failure")
		}
		return manager.DeleteResult(c, id, key)
	}); e == nil {
		t.Fatal("partial failure hidden")
	}
	usage(22, true)
	got, e := repo.Get(personal, task.ID)
	if e != nil || got.ResultDeletedAt != nil {
		t.Fatal("partial deletion marked complete")
	}
	if e = service.DeleteResult(personal, task.ID, manager.DeleteResult); e != nil {
		t.Fatal(e)
	}
	usage(11, true)
	if e = service.DeleteResult(personal, task.ID, manager.DeleteResult); e != nil {
		t.Fatal(e)
	}
	usage(11, true)
	got, e = repo.Get(personal, task.ID)
	if e != nil || got.ResultDeletedAt == nil || got.ResultURL != nil || got.Prompt != "Keep history" || got.Cost.ExpectedUSD != 40000 || got.ProviderResultURL == nil || *got.ProviderResultURL != media.SourceURL {
		t.Fatalf("metadata lost: %+v %v", got, e)
	}
	// An expired attempt can finish after the first deletion marked the task.
	// A later explicit delete must collect that object without replaying old keys.
	late, e := manager.Archive(shared, task.ID+"/attempts/after-delete", source)
	if e != nil {
		t.Fatal(e)
	}
	usage(22, true)
	if e = service.DeleteResult(personal, task.ID, manager.DeleteResult); e != nil {
		t.Fatal(e)
	}
	usage(11, true)
	if object, err := manager.Open(personal, late.StorageKey); !errors.Is(err, storage.ErrObjectNotFound) {
		if err == nil {
			object.Reader.Close()
		}
		t.Fatalf("late shared attempt survived repeat deletion: %v", err)
	}
	// Backfill unresolved legacy sizes through storage metadata.
	if _, e = db.Exec(`UPDATE stored_objects SET size_bytes=NULL WHERE owner_id=$1 AND storage_key=$2`, user.ID, ref.StorageKey); e != nil {
		t.Fatal(e)
	}
	if e = manager.ReconcileUsage(ctx); e != nil {
		t.Fatal(e)
	}
	usage(11, true)
	if e = manager.DeleteReference(personal, ref.StorageKey); e != nil {
		t.Fatal(e)
	}
	usage(1, false)
	if e = manager.DeleteReference(personal, extra.StorageKey); e != nil {
		t.Fatal(e)
	}
	usage(0, false)
}

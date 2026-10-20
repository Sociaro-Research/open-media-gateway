package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/google/uuid"
)

func TestStorageSettingsPersistenceAndArchivedFrame(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set GATEWAY_TEST_DATABASE_URL to an isolated test database")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	alice, bob := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tasks WHERE owner_id = $1`, alice)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM storage_settings WHERE owner_id = $1`, alice)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM storage_connections WHERE owner_id = $1`, alice)
	})
	aliceCtx := identity.WithOwnerID(ctx, alice)
	bobCtx := identity.WithOwnerID(ctx, bob)
	repo := NewStorageSettingsRepository(db)
	if _, err := repo.Get(aliceCtx); !errors.Is(err, storage.ErrSettingsNotFound) {
		t.Fatal(err)
	}
	record := storage.SettingsRecord{Driver: "s3", ConnectionID: uuid.NewString(), Ciphertext: []byte("encrypted-config")}
	if err := repo.Save(aliceCtx, record); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.Get(aliceCtx)
	if err != nil || saved.Driver != "s3" || saved.ConnectionID != record.ConnectionID || string(saved.Ciphertext) != "encrypted-config" {
		t.Fatalf("settings lost: %+v %v", saved, err)
	}
	if _, err := repo.Connection(bobCtx, record.ConnectionID); !errors.Is(err, storage.ErrSettingsNotFound) {
		t.Fatal("connection crossed owners")
	}
	record.Ciphertext = []byte("rotated-credentials")
	if err := repo.Save(aliceCtx, record); err != nil {
		t.Fatal(err)
	}
	saved, err = repo.Connection(aliceCtx, record.ConnectionID)
	if err != nil || string(saved.Ciphertext) != "rotated-credentials" {
		t.Fatal("rotation lost")
	}
	originalID := record.ConnectionID
	record.ConnectionID = uuid.NewString()
	record.Ciphertext = []byte("new-destination")
	if err := repo.Save(aliceCtx, record); err != nil {
		t.Fatal(err)
	}
	record.Driver = "local"
	if err := repo.Save(aliceCtx, record); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Connection(aliceCtx, originalID); err != nil {
		t.Fatal("history connection lost")
	}
	tasks := NewTaskRepository(db)
	task := core.Task{ID: uuid.NewString(), OwnerID: alice, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "Storage integration", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if _, _, err := tasks.CreateOrGet(aliceCtx, task, "storage-test", "storage-hash"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.MarkRunning(aliceCtx, task.ID); err != nil {
		t.Fatal(err)
	}
	media := core.StoredMedia{StorageKey: "s3:" + originalID + "/video.mp4", LastFrameStorageKey: "s3:" + originalID + "/last-frame/image.png", ContentType: "video/mp4", SizeBytes: 12, SHA256: "test-sha"}
	if err := tasks.MarkSucceeded(aliceCtx, task.ID, media, "/v1/generations/"+task.ID+"/last-frame"); err != nil {
		t.Fatal(err)
	}
	savedTask, err := tasks.Get(aliceCtx, task.ID)
	if err != nil || savedTask.LastFrameStorageKey == nil || *savedTask.LastFrameStorageKey != media.LastFrameStorageKey || *savedTask.StorageKey != media.StorageKey {
		t.Fatalf("artifact location lost: %+v %v", savedTask, err)
	}
	replay, created, err := tasks.CreateOrGet(aliceCtx, task, "storage-test", "storage-hash")
	if err != nil || created || replay.LastFrameStorageKey == nil || *replay.LastFrameStorageKey != media.LastFrameStorageKey {
		t.Fatal("idempotent replay lost artifact")
	}
}

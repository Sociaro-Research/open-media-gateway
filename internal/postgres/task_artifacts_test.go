package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func taskArtifactsDatabase82(t *testing.T) *sql.DB {
	t.Helper()
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 81); err != nil {
		t.Fatal(err)
	}
	return db
}
func taskArtifactsCreate(t *testing.T, db *sql.DB, ctx context.Context) core.Task {
	t.Helper()
	now := time.Now().UTC()
	task := core.Task{ID: uuid.NewString(), Status: core.TaskQueued, Provider: "mock", Model: "synthetic-artifact-video", MediaType: "video", Prompt: "Synthetic durable artifact contract", CreatedAt: now, UpdatedAt: now}
	got, created, err := NewTaskRepository(db).CreateOrGet(ctx, task, "", "synthetic-artifact-request")
	if err != nil || !created {
		t.Fatalf("create owner task: created=%t err=%v", created, err)
	}
	return got
}
func taskArtifactsMedia(id, attempt string) core.StoredMedia {
	artifacts := []core.StoredArtifact{
		{ID: "1", ProviderID: "9007199254740993", MediaType: "video", Format: "mp4", Duration: 12.5, GroupID: "product_video", StorageKey: id + "/attempts/" + attempt + "/primary.mp4", ContentType: "video/mp4", SizeBytes: 1024, SHA256: strings.Repeat("a", 64)},
		{ID: "2", ProviderID: "9007199254740994", MediaType: "image", Format: "png", GroupID: "accompanying_images", StorageKey: id + "/attempts/" + attempt + "/detail.png", ContentType: "image/png", SizeBytes: 2048, SHA256: strings.Repeat("b", 64)},
	}
	first := artifacts[0]
	return core.StoredMedia{Artifacts: artifacts, StorageKey: first.StorageKey, ContentType: first.ContentType, SizeBytes: first.SizeBytes, SHA256: first.SHA256, SourceURL: "https://cdn.example/synthetic-source.mp4"}
}
func taskArtifactsAssertStored(t *testing.T, db *sql.DB, ctx context.Context, id string, want core.StoredMedia) core.Task {
	t.Helper()
	repo := NewTaskRepository(db)
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != core.TaskSucceeded || !reflect.DeepEqual(got.StoredArtifacts, want.Artifacts) {
		t.Fatalf("durable artifact metadata: status=%s got=%+v want=%+v", got.Status, got.StoredArtifacts, want.Artifacts)
	}
	if got.StorageKey == nil || *got.StorageKey != want.StorageKey || got.ResultType == nil || *got.ResultType != want.ContentType || got.ResultSize == nil || *got.ResultSize != want.SizeBytes || got.ResultSHA256 == nil || *got.ResultSHA256 != want.SHA256 || got.ResultURL == nil || *got.ResultURL != "/v1/generations/"+id+"/content" {
		t.Fatalf("scalar primary compatibility lost: %+v", got)
	}
	keys, err := repo.StoredResultKeys(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	wantKeys := []string{want.Artifacts[0].StorageKey, want.Artifacts[1].StorageKey}
	slices.Sort(wantKeys)
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("artifact deletion discovery: got=%v want=%v", keys, wantKeys)
	}
	view := core.GenerationView(got)
	if len(view.Artifacts) != 2 || view.Artifacts[0].Duration != 12.5 || view.ResultURL == nil || *view.ResultURL != "/v1/videos/"+id+"/content" {
		t.Fatalf("public primary/artifact view: %+v", view)
	}
	for i, a := range view.Artifacts {
		if a.ID != want.Artifacts[i].ID || a.ContentURL != "/v1/generations/"+id+"/artifacts/"+a.ID+"/content" {
			t.Fatal(a)
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{want.StorageKey, want.Artifacts[1].StorageKey, want.Artifacts[0].ProviderID, want.Artifacts[1].ProviderID, "cdn.example"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("private artifact identity exposed in public JSON: %s", private)
		}
	}
	return got
}
func taskArtifactsLegacyInsert(t *testing.T, db *sql.DB, owner, id string) {
	t.Helper()
	// This deliberately uses only schema-81 columns, as the rolled-back binary
	// would. Newer result_artifacts metadata must be defaulted rather than required.
	_, err := db.Exec(`INSERT INTO tasks(id,status,provider,model,media_type,prompt,request_options,owner_id,actor_id,provider_owner_id,storage_owner_id) VALUES($1,'queued','mock','legacy-image','image','Synthetic schema-81 row','{}'::jsonb,$2,$2,$2,$2)`, id, owner)
	if err != nil {
		t.Fatal(err)
	}
}
func taskArtifactsAssertLegacyDefault(t *testing.T, db *sql.DB, ctx context.Context, id string) {
	t.Helper()
	got, err := NewTaskRepository(db).Get(ctx, id)
	if err != nil || len(got.StoredArtifacts) != 0 {
		t.Fatalf("legacy default artifacts: %+v err=%v", got.StoredArtifacts, err)
	}
	var raw string
	if err := db.QueryRow(`SELECT result_artifacts::text FROM tasks WHERE id=$1`, id).Scan(&raw); err != nil || raw != "[]" {
		t.Fatalf("legacy JSON array default: %q err=%v", raw, err)
	}
}

func TestTaskArtifactsPersistenceRollbackAndReupgrade(t *testing.T) {
	db := taskArtifactsDatabase82(t)
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	owner := identity.OwnerID(ctx)
	before82 := uuid.NewString()
	taskArtifactsLegacyInsert(t, db, owner, before82)
	if err := goose.UpTo(db, "migrations", 82); err != nil {
		t.Fatal(err)
	}
	taskArtifactsAssertLegacyDefault(t, db, ctx, before82)
	task := taskArtifactsCreate(t, db, ctx)
	repo := NewTaskRepository(db)
	claim := core.WithClaimToken(ctx, uuid.NewString())
	if acquired, err := repo.AcquireTask(claim, task.ID, time.Minute); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	if err := repo.MarkRunning(claim, task.ID); err != nil {
		t.Fatal(err)
	}
	media := taskArtifactsMedia(task.ID, "winning")
	if err := repo.MarkSucceeded(claim, task.ID, media, ""); err != nil {
		t.Fatal(err)
	}
	taskArtifactsAssertStored(t, db, ctx, task.ID, media)
	foreign := identity.WithOwnerID(context.Background(), uuid.NewString())
	if _, err := NewTaskRepository(db).Get(foreign, task.ID); !errors.Is(err, core.ErrTaskNotFound) {
		t.Fatalf("foreign owner obtained task: %v", err)
	}
	if keys, err := repo.StoredResultKeys(foreign, task.ID); err != nil || len(keys) != 0 {
		t.Fatalf("foreign artifact key discovery: %v %v", keys, err)
	}
	if err := goose.DownTo(db, "migrations", 81); err != nil {
		t.Fatal(err)
	}
	// Migration82 intentionally has a preserving Down. Confirm both the column
	// and every non-primary artifact survive before exercising old SQL writes.
	taskArtifactsAssertStored(t, db, ctx, task.ID, media)
	duringRollback := uuid.NewString()
	taskArtifactsLegacyInsert(t, db, owner, duringRollback)
	if _, err := db.Exec(`UPDATE tasks SET status='succeeded',result_url=$2,storage_key=$3,result_content_type=$4,result_size_bytes=$5,result_sha256=$6,stored_at=NOW(),updated_at=NOW() WHERE id=$1`, task.ID, "/v1/generations/"+task.ID+"/content", media.StorageKey, media.ContentType, media.SizeBytes, media.SHA256); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET status='succeeded',result_url=$2,storage_key=$3,result_content_type='image/png',result_size_bytes=77,result_sha256=$4,stored_at=NOW(),updated_at=NOW() WHERE id=$1`, duringRollback, "/v1/generations/"+duringRollback+"/content", duringRollback+"/legacy.png", strings.Repeat("c", 64)); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 82); err != nil {
		t.Fatal(err)
	}
	taskArtifactsAssertStored(t, db, ctx, task.ID, media)
	taskArtifactsAssertLegacyDefault(t, db, ctx, before82)
	taskArtifactsAssertLegacyDefault(t, db, ctx, duringRollback)
	t.Run("AdjacentMigration83Recovery", func(t *testing.T) {
		if _, err := os.Stat("migrations/083_dedicated_installation_companies.sql"); err != nil {
			t.Skip("requires adjacent schema83 migration")
		}
		// Adjacent migration83 is additive too. Exercise the composed recovery cycle
		// without modifying its unrelated installation schema or migration source.
		if err := goose.UpTo(db, "migrations", 83); err != nil {
			t.Fatal(err)
		}
		if err := goose.DownTo(db, "migrations", 81); err != nil {
			t.Fatal(err)
		}
		if err := goose.UpTo(db, "migrations", 83); err != nil {
			t.Fatal(err)
		}
		taskArtifactsAssertStored(t, db, ctx, task.ID, media)
		taskArtifactsAssertLegacyDefault(t, db, ctx, before82)
		taskArtifactsAssertLegacyDefault(t, db, ctx, duringRollback)

	})
	if err := repo.MarkResultDeleted(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := NewTaskRepository(db).Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.ResultDeletedAt == nil || deleted.HasStoredResult || deleted.ResultURL != nil || !reflect.DeepEqual(deleted.StoredArtifacts, media.Artifacts) {
		t.Fatalf("deletion must hide results and preserve private artifact metadata: %+v", deleted)
	}
	view := core.GenerationView(deleted)
	if len(view.Artifacts) != 0 || view.ResultURL != nil || view.LastFrameURL != nil {
		t.Fatalf("deleted artifacts remain in public view: %+v", view)
	}
	if keys, err := repo.StoredResultKeys(ctx, task.ID); err != nil || len(keys) != 0 {
		t.Fatalf("deleted task returned live artifact keys: %v %v", keys, err)
	}
	t.Log("migration81 legacy rows,82 artifact persistence, preserving Down81, old-writer SQL, reupgrade82 all retain full artifact metadata; owner isolation and result deletion verified")
}

func TestTaskArtifactsStaleClaimsCannotReplaceWinningArray(t *testing.T) {
	db := taskArtifactsDatabase82(t)
	if err := goose.UpTo(db, "migrations", 82); err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	task := taskArtifactsCreate(t, db, ctx)
	repo := NewTaskRepository(db)
	stale := core.WithClaimToken(ctx, uuid.NewString())
	successor := core.WithClaimToken(ctx, uuid.NewString())
	if acquired, err := repo.AcquireTask(stale, task.ID, time.Minute); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	if err := repo.MarkRunning(stale, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	staleMedia := taskArtifactsMedia(task.ID, "expired-worker")
	if err := repo.MarkSucceeded(stale, task.ID, staleMedia, ""); !errors.Is(err, core.ErrLeaseLost) {
		t.Fatalf("expired worker wrote artifact array before successor claim: %v", err)
	}
	check, err := repo.Get(ctx, task.ID)
	if err != nil || check.Status != core.TaskRunning || len(check.StoredArtifacts) != 0 || check.StorageKey != nil {
		t.Fatalf("expired-worker write escaped fencing: %+v %v", check, err)
	}
	if acquired, err := repo.AcquireTask(successor, task.ID, time.Minute); err != nil || !acquired {
		t.Fatal(acquired, err)
	}
	if err := repo.MarkSucceeded(stale, task.ID, staleMedia, ""); !errors.Is(err, core.ErrLeaseLost) {
		t.Fatalf("stale worker wrote after successor claimed: %v", err)
	}
	winner := taskArtifactsMedia(task.ID, "successor")
	if err := repo.MarkSucceeded(successor, task.ID, winner, ""); err != nil {
		t.Fatal(err)
	}
	taskArtifactsAssertStored(t, db, ctx, task.ID, winner)
	if err := repo.MarkSucceeded(stale, task.ID, staleMedia, ""); !errors.Is(err, core.ErrLeaseLost) {
		t.Fatalf("stale worker replaced committed successor artifacts: %v", err)
	}
	taskArtifactsAssertStored(t, db, ctx, task.ID, winner)
	t.Log("expired claim rejected before and after ownership transfer and after terminal successor success; durable array and scalar primary remain the successor's exact metadata")
}

package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

func TestGenerationTimeoutSurvivesRepositoryRestart(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	seconds := int64(600)
	id := uuid.NewString()
	now := time.Now().UTC()
	task := core.Task{ID: id, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "timeout", Options: core.MediaOptions{TimeoutSeconds: &seconds}, CreatedAt: now, UpdatedAt: now}
	r := NewTaskRepository(db)
	if _, _, err = r.CreateOrGet(ctx, task, "", "fixture"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM tasks WHERE id=$1`, id)
	defer db.Exec(`DELETE FROM task_queue_outbox WHERE task_id=$1`, id)
	restarted, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	loaded, err := NewTaskRepository(restarted).Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Options.TimeoutSeconds == nil || *loaded.Options.TimeoutSeconds != 600 {
		t.Fatal("timeout lost", loaded.Options)
	}
}

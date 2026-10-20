package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

func TestTaskClaimsFenceExpiredWorkers(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
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
	r := NewTaskRepository(db)
	id := uuid.NewString()
	now := time.Now().UTC()
	task := core.Task{ID: id, Status: core.TaskQueued, Provider: "mock", Model: "mock-video-v1", MediaType: "video", Prompt: "claims", CreatedAt: now, UpdatedAt: now}
	if _, _, err = r.CreateOrGet(ctx, task, "", "fixture"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM task_queue_outbox WHERE task_id=$1`, id)
	defer db.Exec(`DELETE FROM tasks WHERE id=$1`, id)
	a := core.WithClaimToken(ctx, uuid.NewString())
	b := core.WithClaimToken(ctx, uuid.NewString())
	if ok, err := r.AcquireTask(a, id, time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, err := r.AcquireTask(b, id, time.Minute); err != nil || ok {
		t.Fatal("stole live claim", ok, err)
	}
	if err = r.MarkRunning(a, id); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = r.RenewTask(a, id, time.Minute); !errors.Is(err, core.ErrLeaseLost) {
		t.Fatal("resurrected expired lease", err)
	}
	if ok, err := r.AcquireTask(b, id, time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for name, write := range map[string]func() error{
		"schedule":    func() error { return r.ScheduleTask(a, id, time.Hour, 3) },
		"provider ID": func() error { return r.SetProviderTaskID(a, id, "stale") },
		"usage":       func() error { return r.SetProviderUsage(a, id, core.ProviderUsage{}) },
		"source":      func() error { return r.SetProviderResultURL(a, id, "https://stale.invalid") },
		"failure":     func() error { return r.MarkFailed(a, id, "stale") },
		"success":     func() error { return r.MarkSucceeded(a, id, core.StoredMedia{}, "") },
		"refund":      func() error { return r.ConfirmNoCharge(a, id) },
	} {
		if err := write(); !errors.Is(err, core.ErrLeaseLost) {
			t.Fatal(name, err)
		}
	}
	if err = r.ReleaseTask(a, id); err != nil {
		t.Fatal(err)
	}
	if err = r.SetProviderTaskID(b, id, "current"); err != nil {
		t.Fatal("old release cleared new claim", err)
	}
	if err = r.RenewTask(b, id, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err = r.MarkSucceeded(b, id, core.StoredMedia{StorageKey: id + "/video.mp4"}, ""); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.AcquireTask(a, id, time.Minute); err != nil || ok {
		t.Fatal("claimed terminal task", ok, err)
	}
}

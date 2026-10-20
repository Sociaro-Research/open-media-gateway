package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/google/uuid"
)

// Exercise the production claim -> provider error -> SQL scheduling -> Redis
// acknowledgment path. Large counts are seeded fixtures, not simulated load.
func TestPollingErrorPersistenceBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int
		initial int
		polls   int
	}{
		{"ordinary", 5, 0, 5},
		{"minimum", 1, 0, 1},
		{"integer_boundary", 2147483647, 2147483645, 2},
		{"persisted_integer_maximum", 2147483647, 2147483647, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, repo, q, client, prefix := operationTestServices(t)
			p := &failingPollProvider{providerStub: providerStub{name: "mock"}}
			service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "test-model"}, "mock")
			task, err := service.CreateMediaRequest(ctx, core.CreateMediaInput{Prompt: "poll error boundary fixture", Model: "test-model"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.ExecContext(ctx, `UPDATE tasks SET status='running',provider_task_id='synthetic-upstream',worker_started_at=clock_timestamp(),worker_poll_errors=$2 WHERE id=$1`, task.ID, tc.initial); err != nil {
				t.Fatal(err)
			}
			w := New(repo, q, p, nil, time.Hour, time.Hour, tc.limit, time.Second)
			for step := 1; step <= tc.polls; step++ {
				claimCtx := core.WithClaimToken(ctx, uuid.NewString())
				id, err := q.Dequeue(claimCtx)
				if err != nil || id != task.ID {
					t.Fatalf("step %d delivery: %q, %v", step, id, err)
				}
				if err = w.runClaim(claimCtx, id, repo, q); err != nil {
					t.Fatalf("step %d worker -> SQL: %v", step, err)
				}
				got, err := repo.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				wantCount := tc.initial + step
				wantStatus := core.TaskRunning
				if step == tc.polls {
					wantCount-- // Terminal failures are not scheduled again.
					wantStatus = core.TaskFailed
				}
				if got.Status != wantStatus || got.WorkerPollErrors != wantCount || p.polls != step || p.created != 0 {
					t.Fatalf("step %d: status=%s count=%d polls=%d submissions=%d; want %s/%d", step, got.Status, got.WorkerPollErrors, p.polls, p.created, wantStatus, wantCount)
				}
				if wantStatus == core.TaskFailed && (got.Error == nil || !strings.Contains(*got.Error, "provider polling failed: temporary upstream error")) {
					t.Fatalf("unexpected terminal reason: %v", got.Error)
				}
				for _, key := range []string{"pending", "leases"} {
					var count int64
					if key == "pending" {
						count, err = client.LLen(ctx, prefix+key).Result()
					} else {
						count, err = client.ZCard(ctx, prefix+key).Result()
					}
					if err != nil || count != 0 {
						t.Fatalf("step %d: Redis %s=%d, %v", step, key, count, err)
					}
				}
				var leased bool
				if err = db.QueryRowContext(ctx, `SELECT worker_token IS NOT NULL FROM tasks WHERE id=$1`, id).Scan(&leased); err != nil || leased {
					t.Fatalf("step %d retained DB lease: %v, %v", step, leased, err)
				}
				if step < tc.polls {
					// Advance only the synthetic due timestamp; keep the persisted
					// error count and exercise another delivery with a fresh worker.
					if _, err = db.ExecContext(ctx, `UPDATE tasks SET worker_next_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
						t.Fatal(err)
					}
					if err = q.Enqueue(ctx, id); err != nil {
						t.Fatal(err)
					}
					w = New(repo, q, p, nil, time.Hour, time.Hour, tc.limit, time.Second)
				}
			}
			// A terminal redelivery cannot poll or submit again.
			if err = q.Enqueue(ctx, task.ID); err != nil {
				t.Fatal(err)
			}
			claimCtx := core.WithClaimToken(ctx, uuid.NewString())
			id, err := q.Dequeue(claimCtx)
			if err != nil || id != task.ID {
				t.Fatal(id, err)
			}
			if err = w.runClaim(claimCtx, id, repo, q); err != nil || p.polls != tc.polls || p.created != 0 {
				t.Fatalf("terminal redelivery: polls=%d submissions=%d error=%v", p.polls, p.created, err)
			}
			t.Logf("limit=%d seeded=%d polls=%d: persisted intermediate counts and terminal/redelivery checks passed", tc.limit, tc.initial, p.polls)
		})
	}
}

package worker

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type observedOperationQueue struct {
	*queue.RedisQueue
	dequeued  atomic.Int32
	recovered atomic.Int32
	// Exercise failures before any delivery with a one-slot worker.
	emptyOnce    bool
	errorOnce    bool
	afterDequeue func()
}

func (q *observedOperationQueue) Dequeue(ctx context.Context) (string, error) {
	if q.emptyOnce {
		q.emptyOnce = false
		return "", core.ErrQueueEmpty
	}
	if q.errorOnce {
		q.errorOnce = false
		return "", errors.New("transient dequeue fixture failure")
	}
	id, err := q.RedisQueue.Dequeue(ctx)
	if err == nil {
		q.dequeued.Add(1)
		if q.afterDequeue != nil {
			q.afterDequeue()
		}
	}
	return id, err
}

func (q *observedOperationQueue) Recover(ctx context.Context) error {
	q.recovered.Add(1)
	return q.RedisQueue.Recover(ctx)
}

type gatedPollProvider struct{ parallelProvider }

func (p *gatedPollProvider) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	_, err := p.Generate(ctx, core.MediaRequest{TaskID: id})
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://fixture.invalid/video.mp4"}, err
}

func operationTestServices(t *testing.T) (context.Context, *sql.DB, *postgres.TaskRepository, *observedOperationQueue, *redis.Client, string) {
	t.Helper()
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("requires isolated PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	schema := "operations_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schema + " CASCADE") })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	name := "operations-" + uuid.NewString()
	q, err := queue.Open(ctx, redisURL, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	t.Cleanup(func() { client.Close() })
	return ctx, db, postgres.NewTaskRepository(db), &observedOperationQueue{RedisQueue: q}, client, "media-gateway:{" + name + "}:v2:"
}

func awaitOperation(t *testing.T, ctx context.Context, started <-chan string) string {
	t.Helper()
	select {
	case id := <-started:
		return id
	case <-ctx.Done():
		t.Fatal("operation did not start", ctx.Err())
		return ""
	}
}

// Real claims, delivery leases, provider submissions/polls and expired recovery
// must share the same pre-dequeue bound. Shutdown while full leaves excess work
// unclaimed and drains only the acquired operations.
func TestWorkerOperationsBoundClaimsAndDrain(t *testing.T) {
	for _, polling := range []bool{false, true} {
		name := "submissions"
		if polling {
			name = "recovered polls"
		}
		t.Run(name, func(t *testing.T) {
			ctx, db, repo, q, client, prefix := operationTestServices(t)
			p := &gatedPollProvider{parallelProvider{started: make(chan string, 20), release: make(chan struct{})}}
			service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "test-model"}, "mock")
			const limit, total = 3, 12
			for i := 0; i < total; i++ {
				if _, err := service.CreateMediaRequest(ctx, core.CreateMediaInput{Prompt: "bounded operations", Model: "test-model"}, ""); err != nil {
					t.Fatal(err)
				}
			}
			if polling {
				if _, err := db.ExecContext(ctx, `UPDATE tasks SET status='running',provider_task_id=id,worker_started_at=clock_timestamp()`); err != nil {
					t.Fatal(err)
				}
			}
			// Simulate a dead worker's queued/running task, without waiting 60s.
			deadCtx := core.WithClaimToken(ctx, uuid.NewString())
			expiredID, err := q.RedisQueue.Dequeue(deadCtx)
			if err != nil {
				t.Fatal(err)
			}
			if acquired, err := repo.AcquireTask(deadCtx, expiredID, core.WorkLeaseDuration); err != nil || !acquired {
				t.Fatalf("dead-worker fixture claim: %v %v", acquired, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, expiredID); err != nil {
				t.Fatal(err)
			}
			if err := client.ZAdd(ctx, prefix+"leases", redis.Z{Score: 0, Member: expiredID}).Err(); err != nil {
				t.Fatal(err)
			}
			w := New(repo, q, p, concurrentStorage{}, time.Hour, time.Minute, 2, 10*time.Millisecond)
			w.OperationConcurrency = limit
			workCtx, stop := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- w.Run(workCtx) }()
			joined := false
			t.Cleanup(func() {
				stop()
				if !joined {
					close(p.release)
					<-done
				}
			})
			for i := 0; i < limit; i++ {
				awaitOperation(t, ctx, p.started)
			}
			select {
			case id := <-p.started:
				t.Fatalf("operation limit exceeded by %s", id)
			case <-time.After(150 * time.Millisecond):
			}
			if n := q.dequeued.Load(); n != limit {
				t.Fatalf("dequeued %d tasks while saturated, want %d", n, limit)
			}
			var claims int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE worker_token IS NOT NULL AND worker_lease_until>clock_timestamp()`).Scan(&claims); err != nil || claims != limit {
				t.Fatalf("active DB claims=%d want=%d error=%v", claims, limit, err)
			}
			if n, err := client.ZCard(ctx, prefix+"leases").Result(); err != nil || n != limit {
				t.Fatalf("active queue leases=%d want=%d error=%v", n, limit, err)
			}
			if q.recovered.Load() < 2 {
				t.Fatal("reconciliation stopped while operations were saturated")
			}
			stop()
			select {
			case err := <-done:
				joined = true
				close(p.release)
				t.Fatalf("shutdown did not wait for acquired operations: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			close(p.release)
			if err := <-done; err != nil {
				joined = true
				t.Fatal(err)
			}
			joined = true
			if q.dequeued.Load() != limit {
				t.Fatal("shutdown dequeued excess tasks")
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE worker_token IS NOT NULL AND worker_lease_until>clock_timestamp()`).Scan(&claims); err != nil || claims != 0 {
				t.Fatalf("drained claims=%d error=%v", claims, err)
			}
			// Remaining deliveries, including the recovered lease, complete under
			// a replacement worker with the same bound and no repeated operation.
			restartCtx, restartStop := context.WithCancel(ctx)
			restartDone := make(chan error, 1)
			go func() { restartDone <- w.Run(restartCtx) }()
			defer func() { restartStop(); <-restartDone }()
			for {
				var completed int
				if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE status='succeeded'`).Scan(&completed); err != nil {
					t.Fatal(err)
				}
				if completed == total {
					break
				}
				if !pause(ctx, 10*time.Millisecond) {
					t.Fatal("backlog did not complete after restart")
				}
			}
			if p.calls.Load() != total || p.peak.Load() > limit {
				t.Fatalf("operations=%d peak=%d want calls=%d peak<=%d", p.calls.Load(), p.peak.Load(), total, limit)
			}
		})
	}
}

func TestWorkerOperationCapacitySurvivesEmptyAndFailedDequeue(t *testing.T) {
	ctx, db, repo, q, _, _ := operationTestServices(t)
	q.emptyOnce, q.errorOnce = true, true
	p := &parallelProvider{started: make(chan string, 2), release: make(chan struct{})}
	close(p.release)
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "test-model"}, "mock")
	for i := 0; i < 2; i++ {
		if _, err := service.CreateMediaRequest(ctx, core.CreateMediaInput{Prompt: "capacity after dequeue failure", Model: "test-model"}, ""); err != nil {
			t.Fatal(err)
		}
	}
	w := New(repo, q, p, concurrentStorage{}, time.Second, time.Minute, 2, 10*time.Millisecond)
	w.OperationConcurrency = 1
	workCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(workCtx) }()
	defer func() { stop(); <-done }()
	for i := 0; i < 2; i++ {
		awaitOperation(t, ctx, p.started)
	}
	for {
		var completed int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE status='succeeded'`).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if completed == 2 {
			break
		}
		if !pause(ctx, 10*time.Millisecond) {
			t.Fatal("capacity leaked after empty/error dequeue or claim completion")
		}
	}
}

func TestWorkerOperationCancelledDequeueRequeuesWithoutProcessing(t *testing.T) {
	ctx, _, repo, q, client, prefix := operationTestServices(t)
	p := &parallelProvider{started: make(chan string, 1), release: make(chan struct{})}
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "test-model"}, "mock")
	task, err := service.CreateMediaRequest(ctx, core.CreateMediaInput{Prompt: "cancel during dequeue", Model: "test-model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	q.afterDequeue = stop
	w := New(repo, q, p, concurrentStorage{}, time.Second, time.Minute, 2, 10*time.Millisecond)
	w.OperationConcurrency = 1
	if err := w.Run(workCtx); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 0 {
		t.Fatal("cancelled delivery was processed")
	}
	if n, err := client.ZCard(ctx, prefix+"leases").Result(); err != nil || n != 0 {
		t.Fatalf("cancelled delivery retained leases: %d %v", n, err)
	}
	id, err := q.RedisQueue.Dequeue(core.WithClaimToken(ctx, uuid.NewString()))
	if err != nil || id != task.ID {
		t.Fatalf("cancelled delivery was not requeued: %s %v", id, err)
	}
}

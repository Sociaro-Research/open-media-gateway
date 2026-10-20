package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/google/uuid"
)

type parallelProvider struct {
	started chan string
	release chan struct{}
	calls   atomic.Int32
	active  atomic.Int32
	peak    atomic.Int32
}

func (p *parallelProvider) Name() string { return "mock" }
func (p *parallelProvider) IsFree() bool { return true }
func (p *parallelProvider) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	p.calls.Add(1)
	n := p.active.Add(1)
	defer p.active.Add(-1)
	for old := p.peak.Load(); n > old; old = p.peak.Load() {
		if p.peak.CompareAndSwap(old, n) {
			break
		}
	}
	p.started <- r.TaskID
	select {
	case <-ctx.Done():
		return core.ProviderTask{}, ctx.Err()
	case <-p.release:
	}
	return core.ProviderTask{ID: r.TaskID, ResultURL: "https://fixture.invalid/video.mp4"}, nil
}
func (p *parallelProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://fixture.invalid/video.mp4"}, nil
}

type concurrentStorage struct{}

func (concurrentStorage) Archive(ctx context.Context, id, source string) (core.StoredMedia, error) {
	return core.StoredMedia{StorageKey: id + "/video.mp4", ContentType: "video/mp4", SizeBytes: 12, SHA256: "fixture"}, ctx.Err()
}

// Real HTTP admission, PostgreSQL claims, Redis and independent workers.
// Provider completion is gated: all 20 submissions must start before even one
// can complete, so a sequential implementation cannot pass by running quickly.
func TestTwentyConcurrentHTTPGenerations(t *testing.T) {
	t.Run("single worker with defaults", func(t *testing.T) { testTwentyConcurrentHTTPGenerations(t, false) })
	t.Run("second worker starts during generation", func(t *testing.T) { testTwentyConcurrentHTTPGenerations(t, true) })
}

func testTwentyConcurrentHTTPGenerations(t *testing.T, secondWorker bool) {
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("requires isolated PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "parallel_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec("DROP SCHEMA " + schema + " CASCADE")
	u, _ := url.Parse(dsn)
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	q, err := queue.Open(ctx, redisURL, "parallel-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	repo := postgres.NewTaskRepository(db)
	p := &parallelProvider{started: make(chan string, 100), release: make(chan struct{})}
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "test-model"}, "mock")
	server := httptest.NewServer(api.New(service, nil, "fixture-key"))
	defer server.Close()
	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 2)
	startWorker := func() {
		w := New(repo, q, p, concurrentStorage{}, time.Millisecond, time.Second, 2, 20*time.Millisecond)
		go func() { done <- w.Run(workCtx) }()
	}
	startWorker()
	ids := make(chan string, 20)
	var clients sync.WaitGroup
	start := time.Now()
	for i := 0; i < 20; i++ {
		clients.Add(1)
		go func() {
			defer clients.Done()
			req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/generations", bytes.NewBufferString(`{"model":"mock/test-model","prompt":"parallel test","webhook_url":"https://example.test/generation-events"}`))
			req.Header.Set("Authorization", "Bearer "+"fixture-key")
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			defer response.Body.Close()
			var task struct {
				ID        string `json:"id"`
				CreatedAt int64  `json:"created_at"`
			}
			if err = json.NewDecoder(response.Body).Decode(&task); err != nil {
				t.Error(err)
				return
			}
			if response.StatusCode != 202 {
				t.Errorf("admission status %d", response.StatusCode)
				return
			}
			ids <- task.ID
		}()
	}
	clients.Wait()
	close(ids)
	for i := 0; i < 20; i++ {
		// Start worker two while the first is submitting, exercising live recovery.
		if secondWorker && i == 16 {
			startWorker()
		}
		select {
		case <-p.started:
		case <-ctx.Done():
			t.Fatal("20 generations did not overlap", ctx.Err())
		}
	}
	if p.peak.Load() != 20 {
		t.Fatal("expected 20 simultaneous provider requests", p.peak.Load())
	}
	t.Logf("20 HTTP requests reached provider concurrently in %s", time.Since(start))
	close(p.release)
	for id := range ids {
		for {
			task, err := repo.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if task.Status == core.TaskSucceeded {
				break
			}
			if task.Status == core.TaskFailed {
				t.Fatal("live task failed", task.Error)
			}
			if !pause(ctx, 10*time.Millisecond) {
				t.Fatal("completion timed out")
			}
		}
	}
	stop()
	workerCount := 1
	if secondWorker {
		workerCount = 2
	}
	for i := 0; i < workerCount; i++ {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker shutdown hung")
		}
	}
	var deliveredSnapshots int
	if err := db.QueryRow(`SELECT count(*) FROM webhook_deliveries WHERE event_type='task.succeeded'
 AND payload->>'type'='generation.completed'
 AND payload->'data'->>'status'='completed'
 AND payload->'data'->>'model'='mock/test-model'
 AND jsonb_typeof(payload->'data'->'created_at')='number'
 AND NOT (payload->'data' ? 'result_url')
 AND NOT (payload->'data'->'options' ? 'duration')`).Scan(&deliveredSnapshots); err != nil || deliveredSnapshots != 20 {
		t.Fatalf("public webhook snapshots: count=%d error=%v", deliveredSnapshots, err)
	}
	if p.calls.Load() != 20 {
		t.Fatal("duplicate generation", p.calls.Load())
	}
}

type pendingProvider struct {
	started chan string
	polls   atomic.Int32
}

func (p *pendingProvider) Name() string { return "mock" }
func (p *pendingProvider) IsFree() bool { return true }
func (p *pendingProvider) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	p.started <- r.TaskID
	return core.ProviderTask{ID: r.TaskID}, nil
}
func (p *pendingProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	p.polls.Add(1)
	return core.ProviderResult{Status: core.ProviderRunning}, nil
}

// A thousand long-lived upstream jobs must not block the next submission.
// Restarting the worker must preserve their due times and never resubmit them.
func TestThousandPendingGenerationsLeaveSubmissionsAvailable(t *testing.T) {
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("requires isolated PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "pending_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec("DROP SCHEMA " + schema + " CASCADE")
	u, _ := url.Parse(dsn)
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	q, err := queue.Open(ctx, redisURL, "pending-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	repo := postgres.NewTaskRepository(db)
	p := &pendingProvider{started: make(chan string, 2000)}
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "test-model"}, "mock")
	server := httptest.NewServer(api.New(service, nil, "fixture-key"))
	defer server.Close()
	startWorker := func() (context.CancelFunc, chan error) {
		wc, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		w := New(repo, q, p, concurrentStorage{}, time.Hour, 2*time.Hour, 2, 20*time.Millisecond)
		go func() { done <- w.Run(wc) }()
		return stop, done
	}
	stop, done := startWorker()
	defer func() { stop(); <-done }()
	submit := func() {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/generations", strings.NewReader(`{"model":"mock/test-model","prompt":"pending load test"}`))
		req.Header.Set("Authorization", "Bearer "+"fixture-key")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Error(e)
			return
		}
		res.Body.Close()
		if res.StatusCode != 202 {
			t.Errorf("admission %d", res.StatusCode)
		}
	}
	start := time.Now()
	var clients sync.WaitGroup
	// Client-side batching keeps the test harness from measuring connection storms.
	for batch := 0; batch < 50; batch++ {
		for i := 0; i < 20; i++ {
			clients.Add(1)
			go func() { defer clients.Done(); submit() }()
		}
		clients.Wait()
	}
	for i := 0; i < 1000; i++ {
		select {
		case <-p.started:
		case <-ctx.Done():
			t.Fatal("pending work blocked submissions", ctx.Err())
		}
	}
	t.Logf("1000 HTTP requests accepted and submitted in %s", time.Since(start))
	for {
		var n int
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE status='running' AND worker_next_at>NOW() AND worker_token IS NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1000 {
			break
		}
		if !pause(ctx, 10*time.Millisecond) {
			t.Fatal("pending state not persisted")
		}
	}
	if p.polls.Load() != 0 {
		t.Fatal("polled before due", p.polls.Load())
	}
	stop()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	stop, done = startWorker()
	start = time.Now()
	submit()
	select {
	case <-p.started:
	case <-ctx.Done():
		t.Fatal("new request waits for prior generations")
	}
	t.Logf("new request submitted with 1000 pending jobs after restart in %s", time.Since(start))
	// Force one persisted due time, simulating a scheduled poll after restart.
	if _, err = db.ExecContext(ctx, `UPDATE tasks SET worker_next_at=NOW()-interval '1 second' WHERE id=(SELECT id FROM tasks WHERE worker_token IS NULL ORDER BY id LIMIT 1)`); err != nil {
		t.Fatal(err)
	}
	for p.polls.Load() == 0 {
		if !pause(ctx, 10*time.Millisecond) {
			t.Fatal("due poll not recovered")
		}
	}
	if p.polls.Load() != 1 {
		t.Fatal("unexpected repeated polls", p.polls.Load())
	}
}

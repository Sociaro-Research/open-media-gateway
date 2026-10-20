package reliability_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/api"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/httpclient"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/Sociaro-Research/media-gateway-dev/internal/worker"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestMain(m *testing.M) {
	if os.Getenv("GATEWAY_TEST_LOGS") != "1" {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	os.Exit(m.Run())
}

// This fixture has real HTTP side effects, also visible to a killed child worker.
// All destinations are explicit localhost test servers; it cannot call a vendor.
type fixtureJob struct {
	Prompt             string
	Submissions, Polls int
	Submitted, Ready   time.Time
}
type providerFixture struct {
	connections                  atomic.Int64
	mu                           sync.Mutex
	jobs                         map[string]*fixtureJob
	ready                        bool
	delay                        time.Duration
	blockSubmit                  bool
	submitGate                   chan struct{}
	blockPoll                    bool
	archiveGate                  chan struct{}
	archiveAfterChunk            chan struct{}
	activeArchives, peakArchives int
	server                       *httptest.Server
	media                        []byte
}

func newProviderFixture(t *testing.T) *providerFixture {
	t.Helper()
	p := &providerFixture{jobs: map[string]*fixtureJob{}, ready: true, media: bytes.Repeat([]byte("gateway-reliability-media\n"), 2731)}
	p.server = httptest.NewUnstartedServer(http.HandlerFunc(p.serve))
	p.server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			p.connections.Add(1)
		}
	}
	p.server.Start()
	t.Cleanup(p.server.Close)
	return p
}
func (p *providerFixture) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/submit":
		var input core.MediaRequest
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			w.WriteHeader(400)
			return
		}
		p.mu.Lock()
		job := p.jobs[input.TaskID]
		if job == nil {
			job = &fixtureJob{Prompt: input.Prompt, Submitted: time.Now()}
			p.jobs[input.TaskID] = job
		}
		job.Submissions++
		blocked := p.blockSubmit
		gate := p.submitGate
		p.mu.Unlock()
		if blocked {
			<-r.Context().Done()
			return
		}
		if (input.Prompt == "release-submit" || input.Prompt == "release-sync") && gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if input.Prompt == "submit-failure" {
			w.WriteHeader(503)
			return
		}
		result := core.ProviderTask{ID: input.TaskID}
		if input.Prompt == "release-sync" {
			result.ResultURL = p.server.URL + "/result"
		}
		json.NewEncoder(w).Encode(result)
	case "/poll":
		p.mu.Lock()
		job := p.jobs[r.URL.Query().Get("id")]
		if job == nil {
			p.mu.Unlock()
			w.WriteHeader(404)
			return
		}
		job.Polls++
		prompt, polls := job.Prompt, job.Polls
		blocked := p.blockPoll
		ready := (p.ready || prompt == "fast" || prompt == "release-archive") && time.Since(job.Submitted) >= p.delay
		if ready && job.Ready.IsZero() {
			job.Ready = time.Now()
		}
		p.mu.Unlock()
		if blocked {
			<-r.Context().Done()
			return
		}
		if prompt == "poll-errors" || (prompt == "transient-poll-errors" && polls <= 2) {
			w.WriteHeader(503)
			return
		}
		result := core.ProviderResult{Status: core.ProviderRunning}
		if ready {
			result.Status, result.ResultURL = core.ProviderSucceeded, p.server.URL+"/result"
			switch prompt {
			case "provider-failure":
				result.Status, result.Error = core.ProviderFailed, "fixture failure"
			case "missing-url":
				result.ResultURL = ""
			case "storage-failure":
				result.ResultURL = p.server.URL + "/broken"
			case "with-frame":
				result.LastFrameURL = p.server.URL + "/result"
			}
		}
		json.NewEncoder(w).Encode(result)
	case "/result":
		p.mu.Lock()
		gate := p.archiveGate
		afterChunk := p.archiveAfterChunk
		p.activeArchives++
		p.peakArchives = max(p.peakArchives, p.activeArchives)
		p.mu.Unlock()
		defer func() { p.mu.Lock(); p.activeArchives--; p.mu.Unlock() }()
		if gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "video/mp4")
		if afterChunk != nil {
			w.Header().Set("Content-Length", fmt.Sprint(len(p.media)))
			w.Write(p.media[:4096])
			w.(http.Flusher).Flush()
			select {
			case <-afterChunk:
				w.Write(p.media[4096:])
			case <-r.Context().Done():
			}
			return
		}
		w.Write(p.media)
	default:
		w.WriteHeader(503)
	}
}
func (p *providerFixture) snapshot() map[string]fixtureJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]fixtureJob{}
	for id, job := range p.jobs {
		out[id] = *job
	}
	return out
}
func (p *providerFixture) job(id string) fixtureJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	if job := p.jobs[id]; job != nil {
		return *job
	}
	return fixtureJob{}
}
func (p *providerFixture) release() { p.mu.Lock(); p.ready = true; p.blockPoll = false; p.mu.Unlock() }

type httpProvider struct {
	base   string
	client *http.Client
}

func (p *httpProvider) Name() string { return "mock" }
func (p *httpProvider) IsFree() bool { return true }
func (p *httpProvider) call(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, p.base+path, body)
	if err != nil {
		return err
	}
	response, err := p.client.Do(r)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("fixture returned %d", response.StatusCode)
	}
	return json.NewDecoder(response.Body).Decode(output)
}
func (p *httpProvider) Generate(ctx context.Context, input core.MediaRequest) (core.ProviderTask, error) {
	var result core.ProviderTask
	err := p.call(ctx, "POST", "/submit", input, &result)
	return result, err
}
func (p *httpProvider) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	var result core.ProviderResult
	err := p.call(ctx, "GET", "/poll?id="+url.QueryEscape(id), nil, &result)
	return result, err
}

type testStack struct {
	client                         *http.Client
	t                              *testing.T
	ctx                            context.Context
	db                             *sql.DB
	dsn, redisURL, queueName, root string
	key                            string
	repo                           *postgres.TaskRepository
	q                              *queue.RedisQueue
	redis                          *redis.Client
	provider                       *httpProvider
	fixture                        *providerFixture
	store                          *storage.Local
	service                        *core.TaskService
	api                            *httptest.Server
	workers                        sync.WaitGroup
}

func newStack(t *testing.T, timeout time.Duration) *testStack {
	t.Helper()
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		if os.Getenv("GATEWAY_PERF") == "1" {
			t.Fatal("performance tests require isolated PostgreSQL and Redis")
		}
		t.Skip("requires isolated PostgreSQL and Redis; use scripts/with-test-services.py")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "reliability_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		base.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := base.Exec("DROP SCHEMA " + schema + " CASCADE")
		if err != nil {
			t.Error(err)
		}
		base.Close()
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	name := "reliability-" + uuid.NewString()
	q, err := queue.Open(ctx, redisURL, name)
	if err != nil {
		t.Fatal(err)
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rc := redis.NewClient(options)
	t.Cleanup(func() {
		keys, err := rc.Keys(context.Background(), "media-gateway:{"+name+"}:*").Result()
		if err != nil {
			t.Error(err)
		} else if len(keys) > 0 {
			if err := rc.Del(context.Background(), keys...).Err(); err != nil {
				t.Error(err)
			}
		}
		q.Close()
		rc.Close()
	})
	fixture := newProviderFixture(t)
	providerClient := httpclient.New(90 * time.Second)
	t.Cleanup(providerClient.CloseIdleConnections)
	p := &httpProvider{base: fixture.server.URL, client: providerClient}
	s := &testStack{t: t, ctx: ctx, db: db, dsn: u.String(), redisURL: redisURL, queueName: name, q: q, redis: rc, repo: postgres.NewTaskRepository(db), provider: p, fixture: fixture, key: "fixture-key"}
	s.root = t.TempDir()
	// Use the same directory in parent and crash-test worker processes.
	s.store, err = storage.NewLocal(s.root, providerClient, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	s.service = core.NewTaskServiceWithProviders(s.repo, q, map[string]core.MediaProvider{"mock": p}, map[string]string{"mock": "test-video"}, "mock")
	s.service.EnableAccounting()
	keys := api.WithRequestLimits(postgres.NewAPIKeyRepository(db), postgres.NewSystemRepository(db), q)
	// High test-only limits permit measuring capacity independently of quota policy.
	if _, err = db.Exec(`UPDATE system_settings SET request_limits=(SELECT jsonb_object_agg(key,10000000) FROM jsonb_each(request_limits))`); err != nil {
		t.Fatal(err)
	}
	s.api = httptest.NewUnstartedServer(api.NewGateway(api.Dependencies{Service: s.service, Health: api.NewDependenciesHealth(db, q), APIKey: "fixture-key", Storage: s.store, SigningSecret: "fixture-signing-secret", MediaURLTTL: time.Minute, Keys: keys, System: postgres.NewSystemRepository(db)}))
	// Match the gateway executable's connection deadlines. Large report success
	// with httptest's unlimited WriteTimeout must not mask a production failure.
	s.api.Config = api.NewHTTPServer("", s.api.Config.Handler)
	s.api.Start()
	t.Cleanup(s.api.Close)
	s.client = httpclient.New(90 * time.Second)
	t.Cleanup(s.client.CloseIdleConnections)
	t.Cleanup(func() { cancel(); s.workers.Wait() })
	return s
}
func (s *testStack) startWorkers(n int, poll time.Duration) context.CancelFunc {
	s.t.Helper()
	ctx, cancel := context.WithCancel(s.ctx)
	for i := 0; i < n; i++ {
		w := worker.New(s.repo, s.q, s.provider, s.store, poll, 2*time.Minute, 3, 20*time.Millisecond)
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			if err := w.Run(ctx); err != nil {
				s.t.Error(err)
			}
		}()
	}
	return cancel
}
func (s *testStack) request(method, path, body, key string) (int, []byte, error) {
	r, err := http.NewRequestWithContext(s.ctx, method, s.api.URL+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	r.Header.Set("Authorization", "Bearer "+s.key)
	r.Header.Set("User-Agent", "Sociaro-Gateway-Isolated-Reliability/1.0")
	if key != "" {
		r.Header.Set("Idempotency-Key", key)
	}
	response, err := s.client.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}
func (s *testStack) discardGet(path string) (int, error) {
	r, err := http.NewRequestWithContext(s.ctx, "GET", s.api.URL+path, nil)
	if err != nil {
		return 0, err
	}
	r.Header.Set("Authorization", "Bearer "+s.key)
	r.Header.Set("User-Agent", "Sociaro-Gateway-Isolated-Reliability/1.0")
	response, err := s.client.Do(r)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, err = io.Copy(io.Discard, response.Body)
	return response.StatusCode, err
}
func (s *testStack) submit(prompt, key string, hook bool) string {
	s.t.Helper()
	body := map[string]any{"model": "mock/test-video", "prompt": prompt}
	if hook {
		body["webhook_url"] = "https://receiver.example.test/events"
	}
	b, _ := json.Marshal(body)
	status, data, err := s.request("POST", "/v1/generations", string(b), key)
	if err != nil || status != 202 {
		s.t.Fatalf("admission: status=%d error=%v body=%s", status, err, data)
	}
	var result struct{ ID string }
	if err := json.Unmarshal(data, &result); err != nil || result.ID == "" {
		s.t.Fatalf("invalid acceptance: %s", data)
	}
	return result.ID
}
func eventually(t *testing.T, ctx context.Context, message string, check func() bool) {
	t.Helper()
	for !check() {
		select {
		case <-ctx.Done():
			t.Fatal(message, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (s *testStack) terminal(id string) core.Task {
	s.t.Helper()
	var task core.Task
	eventually(s.t, s.ctx, "task never terminated: "+id, func() bool {
		var err error
		task, err = s.repo.Get(s.ctx, id)
		if err != nil {
			s.t.Fatal(err)
		}
		return task.Status == core.TaskSucceeded || task.Status == core.TaskFailed
	})
	return task
}
func (s *testStack) checkFile(task core.Task) {
	s.t.Helper()
	if task.Status != core.TaskSucceeded {
		s.t.Fatalf("task failed: %v", task.Error)
	}
	status, body, err := s.request("GET", "/v1/generations/"+task.ID+"/content", "", "")
	want := sha256.Sum256(s.fixture.media)
	got := sha256.Sum256(body)
	if err != nil || status != 200 || got != want || task.ResultSHA256 == nil || *task.ResultSHA256 != hex.EncodeToString(want[:]) {
		s.t.Fatalf("download integrity: status=%d bytes=%d error=%v", status, len(body), err)
	}
}

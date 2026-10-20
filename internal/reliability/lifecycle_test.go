package reliability_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/httpclient"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhook"
	"github.com/Sociaro-Research/media-gateway-dev/internal/worker"
	"github.com/redis/go-redis/v9"
)

func TestReliabilityConcurrentIdempotencyAndFiles(t *testing.T) {
	s := newStack(t, 45*time.Second)
	s.startWorkers(2, 20*time.Millisecond)
	const distinct, retries = 24, 4
	var clients sync.WaitGroup
	ids := make(chan string, distinct*retries)
	for i := 0; i < distinct; i++ {
		for j := 0; j < retries; j++ {
			clients.Add(1)
			go func(i int) {
				defer clients.Done()
				ids <- s.submit("with-frame", fmt.Sprintf("reliability-%d", i), true)
			}(i)
		}
	}
	clients.Wait()
	close(ids)
	unique := map[string]bool{}
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != distinct {
		t.Fatalf("idempotent retries created %d tasks, want %d", len(unique), distinct)
	}
	for id := range unique {
		task := s.terminal(id)
		s.checkFile(task)
		status, data, err := s.request("GET", "/v1/generations/"+id+"/last-frame", "", "")
		if err != nil || status != 200 || string(data) != string(s.fixture.media) {
			t.Fatalf("last frame integrity: %d %v", status, err)
		}
		if task.Cost == nil || task.Cost.ActualUSD == nil || *task.Cost.ActualUSD != 0 {
			t.Fatal("free fixture lost its confirmed zero charge")
		}
	}
	for _, job := range s.fixture.snapshot() {
		if job.Submissions != 1 {
			t.Fatalf("duplicate provider submission: %+v", job)
		}
	}
	status, _, err := s.request("POST", "/v1/generations", `{"model":"mock/test-video","prompt":"changed"}`, "reliability-0")
	if err != nil || status != 409 {
		t.Fatalf("different content accepted for reused key: %d %v", status, err)
	}
	var events int
	if err := s.db.QueryRow(`SELECT count(*) FROM webhook_deliveries WHERE event_type='task.succeeded'`).Scan(&events); err != nil || events != distinct {
		t.Fatalf("terminal event count=%d error=%v", events, err)
	}
}

func TestReliabilityProviderAndStorageFailures(t *testing.T) {
	s := newStack(t, 30*time.Second)
	s.startWorkers(2, 20*time.Millisecond)
	for _, prompt := range []string{"submit-failure", "provider-failure", "missing-url", "poll-errors", "storage-failure", "transient-poll-errors"} {
		id := s.submit(prompt, prompt, true)
		task := s.terminal(id)
		if prompt == "transient-poll-errors" {
			s.checkFile(task)
			continue
		}
		if task.Status != core.TaskFailed || task.Error == nil || task.StoredAt != nil {
			t.Fatalf("failure reported as saved success: %s %+v", prompt, task)
		}
		status, _, err := s.request("GET", "/v1/generations/"+id+"/content", "", "")
		if err != nil || status == 200 {
			t.Fatalf("failed result was downloadable: %s %d %v", prompt, status, err)
		}
	}
	files, err := filepath.Glob(filepath.Join(s.root, "*", "attempts", "*", "*.part"))
	rootFiles, rootErr := filepath.Glob(filepath.Join(s.root, "*", "*.part"))
	files = append(files, rootFiles...)
	err = errors.Join(err, rootErr)
	if err != nil || len(files) != 0 {
		t.Fatalf("partial files remain: %v %v", files, err)
	}
	s.checkFile(s.terminal(s.submit("healthy-after-failures", "healthy", true)))
}

func TestReliabilityRedisDatasetLoss(t *testing.T) {
	s := newStack(t, 30*time.Second)
	s.fixture.ready = false
	const count = 100
	ids := make([]string, count)
	for i := range ids {
		ids[i] = s.submit("pending", fmt.Sprint(i), true)
	}
	// Lose the accepted delivery dataset before a consumer starts. PostgreSQL
	// outbox/state must reconstruct it without any provider resubmission.
	keys, err := s.redis.Keys(s.ctx, "media-gateway:{"+s.queueName+"}:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.redis.Del(s.ctx, keys...).Err(); err != nil {
		t.Fatal(err)
	}
	s.startWorkers(2, 30*time.Millisecond)
	eventually(t, s.ctx, "lost queue never reconstructed", func() bool { return len(s.fixture.snapshot()) == count })
	// Lose coordination once more while the durable provider IDs already exist.
	eventually(t, s.ctx, "provider IDs not durable", func() bool {
		var n int
		err := s.db.QueryRow(`SELECT count(*) FROM tasks WHERE provider_task_id IS NOT NULL`).Scan(&n)
		return err == nil && n == count
	})
	keys, err = s.redis.Keys(s.ctx, "media-gateway:{"+s.queueName+"}:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) > 0 {
		if err = s.redis.Del(s.ctx, keys...).Err(); err != nil {
			t.Fatal(err)
		}
	}
	s.fixture.release()
	for _, id := range ids {
		s.checkFile(s.terminal(id))
	}
	for _, job := range s.fixture.snapshot() {
		if job.Submissions != 1 {
			t.Fatal("Redis loss duplicated paid-side submission", job)
		}
	}
}

type childConfig struct{ DSN, Redis, Queue, Provider, Root string }

// Launched only by the crash test using this test binary, never by normal tests.
func TestReliabilityChildWorker(t *testing.T) {
	raw := os.Getenv("GATEWAY_RELIABILITY_CHILD")
	if raw == "" {
		t.Skip("crash-test helper")
	}
	var c childConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := postgres.Open(ctx, c.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q, err := queue.Open(ctx, c.Redis, c.Queue)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	p := &httpProvider{base: c.Provider, client: httpclient.New(90 * time.Second)}
	store, err := storage.NewLocal(c.Root, p.client, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	w := worker.New(postgres.NewTaskRepository(db), q, p, store, 20*time.Millisecond, 2*time.Minute, 3, 20*time.Millisecond)
	if err := w.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestReliabilityHardWorkerCrash(t *testing.T) {
	for _, phase := range []string{"submission", "polling", "archival"} {
		t.Run(phase, func(t *testing.T) {
			duringSubmit := phase == "submission"
			s := newStack(t, 90*time.Second)
			s.fixture.ready = phase == "archival"
			s.fixture.blockSubmit = duringSubmit
			s.fixture.blockPoll = phase == "polling"
			if phase == "archival" {
				s.fixture.archiveAfterChunk = make(chan struct{})
			}
			id := s.submit("crash", "crash", true)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			config, _ := json.Marshal(childConfig{s.dsn, s.redisURL, s.queueName, s.fixture.server.URL, s.root})
			cmd := exec.Command(exe, "-test.run=^TestReliabilityChildWorker$", "-test.timeout=2m")
			cmd.Env = append(os.Environ(), "GATEWAY_RELIABILITY_CHILD="+string(config))
			log, err := os.Create(filepath.Join(t.TempDir(), "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd.Stdout = log
			cmd.Stderr = log
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			killed := false
			t.Cleanup(func() {
				if !killed {
					cmd.Process.Kill()
					cmd.Wait()
				}
			})
			eventually(t, s.ctx, "child never reached provider", func() bool { return len(s.fixture.snapshot()) == 1 })
			if !duringSubmit {
				eventually(t, s.ctx, "provider ID not persisted", func() bool { task, err := s.repo.Get(s.ctx, id); return err == nil && task.ProviderTaskID != nil })
				eventually(t, s.ctx, "child did not enter leased provider poll", func() bool { return s.fixture.job(id).Polls > 0 })
			}
			if phase == "archival" {
				eventually(t, s.ctx, "child did not partially write archive", func() bool {
					files, _ := filepath.Glob(filepath.Join(s.root, id, "attempts", "*", ".media-*.part"))
					for _, file := range files {
						if info, err := os.Stat(file); err == nil && info.Size() > 0 {
							return true
						}
					}
					return false
				})
			}
			var liveClaim bool
			if err := s.db.QueryRow(`SELECT worker_token IS NOT NULL AND worker_lease_until>clock_timestamp() FROM tasks WHERE id=$1`, id).Scan(&liveClaim); err != nil || !liveClaim {
				t.Fatalf("crash must interrupt live ownership: claim=%t err=%v", liveClaim, err)
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			if err = cmd.Wait(); err == nil {
				t.Fatal("child did not fail after SIGKILL")
			}
			killed = true
			crashedAt := time.Now()
			// Normal CI forces both lease deadlines past due. The opt-in performance
			// run also exercises the real 60-second deadline without modifying state.
			if os.Getenv("GATEWAY_REAL_LEASE_EXPIRY") != "1" {
				if _, err = s.db.Exec(`UPDATE tasks SET worker_lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				if err = s.redis.ZAdd(s.ctx, "media-gateway:{"+s.queueName+"}:v2:leases", redis.Z{Score: 0, Member: id}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			s.fixture.release()
			if phase == "archival" {
				close(s.fixture.archiveAfterChunk)
			}
			s.startWorkers(1, 20*time.Millisecond)
			task := s.terminal(id)
			t.Logf("worker recovery after SIGKILL: %s (real expiry=%t)", time.Since(crashedAt), os.Getenv("GATEWAY_REAL_LEASE_EXPIRY") == "1")
			if duringSubmit {
				if task.Status != core.TaskFailed || task.Error == nil || !strings.Contains(*task.Error, "not resubmitted") {
					t.Fatalf("ambiguous submission was repeated or hidden: %+v", task)
				}
			} else {
				s.checkFile(task)
			}
			if s.fixture.snapshot()[id].Submissions != 1 {
				t.Fatal("crash caused duplicate provider submission")
			}
			if phase == "archival" {
				files, err := filepath.Glob(filepath.Join(s.root, id, "attempts", "*", ".media-*.part"))
				if err != nil {
					t.Fatal(err)
				}
				// A hard kill cannot run deferred cleanup. Report remaining private
				// temporary files separately from the verified published result.
				t.Logf("hard archival crash: orphan temporary files=%d", len(files))
			}
		})
	}
}

func TestReliabilityWebhookRetriesAndSignatures(t *testing.T) {
	s := newStack(t, 30*time.Second)
	var mu sync.Mutex
	attempts := map[string]int{}
	payloads := map[string]string{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mac := hmac.New(sha256.New, []byte("fixture-webhook-secret"))
		mac.Write([]byte(r.Header.Get("X-Media-Gateway-Timestamp") + "."))
		mac.Write(body)
		if r.Header.Get("X-Media-Gateway-Signature") != "v1="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("invalid signature")
		}
		id := r.Header.Get("X-Media-Gateway-Delivery")
		if id == "" {
			t.Error("missing stable delivery ID")
		}
		mu.Lock()
		defer mu.Unlock()
		attempts[id]++
		if previous, ok := payloads[id]; ok && previous != string(body) {
			t.Error("retry changed event payload")
		}
		payloads[id] = string(body)
		if attempts[id] == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	// Keep normal Sender serialization and HMAC, but route its explicit test
	// hostname to our local HTTP receiver. Production DNS/SSRF tests are separate.
	transport.RegisterProtocol("https", roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "receiver.example.test" {
			return nil, fmt.Errorf("unexpected test egress %s", r.URL.Host)
		}
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = "http"
		u.Host = receiver.Listener.Addr().String()
		clone.URL = &u
		return receiver.Client().Transport.RoundTrip(clone)
	}))
	client := &http.Client{Transport: transport, Timeout: time.Second}
	defer transport.CloseIdleConnections()
	name := s.queueName + "-webhooks"
	q, err := queue.Open(s.ctx, s.redisURL, name)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	defer func() {
		keys, _ := s.redis.Keys(context.Background(), "media-gateway:{"+name+"}:*").Result()
		if len(keys) > 0 {
			s.redis.Del(context.Background(), keys...)
		}
	}()
	w := webhook.NewWorker(s.repo, q, webhook.NewSender("fixture-webhook-secret", client), 4, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	s.startWorkers(2, 20*time.Millisecond)
	for i := 0; i < 12; i++ {
		s.checkFile(s.terminal(s.submit("hook", fmt.Sprint(i), true)))
	}
	eventually(t, s.ctx, "webhooks never delivered", func() bool {
		var pending int
		err := s.db.QueryRow(`SELECT count(*) FROM webhook_deliveries WHERE status<>'delivered'`).Scan(&pending)
		return err == nil && pending == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 36 {
		t.Fatalf("event lifecycle incomplete: %d", len(attempts))
	}
	for id, n := range attempts {
		if n != 2 {
			t.Fatalf("delivery %s attempts=%d", id, n)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

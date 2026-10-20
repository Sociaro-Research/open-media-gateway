package reliability_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

// Cut live TCP connections and reject reconnects to simulate a dependency
// outage. The actual isolated service survives and is reachable after restore.
type faultProxy struct {
	listener    net.Listener
	target      string
	mu          sync.Mutex
	down        bool
	connections map[net.Conn]bool
	wg          sync.WaitGroup
}

func getServiceURL(t *testing.T, name string) string {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		t.Skip("requires isolated service")
	}
	return raw
}
func issueKey(t *testing.T, s *testStack, user string) string {
	t.Helper()
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Reliability", Scopes: []string{apikey.Generation, apikey.KeysManage}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = postgres.NewAPIKeyRepository(s.db).Create(s.ctx, user, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	return secret
}
func newAuthenticatedRequest(t *testing.T, s *testStack, method, path, body, key string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(s.ctx, method, s.api.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("User-Agent", "Sociaro-Gateway-Isolated-Reliability/1.0")
	return r
}
func newFaultProxy(t *testing.T, target string) *faultProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &faultProxy{listener: listener, target: target, connections: map[net.Conn]bool{}}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			in, err := listener.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				defer in.Close()
				p.mu.Lock()
				down := p.down
				p.mu.Unlock()
				if down {
					return
				}
				out, err := net.DialTimeout("tcp", target, time.Second)
				if err != nil {
					return
				}
				defer out.Close()
				p.mu.Lock()
				if p.down {
					p.mu.Unlock()
					return
				}
				p.connections[in] = true
				p.connections[out] = true
				p.mu.Unlock()
				defer func() { p.mu.Lock(); delete(p.connections, in); delete(p.connections, out); p.mu.Unlock() }()
				done := make(chan struct{})
				go func() { io.Copy(out, in); out.Close(); close(done) }()
				io.Copy(in, out)
				in.Close()
				<-done
			}()
		}
	}()
	t.Cleanup(func() { listener.Close(); p.cut(true); p.wg.Wait() })
	return p
}
func (p *faultProxy) cut(down bool) {
	p.mu.Lock()
	p.down = down
	connections := []net.Conn{}
	if down {
		for c := range p.connections {
			connections = append(connections, c)
		}
	}
	p.mu.Unlock()
	for _, c := range connections {
		c.Close()
	}
}

func TestReliabilityDependencyOutages(t *testing.T) {
	for _, env := range []string{"GATEWAY_TEST_REDIS_URL", "GATEWAY_TEST_DATABASE_URL"} {
		t.Run(env, func(t *testing.T) {
			raw := getServiceURL(t, env)
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			proxy := newFaultProxy(t, u.Host)
			u.Host = proxy.listener.Addr().String()
			t.Setenv(env, u.String())
			// A failed claim cleanup during an outage can leave the real 60-second
			// database lease intact. Recovery must be allowed to wait for expiry.
			s := newStack(t, 2*time.Minute)
			s.personalKey()
			s.fixture.ready = false
			s.startWorkers(1, 50*time.Millisecond)
			id := s.submit("outage", "existing", true)
			eventually(t, s.ctx, "provider ID not persisted before outage", func() bool { task, err := s.repo.Get(s.ctx, id); return err == nil && task.ProviderTaskID != nil })
			proxy.cut(true)
			status, _, err := s.request("GET", "/healthz", "", "")
			if err != nil || status != 503 {
				t.Fatalf("health hid dependency outage: %d %v", status, err)
			}
			status, _, err = s.request("POST", "/v1/generations", `{"model":"mock/test-video","prompt":"during-outage"}`, "during-outage")
			if err == nil && status == 202 {
				t.Fatal("admission bypassed unavailable dependency")
			}
			proxy.cut(false)
			eventually(t, s.ctx, "health did not recover", func() bool {
				status, _, err := s.request("GET", "/healthz", "", "")
				return err == nil && status == 200
			})
			s.fixture.release()
			var lease time.Time
			if err := s.db.QueryRow(`SELECT worker_lease_until FROM tasks WHERE id=$1 AND worker_lease_until IS NOT NULL`, id).Scan(&lease); err == nil {
				t.Logf("dependency restored; remaining task lease: %s", time.Until(lease))
			}
			s.checkFile(s.terminal(id))
			s.checkFile(s.terminal(s.submit("after-outage", "after", true)))
			if s.fixture.snapshot()[id].Submissions != 1 {
				t.Fatal("dependency outage repeated provider submission")
			}
		})
	}
}

func TestReliabilityReportsIsolationAndCancellation(t *testing.T) {
	s := newStack(t, 30*time.Second)
	user := s.personalKey()
	owner := identity.PersonalOwnerID(user.ID)
	seedHistory(t, s, 1000, owner)
	seedHistory(t, s, 100, uuid.NewString())
	base := "/v1/admin/reports?from=2020-01-01&to=2030-01-01"
	status, body, err := s.request("GET", base+"&view=summary&interval=month", "", "")
	var summary core.ReportSummary
	if err != nil || status != 200 || json.Unmarshal(body, &summary) != nil || summary.Requests != 1000 {
		t.Fatalf("summary included another owner or lost rows: status=%d requests=%d err=%v", status, summary.Requests, err)
	}
	status, body, err = s.request("GET", base+"&format=csv&limit=1&offset=900", "", "")
	rows, csvErr := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil || csvErr != nil || status != 200 || len(rows) != 1001 {
		t.Fatalf("CSV incomplete or cross-owner: status=%d rows=%d err=%v %v", status, len(rows), err, csvErr)
	}
	ctx, cancel := context.WithCancel(identity.WithOwnerID(s.ctx, owner))
	defer cancel()
	visited := 0
	err = s.repo.WalkCostReport(ctx, core.ReportFilter{From: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Until: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}, func(core.CostReportRow) error {
		visited++
		if visited == 3 {
			cancel()
		}
		return ctx.Err()
	})
	if err == nil || visited != 3 {
		t.Fatalf("cancelled report continued: rows=%d error=%v", visited, err)
	}
	eventually(t, s.ctx, "cancelled report retained DB connection", func() bool { return s.db.Stats().InUse == 0 })
	s.startWorkers(1, 20*time.Millisecond)
	s.checkFile(s.terminal(s.submit("after-cancel", "healthy", false)))
}

func TestReliabilityQuotasAcrossKeysAndUsers(t *testing.T) {
	s := newStack(t, 30*time.Second)
	user := s.personalKey()
	first := s.key
	// Both seconds and minute windows are configured; the minute user quota
	// must remain shared after issuing a fresh replacement key.
	if _, err := s.db.Exec(`UPDATE system_settings SET request_limits=request_limits||'{"key_generations_minute":100,"user_generations_minute":12,"key_generations_second":1000,"user_generations_second":1000}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	second := issueKey(t, s, user.ID)
	var wg sync.WaitGroup
	accepted, rejected := 0, 0
	var mu sync.Mutex
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := first
			if i%2 == 0 {
				key = second
			}
			r := newAuthenticatedRequest(t, s, "POST", "/v1/generations", `{"model":"mock/test-video","prompt":"quota"}`, key)
			response, err := s.api.Client().Do(r)
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			mu.Lock()
			defer mu.Unlock()
			switch response.StatusCode {
			case 202:
				accepted++
			case 429:
				rejected++
				if response.Header.Get("Retry-After") == "" {
					t.Error("quota denial omitted Retry-After")
				}
			default:
				t.Errorf("unexpected quota response %d", response.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	if accepted != 12 || rejected != 28 {
		t.Fatalf("quota race admitted=%d rejected=%d", accepted, rejected)
	}
	fresh := issueKey(t, s, user.ID)
	r := newAuthenticatedRequest(t, s, "POST", "/v1/generations", `{"model":"mock/test-video","prompt":"quota"}`, fresh)
	response, err := s.api.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 429 {
		t.Fatal("new key reset user quota")
	}
	other, err := postgres.NewAuthRepository(s.db).UpsertGoogleUser(s.ctx, "other-user", "other@example.test", "Other", "")
	if err != nil {
		t.Fatal(err)
	}
	s.key = issueKey(t, s, other.ID)
	s.submit("independent-user", "other", false)
}

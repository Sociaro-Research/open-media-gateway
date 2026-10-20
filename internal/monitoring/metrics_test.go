package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
)

type testDatabase struct{ err error }

func (d *testDatabase) PingContext(context.Context) error { return d.err }
func (d *testDatabase) Stats() sql.DBStats {
	return sql.DBStats{MaxOpenConnections: 20, OpenConnections: 3, InUse: 1, Idle: 2, WaitCount: 5, WaitDuration: time.Second}
}

type testQueue struct{ err error }

func (q *testQueue) Stats(context.Context) (queue.Stats, error) {
	return queue.Stats{Pending: 7, Claimed: 3, Expired: 1}, q.err
}

func scrape(t *testing.T, m *Metrics) string {
	t.Helper()
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("scrape: %d %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

func TestHTTPMetricsUsePatternsAndPreserveStatusAndStreaming(t *testing.T) {
	m := New("gateway", Dependencies{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/generations/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("response"))
		w.WriteHeader(503) // The actual response is already committed as 200.
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		w.WriteHeader(503)
	})
	mux.HandleFunc("GET /failed", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failed", 503) })
	mux.HandleFunc("GET /early", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(103)
		w.WriteHeader(201)
	})
	handler := m.InstrumentHTTP(mux)
	for i := range 20 {
		request := httptest.NewRequest("GET", fmt.Sprintf("/v1/generations/private-id-%d?key=private-secret", i), nil)
		request.Header.Set("Authorization", "Bearer private-secret")
		handler.ServeHTTP(httptest.NewRecorder(), request)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(fmt.Sprintf("PRIVATE-METHOD-%d", i), fmt.Sprintf("/private-path-%d", i), nil))
	}
	for _, path := range []string{"/stream", "/failed", "/early"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	body := scrape(t, m)
	for _, want := range []string{
		`media_gateway_http_requests_total{method="GET",route="GET /v1/generations/{id}",service="gateway",status="200"} 20`,
		`media_gateway_http_requests_total{method="OTHER",route="unmatched",service="gateway",status="404"} 20`,
		`media_gateway_http_requests_total{method="GET",route="GET /stream",service="gateway",status="200"} 1`,
		`media_gateway_http_requests_total{method="GET",route="GET /failed",service="gateway",status="503"} 1`,
		`media_gateway_http_requests_total{method="GET",route="GET /early",service="gateway",status="201"} 1`,
		`media_gateway_http_requests_in_flight{service="gateway"} 0`,
		`go_goroutines{service="gateway"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "private-") || strings.Contains(body, "PRIVATE-METHOD") {
		t.Fatal("request identity leaked into metrics")
	}
}

func TestDependencyFailuresOmitBacklogValues(t *testing.T) {
	db, q := &testDatabase{}, &testQueue{}
	var taskError error
	m := New("gateway", Dependencies{Database: db, Queues: map[string]Queue{"media": q}, Tasks: func(context.Context) (postgres.MonitoringStats, error) {
		return postgres.MonitoringStats{Tasks: map[string]postgres.ActiveTaskStats{"queued": {Count: 9, Due: 2, OldestOverdueSeconds: 60}}}, taskError
	}})
	body := scrape(t, m)
	for _, want := range []string{
		`media_gateway_dependency_up{dependency="postgres",service="gateway"} 1`,
		`media_gateway_dependency_up{dependency="redis_media",service="gateway"} 1`,
		`media_gateway_queue_jobs{queue="media",service="gateway",state="pending"} 7`,
		`media_gateway_active_tasks{service="gateway",status="queued"} 9`,
		`media_gateway_database_connection_wait_seconds_total{service="gateway"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	db.err, q.err, taskError = errors.New("database down"), errors.New("redis down"), errors.New("query timeout")
	body = scrape(t, m)
	for _, want := range []string{
		`media_gateway_dependency_up{dependency="postgres",service="gateway"} 0`,
		`media_gateway_dependency_up{dependency="redis_media",service="gateway"} 0`,
		`media_gateway_collector_success{collector="tasks",service="gateway"} 0`,
		`media_gateway_collector_success{collector="queue_media",service="gateway"} 0`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, "media_gateway_queue_jobs{") || strings.Contains(body, "media_gateway_active_tasks{") {
		t.Fatal("failed snapshot emitted misleading backlog values")
	}
}

func TestWorkerProgressAndClaimOutcomes(t *testing.T) {
	m := New("worker", Dependencies{})
	m.SetWorkerDispatchInterval(2 * time.Minute)
	m.ObserveWorkerLoop("dispatcher", nil)
	m.ObserveWorkerLoop("consumer", errors.New("redis down"))
	finish := m.WorkerClaimStarted()
	if !strings.Contains(scrape(t, m), `media_gateway_worker_active_claims{service="worker"} 1`) {
		t.Fatal("active claim not visible")
	}
	finish(errors.New("claim failed"))
	m.WorkerClaimStarted()(nil)
	body := scrape(t, m)
	for _, want := range []string{
		`media_gateway_worker_active_claims{service="worker"} 0`,
		`media_gateway_worker_loop_stale_after_seconds{loop="dispatcher",service="worker"} 360`,
		`media_gateway_worker_loop_errors_total{loop="consumer",service="worker"} 1`,
		`media_gateway_worker_loop_last_success_timestamp_seconds{loop="consumer",service="worker"} 0`,
		`media_gateway_worker_claims_total{outcome="error",service="worker"} 1`,
		`media_gateway_worker_claims_total{outcome="success",service="worker"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
}

func TestDedicatedListenerRoutesBindFailureAndShutdown(t *testing.T) {
	m := New("gateway", Dependencies{})
	server, err := m.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	if other, err := m.Listen(server.server.Addr); err == nil {
		other.Close()
		t.Fatal("metrics bind failure ignored")
	}
	client := &http.Client{Timeout: time.Second}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"GET", "/metrics", 200}, {"GET", "/v1/info", 404}, {"POST", "/metrics", 405}} {
		r, _ := http.NewRequest(tc.method, "http://"+server.server.Addr+tc.path, nil)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Errorf("%s %s: %d", tc.method, tc.path, response.StatusCode)
		}
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if response, err := client.Get("http://" + server.server.Addr + "/metrics"); err == nil {
		response.Body.Close()
		t.Fatal("metrics listener survived shutdown")
	}
}

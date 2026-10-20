package reliability_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestReliabilityLostAdmissionResponseIsSafeToRetry(t *testing.T) {
	s := newStack(t, 35*time.Second)
	s.personalKey()
	s.startWorkers(2, 50*time.Millisecond)
	target, err := url.Parse(s.api.URL)
	if err != nil {
		t.Fatal(err)
	}
	lost := errors.New("discard committed admission response")
	accepted := make(chan string, 1)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ModifyResponse = func(response *http.Response) error {
		if response.StatusCode != http.StatusAccepted {
			return errors.New("upstream did not accept admission")
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return err
		}
		var task struct{ ID string }
		if err := json.Unmarshal(body, &task); err != nil || task.ID == "" {
			return errors.New("upstream accepted response has no task ID")
		}
		accepted <- task.ID
		return lost
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if !errors.Is(err, lost) {
			t.Error(err)
		}
		// The backend response is complete, so PostgreSQL has committed the task.
		// Close the client socket before forwarding any status or body bytes.
		connection, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}
	server := httptest.NewServer(proxy)
	defer server.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true // Prevent implicit transport retry from masking the lost response.
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request, err := http.NewRequestWithContext(s.ctx, http.MethodPost, server.URL+"/v1/generations", strings.NewReader(`{"model":"mock/test-video","prompt":"lost-response"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+s.key)
	request.Header.Set("Idempotency-Key", "lost-response")
	request.Header.Set("User-Agent", "Sociaro-Gateway-Isolated-Reliability/1.0")
	response, err := client.Do(request)
	if response != nil {
		response.Body.Close()
	}
	if err == nil {
		t.Fatal("proxy did not lose the admission response")
	}
	var originalID string
	select {
	case originalID = <-accepted:
	case <-s.ctx.Done():
		t.Fatal("task was not committed before response loss")
	}
	if retryID := s.submit("lost-response", "lost-response", false); retryID != originalID {
		t.Fatalf("retry created another task: original=%s retry=%s", originalID, retryID)
	}
	s.checkFile(s.terminal(originalID))
	var tasks int
	if err := s.db.QueryRowContext(s.ctx, "SELECT count(*) FROM tasks WHERE prompt='lost-response'").Scan(&tasks); err != nil || tasks != 1 || s.fixture.job(originalID).Submissions != 1 {
		t.Fatalf("response loss duplicated durable work: tasks=%d submissions=%d err=%v", tasks, s.fixture.job(originalID).Submissions, err)
	}
}

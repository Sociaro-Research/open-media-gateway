package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type timeoutObservingTransport struct {
	next    http.RoundTripper
	observe func(*http.Request)
}

func (transport timeoutObservingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.observe(request)
	return transport.next.RoundTrip(request)
}

func TestPerRequestTimeoutIncludesHeadersAndBodyWithoutRetries(t *testing.T) {
	const watchdog = 5 * time.Second
	var calls atomic.Int32
	canceled := make(chan string, 8)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/body" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
		}
		if label := r.Header.Get("X-Test-Cancel"); label != "" {
			// Negative cases have no timer that can race cancellation to success.
			<-r.Context().Done()
			canceled <- label
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(120 * time.Millisecond):
		}
		_, _ = w.Write([]byte("done"))
	}))
	defer server.Close()
	defer server.CloseClientConnections()
	client := server.Client()
	client.Timeout = 30 * time.Millisecond
	type observation struct {
		at, deadline time.Time
		hasDeadline  bool
		method, path string
	}
	observed := make(chan observation, 8)
	client.Transport = timeoutObservingTransport{next: client.Transport, observe: func(request *http.Request) {
		deadline, ok := request.Context().Deadline()
		observed <- observation{time.Now(), deadline, ok, request.Method, request.URL.Path}
	}}
	type result struct {
		body []byte
		err  error
	}
	run := func(request *http.Request) (result, observation) {
		t.Helper()
		requestContext, cancel := context.WithCancel(request.Context())
		t.Cleanup(cancel)
		request = request.WithContext(requestContext)
		completed := make(chan result, 1)
		go func() {
			response, err := Do(client, request)
			var body []byte
			if err == nil {
				body, err = io.ReadAll(response.Body)
				response.Body.Close()
			}
			completed <- result{body, err}
		}()
		timer := time.NewTimer(watchdog)
		defer timer.Stop()
		select {
		case value := <-completed:
			select {
			case trace := <-observed:
				if !trace.hasDeadline || trace.method != "POST" || trace.path != request.URL.Path {
					t.Fatalf("unexpected transport context: %+v", trace)
				}
				return value, trace
			default:
				t.Fatal("request did not reach the observing transport")
			}
		case <-timer.C:
			cancel()
			server.CloseClientConnections()
			t.Fatal("request did not finish before the independent watchdog")
		}
		return result{}, observation{}
	}
	assertCanceled := func(label string, before int32) {
		t.Helper()
		timer := time.NewTimer(watchdog)
		defer timer.Stop()
		select {
		case got := <-canceled:
			if got != label {
				t.Fatalf("canceled request = %q, want %q", got, label)
			}
		case <-timer.C:
			t.Fatal("server did not observe request cancellation")
		}
		if calls.Load()-before != 1 {
			t.Fatal("request was missing or retried")
		}
	}
	for _, path := range []string{"/headers", "/body"} {
		r, _ := http.NewRequestWithContext(WithRequestTimeout(context.Background(), time.Second), "POST", server.URL+path, nil)
		started := time.Now()
		value, trace := run(r)
		if value.err != nil || string(value.body) != "done" {
			t.Fatal(string(value.body), value.err)
		}
		// Bracket the configured deadline, rather than elapsed scheduling time.
		if trace.deadline.Before(started.Add(time.Second)) || trace.deadline.After(trace.at.Add(time.Second)) {
			t.Fatal("generation override did not install the one-second deadline")
		}
		r, _ = http.NewRequestWithContext(WithRequestTimeout(context.Background(), 20*time.Millisecond), "POST", server.URL+path, nil)
		r.Header.Set("X-Test-Cancel", path)
		before := calls.Load()
		started = time.Now()
		value, trace = run(r)
		if !errors.Is(value.err, context.DeadlineExceeded) {
			t.Fatalf("expected request deadline, got %v", value.err)
		}
		if trace.deadline.Before(started.Add(20*time.Millisecond)) || trace.deadline.After(trace.at.Add(20*time.Millisecond)) {
			t.Fatal("generation override did not install the twenty-millisecond deadline")
		}
		assertCanceled(path, before)
	}
	if client.Timeout != 30*time.Millisecond {
		t.Fatal("shared client changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r, _ := http.NewRequestWithContext(WithRequestTimeout(ctx, time.Second), "POST", server.URL+"/headers", nil)
	r.Header.Set("X-Test-Cancel", "caller")
	value, trace := run(r)
	if !errors.Is(value.err, context.DeadlineExceeded) {
		t.Fatalf("expected earlier caller deadline, got %v", value.err)
	}
	callerDeadline, _ := ctx.Deadline()
	if !trace.deadline.Equal(callerDeadline) {
		t.Fatal("earlier caller deadline changed")
	}
}

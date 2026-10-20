package httpclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestPerRequestTimeoutIncludesHeadersAndBodyWithoutRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/body" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(120 * time.Millisecond):
		}
		_, _ = w.Write([]byte("done"))
	}))
	defer server.Close()
	client := server.Client()
	client.Timeout = 30 * time.Millisecond
	for _, path := range []string{"/headers", "/body"} {
		r, _ := http.NewRequestWithContext(WithRequestTimeout(context.Background(), time.Second), "POST", server.URL+path, nil)
		response, err := Do(client, r)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(b) != "done" {
			t.Fatal(string(b), err)
		}
		r, _ = http.NewRequestWithContext(WithRequestTimeout(context.Background(), 20*time.Millisecond), "POST", server.URL+path, nil)
		before := calls.Load()
		response, err = Do(client, r)
		if err == nil {
			_, err = io.ReadAll(response.Body)
			response.Body.Close()
		}
		if err == nil {
			t.Fatal("deadline ignored")
		}
		if calls.Load()-before != 1 {
			t.Fatal("request retried")
		}
	}
	if client.Timeout != 30*time.Millisecond {
		t.Fatal("shared client changed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r, _ := http.NewRequestWithContext(WithRequestTimeout(ctx, time.Second), "POST", server.URL+"/headers", nil)
	if _, err := Do(client, r); err == nil {
		t.Fatal("caller deadline ignored")
	}
}

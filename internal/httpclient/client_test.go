package httpclient

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPollingWavesReuseConnections(t *testing.T) {
	const clients = 16
	started := make(chan struct{}, clients)
	release := make(chan struct{})
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
			w.Write([]byte("ok"))
		case <-r.Context().Done():
		}
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client := New(5 * time.Second)
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for wave := 0; wave < 2; wave++ {
		var requests sync.WaitGroup
		for i := 0; i < clients; i++ {
			requests.Add(1)
			go func() {
				defer requests.Done()
				r, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
				response, err := client.Do(r)
				if err != nil {
					t.Error(err)
					return
				}
				_, err = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if err != nil {
					t.Error(err)
				}
			}()
		}
		for i := 0; i < clients; i++ {
			select {
			case <-started:
			case <-ctx.Done():
				close(release)
				requests.Wait()
				t.Fatal(ctx.Err())
			}
		}
		close(release)
		requests.Wait()
		if n := connections.Load(); n != clients {
			t.Fatalf("polling wave %d opened %d connections; wanted reuse of %d", wave, n, clients)
		}
		release = make(chan struct{})
	}
}

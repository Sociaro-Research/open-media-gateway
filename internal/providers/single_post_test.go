package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
)

// Lose the response on a pooled TLS connection after the peer has read the POST.
func TestMiniMaxSinglePOSTLostReply(t *testing.T) {
	routes := minimax.Definition().SinglePOSTRoutes
	for _, protocol := range []int{1, 2} {
		for _, path := range routes.Paths {
			t.Run(fmt.Sprintf("http%d%s", protocol, path), func(t *testing.T) {
				const body = "{ \"prompt\": \"fixture\" }"
				var posts atomic.Int32
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && r.URL.Path == "/warm" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					b, err := io.ReadAll(r.Body)
					if err != nil || r.ProtoMajor != protocol || r.Method != http.MethodPost || r.URL.Path != path || r.Host != "api.minimax.io" || string(b) != body {
						t.Error("changed destination, method or body")
						w.WriteHeader(400)
						return
					}
					posts.Add(1)
					panic(http.ErrAbortHandler) // HTTP/1 closes the connection, HTTP/2 resets the stream.
				}))
				server.EnableHTTP2 = protocol == 2
				server.StartTLS()
				defer server.Close()
				tr := server.Client().Transport.(*http.Transport).Clone()
				tr.TLSClientConfig.ServerName = "127.0.0.1"
				tr.Proxy = nil
				tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
				}
				defer tr.CloseIdleConnections()
				for _, hint := range []bool{false, true} {
					c := &providerkit.Connection{}
					if hint {
						value := "fixture"
						c.Headers = []providerkit.Header{{Operation: "generate", Name: "Idempotency-Key", Value: &value}}
					}
					inner := roundTripFunc(func(r *http.Request) (*http.Response, error) {
						if r.Method == "POST" && r.GetBody != nil {
							t.Fatal("configured native POST is rewindable")
						}
						return tr.RoundTrip(r)
					})
					client := configurationClient(&http.Client{Transport: inner}, Credentials{BaseURL: routes.Origin, Connection: c}, routes)
					resp, err := client.Get(routes.Origin + "/warm")
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
					req, err := http.NewRequest(http.MethodPost, routes.Origin+path, strings.NewReader(body))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Content-Type", "application/json")
					before := posts.Load()
					resp, err = routes.Do(client, req, path, 0)
					if resp != nil {
						resp.Body.Close()
					}
					want := int32(1)
					if hint {
						want = 0
					}
					if err == nil || errors.Is(err, httpclient.ErrSinglePOSTConfiguration) != hint || posts.Load()-before != want {
						t.Fatalf("replay header=%t: received %d POSTs, error=%v", hint, posts.Load()-before, err)
					}
					t.Logf("replay header=%t, complete POSTs=%d", hint, posts.Load()-before)
				}
			})
		}
	}
}

func TestMiniMaxSinglePOSTConnectionOverrides(t *testing.T) {
	routes := minimax.Definition().SinglePOSTRoutes
	path := routes.Paths[0]
	value := "fixture"
	for _, tc := range []struct {
		name, method, path string
		conn               providerkit.Connection
	}{
		{"other origin", "POST", path, providerkit.Connection{Endpoints: map[string]string{"generate": "https://fixture.invalid" + path}}},
		{"other route", "POST", path, providerkit.Connection{Endpoints: map[string]string{"generate": routes.Paths[1]}}},
		{"legacy into native", "POST", "/v1/image_generation", providerkit.Connection{Endpoints: map[string]string{"generate": path}}},
		{"extra body", "POST", path, providerkit.Connection{ExtraBody: map[string]any{"n": 10}}},
		{"global header", "POST", path, providerkit.Connection{Headers: []providerkit.Header{{Name: "X-Fixture", Value: &value}}}},
		{"wrong method", "GET", path, providerkit.Connection{}},
		{"query", "POST", path + "?test=1", providerkit.Connection{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid call reached transport")
				return nil, nil
			})
			client := configurationClient(&http.Client{Transport: base}, Credentials{BaseURL: routes.Origin, Connection: &tc.conn}, routes)
			req, err := http.NewRequest(tc.method, routes.Origin+tc.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if _, err = client.Do(req); !errors.Is(err, httpclient.ErrSinglePOSTConfiguration) {
				t.Fatal("missing route rejection", err)
			}
		})
	}
}

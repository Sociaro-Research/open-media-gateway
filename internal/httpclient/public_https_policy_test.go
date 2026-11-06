package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicClientHTTPSClassificationPreservesPermittedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, scheme, host string
		trusted            []string
		blocked            bool
	}{
		{"authenticated HTTPS", "https", "storage.example", nil, false},
		{"exact trusted HTTP", "http", "trusted.internal", []string{"TRUSTED.INTERNAL."}, false},
		{"untrusted HTTP", "http", "other.example", []string{"trusted.internal"}, true},
		{"subdomain HTTP", "http", "child.trusted.internal", []string{"trusted.internal"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits, lookups, dials atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != "synthetic-body" || r.Header.Get("Authorization") != "synthetic-credential" {
					t.Error("permitted request changed")
				}
				w.WriteHeader(http.StatusNoContent)
			})
			var server *httptest.Server
			if tc.scheme == "https" {
				server = httptest.NewTLSServer(handler)
			} else {
				server = httptest.NewServer(handler)
			}
			defer server.Close()
			client := newPublic(time.Second, tc.trusted, func(context.Context, string, string) ([]netip.Addr, error) {
				lookups.Add(1)
				ip := "127.0.0.1"
				if tc.scheme == "https" {
					ip = "8.8.8.8"
				}
				return []netip.Addr{netip.MustParseAddr(ip)}, nil
			}, func(ctx context.Context, network, _ string) (net.Conn, error) {
				dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			})
			defer client.CloseIdleConnections()
			if tc.scheme == "https" {
				// Only this controlled loopback TLS fixture bypasses certificate validation.
				client.Transport.(*publicTransport).base.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
			}
			request, _ := http.NewRequest("POST", tc.scheme+"://"+tc.host+"/", strings.NewReader("synthetic-body"))
			request.Header.Set("Authorization", "synthetic-credential")
			response, err := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			if tc.blocked {
				if !errors.Is(err, ErrOutboundHTTPSRequired) || lookups.Load() != 0 || dials.Load() != 0 || hits.Load() != 0 {
					t.Fatalf("untrusted HTTP policy changed: err=%v lookup=%d dial=%d hit=%d", err, lookups.Load(), dials.Load(), hits.Load())
				}
			} else if err != nil || response.StatusCode != http.StatusNoContent || lookups.Load() != 1 || dials.Load() != 1 || hits.Load() != 1 {
				t.Fatalf("permitted request failed: err=%v lookup=%d dial=%d hit=%d", err, lookups.Load(), dials.Load(), hits.Load())
			}
		})
	}
}

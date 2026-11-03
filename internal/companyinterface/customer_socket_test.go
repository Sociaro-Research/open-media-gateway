package companyinterface

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"github.com/google/uuid"
)

// The server consumes and commits the mutation, then closes an already reused
// socket before any response bytes. Failure occurs inside net/http.Transport,
// unlike a fixture which replaces a fully received response body afterward.
func TestCompanyCustomerSocketLossDoesNotReplayMutation(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"generation with idempotency", "POST", "/v1/generations", `{"model":"mock/fixture","prompt":"socket fixture"}`, 202},
		{"empty reference delete", "DELETE", "/v1/references/" + uuid.NewString(), "", 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var reused atomic.Bool
			id := uuid.NewString()
			company := uuid.NewString()
			secret, _ := NewSecret()
			wireKey := "stable-socket-operation"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/warm" {
					w.WriteHeader(204)
					return
				}
				raw, e := io.ReadAll(r.Body)
				if e != nil || string(raw) != tc.body {
					t.Error("server did not consume exact payload", e)
				}
				if r.URL.Path != CustomerSDKPrefix+strings.TrimPrefix(tc.path, "/") || r.Method != tc.method {
					t.Error("unexpected mutation route")
				}
				if tc.method == "POST" && r.Header.Get("Idempotency-Key") != wireKey {
					t.Error("wire idempotency key lost")
				}
				if calls.Add(1) == 1 {
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				}
				// A deliberate later caller retry recovers the already committed object.
				w.Header().Set("Content-Type", "application/json")
				if tc.status == 202 {
					w.Header().Set("Location", "/v1/generations/"+id)
				}
				w.WriteHeader(tc.status)
				if tc.status == 202 {
					json.NewEncoder(w).Encode(map[string]any{"id": id, "object": "generation", "organization_id": company, "status_url": "/v1/generations/" + id})
				}
			}))
			defer server.Close()
			// This explicit loopback exception exists only in the synthetic fixture.
			h := httpclient.NewPublic(5*time.Second, "127.0.0.1")
			defer h.CloseIdleConnections()
			warm, e := h.Get(server.URL + "/warm")
			if e != nil {
				t.Fatal(e)
			}
			io.Copy(io.Discard, warm.Body)
			warm.Body.Close()
			c := &Client{origin: server.URL, http: h}
			prepared := CustomerContext{snapshot: InstallationSnapshot{Installation: LocalInstallation{Company: company, GrantID: uuid.NewString(), CentralOrigin: server.URL, LocalOrigin: "https://local.example", Credential: secret, GrantEpoch: 1}}}
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
				if info.Reused {
					reused.Store(true)
				}
			}})
			headers := http.Header{"Content-Type": []string{"application/json"}, "Idempotency-Key": []string{wireKey}}
			_, e = c.customer(ctx, prepared, tc.method, tc.path, "", []byte(tc.body), headers, true, "gw_"+strings.Repeat("k", 43))
			if !reused.Load() {
				t.Fatal("fixture did not exercise a reused socket")
			}
			if calls.Load() != 1 || !errors.Is(e, ErrUnavailable) {
				t.Fatalf("uncertain committed mutation was automatically replayed: calls=%d err=%v", calls.Load(), e)
			}
			result, e := c.customer(context.Background(), prepared, tc.method, tc.path, "", []byte(tc.body), headers, true, "gw_"+strings.Repeat("k", 43))
			if e != nil || result.Status != tc.status || calls.Load() != 2 {
				t.Fatal("explicit caller recovery failed", e, calls.Load())
			}
			if tc.status == 202 && result.Header.Get("Location") != LocalCustomerPrefix+"v1/generations/"+id {
				t.Fatal("recovery changed committed object")
			}
		})
	}
}

// Current empty-body customer mutations do not admit either idempotency header.
// Exercise the shared production request builder with both net/http replay
// triggers anyway, so a future empty mutation cannot reintroduce this hole.
func TestCompanyCustomerEmptyMutationBodyBlocksBothReplayHeaders(t *testing.T) {
	for _, header := range []string{"Idempotency-Key", "X-Idempotency-Key"} {
		t.Run(header, func(t *testing.T) {
			var calls atomic.Int32
			var reused atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/warm" {
					w.WriteHeader(204)
					return
				}
				raw, e := io.ReadAll(r.Body)
				if e != nil || len(raw) != 0 || r.Header.Get(header) != "stable-empty-operation" {
					t.Error("empty wire payload/key changed", e)
				}
				if calls.Add(1) == 1 {
					conn, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					conn.Close()
					return
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			client := httpclient.NewPublic(5*time.Second, "127.0.0.1")
			defer client.CloseIdleConnections()
			warm, e := client.Get(server.URL + "/warm")
			if e != nil {
				t.Fatal(e)
			}
			io.Copy(io.Discard, warm.Body)
			warm.Body.Close()
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
				if info.Reused {
					reused.Store(true)
				}
			}})
			request, e := newCustomerRequest(ctx, "POST", server.URL+"/synthetic-empty-mutation", nil)
			if e != nil || request.Body == nil || request.Body == http.NoBody || request.GetBody != nil {
				t.Fatal("empty mutation became replayable", e)
			}
			request.Header.Set(header, "stable-empty-operation")
			response, e := client.Do(request)
			if response != nil {
				response.Body.Close()
			}
			if !reused.Load() || calls.Load() != 1 || e == nil {
				t.Fatalf("empty mutation replayed: reused=%v calls=%d err=%v", reused.Load(), calls.Load(), e)
			}
		})
	}
}

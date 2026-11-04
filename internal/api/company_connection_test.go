package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
)

type companyRoundTrip func(*http.Request) (*http.Response, error)

func (f companyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCompanyConnectionExistingAPITransport(t *testing.T) {
	const secret = "synthetic-company-secret-not-live"
	const local = "https://local.example.test"
	calls := 0
	var captured *http.Request
	var body string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		captured = r.Clone(context.Background())
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		if r.Header.Get("Authorization") != "Bearer "+secret || r.Header.Get("Cookie") != "" || r.Header.Get("X-Forwarded-User") != "" || r.Header.Get("X-Company-Context") != "" {
			t.Error("forwarded wrong authority")
		}
		if r.URL.Path == "/v1/redirect" {
			w.Header().Set("Location", "https://other.example.test/resource")
			w.WriteHeader(302)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Range", "bytes 0-2/3")
		w.Header().Set("Set-Cookie", "untrusted=1")
		w.WriteHeader(206)
		w.Write([]byte("abc"))
	}))
	defer upstream.Close()
	transport := upstream.Client().Transport
	client := &http.Client{Transport: companyRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "console.sociaro.ai" {
			t.Fatalf("unexpected destination %s", r.URL.Host)
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Host = strings.TrimPrefix(upstream.URL, "https://")
		copy.URL = &u
		return transport.RoundTrip(copy)
	})}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("f", 32))
	encrypted, _ := cipher.Encrypt([]byte(secret))
	hash := sha256.Sum256([]byte(secret))
	record := remote.Record{Access: wa.CompanyAccess, Owner: "alice", Version: 1, Active: true, Origin: installation.DefaultCentralOrigin, OrganizationID: "company", TokenHash: hash[:], Ciphertext: encrypted}
	repo := &remoteFixture{records: map[string]remote.Record{"alice": record}}
	s, _ := newPersonalTestServer()
	policy := &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true, "bob": true}}
	s.system = policy
	s.remoteWorkspaceManager = remote.New(repo, cipher, client)
	h, err := WithPublicAPI(s.handler(), local, local, "")
	if err != nil {
		t.Fatal(err)
	}
	send := func(method, path, actor, token, origin, contextID, payload string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, local+path, strings.NewReader(payload))
		if actor != "" {
			r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if contextID != "" {
			r.Header.Set("X-Company-Context", contextID)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "same-operation")
		r.Header.Set("Range", "bytes=0-2")
		r.Header.Set("X-Forwarded-User", "forged")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := send("GET", "/auth/company-connection", "alice", "", "", "", "")
	var result struct {
		Connected bool
		Key       string
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || !result.Connected || result.Key != secret || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("bootstrap", w.Code, w.Body)
	}
	for _, actor := range []string{"", "bob"} {
		w = send("GET", "/auth/company-connection", actor, "", "", "", "")
		if strings.Contains(w.Body.String(), secret) || actor == "" && w.Code != 401 {
			t.Fatal("owner isolation", actor, w.Code, w.Body)
		}
	}
	w = send("PATCH", "/auth/company-api/v1/organizations/company?filter=a%2Fb&x=1&x=2", "alice", secret, local, record.SnapshotID(), `{"name":"central"}`)
	if w.Code != 206 || w.Body.String() != "abc" || calls != 1 || body != `{"name":"central"}` || captured.URL.RawQuery != "filter=a%2Fb&x=1&x=2" || captured.Header.Get("Idempotency-Key") != "same-operation" || captured.Header.Get("Range") != "bytes=0-2" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("transport", w.Code, w.Body, calls, body)
	}
	for _, tc := range []struct{ name, actor, key, origin, contextID, path string }{
		{"other owner", "bob", secret, local, record.SnapshotID(), "/v1/models"}, {"wrong key", "alice", "foreign", local, record.SnapshotID(), "/v1/models"}, {"cross origin", "alice", secret, "https://evil.example", record.SnapshotID(), "/v1/models"}, {"stale", "alice", secret, local, "stale", "/v1/models"}, {"auth path", "alice", secret, local, record.SnapshotID(), "/auth/logout"}, {"local control", "alice", secret, local, record.SnapshotID(), "/v1/system/remote-workspace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls
			w := send("POST", "/auth/company-api"+tc.path, tc.actor, tc.key, tc.origin, tc.contextID, `{}`)
			if w.Code < 400 || calls != before {
				t.Fatal(w.Code, w.Body, calls)
			}
		})
	}
	w = send("GET", "/auth/company-api/v1/models/provider%2Fmodel?signature=a%2Bb", "alice", secret, "", record.SnapshotID(), "")
	if w.Code != 206 || captured.URL.EscapedPath() != "/v1/models/provider%2Fmodel" {
		t.Fatal("encoded identifier", w.Code, captured.URL)
	}
	before := calls
	w = send("GET", "/auth/company-api/v1/redirect", "alice", secret, "", record.SnapshotID(), "")
	if w.Code != 302 || calls != before+1 {
		t.Fatal("redirect followed", w.Code, calls)
	}
	record.Active = false
	repo.records["alice"] = record
	before = calls
	w = send("GET", "/auth/company-api/v1/models", "alice", secret, "", "old", "")
	if w.Code < 400 || calls != before {
		t.Fatal("disconnected fallback", w.Code, calls)
	}
}

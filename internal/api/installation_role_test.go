package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInstallationRolePublicMetadata(t *testing.T) {
	for _, tc := range []struct{ name, api, own, host, want string }{
		{"central trusted UI origin", "https://api.fixture.test", "https://console.sociaro.ai", "api.fixture.test", "central"},
		{"canonical central origin", "https://api.fixture.test", "https://CONSOLE.SOCIARO.AI:443/", "api.fixture.test", "central"},
		{"effective default port routing", "https://api.fixture.test:443", "https://CONSOLE.SOCIARO.AI:443/", "api.fixture.test:443", "central"},
		{"private deployment is ordinary", "https://api.fixture.test", "https://private.fixture.test", "api.fixture.test", "ordinary"},
		{"other HTTPS port", "https://api.fixture.test", "https://console.sociaro.ai:8443", "api.fixture.test", "ordinary"},
		{"missing configured origin", "", "", "console.sociaro.ai", "ordinary"},
		{"loopback development", "http://localhost:8080", "http://localhost:8088", "localhost:8080", "ordinary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, err := WithPublicAPI(http.NotFoundHandler(), tc.api, tc.own, "")
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("GET", "https://"+tc.host+"/site.json", nil)
			req.Header.Set("X-Forwarded-Host", "console.sociaro.ai")
			req.Header.Set("Forwarded", "host=console.sociaro.ai;proto=https")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			var meta struct {
				Role    string `json:"installation_role"`
				Central string `json:"central_origin"`
			}
			if err = json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
				t.Fatal(err)
			}
			if meta.Role != tc.want || meta.Central != "https://console.sociaro.ai" {
				t.Fatalf("wrong trusted installation metadata: role=%q central=%q", meta.Role, meta.Central)
			}
		})
	}
}

func TestCentralCompaniesOnlyProfileAndMetadata(t *testing.T) {
	s, keyStore := newPersonalTestServer()
	original := system.Defaults()
	f := &setupFixture{systemFixture: &systemFixture{settings: original, admins: map[string]bool{"alice": true}}}
	s.system = f
	s.storageAdmin = &storageAdminStub{}
	h, err := WithPublicAPI(s.handler(), "https://api.fixture.test", "https://console.sociaro.ai", "")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body string) *httptest.ResponseRecorder {
		return sendTest(h, method, "https://api.fixture.test"+path, body, "alice", "")
	}
	for _, body := range []string{`{"profile":"personal","version":1}`, `{"profile":"sociaro","version":1}`, `{"profile":"operator","allow_personal":true,"version":1}`} {
		if w := call("POST", "/v1/system/setup-profile", body); w.Code != 403 || f.writes != 0 {
			t.Fatal("central mode bypass", w.Code)
		}
	}
	if f.settings != original {
		t.Fatal("role detection mutated stored settings")
	}
	w := call("GET", "/v1/system/settings", "")
	var meta struct {
		Role    string `json:"installation_role"`
		Central string `json:"central_origin"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &meta); err != nil || meta.Role != "central" || meta.Central != "https://console.sociaro.ai" {
		t.Fatal("settings metadata missing", err)
	}
	info := call("GET", "/v1/info", "")
	var public struct {
		Console struct {
			Role     string `json:"installation_role"`
			Central  string `json:"central_origin"`
			Personal bool   `json:"personal_providers"`
		} `json:"console"`
	}
	if err = json.Unmarshal(info.Body.Bytes(), &public); err != nil || info.Code != 200 || public.Console.Role != "central" || public.Console.Personal || public.Console.Central != "https://console.sociaro.ai" {
		t.Fatal("console role metadata or effective policy missing", err)
	}
	w = call("POST", "/v1/system/setup-profile", `{"profile":"operator","version":1}`)
	if w.Code != 200 || f.writes != 1 || !f.settings.SharedResources || f.settings.PersonalProviders {
		t.Fatal("central company setup failed", w.Code, w.Body)
	}
	if err = json.Unmarshal(w.Body.Bytes(), &meta); err != nil || meta.Role != "central" {
		t.Fatal("profile reply lost role", err)
	}
	for _, body := range []string{`{"generation_ui":true,"shared_resources":false,"version":2}`, `{"generation_ui":true,"personal_providers":true,"version":2}`} {
		if w = call("PUT", "/v1/system/settings", body); w.Code != 403 || f.writes != 1 {
			t.Fatal("direct settings bypassed companies-only mode", w.Code)
		}
	}
	for _, path := range []string{"/v1/admin/providers", "/v1/admin/storage"} {
		if w = call("GET", path, ""); w.Code != 403 {
			t.Fatal("personal provider/storage mode accepted", path, w.Code)
		}
	}
	// Existing unbound shared keys are preserved, but cannot start central work.
	legacy, secret, err := apikey.Generate(apikey.CreateInput{Name: "Legacy unbound shared key", UserID: "alice", ResourceMode: "shared", StorageMode: "shared"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = keyStore.Create(context.Background(), "alice", legacy, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body string }{{"GET", "/v1/models", ""}, {"POST", "/v1/generations", `{"prompt":"No unbound work","model":"mock/mock-video-v1"}`}} {
		w = sendTest(h, tc.method, "https://api.fixture.test"+tc.path, tc.body, "", secret)
		if w.Code != 403 {
			t.Fatal("legacy shared key bypassed company binding", tc.path, w.Code)
		}
	}
	if w = sendTest(h, "GET", "https://api.fixture.test/v1/generations", "", "", secret); w.Code != 200 {
		t.Fatal("historical personal reads lost", w.Code)
	}
	if _, _, err = keyStore.Authenticate(context.Background(), apikey.Hash(secret)); err != nil {
		t.Fatal("role change revoked existing key", err)
	}
	for _, path := range []string{"/v1/system/remote-workspace", "/v1/system/remote-workspace/authorization", "/v1/system/remote-workspace/authorization/poll", "/v1/system/remote-workspace/proxy/v1/generations"} {
		if w = call("POST", path, `{}`); w.Code != 403 {
			t.Fatal("central accepted remote-client operation", path, w.Code)
		}
	}
	if w = call("POST", "/v1/api-keys", `{"name":"No personal central key"}`); w.Code != 403 {
		t.Fatal("central issued an unbound personal key", w.Code)
	}
	// Role is configuration. User JSON cannot assert it, even for an administrator.
	if w = call("PUT", "/v1/system/settings", `{"generation_ui":true,"installation_role":"ordinary","version":2}`); w.Code != 400 {
		t.Fatal("client supplied role was accepted", w.Code)
	}
}
func TestOrdinaryProfileIgnoresSpoofedCentralHeaders(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &setupFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}
	s.system = f
	h, err := WithPublicAPI(s.handler(), "https://api.fixture.test", "https://private.fixture.test", "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "https://api.fixture.test/v1/system/setup-profile", strings.NewReader(`{"profile":"personal","version":1}`))
	req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Host", "console.sociaro.ai")
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 || f.writes != 1 || !f.settings.PersonalProviders {
		t.Fatal("untrusted headers changed ordinary role", w.Code)
	}
	req = httptest.NewRequest("GET", "https://console.sociaro.ai/site.json", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 421 {
		t.Fatal("untrusted Host accepted as configured identity", w.Code)
	}
}

func TestCentralNewContentScopePreservesAdmittedCleanup(t *testing.T) {
	s, _ := newPersonalTestServer()
	settings := system.Defaults()
	settings.SharedResources = true
	settings.PersonalProviders = false
	s.system = &systemFixture{settings: settings, admins: map[string]bool{"alice": true}}
	for _, tc := range []struct {
		method, path, pattern string
		company, allowed      bool
	}{
		{"POST", "/v1/references", "", false, false}, {"POST", "/v1/provider-resources", "", false, false}, {"POST", "/v1/provider-resources/estimate", "", false, false}, {"POST", "/v1/provider-resources", "", true, true}, {"DELETE", "/v1/provider-resources/existing", "", false, true}, {"POST", "/v1/references/uploads", "", false, false}, {"POST", "/v1/projects", "", false, false}, {"PATCH", "/v1/projects/existing", "", false, false},
		{"POST", "/v1/references/uploads", "", true, true}, {"POST", "/v1/projects", "", true, true}, {"POST", "/v1/references/uploads/existing/complete", "POST /v1/references/uploads/{id}/complete", false, true},
		{"GET", "/v1/references", "", false, true}, {"DELETE", "/v1/references/existing", "", false, true}, {"DELETE", "/v1/projects/existing", "", false, true}, {"POST", "/v1/system/template-references/uploads", "", false, true},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			called := false
			handler := s.systemPolicy(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Pattern = tc.pattern
			ctx := installation.WithContext(identity.WithUser(req.Context(), identity.User{ID: "alice"}), installation.Metadata{Role: "central", CentralOrigin: installation.DefaultCentralOrigin})
			if tc.company {
				ctx = identity.WithResourceOwners(identity.WithOrganization(ctx, "company", true), identity.SharedOwnerID, identity.SharedOwnerID)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req.WithContext(ctx))
			if called != tc.allowed {
				t.Fatal("company-content boundary", w.Code, called)
			}
			if !tc.allowed && w.Code != 403 {
				t.Fatal(w.Code)
			}
		})
	}
}

func TestCentralRemoteClientReadAndCleanupBoundary(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		blocked      bool
	}{
		{"POST", "/v1/system/remote-workspace", true}, {"POST", "/v1/system/remote-workspace/authorization", true}, {"POST", "/v1/system/remote-workspace/authorization/poll", true}, {"POST", "/v1/system/remote-workspace/proxy/v1/generations", true},
		{"GET", "/v1/system/remote-workspace", false}, {"GET", "/v1/system/remote-workspace/proxy/v1/generations/existing/content", false}, {"DELETE", "/v1/system/remote-workspace", false}, {"DELETE", "/v1/system/remote-workspace/authorization", false},
	} {
		if got := centralRemoteClientRequest(httptest.NewRequest(tc.method, tc.path, nil)); got != tc.blocked {
			t.Fatal("remote compatibility boundary", tc.method, tc.path, got)
		}
	}
}

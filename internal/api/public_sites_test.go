package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicAPIIsolationAndHeadlessSessions(t *testing.T) {
	s := &Server{service: serviceStub{}, health: healthStub{}, apiKey: "service-secret", auth: browserAuthStub{}}
	h, err := WithPublicAPI(s.handler(), "https://api.example.test", "https://console.example.test", "https://docs.example.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, path, cookie, token string
		code                      int
	}{
		{"docs.example.test", "/site.json", "", "", 200},
		{"docs.example.test", "/openapi.json", "", "", 200},
		{"docs.example.test", "/v1/info", "alice", "service-secret", 404},
		{"docs.example.test", "/auth/session", "alice", "", 404},
		{"docs.example.test", "/media/result", "", "", 404},
		{"docs.example.test", "/ui/index.html", "alice", "", 404},
		{"api.example.test", "/", "alice", "", 404},
		{"api.example.test", "/auth/session", "", "service-secret", 401},
		{"api.example.test", "/auth/session", "alice", "", 204},
		{"api.example.test", "/v1/info", "", "", 401},
		{"api.example.test", "/v1/info", "", "service-secret", 200},
		{"wrong.example.test", "/v1/info", "alice", "", 421},
		{"127.0.0.1:8080", "/healthz", "", "", 200},
	} {
		r := httptest.NewRequest("GET", "https://"+tc.host+tc.path, nil)
		r.Header.Set("X-Forwarded-Host", "api.example.test")
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: "test_session", Value: tc.cookie})
		}
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Errorf("%s%s: %d %s", tc.host, tc.path, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/openapi.json", "/openapi.yaml"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://docs.example.test"+path, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "https://api.example.test") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://docs.example.test/site.json", nil))
	var site map[string]any
	if json.Unmarshal(w.Body.Bytes(), &site) != nil || site["ui_url"] != "https://console.example.test" {
		t.Fatal(w.Body.String())
	}
	// Browser-session infrastructure is optional for an API-only installation.
	headless := (&Server{}).handler()
	w = httptest.NewRecorder()
	headless.ServeHTTP(w, httptest.NewRequest("GET", "/auth/session", nil))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
}
func TestPublicAPIRejectsInvalidOrigins(t *testing.T) {
	for _, urls := range [][3]string{{"http://api.test", "", ""}, {"https://api.test/path", "", ""}, {"https://api.test", "https://ui.test?x=1", ""}, {"https://user@api.test", "", ""}, {"https://api.test", "", "https://api.test"}, {"", "", "https://docs.test"}} {
		if _, err := WithPublicAPI(http.NotFoundHandler(), urls[0], urls[1], urls[2]); err == nil {
			t.Fatalf("accepted %v", urls)
		}
	}
}
func TestPublicAPIForwardsCapabilitiesOnlyOnAPIHost(t *testing.T) {
	h, err := WithPublicAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) }), "https://api.test", "", "https://docs.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/media/id", "/references/id", "/v1/payments/webhooks/id", "/auth/google/callback", "/auth/email/verify"} {
		for _, host := range []string{"api.test", "docs.test"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "https://"+host+p, nil))
			want := 418
			if host == "docs.test" {
				want = 404
			}
			if w.Code != want {
				t.Fatal(host, p, w.Code)
			}
		}
	}
}

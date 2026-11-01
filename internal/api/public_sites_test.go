package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/monitoring"
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

func TestPublicAPIPreservesBoundedRouteMetrics(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "headless"
		apiURL, uiURL, docsURL := "", "", ""
		if configured {
			name = "configured origins"
			apiURL = "https://api.example.test"
			uiURL = "https://console.example.test"
			docsURL = "https://docs.example.test"
		}
		t.Run(name, func(t *testing.T) {
			metrics := monitoring.New("gateway", monitoring.Dependencies{})
			mux := http.NewServeMux()
			mux.HandleFunc("GET /v1/metric-fixture/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
			wrapped, err := WithPublicAPI(mux, apiURL, uiURL, docsURL)
			if err != nil {
				t.Fatal(err)
			}
			handler := metrics.InstrumentHTTP(wrapped)
			request := httptest.NewRequest("GET", "https://api.example.test/v1/metric-fixture/private-id?token=private-secret", nil)
			request.Header.Set("Authorization", "Bearer private-secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusAccepted {
				t.Fatal(response.Code)
			}
			if request.Pattern != "GET /v1/metric-fixture/{id}" {
				t.Fatalf("registered route pattern lost: %q", request.Pattern)
			}
			rejected := []struct {
				url    string
				status int
			}{{"https://api.example.test/private-path?token=private-secret", http.StatusNotFound}}
			if configured {
				rejected = append(rejected, struct {
					url    string
					status int
				}{"https://unknown.example.test/v1/metric-fixture/private-id", http.StatusMisdirectedRequest}, struct {
					url    string
					status int
				}{"https://docs.example.test/v1/metric-fixture/private-id", http.StatusNotFound})
			}
			for _, test := range rejected {
				r := httptest.NewRequest("GET", test.url, nil)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != test.status || r.Pattern != "" {
					t.Fatalf("rejected/unmatched request: status=%d pattern=%q", w.Code, r.Pattern)
				}
			}
			scraped := httptest.NewRecorder()
			metrics.Handler().ServeHTTP(scraped, httptest.NewRequest("GET", "/metrics", nil))
			body := scraped.Body.String()
			if !strings.Contains(body, `media_gateway_http_requests_total{method="GET",route="GET /v1/metric-fixture/{id}",service="gateway",status="202"} 1`) {
				t.Fatal("registered request counter missing")
			}
			if !strings.Contains(body, `media_gateway_http_request_duration_seconds_count{method="GET",route="GET /v1/metric-fixture/{id}",service="gateway"} 1`) {
				t.Fatal("registered duration observation missing")
			}
			if !strings.Contains(body, `route="unmatched",service="gateway",status="404"`) {
				t.Fatal("unmatched request counter missing")
			}
			for _, private := range []string{"private-id", "private-path", "private-secret", "unknown.example.test"} {
				if strings.Contains(body, private) {
					t.Fatal("request identity leaked into metrics")
				}
			}
		})
	}
}

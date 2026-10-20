package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type unavailableMonitoringPolicy struct{ system.Repository }

func (unavailableMonitoringPolicy) IsAdmin(context.Context, string) (bool, error) {
	return false, errors.New("database unavailable")
}

func TestMonitoringRequiresCurrentAdministratorBrowserSession(t *testing.T) {
	s, _ := newPersonalTestServer()
	policy := &systemFixture{admins: map[string]bool{"alice": true}}
	s.system = policy
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Monitoring test","scopes":["generation","system:manage"]}`)
	for _, tc := range []struct {
		session, token string
		status         int
	}{{"", "", 401}, {"", key, 401}, {"", "service-secret", 401}, {"bob", "", 403}, {"alice", "", 204}} {
		r := httptest.NewRequest("GET", "/auth/monitoring", nil)
		r.Header.Set("X-WEBAUTH-USER", "forged-admin")
		if tc.session != "" {
			r.AddCookie(&http.Cookie{Name: "test_session", Value: tc.session})
		}
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body)
		}
		if tc.status == 204 {
			if w.Header().Get("X-WEBAUTH-USER") != "gateway-alice" {
				t.Fatal("incorrect trusted identity", w.Header())
			}
		} else if w.Header().Get("X-WEBAUTH-USER") != "" {
			t.Fatal("identity forwarded for rejected request")
		}
	}
	policy.admins["alice"] = false
	if w := sendTest(h, "GET", "/auth/monitoring", "", "alice", ""); w.Code != 403 {
		t.Fatal("revoked Administrator retained monitoring access", w.Code)
	}
	s.system = unavailableMonitoringPolicy{}
	if w := sendTest(s.handler(), "GET", "/auth/monitoring", "", "alice", ""); w.Code < 500 {
		t.Fatal("policy read failure did not fail closed", w.Code)
	}
	s.auth = nil
	if w := sendTest(s.handler(), "GET", "/auth/monitoring", "", "alice", ""); w.Code != 401 {
		t.Fatal("headless installation granted monitoring access", w.Code)
	}
}

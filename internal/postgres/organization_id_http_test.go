package postgres

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A mistyped organization ID is the caller's mistake, not a Gateway outage:
// clients retry 503 and administrators read it as a broken installation.
func TestMalformedOrganizationIDIsInvalidRequest(t *testing.T) {
	f := newInvitationKeyHTTPFixture(t)
	session := f.session(f.admin)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://invitation.fixture.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://invitation.fixture.test")
		r.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: session})
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		return w
	}
	truncated := f.a.ID[:len(f.a.ID)-1]
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/v1/organizations/not-a-uuid", ""},
		{"GET", "/v1/organizations/" + truncated + "/members", ""},
		{"GET", "/v1/organizations/urn:uuid:" + f.a.ID + "/credits", ""},
		{"PUT", "/v1/organizations/" + truncated, `{"name":"Renamed","default_weekly_limit_usd":0}`},
		{"POST", "/v1/organizations/" + truncated + "/invitations", `{"role":"member"}`},
		{"POST", "/v1/api-keys", `{"name":"Synthetic","organization_id":"` + truncated + `"}`},
	} {
		w := send(c.method, c.path, c.body)
		var body struct {
			Error struct{ Code string }
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if w.Code != http.StatusBadRequest || body.Error.Code != "invalid_request" {
			t.Errorf("%s %s = %d %s", c.method, c.path, w.Code, strings.TrimSpace(w.Body.String()))
		}
	}
	if w := send("GET", "/v1/organizations/"+f.a.ID+"/members", ""); w.Code != http.StatusOK {
		t.Fatalf("valid organization ID: %d %s", w.Code, w.Body.String())
	}
}

package api

import (
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

func TestMarkupAdminBoundary(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}
	s.system = f
	h := s.handler()
	for _, rate := range []string{"25", "0", "null"} {
		body := `{"name":"Team","default_weekly_limit_usd":null,"markup_percent":` + rate + `}`
		if w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOrg, body, "bob", ""); w.Code != 403 {
			t.Fatalf("org admin override %s: %d %s", rate, w.Code, w.Body)
		}
	}
	for _, rate := range []string{"-1", "1000.01", "1.001", `"25"`} {
		body := `{"name":"Team","default_weekly_limit_usd":null,"markup_percent":` + rate + `}`
		if w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOrg, body, "alice", ""); w.Code != 400 {
			t.Fatalf("invalid markup %s: %d", rate, w.Code)
		}
	}
	body := `{"generation_ui":true,"personal_providers":true,"version":1,"markup_percent":25}`
	if w := sendTest(h, "PUT", "/v1/system/settings", body, "bob", ""); w.Code != 403 {
		t.Fatal("non-system updated global")
	}
	if w := sendTest(h, "PUT", "/v1/system/settings", body, "alice", ""); w.Code != 200 || f.settings.MarkupPercent != 2500 {
		t.Fatalf("global save %d %s", w.Code, w.Body)
	}
	if w := sendTest(h, "GET", "/v1/info", "", "", ""); strings.Contains(w.Body.String(), "markup_percent") {
		t.Fatal("public markup setting")
	}
}

package api

import (
	"context"
	"strings"
	"testing"

	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type letteredOrganizationFixture struct{ *organizationFixture }

const letteredOrg = "abcdef01-2345-4678-89ab-cdef01234567"

func (f *letteredOrganizationFixture) Organizations(context.Context, string, bool) ([]org.Organization, error) {
	return []org.Organization{{ID: letteredOrg, Name: "Team", Role: "admin"}}, nil
}

func TestOrganizationPathAcceptsUpperCaseID(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &letteredOrganizationFixture{&organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}}
	f.settings.SharedResources, f.settings.PersonalProviders = true, false
	s.system = f
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Org","organization_id":"`+letteredOrg+`","scopes":["generation","organizations:manage"]}`)
	for _, path := range []string{strings.ToUpper(letteredOrg), strings.ReplaceAll(letteredOrg, "-", "")} {
		for _, who := range []struct{ session, key string }{{"alice", ""}, {"", key}} {
			w := sendTest(h, "GET", "/v1/organizations/"+path, "", who.session, who.key)
			if w.Code != 200 || !strings.Contains(w.Body.String(), letteredOrg) {
				t.Fatalf("GET %s (key %t): %d %s", path, who.key != "", w.Code, w.Body.String())
			}
		}
	}
}

func TestAPIKeysAcceptUpperCaseOrganizationID(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &letteredOrganizationFixture{&organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}}
	f.settings.SharedResources, f.settings.PersonalProviders = true, false
	s.system = f
	h := s.handler()
	upper := strings.ToUpper(letteredOrg)
	created, key := issueTestKey(t, h, "alice", `{"name":"Org","organization_id":"`+upper+`","scopes":["generation","organizations:manage","keys:manage"]}`)
	if created.OrganizationID != letteredOrg {
		t.Fatalf("stored organization %q", created.OrganizationID)
	}
	for _, path := range []string{"/v1/api-keys?organization_id=" + upper, "/v1/api-keys/usage?organization_id=" + upper} {
		if w := sendTest(h, "GET", path, "", "", key); w.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

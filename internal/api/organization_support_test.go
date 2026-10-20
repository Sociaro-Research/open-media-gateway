package api

import (
	"context"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"strings"
	"testing"
)

type supportFixture struct {
	*organizationFixture
	calls int
}

func (f *supportFixture) OrganizationSupport(_ context.Context, actor, id string) (org.Support, error) {
	f.calls++
	if actor != "alice" || id != fixtureOrg {
		return org.Support{}, org.ErrForbidden
	}
	return org.Support{ClientManager: &org.SupportPerson{ID: "manager", Name: "My manager"}}, nil
}
func TestOrganizationSupportRequiresScopedBrowserSession(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &supportFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}}
	s.system = f
	h := s.handler()
	for _, test := range []struct {
		actor, id string
		status    int
	}{{"alice", fixtureOrg, 200}, {"bob", fixtureOrg, 403}, {"alice", fixtureOtherOrg, 403}, {"alice", "invalid", 400}, {"", fixtureOrg, 401}} {
		w := sendTest(h, "GET", "/v1/organizations/"+test.id+"/support", "", test.actor, "")
		if w.Code != test.status {
			t.Fatal(test, w.Code, w.Body)
		}
		if w.Code == 200 && !strings.Contains(w.Body.String(), "My manager") {
			t.Fatal(w.Body)
		}
	}
	_, key := issueTestKey(t, h, "alice", `{"name":"Generation","scopes":["generation"]}`)
	before := f.calls
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/support", "", "alice", key); w.Code != 403 {
		t.Fatal(w.Code, w.Body)
	}
	if f.calls != before {
		t.Fatal("key reached contact repository")
	}
}

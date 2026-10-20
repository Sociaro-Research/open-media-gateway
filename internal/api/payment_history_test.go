package api

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type historyAPIFixture struct {
	*organizationFixture
	calls int
}

func (f *historyAPIFixture) BalanceHistory(context.Context, string, string, int) ([]payments.BalanceEntry, error) {
	f.calls++
	return []payments.BalanceEntry{}, nil
}
func TestBalanceHistoryScopeAndBinding(t *testing.T) {
	s, _ := newPersonalTestServer()
	repo := &historyAPIFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
	repo.settings.SharedResources = true
	s.system = repo
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generation","organization_id":"`+fixtureOrg+`"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Manager","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage"]}`)
	for _, tc := range []struct {
		session, key, org string
		want              int
	}{{"", "", fixtureOrg, 401}, {"", "service-secret", fixtureOrg, 403}, {"", generation, fixtureOrg, 403}, {"", manager, fixtureOtherOrg, 403}, {"", manager, fixtureOrg, 200}, {"alice", "", fixtureOrg, 200}} {
		before := repo.calls
		w := sendTest(h, "GET", "/v1/organizations/"+tc.org+"/balance-history", "", tc.session, tc.key)
		if w.Code != tc.want {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
		if tc.want != 200 && repo.calls != before {
			t.Fatal("unauthorized repository access")
		}
	}
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/balance-history?offset=-1", "", "alice", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
}

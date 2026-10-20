package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/audit"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type auditFixture struct {
	*rolePolicyFixture
	filters                []audit.Filter
	failures               int
	failedOrg, correlation string
	action                 string
	details                json.RawMessage
}

func (f *auditFixture) AuditEvents(_ context.Context, filter audit.Filter) ([]audit.Event, error) {
	f.filters = append(f.filters, filter)
	details := f.details
	if details == nil {
		details = json.RawMessage(`{"before":false,"after":true}`)
	}
	return []audit.Event{{ID: 10, Action: f.action, OrganizationID: filter.OrganizationID, Details: details}}, nil
}
func (f *auditFixture) AuditEvent(_ context.Context, id int64, company string) (audit.Event, error) {
	if id != 10 || company != fixtureOrg && company != "" {
		return audit.Event{}, sql.ErrNoRows
	}
	return audit.Event{ID: id, OrganizationID: fixtureOrg, Action: f.action, Details: f.details}, nil
}
func (f *auditFixture) RevokedKeys(_ context.Context, filter audit.Filter) ([]audit.RevokedKey, error) {
	f.filters = append(f.filters, filter)
	return []audit.RevokedKey{}, nil
}
func (f *auditFixture) AuditFailure(ctx context.Context, company, _, _, _ string, _ int) error {
	f.failures++
	f.failedOrg = company
	f.correlation = audit.Correlation(ctx)
	return nil
}
func auditServer() (*Server, *memoryKeys, *auditFixture) {
	s, keys := newPersonalTestServer()
	f := &auditFixture{rolePolicyFixture: &rolePolicyFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: org.Supermanager}}
	s.system = f
	return s, keys, f
}
func TestAuditCurrentRolesAndOrganizationIsolation(t *testing.T) {
	s, _, f := auditServer()
	h := s.handler()
	for _, role := range []string{org.Supermanager, org.Manager, org.ContentOfficer, org.Developer, org.MemberRole, "client_manager"} {
		f.role = role
		w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/audit/events?organization_id="+fixtureOtherOrg, "", "bob", "")
		want := 403
		if org.Manage(role) {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("%s: %d %s", role, w.Code, w.Body)
		}
		if want == 200 && f.filters[len(f.filters)-1].OrganizationID != fixtureOrg {
			t.Fatal("query widened organization scope")
		}
	}
	f.role = org.Manager
	w := sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg+"/audit/events/10", "", "bob", "")
	if w.Code != 404 {
		t.Fatal("cross-organization detail exposed", w.Code, w.Body)
	}
	w = sendTest(h, "GET", "/v1/audit/events", "", "bob", "")
	if w.Code != 403 {
		t.Fatal("ordinary user read global audit")
	}
	w = sendTest(h, "GET", "/v1/audit/events?from=2026-03-28&to=2026-03-29&timezone=Europe/Berlin&actor_id=someone@example.test", "", "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	filter := f.filters[len(f.filters)-1]
	if filter.Until.Sub(filter.From) != 47*time.Hour {
		t.Fatal("DST date range incorrect", filter)
	}
	f.admins["alice"] = false
	w = sendTest(h, "GET", "/v1/audit/events", "", "alice", "")
	if w.Code != 403 {
		t.Fatal("revoked Administrator retained audit access")
	}
}
func TestAuditKeyScopesAndCredentialOrganization(t *testing.T) {
	for _, scopes := range [][]string{{apikey.Generation}, {apikey.Generation, apikey.OrganizationsManage}, {apikey.Generation, apikey.SystemManage}, {apikey.Generation, apikey.OrganizationsManage, apikey.SystemManage}} {
		s, keys, f := auditServer()
		f.role = org.Manager
		key, secret, _ := apikey.Generate(apikey.CreateInput{Name: "test", Scopes: scopes}, time.Now())
		hash := apikey.Hash(secret)
		key.OrganizationID = fixtureOrg
		key.OrganizationRole = org.Manager
		key.OrganizationAdmin = true
		keys.Create(context.Background(), "alice", key, hash)
		h := s.handler()
		w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/audit/events", "", "", secret)
		hasOrg := strings.Contains(strings.Join(scopes, ","), apikey.OrganizationsManage)
		if hasOrg && w.Code != 200 || !hasOrg && w.Code != 403 {
			t.Fatal("organization audit scope", scopes, w.Code, w.Body)
		}
		w = sendTest(h, "GET", "/v1/audit/events", "", "", secret)
		if w.Code != 403 {
			t.Fatal("organization key read global audit", scopes, w.Code)
		}
		w = sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg+"/audit/revoked-keys", "", "", secret)
		if w.Code != 403 {
			t.Fatal("key crossed organization boundary")
		}
	}
}

func TestAuditOrganizationProjectionAppliesToListAndDetails(t *testing.T) {
	s, _, f := auditServer()
	f.role = org.Manager
	h := s.handler()
	for _, action := range []string{"markups", "request_limits", "model_access", "organization_client_manager", "client_onboarding", "payment_settlement", "settings"} {
		f.action = action
		f.details = json.RawMessage(`{"before":{"name":"Previous name","markup_basis_points":750,"client_manager_id":"internal-person"},"after":{"name":"New name","request_limits":{"rpm":42},"model_ids":["private-model"],"settlement":{"provider_cost":"internal-price"}}}`)
		for _, suffix := range []string{"", "/10"} {
			path := "/v1/organizations/" + fixtureOrg + "/audit/events" + suffix
			w := sendTest(h, "GET", path, "", "bob", "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			for _, forbidden := range []string{"750", "internal-person", "rpm", "private-model", "internal-price"} {
				if strings.Contains(w.Body.String(), forbidden) {
					t.Fatal("manager read restricted details", action, suffix, w.Body)
				}
			}
			if action == "settings" && !strings.Contains(w.Body.String(), "New name") {
				t.Fatal("permitted change details lost", w.Body)
			}
			w = sendTest(h, "GET", path, "", "alice", "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "internal-price") {
				t.Fatal("Administrator lost full detail", w.Code, w.Body)
			}
		}
	}
}
func TestAuditFiltersPaginationAndSafeFailures(t *testing.T) {
	s, _, f := auditServer()
	h := s.handler()
	for _, filter := range []string{"before_id=-1", "from=invalid", "from=2026-10-10&to=2026-10-01", "organization_id=bad", "outcome=whatever", "timezone=bad"} {
		w := sendTest(h, "GET", "/v1/audit/events?"+filter, "", "alice", "")
		if w.Code != 400 {
			t.Fatal(filter, w.Code, w.Body)
		}
	}
	w := sendTest(h, "GET", "/v1/audit/events?before_id=10", "", "alice", "")
	if w.Code != 200 || f.filters[len(f.filters)-1].BeforeID != 10 {
		t.Fatal("cursor ignored")
	}
	w = sendTest(h, "PUT", "/v1/system/settings?token=never-log", "{\"secret\":\"never-log\"}", "bob", "")
	if w.Code != 403 || f.failures != 1 || f.correlation == "" {
		t.Fatal("denied mutation absent", w.Code, f)
	}
	f.role = org.Supermanager
	_, secret := issueTestKey(t, h, "bob", `{"name":"Generation only","scopes":["generation"]}`)
	w = sendTest(h, "PUT", "/v1/system/settings", "{}", "", secret)
	if w.Code != 403 || f.failures != 2 {
		t.Fatal("insufficient scope mutation absent", w.Code, f.failures)
	}
	// The old public reader is removed; it must never silently return legacy data.
	w = sendTest(h, http.MethodGet, "/v1/system/events", "", "alice", "")
	if w.Code == 200 {
		t.Fatal("legacy API still exposed")
	}
}

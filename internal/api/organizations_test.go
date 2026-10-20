package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"strings"
	"testing"
)

type organizationFixture struct {
	*systemFixture
	org.Repository
	credits int
}

const fixtureOrg = "11111111-1111-4111-8111-111111111111"
const fixtureOtherOrg = "22222222-2222-4222-8222-222222222222"

func (f *organizationFixture) Organizations(_ context.Context, actor string, all bool) ([]org.Organization, error) {
	return []org.Organization{{ID: fixtureOrg, Name: "Team", Role: "admin"}, {ID: fixtureOtherOrg, Name: "Other", Role: "member"}}, nil
}
func (f *organizationFixture) OrganizationAccess(context.Context, string, string) (string, error) {
	return "admin", nil
}

type keyMembershipFixture struct {
	*organizationFixture
	role string
}

func (f *keyMembershipFixture) Organizations(_ context.Context, _ string, all bool) ([]org.Organization, error) {
	if all {
		return []org.Organization{{ID: fixtureOrg, Role: "system_admin"}}, nil
	}
	if f.role == "" {
		return []org.Organization{}, nil
	}
	return []org.Organization{{ID: fixtureOrg, Role: f.role}}, nil
}

func (f *keyMembershipFixture) OrganizationAccess(context.Context, string, string) (string, error) {
	if f.role == "" {
		return "", org.ErrForbidden
	}
	return f.role, nil
}

func TestOrganizationKeyCreationRequiresMembershipEvenForSystemAdmin(t *testing.T) {
	for _, actor := range []string{"alice", "bob"} {
		for _, role := range []string{"", "system_admin", "client_manager", "member", "developer", "content_officer", "admin", "supermanager"} {
			t.Run(actor+"/"+role, func(t *testing.T) {
				s, _ := newPersonalTestServer()
				f := &keyMembershipFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: role}
				f.settings.SharedResources = true
				s.system = f
				w := sendTest(s.handler(), "POST", "/v1/api-keys", `{"name":"Organization key","organization_id":"`+fixtureOrg+`"}`, actor, "")
				want := 403
				if role == "developer" || role == "admin" || role == "supermanager" {
					want = 201
				}
				if w.Code != want {
					t.Fatalf("key creation: got %d want %d: %s", w.Code, want, w.Body.String())
				}
			})
		}
	}
}
func (f *organizationFixture) AddOrganizationCredit(_ context.Context, _ string, _ string, c org.Credit) (org.Credit, error) {
	f.credits++
	return c, nil
}
func TestOrganizationAPIRequiresRoleScopeAndKeyBinding(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}
	f.settings.SharedResources = true
	s.system = f
	h := s.handler()
	body := `{"amount_usd":10,"kind":"payment","external_id":"test"}`
	path := "/v1/organizations/" + fixtureOrg + "/credits"
	if w := sendTest(h, "POST", path, body, "bob", ""); w.Code != 403 {
		t.Fatalf("org admin credited: %d", w.Code)
	}
	if w := sendTest(h, "POST", path, body, "alice", ""); w.Code != 201 {
		t.Fatalf("system admin blocked: %d %s", w.Code, w.Body.String())
	}
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generate","organization_id":"`+fixtureOrg+`"}`)
	_, orgManager := issueTestKey(t, h, "alice", `{"name":"Org manager","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage","keys:manage"]}`)
	_, systemManager := issueTestKey(t, h, "alice", `{"name":"System manager","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage","system:manage"]}`)
	for _, test := range []struct {
		key  string
		want int
	}{{generation, 403}, {orgManager, 403}, {systemManager, 201}} {
		w := sendTest(h, "POST", path, body, "", test.key)
		if w.Code != test.want {
			t.Fatalf("credit permission: %d want %d %s", w.Code, test.want, w.Body.String())
		}
	}
	if f.credits != 2 {
		t.Fatal("unexpected ledger writes", f.credits)
	}
	f.admins["alice"] = false
	revoked := sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg, "", "", systemManager)
	if revoked.Code != 403 {
		t.Fatal("revoked system role kept global organization access", revoked.Code)
	}
	f.admins["alice"] = true
	w := sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg, "", "", orgManager)
	if w.Code != 403 {
		t.Fatal("cross-organization key access", w.Code)
	}
	w = sendTest(h, "GET", "/v1/organizations", "", "", orgManager)
	var response struct {
		Data []org.Organization `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data) != 1 || response.Data[0].ID != fixtureOrg {
		t.Fatal("key listed foreign organization", w.Body.String())
	}
	w = sendTest(h, "POST", "/v1/api-keys", `{"name":"Foreign","organization_id":"`+fixtureOtherOrg+`"}`, "", orgManager)
	if w.Code != 403 {
		t.Fatal("key issued foreign organization key", w.Code)
	}
	w = sendTest(h, "POST", "/v1/api-keys", `{"name":"Personal"}`, "alice", "")
	if w.Code != 201 {
		t.Fatal("personal key without organization", w.Code)
	}
	var personalKey struct {
		OrganizationID string `json:"organization_id"`
		ResourceMode   string `json:"resource_mode"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &personalKey); err != nil || personalKey.OrganizationID != "" || personalKey.ResourceMode != "personal" {
		t.Fatalf("wrong personal key context: %s", w.Body.String())
	}
	for _, body := range []string{`{"name":"Invalid","resource_mode":"shared"}`, `{"name":"Invalid","organization_id":"` + fixtureOrg + `","resource_mode":"personal"}`} {
		if result := sendTest(h, "POST", "/v1/api-keys", body, "alice", ""); result.Code != 400 {
			t.Fatalf("accepted contradictory key context: %d %s", result.Code, result.Body.String())
		}
	}
	if result := sendTest(h, "GET", "/v1/admin/reports", "", "", orgManager); result.Code != 403 {
		t.Fatalf("organization key accessed personal reports: %d", result.Code)
	}
	w = sendTest(h, "POST", "/v1/invitations/accept", `{"token":"secret"}`, "", "")
	if w.Code != 401 {
		t.Fatal("anonymous invitation acceptance", w.Code)
	}
}

func TestMemberTaskResponseOmitsProviderSettlement(t *testing.T) {
	value := money.USD(40_000)
	zero := money.USD(0)
	units := float64(1)
	task := core.Task{Cost: &core.TaskCost{ExpectedUSD: value, TariffUSD: &value, ActualUSD: &zero, PackageUnits: &units, InvoiceIDs: []string{"private-invoice"}, BillingInstanceID: "private-instance", ActualSource: "provider_bill"}}
	response := core.GenerationView(task)
	b, e := core.CustomerJSON(response)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"actual_usd", "package_units", "private-invoice", "private-instance", "provider_bill"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("settlement leaked", secret)
		}
	}
	if response.Cost.TariffUSD == nil || *response.Cost.TariffUSD != value || task.Cost.ActualUSD == nil {
		t.Fatal("consumption or source cost changed")
	}
}

type memberLimitFixture struct {
	*organizationFixture
	called, provided bool
	limit            *money.USD
}

func (f *memberLimitFixture) SetOrganizationMember(_ context.Context, _, _, _, _ string, limit *money.USD, provided, _ bool) error {
	f.called, f.provided, f.limit = true, provided, limit
	return nil
}

func TestOrganizationMemberOptionalWeeklyLimit(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		provided   bool
		want       string
	}{
		{"inherit", `{"role":"admin"}`, 204, false, "null"},
		{"unlimited", `{"role":"member","weekly_limit_usd":null}`, 204, true, "null"},
		{"zero", `{"role":"member","weekly_limit_usd":0}`, 204, true, "0.000000"},
		{"positive", `{"role":"member","weekly_limit_usd":25}`, 204, true, "25.000000"},
		{"invalid", `{"role":"member","weekly_limit_usd":"wrong"}`, 400, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := &memberLimitFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}}
			s.system = f
			w := sendTest(s.handler(), "PUT", "/v1/organizations/"+fixtureOrg+"/members/bob", tc.body, "alice", "")
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.status != 204 {
				if f.called {
					t.Fatal("invalid request reached repository")
				}
				return
			}
			got, _ := json.Marshal(f.limit)
			if !f.called || f.provided != tc.provided || string(got) != tc.want {
				t.Fatalf("called=%v provided=%v limit=%s", f.called, f.provided, got)
			}
		})
	}
}

type invitationReopenFixture struct {
	*organizationFixture
	err error
}

func (f *invitationReopenFixture) OrganizationInvitation(context.Context, string, string, bool) (org.Invite, error) {
	return org.Invite{OrganizationID: fixtureOrg, AlreadyMember: true}, f.err
}

func TestOrganizationInvitationReopenResponse(t *testing.T) {
	for _, action := range []string{"preview", "accept"} {
		for _, unavailable := range []bool{false, true} {
			s, _ := newPersonalTestServer()
			f := &invitationReopenFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{}}}}
			if unavailable {
				f.err = org.ErrInvitationUnavailable
			}
			s.system = f
			w := sendTest(s.handler(), "POST", "/v1/invitations/"+action, `{"token":"test"}`, "alice", "")
			if unavailable {
				if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"invitation_unavailable"`) || strings.Contains(w.Body.String(), "preserve the last administrator") {
					t.Fatalf("unavailable invitation: %d %s", w.Code, w.Body.String())
				}
			} else if w.Code != 200 || !strings.Contains(w.Body.String(), `"already_member":true`) {
				t.Fatalf("existing member: %d %s", w.Code, w.Body.String())
			}
		}
	}
}

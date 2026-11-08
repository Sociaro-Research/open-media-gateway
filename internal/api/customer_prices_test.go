package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type billingStatusFixture struct {
	*accountService
	owner string
	calls int
}

func (s *billingStatusFixture) BillingStatus(ctx context.Context) ([]core.BillingStatus, error) {
	s.owner = identity.OwnerID(ctx)
	s.calls++
	return []core.BillingStatus{{Provider: "mock", Status: "synced"}}, nil
}

func TestBillingStatusRequiresEffectiveAdministratorAndBothKeyScopes(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := &keyMembershipFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{
		settings: system.Defaults(), admins: map[string]bool{"alice": true},
	}}, role: "supermanager"}
	s.system = f
	service := &billingStatusFixture{accountService: s.service.(*accountService)}
	s.service = service
	h := s.handler()
	_, keysOnly := issueTestKey(t, h, "alice", `{"name":"Key manager","scopes":["generation","keys:manage"]}`)
	_, systemOnly := issueTestKey(t, h, "alice", `{"name":"System manager","scopes":["generation","system:manage"]}`)
	_, full := issueTestKey(t, h, "alice", `{"name":"Administrator","scopes":["generation","keys:manage","system:manage"]}`)
	f.settings.SharedResources = true // Issue the company key after the personal keys.
	_, bound := issueTestKey(t, h, "alice", `{"name":"Company administrator","organization_id":"`+fixtureOrg+`","scopes":["generation","keys:manage","system:manage"]}`)
	for _, tc := range []struct {
		session, key string
		status       int
	}{
		{"", "", 401}, {"bob", "", 403}, {"alice", "", 200},
		{"", keysOnly, 403}, {"", systemOnly, 403}, {"", full, 200},
		// A direct key retains its live global administrator's authority.
		{"", bound, 200}, {"", "service-secret", 403},
	} {
		before := service.calls
		w := sendTest(h, "GET", "/v1/api-keys/billing", "", tc.session, tc.key)
		if w.Code != tc.status {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body)
		}
		if tc.status == 200 {
			if service.calls != before+1 || service.owner != "alice" || !strings.Contains(w.Body.String(), `"provider":"mock"`) {
				t.Fatal("billing status lost authenticated owner scope", service.owner, w.Body)
			}
		} else if service.calls != before {
			t.Fatal("forbidden caller read provider billing status")
		}
	}
	// Delegated company authority is a separate ceiling from direct-key binding.
	r := httptest.NewRequest("GET", "/v1/api-keys/billing", nil)
	r.Header.Set("Authorization", "Bearer "+full)
	r = r.WithContext(identity.WithCompanyBoundary(r.Context(), fixtureOrg))
	before := service.calls
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || service.calls != before {
		t.Fatal("delegated company context read provider billing status", w.Code, w.Body)
	}
}

func TestFinancialResponsesRequireSystemAdministrator(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"root": true}}
	raw := money.USD(2000000)
	cost := core.TaskCost{ExpectedUSD: 1000000, MeteredUSD: &raw, TariffUSD: &raw, ActualUSD: &raw, MarkupPercent: 2500, Pricing: core.PricingSnapshot{Price: raw, Source: "PRIVATE TARIFF"}}
	for _, role := range []string{"root", "member", "organization-admin", "personal", "restricted-admin-key"} {
		t.Run(role, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/generations", nil)
			actor := role
			if role == "restricted-admin-key" {
				actor = "root"
			}
			ctx := identity.WithUser(r.Context(), identity.User{ID: actor})
			if role == "member" || role == "organization-admin" {
				ctx = identity.WithOrganization(ctx, "org", role == "organization-admin")
			}
			if role == "restricted-admin-key" {
				ctx = identity.WithAPIKeyID(ctx, "key")
			}
			w := httptest.NewRecorder()
			s.writeViewerJSON(w, r.WithContext(ctx), 200, map[string]any{"cost": cost, "usage": core.ProviderUsage{Raw: json.RawMessage(`{"price":99}`)}})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			var result struct {
				Cost map[string]any `json:"cost"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if role == "root" {
				if result.Cost["expected_usd"] != float64(1) || result.Cost["markup_percent"] != float64(25) {
					t.Fatal(w.Body)
				}
				return
			}
			if result.Cost["expected_usd"] != 1.25 || result.Cost["metered_usd"] != 2.5 {
				t.Fatal(w.Body)
			}
			for _, hidden := range []string{"markup_percent", "pricing", "actual_usd", "PRIVATE TARIFF", `"raw"`} {
				if strings.Contains(w.Body.String(), hidden) {
					t.Fatalf("leaked %s: %s", hidden, w.Body)
				}
			}
		})
	}
	if cost.ExpectedUSD != 1000000 || cost.MarkupPercent != 2500 {
		t.Fatal("mutated stored cost")
	}
}

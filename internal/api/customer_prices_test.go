package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

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

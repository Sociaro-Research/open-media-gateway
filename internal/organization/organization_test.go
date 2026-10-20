package organization

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"testing"
)

func TestMemberFinancialView(t *testing.T) {
	limit := money.USD(20 * money.Scale)
	o := Organization{BillingProfile: &payments.BillingProfile{BusinessName: "Private company"}, ID: "organization", Role: "member", Balance: money.USD(100 * money.Scale), Available: money.USD(100 * money.Scale), WeeklyLimit: &limit, WeeklyCommitted: money.USD(5 * money.Scale)}
	b, e := json.Marshal(o)
	if e != nil {
		t.Fatal(e)
	}
	var view map[string]any
	if e = json.Unmarshal(b, &view); e != nil {
		t.Fatal(e)
	}
	o.Usage = &UsageSummary{Spend: money.USD(100 * money.Scale)}
	b, _ = json.Marshal(o)
	json.Unmarshal(b, &view)
	for _, key := range []string{"usage", "billing_profile", "balance_usd", "reserved_usd", "credit_limit_usd", "default_weekly_limit_usd", "storage_bytes"} {
		if _, ok := view[key]; ok {
			t.Fatal("member financial leak", key)
		}
	}
	if view["available_usd"] != float64(15) {
		t.Fatal("weekly allowance ignored", view)
	}
	o.Role = "admin"
	b, e = json.Marshal(o)
	if e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(b, &view)
	if view["balance_usd"] != float64(100) {
		t.Fatal("admin balance missing")
	}
}

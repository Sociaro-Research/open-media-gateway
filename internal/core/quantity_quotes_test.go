package core

import (
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"strings"
	"testing"
)

func TestKnownTariffUnknownQuantityIsNeverZeroOrFree(t *testing.T) {
	cost := TaskCost{QuantityUnknown: true, Status: "quantity_unknown", Pricing: PricingSnapshot{Currency: "USD", Unit: "token", Price: 123, Per: 1000000}}
	if _, err := cost.CustomerExpected(); !errors.Is(err, ErrPricingUnavailable) {
		t.Fatal(err)
	}
	raw, err := json.Marshal(cost)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"expected_usd"`, `"customer_expected_usd"`, `"estimated_units"`} {
		if strings.Contains(string(raw), key) {
			t.Fatal("unknown quantity became numeric zero", string(raw))
		}
	}
	var persisted TaskCost
	if err = json.Unmarshal(raw, &persisted); err != nil || !persisted.QuantityUnknown || persisted.Pricing.Price != money.USD(123) {
		t.Fatal(persisted, err)
	}
	raw, err = json.Marshal(MediaModelPricing{Known: true, ReferenceQuote: &cost})
	if err != nil || strings.Contains(string(raw), `"reference_price_usd"`) || !strings.Contains(string(raw), `"quantity_unknown":true`) {
		t.Fatal(string(raw), err)
	}
	known := FreeCost(cost.QuotedAt)
	raw, err = json.Marshal(known)
	if err != nil || !strings.Contains(string(raw), `"expected_usd":0`) || !strings.Contains(string(raw), `"customer_expected_usd":0`) {
		t.Fatal("explicit free cost changed", string(raw), err)
	}
}

func TestUnknownQuantityRequiresAnActualVerifiedTariff(t *testing.T) {
	base := TaskCost{QuantityUnknown: true, Pricing: PricingSnapshot{Currency: "USD", Unit: "token", Per: 1000000, Source: "https://pricing.example.test/tokens", Components: []PricingComponent{{Metric: "input_tokens", Price: 1000000, Per: 1000000}}}}
	if !HasVerifiedQuantityUnknownTariff(base) {
		t.Fatal("complete typed tariff rejected")
	}
	for _, change := range []func(*TaskCost){func(c *TaskCost) { c.Pricing.Source = "" }, func(c *TaskCost) { c.Pricing.Currency = "CNY" }, func(c *TaskCost) { c.Pricing.Per = 0 }, func(c *TaskCost) { c.Pricing.Components = nil; c.Pricing.Price = 0 }, func(c *TaskCost) { c.Pricing.Components = []PricingComponent{{Metric: "input_tokens", Per: 1000000}} }} {
		copy := base
		change(&copy)
		if HasVerifiedQuantityUnknownTariff(copy) {
			t.Fatal("missing tariff promoted to known", copy)
		}
	}
}

package providers

import (
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestPureCatalogRateQuotePreservesUnknownQuantityAndBillingContract(t *testing.T) {
	base := core.TaskCost{QuantityUnknown: true, Pricing: core.PricingSnapshot{Currency: "USD", Region: "Global", Unit: "token", Per: 1000000, Source: "https://official.example/pricing", Components: []core.PricingComponent{{Metric: "prompt_tokens", Price: 900000, Per: 1000000}, {Metric: "completion_tokens", Price: 3600000, Per: 1000000}}}}
	rate := catalogsync.Rate{Region: "Global", Unit: "token", Per: 1000000, Components: []core.PricingComponent{{Metric: "prompt_tokens", Price: 1000000, Per: 1000000}, {Metric: "completion_tokens", Price: 4000000, Per: 1000000}}}
	if err := validatePureCatalogRateQuote(base, rate); err != nil {
		t.Fatal("exact changed rate rejected", err)
	}
	for name, change := range map[string]func(*core.TaskCost){"known quantity": func(q *core.TaskCost) { q.QuantityUnknown = false }, "invented total": func(q *core.TaskCost) { q.ExpectedUSD = 1 }, "invented quantity": func(q *core.TaskCost) { q.EstimatedUnits = 1 }, "component quantity": func(q *core.TaskCost) { q.Pricing.Components[0].EstimatedUnits = 1 }, "account override": func(q *core.TaskCost) { q.Pricing.AccountSpecific = true }, "currency": func(q *core.TaskCost) { q.Pricing.Currency = "CNY" }, "actual bill": func(q *core.TaskCost) { v := money.USD(1); q.ActualUSD = &v }} {
		t.Run(name, func(t *testing.T) {
			q := base
			q.Pricing.Components = append([]core.PricingComponent(nil), base.Pricing.Components...)
			change(&q)
			if validatePureCatalogRateQuote(q, rate) == nil {
				t.Fatal("unverified pure quote accepted")
			}
		})
	}
	for name, change := range map[string]func(*catalogsync.Rate){"new metric": func(r *catalogsync.Rate) { r.Components[0].Metric = "undocumented" }, "missing metric": func(r *catalogsync.Rate) { r.Components = r.Components[:1] }, "new divisor": func(r *catalogsync.Rate) { r.Components[0].Per = 1 }, "new unit": func(r *catalogsync.Rate) { r.Unit = "image" }, "all free": func(r *catalogsync.Rate) {
		for i := range r.Components {
			r.Components[i].Price = 0
		}
	}} {
		t.Run(name, func(t *testing.T) {
			r := rate
			r.Components = append([]core.PricingComponent(nil), rate.Components...)
			change(&r)
			if validatePureCatalogRateQuote(base, r) == nil {
				t.Fatal("unknown/free/changed billing contract accepted")
			}
		})
	}
	if base.ExpectedUSD != 0 || base.Pricing.Components[0].Price != 900000 {
		t.Fatal("source validation mutated accepted tariff")
	}
}

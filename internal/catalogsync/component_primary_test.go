package catalogsync

import (
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestComponentRefreshUpdatesDeclaredPrimaryScalarWithoutChangingQuantities(t *testing.T) {
	base := core.TaskCost{EstimatedUnits: 5, ExpectedUSD: 550000, Pricing: core.PricingSnapshot{Currency: "USD", Region: "Global", Unit: "second", Price: 50000, Per: 1, Components: []core.PricingComponent{{Metric: "output_video_duration", Price: 50000, Per: 1, EstimatedUnits: 5}, {Metric: "input_video_duration", Price: 50000, Per: 1, EstimatedUnits: 4}, {Metric: "billable_input_images", Price: 25000, Per: 1, EstimatedUnits: 4}}}}
	rate := Rate{Region: "Global", Unit: "second", Price: 100000, Per: 1, Components: []core.PricingComponent{{Metric: "billable_input_images", Price: 50000, Per: 1}, {Metric: "input_video_duration", Price: 100000, Per: 1}, {Metric: "output_video_duration", Price: 100000, Per: 1}}}
	got, err := Reprice(base, rate)
	if err != nil || got.ExpectedUSD != 1100000 || got.Pricing.Price != 100000 || got.EstimatedUnits != 5 || got.Pricing.Components[0].EstimatedUnits != 5 {
		t.Fatal("scalar/component tariff disagreed", got, err)
	}
	if base.Pricing.Price != 50000 || base.Pricing.Components[0].Price != 50000 {
		t.Fatal("accepted snapshot mutated")
	}
}

func TestComponentRefreshRejectsInconsistentScalarAndPreservesComponentOnlyTariff(t *testing.T) {
	base := core.TaskCost{QuantityUnknown: true, Pricing: core.PricingSnapshot{Currency: "USD", Region: "Global", Unit: "token", Per: 1000000, Source: "https://official.example/pricing", Components: []core.PricingComponent{{Metric: "prompt_tokens", Price: 900000, Per: 1000000}, {Metric: "completion_tokens", Price: 3600000, Per: 1000000}}}}
	rate := Rate{Region: "Global", Unit: "token", Per: 1000000, Components: []core.PricingComponent{{Metric: "completion_tokens", Price: 4000000, Per: 1000000}, {Metric: "prompt_tokens", Price: 1000000, Per: 1000000}}}
	got, err := Reprice(base, rate)
	if err != nil || !got.QuantityUnknown || got.Pricing.Price != 0 || got.ExpectedUSD != 0 || got.Pricing.Components[0].Price != 1000000 || !core.HasVerifiedQuantityUnknownTariff(got) {
		t.Fatal("component-only unknown tariff fabricated a primary or quantity", got, err)
	}
	rate.Price = 5000000
	if _, err := Reprice(base, rate); err == nil {
		t.Fatal("contradictory primary scalar accepted")
	}
	rate.Price = 1000000
	rate.Components[1].Per = 1
	if _, err := Reprice(base, rate); err == nil {
		t.Fatal("component divisor changed")
	}
	rate.Components[1].Per = 1000000
	rate.Components[1].Metric = "undocumented"
	if _, err := Reprice(base, rate); err == nil {
		t.Fatal("new metric introduced")
	}
	if base.Pricing.Price != 0 || base.Pricing.Components[0].Price != 900000 {
		t.Fatal("base tariff mutated")
	}
}

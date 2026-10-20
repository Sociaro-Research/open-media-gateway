package core

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"testing"
)

func TestExplicitInputVideoBillingAndImmutableSnapshot(t *testing.T) {
	for _, billInput := range []bool{false, true} {
		cost := TaskCost{Pricing: PricingSnapshot{Currency: "USD", Unit: "second", Per: 1000, Price: money.USD(100 * money.Scale), BillInputVideo: &billInput}}
		data, _ := json.Marshal(cost)
		var restored TaskCost
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		usage := ProviderUsage{Metrics: []UsageMetric{{Name: "output_video_duration", Quantity: 5}, {Name: "input_video_duration", Quantity: 2}}}
		if !restored.MeterUsage(usage) {
			t.Fatal("complete usage rejected")
		}
		want := money.USD(500000)
		if billInput {
			want = 700000
		}
		if *restored.MeteredUSD != want {
			t.Fatal("wrong input/output billing", billInput, restored.MeteredUSD)
		}
		missing := TaskCost{Pricing: cost.Pricing}
		if missing.MeterUsage(ProviderUsage{Metrics: usage.Metrics[:1]}) == billInput {
			t.Fatal("missing charged input was silently metered", billInput)
		}
	}
}

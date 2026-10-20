package core

import (
	"encoding/json"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestMarkupSerializationPreservesProviderCosts(t *testing.T) {
	metered, tariff, cash := money.USD(1000000), money.USD(2000000), money.USD(0)
	cost := TaskCost{MarkupPercent: 2500, ExpectedUSD: 1000000, MeteredUSD: &metered, TariffUSD: &tariff, ActualUSD: &cash}
	for _, source := range []string{"invoice", "free_provider", "provider_failed_no_charge"} {
		cost.ActualSource = source
		b, err := json.Marshal(cost)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			CustomerExpected money.USD `json:"customer_expected_usd"`
			CustomerMetered  money.USD `json:"customer_metered_usd"`
			CustomerTariff   money.USD `json:"customer_tariff_usd"`
		}
		if err = json.Unmarshal(b, &result); err != nil {
			t.Fatal(err)
		}
		wantMetered, wantTariff := money.USD(1250000), money.USD(2500000)
		if source != "invoice" {
			wantMetered, wantTariff = 0, 0
		}
		if result.CustomerExpected != 1250000 || result.CustomerMetered != wantMetered || result.CustomerTariff != wantTariff {
			t.Fatalf("%s: %+v", source, result)
		}
		var restored TaskCost
		if err = json.Unmarshal(b, &restored); err != nil {
			t.Fatal(err)
		}
		if restored.MarkupPercent != 2500 || restored.ExpectedUSD != 1000000 || *restored.MeteredUSD != metered || *restored.TariffUSD != tariff || *restored.ActualUSD != cash {
			t.Fatalf("provider prices changed: %+v", restored)
		}
	}
}

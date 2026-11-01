package core

import (
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"strings"
	"testing"
	"time"
)

func TestUnknownQuantityCustomerProjectionNeverExposesProviderFinancials(t *testing.T) {
	metered, tariff, paid := money.USD(2000000), money.USD(3000000), money.USD(1100000)
	cost := TaskCost{QuantityUnknown: true, QuotedAt: time.Now(), MarkupPercent: 1000, MeteredUSD: &metered, TariffUSD: &tariff, ActualUSD: &paid, InvoiceIDs: []string{"private-invoice-marker"}, BillingInstanceID: "private-instance-marker", Pricing: PricingSnapshot{Currency: "USD", Unit: "token", Price: 123456, Per: 1000000, Source: "private-tariff-marker"}}
	value := map[string]any{"cost": cost, "model_price": MediaModelPricing{Known: true, USDPerMillionTokens: 12.3456, ReferenceQuote: &cost, ReferenceDescription: "private-price-description $0.123456 per provider token"}}
	original, _ := json.Marshal(value)
	for _, project := range []func(any) ([]byte, error){CustomerJSON, SalesJSON} {
		raw, err := project(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{"private-price-description", "0.123456", "private-invoice-marker", "private-instance-marker", "private-tariff-marker", `"actual_usd"`, `"pricing"`, `"invoice_ids"`, `"billing_instance_id"`, `"usd_per_million_tokens"`, `"reference_price_usd"`, `"expected_usd"`, `"customer_expected_usd"`} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("provider financial disclosure", secret, string(raw))
			}
		}
		if !strings.Contains(string(raw), `"quantity_unknown":true`) || !strings.Contains(string(raw), `"metered_usd":2.2`) || !strings.Contains(string(raw), `"tariff_usd":3.3`) {
			t.Fatal("safe customer quantity/metering lost", string(raw))
		}
	}
	after, _ := json.Marshal(value)
	if string(original) != string(after) {
		t.Fatal("projection mutated private accounting")
	}
}

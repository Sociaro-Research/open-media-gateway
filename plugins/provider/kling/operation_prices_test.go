package kling

import (
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func TestExplicitAccountOperationTariffsDoNotInventPublicPrices(t *testing.T) {
	a, _ := New("synthetic", "", nil)
	if _, _, err := a.quoteFixedOperation("selection_init"); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("unknown tariff is not free", err)
	}
	for _, bad := range []string{"0", "-1", "NaN", "invalid"} {
		if a.setOperationPrices(providerkit.Credentials{Extra: map[string]string{"selection_init_price": bad}}) == nil {
			t.Fatal("invalid tariff", bad)
		}
	}
	if err := a.setOperationPrices(providerkit.Credentials{Extra: map[string]string{"selection_init_price": "0.123456", "avatar_std_price": "0.5", "element_multi_image_price": "0.1"}}); err != nil {
		t.Fatal(err)
	}
	q, _, err := a.quoteFixedSessionOperation("kling-selection-init")
	if err != nil || q.ExpectedUSD != 123456 || q.Pricing.Price != 123456 || q.Pricing.Unit != "request" || !q.Pricing.AccountSpecific || q.Pricing.Source != "Operator-confirmed account tariff" {
		t.Fatal(q, err)
	}
	if _, _, err := a.quoteFixedSessionOperation("kling-selection-preview"); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("configured init leaked into preview")
	}
	if _, _, err := a.quoteFixedOperation("avatar_pro"); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("configured std leaked into pro")
	}
}
func TestReviewedKlingSourcesIncludeAllPriceContracts(t *testing.T) {
	definition := Definition()
	seen := map[string]bool{}
	required := map[string]string{"pricing": priceMarkdownSource, "pricing-image": imagePriceSource + ".md", "pricing-effects": effectsPriceSource + ".md", "pricing-ecommerce": ecommercePriceSource + ".md"}
	for _, source := range definition.CatalogSources {
		if seen[source.ID] {
			t.Fatal("duplicate source identity", source.ID)
		}
		seen[source.ID] = true
		if want, ok := required[source.ID]; ok {
			if source.URL != want {
				t.Fatal(source.ID, source.URL, want)
			}
			delete(required, source.ID)
		}
		if definition.CatalogSourceAliases[source.URL[:len(source.URL)-3]] != source.URL {
			t.Fatal("source alias", source.URL)
		}
	}
	if len(required) > 0 {
		t.Fatal("missing authoritative tariff source", required)
	}
	for _, id := range []string{"kling4-omni", "doc-api-video-3-0-omni-video-omni", "doc-api-video-o1-video-omni", "doc-api-video-multi-elements", "doc-api-video-lip-sync"} {
		if !seen[id] {
			t.Fatal("missing supported operation contract source", id)
		}
	}
}

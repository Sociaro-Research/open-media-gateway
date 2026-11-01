package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/markup"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestCustomerCatalogProjectionAndLegacyDelivery(t *testing.T) {
	for _, input := range []string{
		`{"data":{"task":{"cost":{"expected_usd":1,"metered_usd":2,"markup_percent":25,"pricing":{"price":1,"per":1}},"usage":{"raw":{"amount":99},"estimated_cost":99}}}}`,
		`{"cost":{"expected_usd":1,"customer_expected_usd":1.25,"customer_metered_usd":2.5,"markup_percent":25,"pricing":{"price":1}}}`,
	} {
		b, err := CustomerJSON(json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), `"expected_usd":1.25`) || !strings.Contains(string(b), `"metered_usd":2.5`) {
			t.Fatal(string(b))
		}
		for _, hidden := range []string{"markup", "pricing", "raw", "estimated_cost", "customer_expected_usd", "customer_metered_usd", "customer_tariff_usd"} {
			if strings.Contains(string(b), hidden) {
				t.Fatal(string(b))
			}
		}
	}
	b, err := CustomerJSON(MediaModel{Metadata: &ModelMetadata{Facts: []ModelFact{{Label: "Published tariff", Value: "SECRET"}, {Label: "Provider: Max resolution", Value: "4K"}, {Label: "New unknown fact", Value: "SECRET"}}, Sources: []ModelSource{{Title: "SECRET", URL: "https://example.com"}}}, Pricing: MediaModelPricing{Known: true, ReferencePriceUSD: 1.25, USDPerMillionTokens: 123, ReferenceDescription: "1 image. Provider standard list-rate estimate; excludes organization markup"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"SECRET", "usd_per_million", "excludes", "markup", "sources"} {
		if strings.Contains(string(b), hidden) {
			t.Fatal(string(b))
		}
	}
	if !strings.Contains(string(b), "4K") || !strings.Contains(string(b), "Gateway price") {
		t.Fatal(string(b))
	}
}

type catalogMarkupRepository struct {
	memoryRepository
	rate money.Percent
	fail bool
}

func (r *catalogMarkupRepository) ApplyMarkup(_ context.Context, task *Task) error {
	if r.fail {
		return errors.New("unavailable")
	}
	task.Cost.MarkupPercent = r.rate
	return nil
}
func TestCatalogUsesCurrentMarkupWithoutMutatingCachedQuote(t *testing.T) {
	cost := TaskCost{ExpectedUSD: 1000000}
	provider := catalogProvider{models: []MediaModel{{ID: "image", MediaType: "image", Pricing: MediaModelPricing{Known: true, ReferencePriceUSD: 1, ReferenceQuote: &cost}}}}
	repo := &catalogMarkupRepository{}
	service := NewTaskServiceWithProviders(repo, &memoryQueue{}, map[string]MediaProvider{"test": provider}, map[string]string{"test": "image"}, "test")
	ctx := identity.WithResourceOwners(identity.WithOrganization(context.Background(), "org", false), identity.SharedOwnerID, identity.SharedOwnerID)
	for _, rate := range []money.Percent{2500, 0, 5000} {
		repo.rate = rate
		models := service.ListProviders(ctx)
		got := models[0].Models[0].Pricing
		if got.ReferencePriceUSD != 1+float64(rate)/10000 {
			t.Fatal(got)
		}
		if cost.MarkupPercent != 0 {
			t.Fatal("changed cached provider quote")
		}
	}
	repo.fail = true
	if service.ListProviders(ctx)[0].Models[0].Pricing.Known {
		t.Fatal("published unmarked provider price on failure")
	}
}

type policyCatalogRepository struct {
	memoryRepository
	policy markup.Policy
	calls  int
}

func (r *policyCatalogRepository) MarkupPolicy(context.Context, string) (markup.Policy, error) {
	r.calls++
	return r.policy, nil
}
func TestCatalogResolvesEveryModelWithOnePolicyRead(t *testing.T) {
	p := func(n money.Percent) *money.Percent { return &n }
	cost := TaskCost{ExpectedUSD: 1000000}
	models := []MediaModel{{ID: "X", Pricing: MediaModelPricing{Known: true, ReferenceQuote: &cost}}, {ID: "Y", Pricing: MediaModelPricing{Known: true, ReferenceQuote: &cost}}}
	repo := &policyCatalogRepository{policy: markup.Policy{System: markup.Config{Default: p(2000), Exceptions: []markup.Rule{{Provider: "openai", Percent: p(2500)}, {Provider: "openai", Model: "X", Percent: p(3000)}}}, Company: markup.Config{Version: 1, Exceptions: []markup.Rule{{Provider: "openai", Model: "X", Percent: p(0)}}}}}
	service := NewTaskServiceWithProviders(repo, &memoryQueue{}, map[string]MediaProvider{"openai": catalogProvider{models: models}, "alibaba": catalogProvider{models: models}}, map[string]string{"openai": "X", "alibaba": "X"}, "openai")
	ctx := identity.WithResourceOwners(identity.WithOrganization(context.Background(), "org", false), identity.SharedOwnerID, identity.SharedOwnerID)
	result := service.ListProviders(ctx)
	if repo.calls != 1 {
		t.Fatalf("%d reads for a catalog", repo.calls)
	}
	for _, provider := range result {
		for _, model := range provider.Models {
			want := 1.2
			if provider.ID == "openai" {
				want = 1.25
				if model.ID == "X" {
					want = 1
				}
			}
			if model.Pricing.ReferencePriceUSD != want {
				t.Fatalf("%s/%s: %v, want %v", provider.ID, model.ID, model.Pricing.ReferencePriceUSD, want)
			}
		}
	}
	if cost.MarkupPercent != 0 {
		t.Fatal("cached base cost mutated")
	}
	repo.policy.Company.Default = p(1000)
	for _, provider := range service.ListProviders(ctx) {
		for _, model := range provider.Models {
			want := 1.1
			if provider.ID == "openai" && model.ID == "X" {
				want = 1
			}
			if model.Pricing.ReferencePriceUSD != want {
				t.Fatal("stale policy", provider.ID, model)
			}
		}
	}
}

func TestImportedOperationalProvenanceRemainsAdministratorOnly(t *testing.T) {
	data, err := CustomerJSON(MediaModel{Metadata: &ModelMetadata{Operational: map[string]OperationalFact{"field": {Value: json.RawMessage(`"PRIVATE_SOURCE_VALUE"`), Source: ModelSource{URL: "https://private-source.example.test"}, Snapshot: "private-hash", CheckedAt: "2026-10-26T00:00:00Z"}}}})
	if err != nil || strings.Contains(string(data), "PRIVATE_SOURCE_VALUE") || strings.Contains(string(data), "private-source") {
		t.Fatal("new imported evidence bypassed existing customer metadata projection", string(data), err)
	}
}

func TestOrdinaryOperationalParameterIsNotImportedMetadata(t *testing.T) {
	data, err := CustomerJSON(map[string]any{"parameters": map[string]any{"operational": true}})
	if err != nil || !strings.Contains(string(data), `"operational":true`) {
		t.Fatal("unrelated parameter was redacted", string(data), err)
	}
}

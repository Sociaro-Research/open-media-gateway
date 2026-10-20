package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

type providerCatalogFixture struct {
	catalogsync.Repository
	state catalogsync.State
	reads int
}

func (f *providerCatalogFixture) State(context.Context, string) (catalogsync.State, error) {
	f.reads++
	return f.state, nil
}
func TestCatalogSharedMetadataAndGenerationQuotesUseSamePublishedVersion(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	shared := identity.WithResourceOwners(ctx, identity.SharedOwnerID, identity.SharedOwnerID)
	const id = "dreamina-seedance-2-5-260628"
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || !strings.Contains(r.URL.Path, "models") {
			t.Fatal("catalogue check submitted generation", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"` + id + `"}]}`)), Request: r}, nil
	})
	if _, err := m.Update(shared, "bytedance", ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true)}); err != nil {
		t.Fatal(err)
	}
	provider := m.ManagedProviders()["bytedance"].(*managedProvider)
	models, err := provider.ListModels(shared)
	if err != nil || len(models) == 0 {
		t.Fatal(models, err)
	}
	model := models[0]
	request, _ := referenceRequest(model)
	before, _, err := provider.Quote(shared, model, request)
	if err != nil {
		t.Fatal(err)
	}
	rate := catalogsync.Rate{Region: before.Pricing.Region, Resolution: request.Options.Resolution, Unit: before.Pricing.Unit, Per: before.Pricing.Per, Price: before.Pricing.Price * 2, Source: "https://example.test/prices"}
	fixture := &providerCatalogFixture{state: catalogsync.State{Version: 7, Models: map[string]catalogsync.Entry{id: {ID: id, Metadata: &core.ModelMetadata{ReleaseDate: "2026-06", Description: "Updated description"}, Rates: []catalogsync.Rate{rate}}}}}
	m.SetCatalog(fixture)
	models, err = provider.ListModels(shared)
	if err != nil || models[0].Metadata.ReleaseDate != "2026-06" {
		t.Fatal(models, err)
	}
	after, _, err := provider.Quote(shared, models[0], request)
	want, _ := rate.Price.Multiply(before.EstimatedUnits, rate.Per)
	if err != nil || after.ExpectedUSD != want || !strings.HasPrefix(after.Pricing.Version, "catalog-7/") || after.Pricing.Source != rate.Source {
		t.Fatal("generation quote missed published tariff", before, after, err)
	}
	if models[0].Pricing.ReferenceQuote.ExpectedUSD != after.ExpectedUSD {
		t.Fatal("picker estimate differs from generation quote")
	}
	if before.Pricing.Price != rate.Price/2 {
		t.Fatal("accepted snapshot changed")
	}
	reads := fixture.reads
	if _, err := m.Update(ctx, "bytedance", ProviderUpdate{APIKey: pointer("personal"), Enabled: pointer(true)}); err != nil {
		t.Fatal(err)
	}
	personal, err := provider.ListModels(ctx)
	if err != nil || personal[0].Metadata.ReleaseDate == "2026-06" || fixture.reads != reads {
		t.Fatal("shared snapshot leaked into personal catalogue", err, fixture.reads, reads)
	}
	manual := model
	manual.Manual = true
	if applyCatalogModel(manual, fixture.state).Metadata.Description == "Updated description" {
		t.Fatal("manual descriptor overwritten")
	}
	unchanged, err := applyCatalogQuote(before, nil, manual, request, fixture.state)
	if err != nil || unchanged.ExpectedUSD != before.ExpectedUSD {
		t.Fatal("manual pricing overwritten")
	}
}
func TestCatalogTariffsOnlyMatchReviewedReferenceAndRegionRules(t *testing.T) {
	base := core.TaskCost{ExpectedUSD: money.USD(money.Scale), EstimatedUnits: 1, Pricing: core.PricingSnapshot{Currency: "USD", Region: "Singapore", Unit: "second", Per: 1000, Price: 1000 * money.USD(money.Scale)}}
	state := catalogsync.State{Version: 2, Models: map[string]catalogsync.Entry{"m": {Rates: []catalogsync.Rate{{Region: "Singapore", Resolution: "720p", Unit: "second", Per: 1000, Price: 2000 * money.USD(money.Scale)}}}}}
	for _, request := range []core.MediaRequest{{Options: core.MediaOptions{Resolution: "1080p"}}, {Options: core.MediaOptions{Resolution: "720p", VideoURL: "https://example.test/video.mp4"}}, {Options: core.MediaOptions{Resolution: "720p", AudioURL: "https://example.test/voice.wav"}}} {
		cost, err := applyCatalogQuote(base, nil, core.MediaModel{ID: "m"}, request, state)
		if err != nil || cost.ExpectedUSD != base.ExpectedUSD {
			t.Fatal("incompatible reference tariff applied", cost, err)
		}
	}
}

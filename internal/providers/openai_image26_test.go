package providers

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestOpenAIImage26StructuredTariffsAndFailedSourcePreservation(t *testing.T) {
	openaiImage26Replay(t, "testdata/openai-catalog/pricing-image26.html")
}

func openaiImage26Replay(t *testing.T, pricingFile string) {
	t.Helper()
	m, ctx, capture := openaiCatalogFixture(t)
	body, err := os.ReadFile(pricingFile)
	if err != nil {
		t.Fatal(err)
	}
	source := catalogsync.Source{ID: "pricing", URL: "https://developers.openai.com/api/docs/pricing", Parser: "openai-pricing"}
	entries, text, err := catalogsync.Parse(source, body)
	var selected []catalogsync.Entry
	for _, entry := range entries {
		if entry.ID == "gpt-image-2.6-flare" || entry.ID == "gpt-image-2.6-sunburst" {
			selected = append(selected, entry)
		}
	}
	entries = selected
	if err != nil || len(entries) != 2 {
		t.Fatal(entries, err)
	}
	for _, model := range capture.Reviewed {
		if model.ID == "gpt-image-2.6-flare" || model.ID == "gpt-image-2.6-sunburst" {
			capture.Models = append(capture.Models, model)
			capture.IDs = append(capture.IDs, model.ID)
		}
	}
	check := func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "openai", model, rate)
	}
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	observations := []catalogsync.Observation{{Source: source, Hash: "image26-pricing", Text: text, Entries: entries}}
	state, run := catalogsync.Reconcile(catalogsync.State{}, capture, observations, now, check)
	if len(run.OpenWarnings) != 0 {
		t.Fatal(run.OpenWarnings)
	}
	for _, entry := range entries {
		stored := state.Models[entry.ID]
		if stored.Contract == nil || stored.Inactive || len(stored.Rates) != 1 {
			t.Fatal(stored)
		}
		prices := map[string]money.USD{}
		for _, component := range stored.Rates[0].Components {
			prices[component.Metric] = component.Price
		}
		if !reflect.DeepEqual(prices, map[string]money.USD{"text_input_tokens": 5_000_000, "image_input_tokens": 8_000_000, "image_output_tokens": 30_000_000}) {
			t.Fatal(prices)
		}
		r, _ := referenceRequest(*stored.Contract)
		adapter, err := m.resolve(ctx, "openai", true)
		if err != nil {
			t.Fatal(err)
		}
		base, err := (&managedProvider{id: "openai", manager: m}).referenceQuoter(ctx, adapter)(*stored.Contract, r)
		cost, err := applyCatalogQuote(base, err, *stored.Contract, r, state)
		if err != nil || cost.ExpectedUSD != base.ExpectedUSD || len(cost.Pricing.Components) != 3 {
			t.Fatal(cost, base, err)
		}
		for _, c := range cost.Pricing.Components {
			if c.Metric == "image_output_tokens" && c.EstimatedUnits != 1449 {
				t.Fatal("2.6 estimate inherited old calculator", c)
			}
		}
	}
	// Both a source error and an unreviewed future ID must preserve confirmed
	// model contracts, tariffs, activation and published catalogue version.
	failed := []catalogsync.Observation{{Source: source, Error: "fixture source unavailable"}}
	capture.IDs = append(capture.IDs, "gpt-image-2.6-flare-2099-01-01")
	next, failedRun := catalogsync.Reconcile(state, capture, failed, now.Add(time.Hour), check)
	if next.Version != state.Version || len(failedRun.Changes) != 0 || next.Models["gpt-image-2.6-flare-2099-01-01"].Contract != nil {
		t.Fatal(next.Version, state.Version, failedRun)
	}
	for _, id := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		before, _ := json.Marshal(state.Models[id])
		after, _ := json.Marshal(next.Models[id])
		if string(before) != string(after) {
			t.Fatal("failed input changed confirmed entry", id)
		}
	}
}

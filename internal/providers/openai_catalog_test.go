package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func openaiCatalogFixture(t *testing.T) (*Manager, context.Context, catalogsync.Capture) {
	t.Helper()
	m, _, ctx := fixtureConnection(t)
	ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
	body, err := os.ReadFile("testdata/openai-catalog/api-models.json")
	if err != nil {
		t.Fatal(err)
	}
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Fatal("catalogue submitted a generation", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})
	if _, err = m.Update(ctx, "openai", ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true), Connection: &providerkit.Connection{}}); err != nil {
		t.Fatal(err)
	}
	capture, err := m.CaptureCatalog(ctx, "openai")
	if err != nil || len(capture.Reviewed) != 24 || len(capture.Inactive) != 5 || len(capture.TariffAliases) != 11 {
		t.Fatal(capture, err)
	}
	return m, ctx, capture
}

func TestOpenAIReportWarningsResolveAfterCodeUpgrade(t *testing.T) {
	openaiReportReplay(t, "testdata/openai-catalog/pricing.html")
}

func TestOpenAIReviewedSpeechShutdownBackfillsExistingCatalogue(t *testing.T) {
	_, _, capture := openaiCatalogFixture(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	state, _ := catalogsync.Reconcile(catalogsync.State{}, capture, nil, now, func(core.MediaModel, catalogsync.Rate) error { return nil })
	ids := []string{"tts-1", "tts-1-hd", "gpt-4o-mini-tts-2025-03-20", "gpt-4o-mini-tts-2025-12-15"}
	// Model the existing catalogue before this lifecycle review, without
	// assuming that a live /models response includes shutdown_date.
	for _, id := range ids {
		entry := state.Models[id]
		entry.Metadata.ShutdownDate = ""
		var sources []core.ModelSource
		for _, source := range entry.Metadata.Sources {
			if source.URL != "https://developers.openai.com/api/docs/deprecations" {
				sources = append(sources, source)
			}
		}
		entry.Metadata.Sources = sources
		state.Models[id] = entry
	}
	next, run := catalogsync.Reconcile(state, capture, nil, now, func(core.MediaModel, catalogsync.Rate) error { return nil })
	for _, id := range ids {
		entry := next.Models[id]
		if entry.Metadata.ShutdownDate != "2027-01-06" || entry.Inactive || entry.Contract == nil {
			t.Fatal("missing future shutdown or premature deactivation", id, entry)
		}
		found := false
		for _, source := range entry.Metadata.Sources {
			found = found || source.URL == "https://developers.openai.com/api/docs/deprecations"
		}
		if !found || state.Models[id].Metadata.ShutdownDate != "" {
			t.Fatal("missing provenance or mutated previous version", id)
		}
	}
	if len(run.Changes) != len(ids) || next.Version != state.Version+1 || len(run.OpenWarnings) != 0 {
		t.Fatal("lifecycle backfill changed unrelated fields", run)
	}
	for _, id := range []string{"gpt-4o-mini-tts", "tts-1-1106", "tts-1-hd-1106"} {
		if next.Models[id].Metadata.ShutdownDate != "" {
			t.Fatal("inferred shutdown for an unlisted alias/snapshot", id)
		}
	}
	_, repeat := catalogsync.Reconcile(next, capture, nil, now.Add(time.Hour), func(core.MediaModel, catalogsync.Rate) error { return nil })
	if len(repeat.Changes) != 0 || repeat.EmailStatus != "not_needed" {
		t.Fatal("reviewed shutdown backfill repeated", repeat)
	}
}
func TestOpenAIDownloadedReportReplay(t *testing.T) {
	dir := os.Getenv("GATEWAY_OPENAI_REVIEW_DIR")
	if dir == "" {
		t.Skip("no downloaded public OpenAI sources")
	}
	openaiReportReplay(t, dir+"/pricing.html")
}
func openaiReportReplay(t *testing.T, pricingFile string) {
	t.Helper()
	m, ctx, capture := openaiCatalogFixture(t)
	body, err := os.ReadFile(pricingFile)
	if err != nil {
		t.Fatal(err)
	}
	source := catalogsync.Source{ID: "pricing", URL: "https://developers.openai.com/api/docs/pricing", Parser: "openai-pricing"}
	entries, text, err := catalogsync.Parse(source, body)
	if err != nil || len(entries) != 13 {
		t.Fatal("media pricing coverage", len(entries), err)
	}
	observations := []catalogsync.Observation{{Source: source, Hash: "pricing", Text: text, Entries: entries}, {Source: catalogsync.Source{ID: "_api", Parser: "api"}, Hash: "api"}}
	var previous catalogsync.State
	body, err = os.ReadFile("testdata/openai-catalog/report-state.json")
	if err != nil || json.Unmarshal(body, &previous) != nil || len(previous.Warnings) != 20 {
		t.Fatal("original report", err)
	}
	check := func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "openai", model, rate)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	state, run := catalogsync.Reconcile(previous, capture, observations, now, check)
	if len(run.ResolvedWarnings) != 20 || len(run.OpenWarnings) != 0 {
		t.Fatalf("resolved %d/20, open %+v", len(run.ResolvedWarnings), run.OpenWarnings)
	}
	for _, id := range []string{"sora-2", "sora-2-pro"} {
		e := state.Models[id]
		if !e.Inactive || e.Contract != nil || e.Metadata == nil || len(e.Metadata.Sources) == 0 {
			t.Fatal("retirement evidence or disablement missing", e)
		}
	}
	for _, model := range capture.Reviewed {
		if len(state.Models[model.ID].Rates) != 1 {
			t.Fatal("snapshot or alias tariff missing", model.ID)
		}
	}
	_, repeat := catalogsync.Reconcile(state, capture, observations, now.Add(24*time.Hour), check)
	if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || len(repeat.OpenWarnings) != 0 || repeat.EmailStatus != "not_needed" {
		t.Fatal("quiet repeat changed catalogue", repeat)
	}
	// A future snapshot must still require review, even if its base is known.
	capture.IDs = append(capture.IDs, "gpt-image-2-2099-01-01")
	_, unknown := catalogsync.Reconcile(state, capture, observations, now, check)
	if len(unknown.OpenWarnings) != 1 || unknown.OpenWarnings[0].Model != "gpt-image-2-2099-01-01" {
		t.Fatal("unknown snapshot inherited a contract", unknown)
	}

	// The same published version drives the picker and execution quote. A
	// changed list rate must affect exact snapshots as well as rolling aliases.
	for i := range observations[0].Entries {
		for j := range observations[0].Entries[i].Rates {
			r := &observations[0].Entries[i].Rates[j]
			r.Price *= 2
			for k := range r.Components {
				r.Components[k].Price *= 2
			}
		}
	}
	capture.IDs = capture.IDs[:len(capture.IDs)-1]
	updated, changed := catalogsync.Reconcile(state, capture, observations, now.Add(48*time.Hour), check)
	if len(changed.OpenWarnings) != 0 || len(changed.Changes) == 0 {
		t.Fatal(changed)
	}
	for _, model := range capture.Reviewed {
		r, _ := referenceRequest(model)
		adapter, err := m.resolve(ctx, "openai", true)
		if err != nil {
			t.Fatal(err)
		}
		base, err := (&managedProvider{id: "openai", manager: m}).referenceQuoter(ctx, adapter)(model, r)
		before, err := applyCatalogQuote(base, err, model, r, state)
		if err != nil {
			t.Fatal(model.ID, err)
		}
		after, err := applyCatalogQuote(base, nil, model, r, updated)
		delta := after.ExpectedUSD - before.ExpectedUSD*2
		if delta < 0 {
			delta = -delta
		}
		// Components are rounded to microdollars independently; doubling a
		// tariff can differ from doubling its already-rounded total by one unit
		// per component.
		if err != nil || delta > money.USD(max(1, len(before.Pricing.Components))) {
			t.Fatal("tariff refresh ignored", model.ID, before.ExpectedUSD, after.ExpectedUSD, err)
		}
		if base.ExpectedUSD != before.ExpectedUSD {
			t.Fatal("baseline planning amount changed", model.ID)
		}
		// Accepted jobs continue using their original token tariffs.
		if model.ID == "gpt-image-1-mini" {
			usage := core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "text_input_tokens", Quantity: 0}, {Name: "image_input_tokens", Quantity: 0}, {Name: "image_output_tokens", Quantity: 1000}}}
			if !before.MeterUsage(usage) || !after.MeterUsage(usage) || *before.MeteredUSD != money.USD(8000) || *after.MeteredUSD != money.USD(16000) {
				t.Fatal("snapshot metering changed", before, after)
			}
		}
	}
}

func TestConfiguredOpenAICataloguePreservesManualOverrides(t *testing.T) {
	m, ctx, _ := openaiCatalogFixture(t)
	adapter, err := m.resolve(ctx, "openai", true)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := adapter.(*connectionAdapter)
	wrapped.credentials.Connection.Models = []providerkit.ManualModel{{Model: core.MediaModel{ID: "tts-1-hd-1106", Manual: true}}}
	capture, err := m.CaptureCatalog(ctx, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if capture.TariffAliases["tts-1-hd-1106"] != "" {
		t.Fatal("native tariff overrides manual snapshot")
	}
	for _, model := range capture.Reviewed {
		if model.ID == "tts-1-hd-1106" {
			t.Fatal("native reviewed model overrides manual snapshot")
		}
	}
}

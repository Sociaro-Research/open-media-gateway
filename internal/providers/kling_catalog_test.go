package providers

import (
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingSynchronizedPricesMatchReviewedOperationContractsAndPreserveOverrides(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("catalogue and estimates contacted provider")
		return nil, nil
	})
	if _, err := m.Update(ctx, "kling", ProviderUpdate{APIKey: pointer("fixture-secret"), Enabled: pointer(true), Connection: &providerkit.Connection{ManualCatalog: false}}); err != nil {
		t.Fatal(err)
	}
	capture, err := m.CaptureCatalog(ctx, "kling")
	if err != nil {
		t.Fatal(capture, err)
	}
	assertKlingContracts(t, capture.Models)
	raw, err := os.ReadFile("../../plugins/provider/kling/testdata/pricing.md")
	if err != nil {
		t.Fatal(err)
	}
	source := catalogsync.Source{ID: "pricing", URL: "https://kling.ai/document-api/pricing/base/video.md", Parser: "document"}
	entries, err := m.InterpretCatalogSource(ctx, "kling", source, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	obs := catalogsync.Observation{Source: source, Entries: entries, Hash: "first", Text: string(raw)}
	state, run := catalogsync.Reconcile(catalogsync.State{}, capture, []catalogsync.Observation{obs}, time.Now(), func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "kling", model, rate)
	})
	documentedGaps := map[string]string{kling.OmniVideo3: "Video-character element tariff classification", kling.AvatarImageToVideo: "native std/pro modes", kling.MultiElementsEditing: "native std/pro modes", kling.LipSync: "partial-block rounding", kling.ResourceCreateElement: "numeric element-creation tariff"}
	for _, warning := range run.OpenWarnings {
		if required, ok := documentedGaps[warning.Model]; ok && warning.Reason == "Published tariff needs a structured pricing rule" && strings.Contains(string(warning.Evidence), required) {
			continue
		}
		if strings.Contains(warning.Reason, "tariff") || strings.Contains(warning.Reason, "price") {
			t.Fatal(warning)
		}
	}
	m.SetCatalog(&providerCatalogFixture{state: state})
	provider := m.ManagedProviders()["kling"].(*managedProvider)
	models, err := provider.ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := m.resolve(ctx, "kling", true)
	if err != nil {
		t.Fatal(err)
	}
	hook := adapter.(catalogRateRequestProvider)
	for _, model := range models {
		entry, ok := state.Models[model.ID]
		if !ok {
			continue
		}
		for _, rate := range entry.Rates {
			request, err := hook.CatalogRateRequest(model, rate)
			if err != nil {
				t.Fatal(model.ID, rate, err)
			}
			cost, _, err := provider.Quote(ctx, model, request)
			if err != nil || !strings.HasPrefix(cost.Pricing.Version, "catalog-") || cost.Pricing.Source != source.URL || cost.Pricing.Price != rate.Price || cost.Pricing.DurationBillingRule != rate.DurationBillingRule {
				t.Fatal(model.ID, rate, cost, err)
			}
		}
	}
	// A resolution price must never be projected onto undocumented Avatar modes.
	for _, mode := range []string{"std", "pro"} {
		_, _, err := provider.Quote(ctx, core.MediaModel{ID: kling.AvatarImageToVideo}, core.MediaRequest{Model: kling.AvatarImageToVideo, Options: core.MediaOptions{ImageURLs: []string{"https://cdn.example/avatar.png"}, AudioURLs: []string{"https://cdn.example/speech.wav"}, Parameters: map[string]any{"mode": mode}}})
		if !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("invented Avatar mode tariff", mode, err)
		}
	}
	changed := strings.Replace(string(raw), "($0.235)", "($0.240)", 1)
	updates, err := m.InterpretCatalogSource(ctx, "kling", source, changed)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range updates {
		state.Models[entry.ID] = entry
	}
	state.Version++
	m.SetCatalog(&providerCatalogFixture{state: state})
	request := core.MediaRequest{Model: kling.TextToVideo4, Prompt: "Synthetic fixture"}
	cost, _, err := provider.Quote(ctx, core.MediaModel{ID: kling.TextToVideo4, Resolutions: []string{"1080p", "720p"}}, request)
	if err != nil || cost.ExpectedUSD != 2400000 {
		t.Fatal(cost, err)
	}
	if _, err = m.Update(ctx, "kling", ProviderUpdate{Extra: map[string]string{"kling4_1080p_price": "0.200001"}}); err != nil {
		t.Fatal(err)
	}
	cost, _, err = provider.Quote(ctx, core.MediaModel{ID: kling.TextToVideo4}, request)
	if err != nil || cost.ExpectedUSD != 2000010 || !cost.Pricing.AccountSpecific {
		t.Fatal("public synchronization replaced account override", cost, err)
	}
	// A malformed later observation keeps confirmed rates and adds a warning.
	bad := catalogsync.Observation{Source: source, Error: "interpretation failed", FetchedHash: "bad", Text: "HTML shell"}
	next, failed := catalogsync.Reconcile(state, capture, []catalogsync.Observation{bad}, time.Now(), func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "kling", model, rate)
	})
	if len(next.Models[kling.TextToVideo4].Rates) != 2 || len(failed.OpenWarnings) == 0 {
		t.Fatal("failed source erased confirmed rates or warning", failed)
	}
}

func TestKlingDisabledConnectionCanAuthenticateWithoutEnablingGeneration(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	m.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"code":0,"resource_pack_subscribe_infos":[]}}`))}, nil
	})
	if _, err := m.Update(ctx, "kling", ProviderUpdate{APIKey: pointer("fixture-secret")}); err != nil {
		t.Fatal(err)
	}
	p, err := m.Check(ctx, "kling")
	if err != nil || p.Enabled || p.Status != "authenticated" || !strings.Contains(p.StatusMessage, "Provider is disabled") {
		t.Fatal(p, err)
	}
}

func TestKlingLegacyScheduleUsesOfficialMarkdownWithoutOverwritingCustomSources(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	for _, tc := range []struct {
		source catalogsync.Source
		want   string
	}{
		{catalogsync.Source{ID: "pricing", URL: "https://kling.ai/document-api/pricing/base/video", Parser: "document"}, "https://kling.ai/document-api/pricing/base/video.md"},
		{catalogsync.Source{ID: "custom", URL: "https://example.test/prices", Parser: "document"}, "https://example.test/prices"},
		{catalogsync.Source{ID: "custom", URL: "https://kling.ai/document-api/pricing/base/video", Parser: "json"}, "https://kling.ai/document-api/pricing/base/video"},
	} {
		got := m.ResolveCatalogSource(ctx, "kling", tc.source)
		if got.URL != tc.want || got.ID != tc.source.ID || got.Parser != tc.source.Parser {
			t.Fatal(got)
		}
	}
}

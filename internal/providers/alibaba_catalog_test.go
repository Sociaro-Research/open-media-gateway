package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/catalogsync"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

func alibabaCatalogFixture(t *testing.T) (*Manager, context.Context, catalogsync.Capture) {
	t.Helper()
	m, _, ctx := fixtureConnection(t)
	ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
	body, err := os.ReadFile("testdata/alibaba-catalog/api-models.json")
	if err != nil {
		t.Fatal(err)
	}
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/models") {
			t.Fatal("catalogue submitted a generation", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})
	if _, err = m.Update(ctx, "alibaba", ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true), Connection: &providerkit.Connection{}}); err != nil {
		t.Fatal(err)
	}
	capture, err := m.CaptureCatalog(ctx, "alibaba")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Reviewed) == 0 || len(capture.Excluded) == 0 {
		t.Fatal("configured connection discarded reviewed native catalogue information")
	}
	return m, ctx, capture
}

func TestConfiguredAlibabaCataloguePreservesManualOverrides(t *testing.T) {
	m, ctx, _ := alibabaCatalogFixture(t)
	adapter, err := m.resolve(ctx, "alibaba", true)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, ok := adapter.(*connectionAdapter)
	if !ok {
		t.Fatal("fixture must exercise the production connection wrapper")
	}
	wrapped.credentials.Connection.Models = []providerkit.ManualModel{{Model: core.MediaModel{ID: "qwen-image-2.1-pro"}}, {Model: core.MediaModel{ID: "voice-enrollment"}}}
	for _, model := range wrapped.ReviewedCatalogModels() {
		if model.ID == "qwen-image-2.1-pro" {
			t.Fatal("native review would overwrite a manual model")
		}
	}
	for _, entry := range wrapped.CatalogExclusions() {
		if entry.ID == "voice-enrollment" {
			t.Fatal("native scope decision would overwrite a manual model")
		}
	}
	wrapped.credentials.Connection.ManualCatalog = true
	if len(wrapped.ReviewedCatalogModels()) != 0 || len(wrapped.CatalogExclusions()) != 0 {
		t.Fatal("manual catalogue must not receive native catalogue changes")
	}
}

func TestAlibabaReportWarningsResolveAfterCodeUpgrade(t *testing.T) {
	alibabaReportReplay(t, "testdata/alibaba-catalog", false)
}
func TestAlibabaDownloadedReportReplay(t *testing.T) {
	dir := os.Getenv("GATEWAY_ALIBABA_REVIEW_DIR")
	if dir == "" {
		t.Skip("no downloaded Alibaba source snapshots")
	}
	alibabaReportReplay(t, dir, true)
}

func TestAlibabaProductionConnectionFollowup(t *testing.T) {
	dir := os.Getenv("GATEWAY_ALIBABA_REVIEW_DIR")
	if dir == "" {
		t.Skip("no production catalogue snapshot")
	}
	data, err := os.ReadFile(filepath.Join(dir, "production-version-2.json"))
	if err != nil {
		t.Skip("no production version 2 snapshot")
	}
	var previous catalogsync.State
	if err = json.Unmarshal(data, &previous); err != nil {
		t.Fatal(err)
	}
	m, ctx, capture := alibabaCatalogFixture(t)
	// The live check also discovered this exact new ID after the API fixture
	// was captured. It stays unreviewed rather than inheriting a 3.0 contract.
	capture.IDs = append(capture.IDs, "qwen-audio-3.1-tts-flash")
	var obs []catalogsync.Observation
	for _, id := range []string{"_api", "releases", "pricing", "image-capabilities", "retirements"} {
		obs = append(obs, previous.Observations[id])
	}
	check := func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "alibaba", model, rate)
	}
	now := time.Now().UTC()
	next, run := catalogsync.Reconcile(previous, capture, obs, now, check)
	if len(run.ResolvedWarnings) != 43 || len(run.OpenWarnings) != 3 {
		t.Fatalf("production follow-up resolved %d, open %d: %+v", len(run.ResolvedWarnings), len(run.OpenWarnings), run.OpenWarnings)
	}
	for _, w := range run.OpenWarnings {
		if w.Model != "qwen-audio-3.1-tts-next" && w.Model != "qwen-audio-3.1-tts-flash" {
			t.Fatal("reviewed contract or exclusion lost", w)
		}
	}
	_, repeat := catalogsync.Reconcile(next, capture, obs, now.Add(time.Hour), check)
	if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || len(repeat.OpenWarnings) != 3 {
		t.Fatal("production follow-up does not remain quiet", repeat)
	}
}
func alibabaReportReplay(t *testing.T, dir string, downloaded bool) {
	t.Helper()
	m, ctx, capture := alibabaCatalogFixture(t)
	var obs []catalogsync.Observation
	for _, tc := range []struct{ id, file, parser string }{{"pricing", "model-pricing", "alibaba-pricing"}, {"releases", "newly-released-models", "alibaba-releases"}, {"image-capabilities", "image-model", "alibaba-image-models"}, {"retirements", "retirement", "alibaba-retirements"}} {
		file := tc.id
		if downloaded {
			file = tc.file
		}
		data, err := os.ReadFile(filepath.Join(dir, file+".html"))
		if err != nil {
			t.Fatal(err)
		}
		source := catalogsync.Source{ID: tc.id, URL: "https://www.alibabacloud.com/help/en/model-studio/" + tc.file, Parser: tc.parser}
		if tc.id == "retirements" {
			source.URL = "https://www.alibabacloud.com/en/notice/model_studio_notice_of_retirement_for_selected_legacy_models_7d9"
		}
		entries, text, err := catalogsync.Parse(source, data)
		if err != nil {
			t.Fatal(tc.id, err)
		}
		obs = append(obs, catalogsync.Observation{Source: source, Hash: tc.id, Entries: entries, Text: text})
	}
	obs = append(obs, catalogsync.Observation{Source: catalogsync.Source{ID: "_api", Parser: "api"}, Hash: "api"})
	var previous catalogsync.State
	data, err := os.ReadFile("testdata/alibaba-catalog/report-state.json")
	if err != nil || json.Unmarshal(data, &previous) != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	state, run := catalogsync.Reconcile(previous, capture, obs, now, func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "alibaba", model, rate)
	})
	for _, w := range run.OpenWarnings {
		t.Log(w.Model, w.Source, w.Reason, string(w.Evidence))
	}
	for _, w := range run.OpenWarnings {
		if w.Model != "qwen-audio-3.1-tts-next" && w.Model != "qwen-audio-3.1-tts-flash" && w.Model != "qwen-tts-flash" {
			t.Fatalf("unexpected unresolved warning: %+v", w)
		}
		if w.Reason != "Model requires an adapter contract before it can be enabled" || !strings.Contains(strings.ToLower(string(w.Evidence)), "token") {
			t.Fatalf("unsupported token pricing was not preserved as evidence: %+v", w)
		}
	}
	if len(run.ResolvedWarnings) != 207 {
		t.Fatalf("resolved %d of 207 original warnings", len(run.ResolvedWarnings))
	}
	_, repeat := catalogsync.Reconcile(state, capture, obs, now.Add(24*time.Hour), func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "alibaba", model, rate)
	})
	if len(repeat.Changes) != 0 || len(repeat.NewWarnings) != 0 || len(repeat.OpenWarnings) != len(run.OpenWarnings) {
		t.Fatal("repeat is not quiet", repeat.Changes, repeat.OpenWarnings)
	}
	for _, id := range []string{"qwen-image", "qwen3-tts-instruct-flash"} {
		if !state.Models[id].Inactive || state.Models[id].Contract != nil {
			t.Fatal("retired model enabled", id)
		}
	}
	for _, id := range []string{"happyoyster-1.0-adventure", "voice-enrollment", "qwen3.5-omni-plus-realtime"} {
		if state.Models[id].ExcludedReason == "" || state.Models[id].Contract != nil {
			t.Fatal("out-of-scope service not documented", id)
		}
	}
}

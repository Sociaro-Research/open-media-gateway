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
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

func bytePlusCatalogFixture(t *testing.T) (*Manager, context.Context, catalogsync.Capture) {
	t.Helper()
	m, _, ctx := fixtureConnection(t)
	ctx = identity.WithResourceOwners(identity.WithOwnerID(ctx, identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
	ids := []string{"dreamina-seedance-2-5-260628", "dreamina-seedance-2-0-260128", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-mini-260615", "seedance-1-5-pro-251215", "seedance-1-0-pro-250528", "seedance-1-0-pro-fast-251015", "seedance-1-0-lite-t2v-250428", "seedance-1-0-lite-i2v-250428", "dola-seedream-5-0-pro-260628", "dola-seedream-5-0-flash-260915", "seedream-5-0-260128", "seedream-5-0-lite-260128", "seedream-4-5-251128", "seedream-4-0-250828", "seedream-4-0-20260415", "seedream-3-0-t2i-250415"}
	var rows []map[string]string
	for _, id := range ids {
		rows = append(rows, map[string]string{"id": id})
	}
	body, _ := json.Marshal(map[string]any{"data": rows})
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || !strings.HasSuffix(r.URL.Path, "/models") {
			t.Fatal("catalogue submitted a generation", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
	})
	if _, err := m.Update(ctx, "bytedance", ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true)}); err != nil {
		t.Fatal(err)
	}
	capture, err := m.CaptureCatalog(ctx, "bytedance")
	if err != nil {
		t.Fatal(err)
	}
	return m, ctx, capture
}

// Optional acceptance against complete, read-only official source snapshots.
func TestBytePlusDownloadedReportReplay(t *testing.T) {
	dir := os.Getenv("GATEWAY_BYTEPLUS_REVIEW_DIR")
	if dir == "" {
		t.Skip("no downloaded BytePlus source snapshots")
	}
	bytePlusReportReplay(t, dir, ".html")
}

func TestBytePlusFirstReportWarningsResolveAfterCodeUpgrade(t *testing.T) {
	bytePlusReportReplay(t, "testdata/byteplus-catalog", ".md")
}

func bytePlusReportReplay(t *testing.T, dir, extension string) {
	t.Helper()
	m, ctx, capture := bytePlusCatalogFixture(t)
	var obs []catalogsync.Observation
	for _, tc := range []struct{ file, parser string }{{"models", "byteplus-models"}, {"pricing", "byteplus-pricing"}, {"releases", "byteplus-releases"}, {"archive", "byteplus-releases"}} {
		body, err := os.ReadFile(filepath.Join(dir, tc.file+extension))
		if err != nil {
			t.Fatal(err)
		}
		entries, evidence, err := catalogsync.Parse(catalogsync.Source{Parser: tc.parser}, body)
		if err != nil {
			t.Fatal(tc.file, err)
		}
		sourceID := tc.file
		if sourceID == "archive" {
			sourceID = "releases-archive"
		}
		obs = append(obs, catalogsync.Observation{Source: catalogsync.Source{ID: sourceID, URL: "https://example.test/" + tc.file, Parser: tc.parser}, Entries: entries, Text: evidence, Hash: tc.file})
	}
	obs = append(obs, catalogsync.Observation{Source: catalogsync.Source{ID: "_api", Parser: "api"}, Hash: "api"})
	var previous catalogsync.State
	data, err := os.ReadFile("testdata/byteplus-catalog/report-warnings.json")
	if err != nil || json.Unmarshal(data, &previous.Warnings) != nil {
		t.Fatal("original report fixture", err)
	}
	state, run := catalogsync.Reconcile(previous, capture, obs, time.Now(), func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "bytedance", model, rate)
	})
	for _, w := range run.OpenWarnings {
		t.Log(w.Model, w.Reason, string(w.Evidence))
	}
	if len(run.OpenWarnings) != 0 {
		t.Fatalf("expected no warning after review, got %d", len(run.OpenWarnings))
	}
	if len(run.ResolvedWarnings) != 21 {
		t.Fatalf("resolved %d of 21 original warnings", len(run.ResolvedWarnings))
	}
	if !state.Models["seedream-3-0-t2i-250415"].Inactive || state.Models["seedream-3-0-t2i-250415"].Contract != nil {
		t.Fatal("deactivated model became executable")
	}
	_, repeated := catalogsync.Reconcile(state, capture, obs, time.Now(), func(model core.MediaModel, rate catalogsync.Rate) error {
		return m.CheckCatalogRate(ctx, "bytedance", model, rate)
	})
	if len(repeated.Changes) != 0 || len(repeated.NewWarnings) != 0 || len(repeated.OpenWarnings) != 0 {
		t.Fatal("unchanged daily check is not stable", repeated)
	}
}

func TestBytePlusCatalogAudioAndReferenceImageTariffs(t *testing.T) {
	m, ctx, capture := bytePlusCatalogFixture(t)
	byID := map[string]core.MediaModel{}
	for _, model := range capture.Models {
		byID[model.ID] = model
	}
	if len(capture.Models) != 15 {
		t.Fatal("duplicate aliases or deactivated model leaked into executable catalogue", len(capture.Models))
	}
	pro := byID["dola-seedream-5-0-pro-260628"]
	request, _ := referenceRequest(pro)
	request.Options.Resolution = "1.5K"
	request.Options.ImageURLs = []string{"https://example.test/1.png", "https://example.test/2.png", "https://example.test/3.png"}
	d, _ := m.findSpec("bytedance")
	before, err := d.Quote("https://ark.ap-southeast.bytepluses.com", pro, request, time.Now())
	if err != nil || before.ExpectedUSD != 51_000 {
		t.Fatal(before, err)
	}
	rate := catalogsync.Rate{Region: "Singapore", Resolution: "1.5K", Unit: "image", Per: 1, Price: 50_000, InputImages: &catalogsync.ImageInputTariff{Free: 1, Price: 4_000}}
	if err := m.CheckCatalogRate(ctx, "bytedance", pro, rate); err != nil {
		t.Fatal(err)
	}
	state := catalogsync.State{Version: 2, Models: map[string]catalogsync.Entry{pro.ID: {Rates: []catalogsync.Rate{rate}}}}
	after, err := applyCatalogQuote(before, nil, pro, request, state)
	if err != nil || after.ExpectedUSD != 58_000 || after.Pricing.FixedUSD != 8_000 || before.ExpectedUSD != 51_000 || before.Pricing.FixedUSD != 6_000 {
		t.Fatal("surcharge or accepted snapshot incorrect", before, after, err)
	}
	video := byID["seedance-1-5-pro-251215"]
	silent, audio := false, true
	state.Models = map[string]catalogsync.Entry{video.ID: {Rates: []catalogsync.Rate{{Region: "Singapore", Unit: "token", Per: 1_000_000, Price: money.USD(3 * money.Scale), Audio: &silent}, {Region: "Singapore", Unit: "token", Per: 1_000_000, Price: money.USD(6 * money.Scale), Audio: &audio}}}}
	for _, on := range []bool{false, true} {
		request, _ = referenceRequest(video)
		request.Options.GenerateAudio = &on
		before, err = d.Quote("https://ark.ap-southeast.bytepluses.com", video, request, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		after, err = applyCatalogQuote(before, nil, video, request, state)
		want := money.USD(3 * money.Scale)
		if on {
			want *= 2
		}
		if err != nil || after.Pricing.Price != want {
			t.Fatal("audio tariff cross-applied", on, after, err)
		}
	}
}

func TestBytePlusVideoReferenceSelectorAndFrozenReserve(t *testing.T) {
	m, _, capture := bytePlusCatalogFixture(t)
	var model core.MediaModel
	for _, candidate := range capture.Models {
		if candidate.ID == "dreamina-seedance-2-5-260628" {
			model = candidate
		}
	}
	d, _ := m.findSpec("bytedance")
	on := true
	state := catalogsync.State{Version: 3, Models: map[string]catalogsync.Entry{model.ID: {Rates: []catalogsync.Rate{
		{Region: "Singapore", Resolution: "480p", Unit: "token", Per: 1_000_000, Price: 20_000_000},
		{Region: "Singapore", Resolution: "480p", Unit: "token", Per: 1_000_000, Price: 8_000_000, VideoInput: &on},
	}}}}
	for _, references := range []bool{false, true} {
		request, _ := referenceRequest(model)
		if references {
			request.Options.VideoURL = "https://example.test/ref.mp4"
			request.Options.AudioURL = "https://example.test/ref.wav"
		}
		base, err := d.Quote("https://ark.ap-southeast.bytepluses.com", model, request, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		updated, err := applyCatalogQuote(base, nil, model, request, state)
		want := money.USD(20_000_000)
		if references {
			want = 8_000_000
		}
		if err != nil || updated.Pricing.Price != want || updated.EstimatedUnits != base.EstimatedUnits {
			t.Fatal("reference tariff cross-applied", references, updated, err)
		}
		if references && !strings.Contains(updated.Pricing.EstimateDescription, "Conservative reserve") {
			t.Fatal("reserve explanation lost")
		}
		if !updated.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "completion_tokens", Quantity: 100_000}}}) || *updated.MeteredUSD != want/10 {
			t.Fatal("snapshot not metered from actual tokens", updated)
		}
		if base.Pricing.Version == updated.Pricing.Version || base.Pricing.Price == updated.Pricing.Price {
			t.Fatal("accepted native snapshot mutated")
		}
	}
}

package minimax_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/internal/providers"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"github.com/google/uuid"
)

type managerRecords struct {
	mu   sync.Mutex
	rows map[string]providerconfig.Record
}

func managerRecordKey(ctx context.Context, id string) string {
	return identity.ProviderOwnerID(ctx) + "/" + id
}
func (r *managerRecords) Get(ctx context.Context, id string) (providerconfig.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.rows[managerRecordKey(ctx, id)]
	if !ok {
		return v, providerconfig.ErrNotFound
	}
	v.CredentialsCiphertext = append([]byte(nil), v.CredentialsCiphertext...)
	return v, nil
}
func (r *managerRecords) List(ctx context.Context) ([]providerconfig.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []providerconfig.Record{}
	for key, v := range r.rows {
		if strings.HasPrefix(key, identity.ProviderOwnerID(ctx)+"/") {
			out = append(out, v)
		}
	}
	return out, nil
}
func (r *managerRecords) Upsert(ctx context.Context, v providerconfig.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	v.UpdatedAt = time.Now().UTC()
	v.CredentialsCiphertext = append([]byte(nil), v.CredentialsCiphertext...)
	r.rows[managerRecordKey(ctx, v.ProviderID)] = v
	return nil
}

type managerCatalogue struct {
	catalogsync.Repository
	state catalogsync.State
}

func (r managerCatalogue) State(context.Context, string) (catalogsync.State, error) {
	return r.state, nil
}

// This exercises the real registered manager, connectionAdapter hook forwarding,
// catalogue validator and synchronized quotation. Provider HTTP is never needed:
// the cancelled context also prevents accidental external I/O by a regression.
func TestMiniMaxManagedCatalogueAcceptsEveryOfficialTariff(t *testing.T) {
	records := &managerRecords{rows: map[string]providerconfig.Record{}}
	cipher, err := providerconfig.NewCipher("manager-integration-fixture-cipher")
	if err != nil {
		t.Fatal(err)
	}
	manager := providers.NewManager(records, cipher, config.Config{ProviderHTTPTimeout: time.Second})
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), identity.SharedOwnerID), identity.SharedOwnerID, identity.SharedOwnerID)
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	key, enabled := "manager-integration-fixture", true
	if _, err = manager.Update(ctx, "minimax", providerkit.ProviderUpdate{APIKey: &key, Enabled: &enabled, Connection: &providerkit.Connection{ManualCatalog: false}}); err != nil {
		t.Fatal(err)
	}
	saved, err := records.Get(ctx, "minimax")
	if err != nil || strings.Contains(string(saved.CredentialsCiphertext), key) {
		t.Fatal("credentials were not encrypted", err)
	}
	capture, err := manager.CaptureCatalog(ctx, "minimax")
	if err != nil || len(capture.Models) != 37 {
		t.Fatal("registered manager catalogue", len(capture.Models), err)
	}
	raw, err := os.ReadFile("testdata/pricing.md")
	if err != nil {
		t.Fatal(err)
	}
	source := catalogsync.Source{ID: "pricing", URL: "https://platform.minimax.io/docs/pricing/overview.md", Parser: "document"}
	entries, err := manager.InterpretCatalogSource(ctx, "minimax", source, string(raw))
	if err != nil || len(entries) != 11 {
		t.Fatal("official tariff interpretation", len(entries), err)
	}
	models := map[string]core.MediaModel{}
	for _, m := range capture.Models {
		models[m.ID] = m
	}
	native, err := minimax.New(key, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	state := catalogsync.State{Version: 7, Models: map[string]catalogsync.Entry{}}
	count := 0
	for _, entry := range entries {
		state.Models[entry.ID] = entry
		for _, rate := range entry.Rates {
			count++
			if err = manager.CheckCatalogRate(ctx, "minimax", models[entry.ID], rate); err != nil {
				t.Errorf("managed tariff %s/%s/%ds/video=%v: %v", entry.ID, rate.Resolution, rate.Duration, rate.VideoInput != nil && *rate.VideoInput, err)
			}
		}
	}
	if count != 51 {
		t.Fatal("tariff coverage changed", count)
	}
	manager.SetCatalog(managerCatalogue{state: state})
	managed, err := manager.Provider(ctx, "minimax")
	if err != nil {
		t.Fatal(err)
	}
	quoter, ok := managed.(core.CostQuoter)
	if !ok {
		t.Fatal("managed provider lacks quotation")
	}
	for _, entry := range entries {
		for _, rate := range entry.Rates {
			m := models[entry.ID]
			if entry.ID == minimax.Regeneration {
				// Protected source-task quote needs a real owned source; its native
				// and PostgreSQL tests cover actual source metering separately.
				continue
			}
			request, err := native.CatalogRateRequest(m, rate)
			if err != nil {
				t.Fatal(err)
			}
			expected, _, err := native.Quote(ctx, m, request)
			if err != nil {
				t.Fatal(err)
			}
			got, revision, err := quoter.Quote(ctx, m, request)
			if err != nil || revision == "" || got.ExpectedUSD != expected.ExpectedUSD || !strings.HasPrefix(got.Pricing.Version, "catalog-7/") {
				t.Fatalf("managed synchronized quote %s/%s/%ds: expected=%s got=%s revision_set=%v version=%s err=%v", m.ID, rate.Resolution, rate.Duration, expected.ExpectedUSD.String(), got.ExpectedUSD.String(), revision != "", got.Pricing.Version, err)
			}
		}
	}
}

func TestMiniMaxManagedSourceRegenerationUsesRefreshedTariffWithoutRefreshHTTP(t *testing.T) {
	var calls atomic.Int32
	vendorHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			id := "123"
			if body["source_task_id"] != nil {
				id = "777"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"task_id": id})
			return
		}
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		kind, resolution := "generation", "768P"
		if id == "777" {
			kind, resolution = "regeneration", "2K"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"task": map[string]any{"id": id, "model": "MiniMax-H3", "status": "succeeded", "task_type": kind, "resolution": resolution, "duration": 5, "modality": "video", "content": map[string]string{"url": "https://cdn.fixture/owned.mp4"}, "usage": map[string]int{"output_seconds": 5, "input_seconds": 4, "input_image_count": 9}}})
	})
	server := httptest.NewServer(vendorHandler)
	defer server.Close()
	records := &managerRecords{rows: map[string]providerconfig.Record{}}
	cipher, err := providerconfig.NewCipher("source-rate-fixture-cipher")
	if err != nil {
		t.Fatal(err)
	}
	// In-memory test connection overrides target only this loopback fixture;
	// no application config, real credential or provider endpoint is changed.
	manager := providers.NewManager(records, cipher, config.Config{ProviderHTTPTimeout: 5 * time.Second, OutboundPrivateHosts: []string{"127.0.0.1"}})
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "source-rate-owner"), identity.SharedOwnerID, identity.SharedOwnerID)
	key, enabled := "source-rate-fixture-key", true
	_, err = manager.Update(ctx, "minimax", providerkit.ProviderUpdate{APIKey: &key, Enabled: &enabled, Connection: &providerkit.Connection{Endpoints: map[string]string{"poll": server.URL + "/task/{task_id}"}}})
	if err != nil {
		t.Fatal(err)
	}
	capture, err := manager.CaptureCatalog(ctx, "minimax")
	if err != nil {
		t.Fatal(err)
	}
	var model core.MediaModel
	for _, m := range capture.Models {
		if m.ID == minimax.Regeneration {
			model = m
		}
	}
	if model.ID == "" {
		t.Fatal("source regeneration descriptor missing")
	}
	rate := catalogsync.Rate{Region: "Global", Unit: "second", Per: 1, Price: 100000, Source: "https://platform.minimax.io/docs/pricing/overview", Components: []core.PricingComponent{{Metric: "output_video_duration", Price: 100000, Per: 1}, {Metric: "input_video_duration", Price: 100000, Per: 1}, {Metric: "billable_input_images", Price: 50000, Per: 1}}}
	if err = manager.CheckCatalogRate(ctx, "minimax", model, rate); err != nil {
		t.Fatal("pure source tariff validation", err)
	}
	if calls.Load() != 0 {
		t.Fatal("catalogue refresh queried a task", calls.Load())
	}
	manager.SetCatalog(managerCatalogue{state: catalogsync.State{Version: 8, Models: map[string]catalogsync.Entry{minimax.Regeneration: {ID: minimax.Regeneration, Rates: []catalogsync.Rate{rate}}}}})
	managed, err := manager.Provider(ctx, "minimax")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := manager.ResolveAdmissionConnection(ctx, "minimax")
	if err != nil {
		t.Fatal(err)
	}
	native, err := minimax.New(key, "", &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		vendorHandler.ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	nativeContext := core.WithProviderConnectionScope(ctx, scope)
	original := core.MediaRequest{Model: "MiniMax-H3", Prompt: "Original scene", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Resolution: "768p", Ratio: "16:9", VideoURLs: []string{"https://media.fixture/original.mp4"}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{"https://media.fixture/original.mp4": {Type: "video", Duration: 4, AudioStreams: new(int)}}}}
	for i := 0; i < 9; i++ {
		original.Options.ImageURLs = append(original.Options.ImageURLs, "https://media.fixture/image.png")
	}
	task, err := native.Generate(nativeContext, original)
	if err != nil {
		t.Fatal(err)
	}
	resourceProvider, ok := managed.(core.ResourceProvider)
	if !ok {
		t.Fatal("managed resource provider absent")
	}
	source, err := resourceProvider.CreateResource(ctx, core.ResourceCreateRequest{Kind: "file_resource", Subtype: "regeneration_source", Name: "Owned source", GeneratedVideo: &core.GeneratedResourceSource{TaskID: uuid.NewString(), TaskHandle: task.ID, Model: "MiniMax-H3"}})
	if err != nil || len(source.Resources) != 1 {
		t.Fatal("source capability binding", err)
	}
	binding := uuid.NewString()
	request := core.MediaRequest{Model: minimax.Regeneration, Options: core.MediaOptions{ResourceReferences: []core.ResourceReference{{BindingID: binding, ReferenceID: "source_task"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "source_task", Kind: "file_resource", Subtype: "regeneration_source", RemoteID: source.Resources[0].RemoteID, ConnectionIdentity: scope.Revision}}}}
	quote, revision, err := managed.(core.CostQuoter).Quote(ctx, model, request)
	if err != nil || quote.ExpectedUSD != 1100000 || revision != scope.Revision || !strings.HasPrefix(quote.Pricing.Version, "catalog-8/") {
		t.Fatal("source quote dropped synchronized tariff", err, quote.ExpectedUSD, quote.Pricing.Version)
	}
	if quote.Pricing.Price != 100000 {
		t.Errorf("refreshed primary output tariff must match component and fetched rate: got=%s want=0.1", quote.Pricing.Price.String())
	}
	regenerated, err := native.Generate(nativeContext, request)
	if err != nil {
		t.Fatal(err)
	}
	result, err := managed.GetTask(ctx, regenerated.ID)
	if err != nil || result.Status != core.ProviderSucceeded || result.Usage == nil {
		t.Fatal("regeneration metering unavailable", err)
	}
	if !quote.MeterUsage(*result.Usage) || quote.MeteredUSD == nil || *quote.MeteredUSD != 1100000 {
		t.Fatal("source regeneration did not meter refreshed component prices")
	}
	foreign := identity.WithOwnerID(ctx, "foreign-owner")
	before := calls.Load()
	if _, _, err = managed.(core.CostQuoter).Quote(foreign, model, request); err == nil || calls.Load() != before {
		t.Fatal("foreign source quote reached provider")
	}
}

func TestMiniMaxManagedBaseFileReferenceKeepsRefreshedTariff(t *testing.T) {
	records := &managerRecords{rows: map[string]providerconfig.Record{}}
	cipher, err := providerconfig.NewCipher("base-file-rate-fixture")
	if err != nil {
		t.Fatal(err)
	}
	manager := providers.NewManager(records, cipher, config.Config{ProviderHTTPTimeout: time.Second})
	ctx := identity.WithResourceOwners(identity.WithOwnerID(context.Background(), "base-file-owner"), identity.SharedOwnerID, identity.SharedOwnerID)
	key, enabled := "base-file-fixture-key", true
	if _, err = manager.Update(ctx, "minimax", providerkit.ProviderUpdate{APIKey: &key, Enabled: &enabled, Connection: &providerkit.Connection{ManualCatalog: false}}); err != nil {
		t.Fatal(err)
	}
	capture, err := manager.CaptureCatalog(ctx, "minimax")
	if err != nil {
		t.Fatal(err)
	}
	var model core.MediaModel
	for _, m := range capture.Models {
		if m.ID == minimax.RegenerationVideo {
			model = m
		}
	}
	scope, err := manager.ResolveAdmissionConnection(ctx, "minimax")
	if err != nil {
		t.Fatal(err)
	}
	data := inspectedBaseVideoFixture()
	info, err := mediafile.Inspect(data, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	native, err := minimax.New(key, "", &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/v1/files/upload" {
			t.Fatal("unexpected base fixture route")
		}
		return workerJSON(map[string]any{"file": map[string]any{"file_id": "456", "bytes": len(data), "purpose": "video_generation_input"}, "base_resp": map[string]int{"status_code": 0}}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	file, err := native.UploadFile(core.WithProviderConnectionScope(ctx, scope), "base.mp4", "video/mp4", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	duration := info.Duration
	source := core.InspectedResourceSource(core.ResourceSourceInput{ReferenceID: uuid.NewString(), MediaType: "video", ContentType: info.ContentType, SizeBytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Width: info.Width, Height: info.Height, FPS: info.FPS, FrameCount: info.FrameCount, AudioStreams: info.AudioStreams, VideoStreams: info.VideoStreams, Duration: &duration})
	binding := uuid.NewString()
	request := core.MediaRequest{Model: minimax.RegenerationVideo, Prompt: "Original final scene", Options: core.MediaOptions{ReferenceMode: "references", ReferenceRoles: map[string]string{"video_1": "base_video"}, ResourceReferences: []core.ResourceReference{{BindingID: binding, ReferenceID: "video_1"}}, ResourceSnapshots: []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "video_1", Kind: "file_resource", Subtype: "video", RemoteID: file.Handle, ConnectionIdentity: scope.Revision, Sources: []core.ResourceSourceEvidence{source}}}}}
	video, audio := true, false
	rate := catalogsync.Rate{Region: "Global", Unit: "second", Per: 1, Price: 100000, VideoInput: &video, AudioInput: &audio, Source: "https://platform.minimax.io/docs/pricing/overview", Components: []core.PricingComponent{{Metric: "output_video_duration", Price: 100000, Per: 1}, {Metric: "input_video_duration", Price: 100000, Per: 1}, {Metric: "billable_input_images", Price: 50000, Per: 1}}}
	manager.SetCatalog(managerCatalogue{state: catalogsync.State{Version: 9, Models: map[string]catalogsync.Entry{model.ID: {ID: model.ID, Rates: []catalogsync.Rate{rate}}}}})
	managed, err := manager.Provider(ctx, "minimax")
	if err != nil {
		t.Fatal(err)
	}
	// No operation in this quote needs upstream I/O; a cancelled context makes
	// any accidental real task/provider call fail instead of reaching an account.
	quoteCtx, cancel := context.WithCancel(ctx)
	cancel()
	quote, _, err := managed.(core.CostQuoter).Quote(quoteCtx, model, request)
	if err != nil || quote.ExpectedUSD != 516667 || !strings.HasPrefix(quote.Pricing.Version, "catalog-9/") {
		t.Fatalf("file-bound base lost synchronized video selector: amount=%s version=%s err=%v", quote.ExpectedUSD.String(), quote.Pricing.Version, err)
	}
}

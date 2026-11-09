package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/bytedance"
)

func TestBytePlusManualRetirementUsesOutgoingIDBeforeProfileRouting(t *testing.T) {
	const retired = "seedance-1-5-pro-251215"
	const active = "dreamina-seedance-2-0-260128"
	cutoff := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ name, outgoing, profile string }{
		{"retired outgoing with active serializer", retired, active},
		{"active outgoing with retained retired serializer", active, retired},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := cutoff.Add(-time.Nanosecond)
			price := 0.25
			model := core.MediaModel{ID: tc.outgoing, MediaType: "video", Manual: true, MinDuration: 4, MaxDuration: 12, DefaultDuration: 5}
			credentials := Credentials{APIKey: "fixture", BaseURL: "https://ark.ap-southeast.bytepluses.com/api/v3", Connection: &providerkit.Connection{
				ManualCatalog: true,
				Models:        []providerkit.ManualModel{{Model: model, Profile: tc.profile, PriceUSD: &price}},
			}}
			posts := 0
			client := &http.Client{Transport: &connectionTransport{credentials: credentials, base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost {
					t.Fatal("manual admission discovered/polled unexpectedly", r.Method)
				}
				posts++
				var body struct {
					Model string `json:"model"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body.Model != tc.outgoing {
					t.Fatal("profile or replacement escaped as outgoing ID", body.Model)
				}
				return jsonResponseForRetirement(r, `{"id":"accepted"}`), nil
			})}}
			native, err := bytedance.New(credentials.APIKey, credentials.BaseURL, client)
			if err != nil {
				t.Fatal(err)
			}
			a := connectionAdapter{MediaProvider: native, definition: bytedance.Definition(), credentials: credentials, clock: func() time.Time { return now }}
			request := core.MediaRequest{Model: tc.outgoing, Prompt: "sunrise", Options: core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9"}}
			before, _ := json.Marshal(credentials)
			for _, instant := range []time.Time{cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(time.Nanosecond)} {
				now = instant
				blocked := tc.outgoing == retired && !now.Before(cutoff)
				models, err := a.ListModels(context.Background())
				if err != nil || len(models) != map[bool]int{true: 0, false: 1}[blocked] {
					t.Fatal(models, err)
				}
				validation := a.ValidateRequest(context.Background(), request)
				_, _, quoteErr := a.Quote(context.Background(), model, request)
				beforePosts := posts
				_, generateErr := a.Generate(context.Background(), request)
				if blocked {
					if !errors.Is(validation, core.ErrModelUnavailable) || !errors.Is(quoteErr, core.ErrModelUnavailable) || !errors.Is(generateErr, core.ErrModelUnavailable) || posts != beforePosts {
						t.Fatal("manual outgoing ID bypassed retirement", validation, quoteErr, generateErr, posts)
					}
				} else if validation != nil || quoteErr != nil || generateErr != nil || posts != beforePosts+1 {
					t.Fatal("valid active outgoing model was blocked by its serializer", validation, quoteErr, generateErr)
				}
			}
			after, _ := json.Marshal(credentials)
			if string(before) != string(after) || request.Model != tc.outgoing {
				t.Fatal("saved configuration or request identity was rewritten")
			}
		})
	}
}

func TestBytePlusReviewedCutoffSurvivesOlderSavedCatalogueMetadata(t *testing.T) {
	const id = "seedance-1-5-pro-251215"
	native := modelcatalog.Enrich("bytedance", core.MediaModel{ID: id, MediaType: "video"})
	stale := modelcatalog.Enrich("bytedance", core.MediaModel{ID: id})
	stale.Metadata.ShutdownAt = ""
	stale.Metadata.ShutdownDate = "2030-01-01"
	state := catalogsync.State{Models: map[string]catalogsync.Entry{id: {ID: id, Metadata: stale.Metadata}}}
	result := applyCatalogModel(native, state)
	if result.Metadata.ShutdownAt != "2026-11-11T09:00:00Z" || result.Metadata.ShutdownDate != "2026-11-11" {
		t.Fatal("saved catalogue displaced the compiled reviewed cutoff", result.Metadata)
	}
}

func jsonResponseForRetirement(r *http.Request, body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Request: r, Body: io.NopCloser(strings.NewReader(body))}
}

func TestBytePlusNativeCatalogueEnrichesReviewedFactsOnlyOnce(t *testing.T) {
	manager, ctx, capture := bytePlusCatalogFixture(t)
	provider, err := manager.Provider(ctx, "bytedance")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := provider.(core.ModelCatalog).ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, models := range map[string][]core.MediaModel{
		"captured live models":     capture.Models,
		"captured reviewed models": capture.Reviewed,
		"captured history":         capture.Inactive,
		"public model references":  listed,
	} {
		for _, model := range models {
			if model.Metadata == nil {
				continue
			}
			facts := map[core.ModelFact]bool{}
			for _, fact := range model.Metadata.Facts {
				if facts[fact] {
					t.Fatalf("%s duplicated compiled fact for %s: %+v", name, model.ID, fact)
				}
				facts[fact] = true
			}
		}
	}
}

func TestBytePlusHistoricalUsageAndReceiptKeepPinnedManualRevision(t *testing.T) {
	const retiredID = "seedance-1-5-pro-251215"
	manager, repo, ctx := fixtureConnection(t)
	calls := 0
	manager.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		t.Fatal("receipt or historical tariff lookup reached provider", r.Method, r.URL)
		return nil, errors.New("unexpected provider call")
	})
	created, err := manager.Create(ctx, CreateConnection{APIType: "bytedance", Name: "Historical fixture"})
	if err != nil {
		t.Fatal(err)
	}
	retired := manualVideo(manager)
	retired.Model.ID = retiredID
	retired.Model.DisplayName = "Accepted historical manual model"
	// Existing configuration validation clears client-supplied token tariffs.
	retired.Model.Pricing = core.MediaModelPricing{USDPerMillionTokens: 7}
	active := manualVideo(manager)
	config := &providerkit.Connection{ManualCatalog: true, Models: []providerkit.ManualModel{retired, active}}
	if _, err = manager.Update(ctx, created.ID, ProviderUpdate{APIKey: pointer("fixture"), Enabled: pointer(true), Connection: config}); err != nil {
		t.Fatal(err)
	}
	provider, err := manager.Provider(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Use an unaffected active ID only to obtain the immutable connection revision.
	_, revision, err := provider.(core.CostQuoter).Quote(ctx, active.Model, core.MediaRequest{Model: active.Model.ID})
	if err != nil {
		t.Fatal(err)
	}
	pinned := core.WithProviderRevision(ctx, revision)
	task := core.Task{ID: "accepted-legacy", Provider: created.ID, Model: retiredID, ProviderIdentity: revision,
		OwnerID: identity.OwnerID(ctx), ProviderOwnerID: identity.ProviderOwnerID(ctx), StorageOwnerID: identity.StorageOwnerID(ctx)}
	before, _ := json.Marshal(task)
	if _, err = manager.Update(ctx, created.ID, ProviderUpdate{Archived: pointer(true), Connection: &providerkit.Connection{ManualCatalog: true}}); err != nil {
		t.Fatal(err)
	}
	restarted := NewManager(repo, manager.cipher, manager.cfg)
	restarted.client = manager.client
	recovered, err := restarted.Provider(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.(core.AcceptedReceiptValidator).ValidateAcceptedReceipt(pinned, task); err != nil {
		t.Fatal("accepted receipt revalidated against retired/current model", err)
	}
	model, found, err := recovered.(core.HistoricalUsageCatalog).HistoricalUsageModel(pinned, retiredID)
	if err != nil || !found || model.ID != retiredID || !model.Pricing.Known || model.Pricing.USDPerMillionTokens != 0 || model.DisplayName != "Accepted historical manual model" {
		t.Fatal("pinned manual descriptor lost or inherited an untrusted/native token tariff", model, found, err)
	}
	foreign := identity.WithOwnerID(pinned, "unrelated-owner")
	if _, _, err := recovered.(core.HistoricalUsageCatalog).HistoricalUsageModel(foreign, retiredID); err == nil {
		t.Fatal("historical lookup crossed owner scope")
	}
	after, _ := json.Marshal(task)
	if string(before) != string(after) || calls != 0 {
		t.Fatal("accepted snapshot changed or provider was contacted", calls)
	}
}

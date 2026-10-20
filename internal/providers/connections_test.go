package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/config"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

type revisionRepository struct {
	*memoryProviderRepository
	revisionMu sync.Mutex
	revisions  map[string][]byte
}

func (r *revisionRepository) SaveRevision(ctx context.Context, id, revision string, b []byte) error {
	r.revisionMu.Lock()
	defer r.revisionMu.Unlock()
	r.revisions[providerRecordKey(ctx, id)+revision] = append([]byte(nil), b...)
	return nil
}
func (r *revisionRepository) GetRevision(ctx context.Context, id, revision string) ([]byte, error) {
	r.revisionMu.Lock()
	defer r.revisionMu.Unlock()
	b, ok := r.revisions[providerRecordKey(ctx, id)+revision]
	if !ok {
		return nil, providerconfig.ErrNotFound
	}
	return b, nil
}
func pointer[T any](v T) *T { return &v }
func fixtureConnection(t *testing.T) (*Manager, *revisionRepository, context.Context) {
	t.Helper()
	repo := &revisionRepository{memoryProviderRepository: &memoryProviderRepository{records: map[string]providerconfig.Record{}}, revisions: map[string][]byte{}}
	cipher, _ := providerconfig.NewCipher("connection-regression-secret")
	return NewManager(repo, cipher, config.Config{ProviderHTTPTimeout: time.Second}), repo, identity.WithOwnerID(context.Background(), identity.SystemOwnerID)
}
func manualVideo(m *Manager) providerkit.ManualModel {
	for _, kind := range m.Types() {
		if kind.ID == "bytedance" {
			for _, model := range kind.Profiles {
				if model.ID == "dreamina-seedance-2-0-260128" {
					return providerkit.ManualModel{Profile: model.ID, Model: core.MediaModel{ID: "private/Unlisted-Video", DisplayName: "Private video", MediaType: "video", Resolutions: []string{"720p"}, Ratios: []string{"16:9"}, MinDuration: 4, MaxDuration: 15, DefaultDuration: 5}, PriceUSD: pointer(0.25), ExtraBody: map[string]any{"private_option": true}}
				}
			}
		}
	}
	panic("profile missing")
}

func TestIndependentConnectionsManualModelAndPinnedPolling(t *testing.T) {
	m, repo, ctx := fixtureConnection(t)
	var calls []string
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Path, "models") {
			t.Fatal("manual connections must never discover models")
		}
		calls = append(calls, r.Method+" "+r.URL.String()+" "+r.Header.Get("Authorization")+" "+r.Header.Get("X-Private"))
		response := `{"id":"job-123"}`
		if r.Method == "POST" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["model"] != "private/Unlisted-Video" || body["private_option"] != true || body["beta"] != "enabled" {
				t.Fatalf("incorrect upstream payload %#v", body)
			}
		} else {
			response = `{"id":"job-123","status":"succeeded","content":{"video_url":"https://cdn.example/result.mp4"}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
	})
	first, err := m.Create(ctx, CreateConnection{APIType: "bytedance", Name: "Preview"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Create(ctx, CreateConnection{APIType: "bytedance", Name: "Production"})
	if err != nil || first.ID == second.ID {
		t.Fatal(second, err)
	}
	model := manualVideo(m)
	config := &providerkit.Connection{ManualCatalog: true, Headers: []providerkit.Header{{Name: "X-Private", Value: pointer("hidden-secret")}, {Name: "Authorization", Value: pointer("Special auth")}}, Endpoints: map[string]string{"generate": "https://private.example/create", "poll": "https://private.example/status/{task_id}"}, Models: []providerkit.ManualModel{model}, ExtraBody: map[string]any{"beta": "enabled"}}
	first, err = m.Update(ctx, first.ID, ProviderUpdate{APIKey: pointer("preview-key"), BaseURL: pointer("https://api.example/v3"), Enabled: pointer(true), Connection: config})
	if err != nil {
		t.Fatal(err)
	}
	if first.APIType != "bytedance" || first.Name != "Preview" || first.ModelCount != 1 {
		t.Fatalf("bad connection %#v", first)
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw), "hidden-secret") || strings.Contains(string(raw), "Special auth") || strings.Contains(string(raw), "preview-key") {
		t.Fatal("secret leaked")
	}
	p, err := m.Provider(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	cost, revision, err := p.(core.CostQuoter).Quote(ctx, model.Model, core.MediaRequest{Model: model.Model.ID})
	if err != nil || cost.ExpectedUSD != 250000 || cost.BillingRequired {
		t.Fatal(cost, err)
	}
	// Billing credentials affect only newly accepted quotes; their verification requirement is persisted.
	if _, err = m.Update(ctx, first.ID, ProviderUpdate{BillingAccessKeyID: pointer("billing-id"), BillingAccessKeySecret: pointer("billing-secret")}); err != nil {
		t.Fatal(err)
	}
	withBilling, _, err := p.(core.CostQuoter).Quote(ctx, model.Model, core.MediaRequest{Model: model.Model.ID})
	if err != nil || !withBilling.BillingRequired {
		t.Fatal("missing billing requirement", withBilling, err)
	}
	task, err := p.Generate(ctx, core.MediaRequest{Model: model.Model.ID, ProviderIdentity: revision, Prompt: "test", Options: core.MediaOptions{Duration: 5, Resolution: "720p", Ratio: "16:9"}})
	if err != nil || task.ID != "job-123" {
		t.Fatal(task, err)
	}
	// Replace endpoint, key and model settings; an accepted job must retain all three.
	config.Endpoints["poll"] = "https://other.example/jobs/{task_id}"
	config.Headers[0].Value = pointer("changed")
	config.Models = nil
	if _, err = m.Update(ctx, first.ID, ProviderUpdate{APIKey: pointer(""), Connection: config, Archived: pointer(true)}); err != nil {
		t.Fatal(err)
	}
	restarted := NewManager(repo, m.cipher, m.cfg)
	restarted.client = m.client
	p, err = restarted.Provider(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.GetTask(core.WithProviderRevision(ctx, revision), task.ID)
	if err != nil || result.Status != core.ProviderSucceeded {
		t.Fatal(result, err)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "https://private.example/status/job-123 Special auth hidden-secret") {
		t.Fatalf("wrong revision %v", calls)
	}
	available, err := restarted.Providers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := available[first.ID]; ok {
		t.Fatal("archived connection listed")
	}
	other, err := restarted.Get(ctx, second.ID)
	if err != nil || other.Configured {
		t.Fatal("connection settings crossed", err)
	}
	owner := identity.WithOwnerID(ctx, "other-owner")
	if _, err = restarted.Provider(owner, first.ID); !errors.Is(err, providerconfig.ErrNotFound) {
		t.Fatal("owner isolation failed", err)
	}
}

func TestManualConfigurationValidationAndSecretPreservation(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	created, err := m.Create(ctx, CreateConnection{APIType: "openai", Name: "Private"})
	if err != nil {
		t.Fatal(err)
	}
	c := &providerkit.Connection{ManualCatalog: true, Headers: []providerkit.Header{{Name: "X-Secret", Value: pointer("abc-secret")}}}
	info, err := m.Update(ctx, created.ID, ProviderUpdate{Connection: c})
	if err != nil {
		t.Fatal(err)
	}
	info, err = m.Update(ctx, created.ID, ProviderUpdate{Connection: info.Connection})
	if err != nil {
		t.Fatal(err)
	}
	record, _ := m.repo.Get(ctx, created.ID)
	saved, _ := m.decryptCredentials(record.CredentialsCiphertext)
	if *saved.Connection.Headers[0].Value != "abc-secret" {
		t.Fatal("secret overwritten")
	}
	for _, bad := range []*providerkit.Connection{{Headers: []providerkit.Header{{Name: "X-Key", Value: pointer("a\r\nb")}}}, {Headers: []providerkit.Header{{Name: "content-length", Value: pointer("3")}}}, {ExtraBody: map[string]any{"model": "override"}}, {Endpoints: map[string]string{"poll": "file:///etc/passwd"}}, {Models: []providerkit.ManualModel{manualVideo(m)}}} {
		if _, err := m.Update(ctx, created.ID, ProviderUpdate{Connection: bad}); err == nil {
			t.Fatalf("invalid config accepted %#v", bad)
		}
	}
}

func TestManualUnknownPriceAndCatalogFailure(t *testing.T) {
	m, _, ctx := fixtureConnection(t)
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})
	created, _ := m.Create(ctx, CreateConnection{APIType: "bytedance", Name: "Unknown price"})
	model := manualVideo(m)
	model.PriceUSD = nil
	_, err := m.Update(ctx, created.ID, ProviderUpdate{APIKey: pointer("key"), Enabled: pointer(true), Connection: &providerkit.Connection{Models: []providerkit.ManualModel{model}}})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := m.Provider(ctx, created.ID)
	models, err := p.(core.ModelCatalog).ListModels(ctx)
	if err != nil || len(models) != 1 {
		t.Fatal(models, err)
	}
	if _, _, err := p.(core.CostQuoter).Quote(ctx, model.Model, core.MediaRequest{Model: model.Model.ID}); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("unknown price treated as free", err)
	}
}

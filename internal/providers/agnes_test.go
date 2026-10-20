package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/agnes"
)

func TestAgnesEncryptedConfigurationPublicStatusAndQuotes(t *testing.T) {
	m, repo, ctx := fixtureConnection(t)
	submissions := 0
	m.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"agnes-image-2.5-flash"},{"id":"agnes-video-2.5"}]}`
		if r.Method == "POST" {
			submissions++
			if r.Header.Get("Authorization") != "Bearer agnes-fixture-secret" {
				t.Fatal("key was not decrypted for inference")
			}
			body = `{"data":[{"url":"https://cdn.example/result.png"}]}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	key, enabled := "agnes-fixture-secret", true
	p, err := m.Update(ctx, "agnes", ProviderUpdate{APIKey: &key, Enabled: &enabled})
	if err != nil || !p.Configured || p.Status != "unverified" || !strings.Contains(p.StatusMessage, "not verified") || len(p.Models) != 2 || submissions != 0 {
		t.Fatal(p, err, submissions)
	}
	record, err := repo.Get(ctx, "agnes")
	if err != nil || strings.Contains(string(record.CredentialsCiphertext), key) {
		t.Fatal("key was not encrypted", err)
	}
	public, _ := json.Marshal(p)
	if strings.Contains(string(public), key) {
		t.Fatal("key leaked in management output")
	}
	provider, err := m.Provider(ctx, "agnes")
	if err != nil {
		t.Fatal(err)
	}
	models, err := provider.(core.ModelCatalog).ListModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		if model.Pricing.ReferenceQuote == nil {
			t.Fatal("catalog quote unavailable", model)
		}
		if model.ID == agnes.DefaultModel && (!strings.Contains(model.Pricing.ReferenceDescription, "promotion") || strings.Contains(model.Pricing.ReferenceDescription, "excludes") && strings.Contains(model.Pricing.ReferenceDescription, "temporary promotions")) {
			t.Fatal(model.Pricing)
		}
	}
	checked, err := m.Check(ctx, "agnes")
	if err != nil || checked.Status != "unverified" || submissions != 0 {
		t.Fatal(checked, err, submissions)
	}
	list, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range list {
		if item.ID == "agnes" && item.Status != "unverified" {
			t.Fatal(item)
		}
	}
	r := core.MediaRequest{Model: agnes.DefaultModel, Prompt: "fixture"}
	cost, identity, err := provider.(core.CostQuoter).Quote(ctx, p.Models[0], r)
	if err != nil || identity == "" || cost.ExpectedUSD != 0 || cost.ActualUSD != nil {
		t.Fatal(cost, identity, err)
	}
	result, err := provider.Generate(core.WithProviderRevision(ctx, identity), r)
	if err != nil || result.ResultURL != "https://cdn.example/result.png" || submissions != 1 {
		t.Fatal(result, err)
	}
}

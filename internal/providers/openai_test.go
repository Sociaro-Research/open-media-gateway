package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/config"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

func TestOpenAIManagedConnectionQuotesAndOwnerIsolation(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("provider-test-secret")
	manager := NewManager(repo, cipher, config.Config{ProviderHTTPTimeout: time.Second})
	calls := 0
	manager.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.String() != "https://api.openai.com/v1/models" || r.Header.Get("Authorization") != "Bearer openai-fixture-secret" {
			t.Fatal("unexpected model check", r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-image-2.5-flare"},{"id":"tts-1"}]}`))}, nil
	})}
	owner := identity.WithOwnerID(context.Background(), "owner-openai")
	key, enabled := "openai-fixture-secret", true
	descriptor, err := manager.Update(owner, "openai", ProviderUpdate{APIKey: &key, Enabled: &enabled})
	if err != nil || descriptor.ModelCount != 2 || !descriptor.Enabled || descriptor.SupportsBillingCredentials || descriptor.BaseURL != "https://api.openai.com/v1" {
		t.Fatal(descriptor, err)
	}
	serialized, _ := json.Marshal(descriptor)
	if strings.Contains(string(serialized), key) {
		t.Fatal("secret leaked in admin response")
	}
	for _, record := range repo.records {
		if strings.Contains(string(record.CredentialsCiphertext), key) {
			t.Fatal("plaintext key in storage")
		}
	}
	managed := manager.ManagedProviders()["openai"]
	req := core.MediaRequest{Model: "gpt-image-2.5-flare", Prompt: "A lake"}
	cost, fingerprint, err := managed.(core.CostQuoter).Quote(owner, core.MediaModel{ID: req.Model, MediaType: "image"}, req)
	if err != nil || cost.ExpectedUSD <= 0 || len(cost.Pricing.Components) != 3 || len(fingerprint) != 64 {
		t.Fatal(cost, fingerprint, err)
	}
	if calls != 1 {
		t.Fatal("quote did not reuse catalog", calls)
	}
	other := identity.WithOwnerID(context.Background(), "other-owner")
	otherDescriptor, err := manager.Get(other, "openai")
	if err != nil || otherDescriptor.Configured || otherDescriptor.Enabled {
		t.Fatal(otherDescriptor, err)
	}
	enabled = false
	descriptor, err = manager.Update(owner, "openai", ProviderUpdate{Enabled: &enabled})
	if err != nil || descriptor.Enabled || !descriptor.Configured {
		t.Fatal(descriptor, err)
	}
	if _, _, err = managed.(core.CostQuoter).Quote(owner, core.MediaModel{}, req); err != ErrProviderDisabled {
		t.Fatal(err)
	}
	bad := "billing-key"
	if _, err = manager.Update(owner, "openai", ProviderUpdate{BillingAccessKeyID: &bad}); err == nil {
		t.Fatal("accepted unsupported billing credentials")
	}
}

func TestOpenAIEnvironmentBootstrap(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("provider-test-secret")
	manager := NewManager(repo, cipher, config.Config{Providers: map[string]providerkit.Settings{"openai": {"api_key": "environment-openai-secret"}}})
	if err := manager.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	record := repo.records["openai"]
	if !record.Enabled || record.CredentialSource != "environment" {
		t.Fatal(record)
	}
	credentials, err := manager.decryptCredentials(record.CredentialsCiphertext)
	if err != nil || credentials.BaseURL != "https://api.openai.com/v1" {
		t.Fatal(err)
	}
	// A second bootstrap must not overwrite a console edit or disabled record.
	record.Enabled = false
	record.CredentialSource = "console"
	repo.records["openai"] = record
	if err = manager.Bootstrap(context.Background()); err != nil || repo.records["openai"].Enabled {
		t.Fatal(err)
	}
}

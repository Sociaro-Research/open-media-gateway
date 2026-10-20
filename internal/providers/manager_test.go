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

type memoryProviderRepository struct {
	mu      sync.Mutex
	records map[string]providerconfig.Record
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func providerRecordKey(ctx context.Context, id string) string {
	ownerID := identity.OwnerID(ctx)
	if ownerID == identity.SystemOwnerID {
		return id
	}
	return ownerID + ":" + id
}

func (r *memoryProviderRepository) Get(ctx context.Context, id string) (providerconfig.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[providerRecordKey(ctx, id)]
	if !ok {
		return providerconfig.Record{}, providerconfig.ErrNotFound
	}
	record.CredentialsCiphertext = append([]byte(nil), record.CredentialsCiphertext...)
	return record, nil
}

func (r *memoryProviderRepository) List(context.Context) ([]providerconfig.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]providerconfig.Record, 0, len(r.records))
	for _, record := range r.records {
		result = append(result, record)
	}
	return result, nil
}

func (r *memoryProviderRepository) Upsert(ctx context.Context, record providerconfig.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now().UTC()
	}
	record.UpdatedAt = time.Now().UTC()
	record.CredentialsCiphertext = append([]byte(nil), record.CredentialsCiphertext...)
	r.records[providerRecordKey(ctx, record.ProviderID)] = record
	return nil
}

func TestManagerKeepsCredentialsIsolatedByOwner(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("provider-test-secret")
	manager := NewManager(repo, cipher, config.Config{
		ProviderHTTPTimeout: time.Second,
	})
	manager.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK, Status: http.StatusText(http.StatusOK), Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"seedream-4-0-250828"}]}`)), Request: request,
		}, nil
	})}
	ownerOne := identity.WithOwnerID(context.Background(), "11111111-1111-1111-1111-111111111111")
	ownerTwo := identity.WithOwnerID(context.Background(), "22222222-2222-2222-2222-222222222222")
	enabled := true
	key, endpoint := "owner-one-key", "https://provider.example/api/v3"
	if _, err := manager.Update(ownerOne, "bytedance", ProviderUpdate{Enabled: &enabled, APIKey: &key, BaseURL: &endpoint}); err != nil {
		t.Fatal(err)
	}
	first, err := manager.Get(ownerOne, "bytedance")
	if err != nil || !first.Configured || !first.Enabled {
		t.Fatalf("owner one provider = %#v, %v", first, err)
	}
	second, err := manager.Get(ownerTwo, "bytedance")
	if err != nil {
		t.Fatal(err)
	}
	if second.Configured || second.Enabled || second.CredentialHint != "" {
		t.Fatalf("owner two received owner one's settings: %#v", second)
	}
}

func TestManagerBootstrapsProvidersAndEncryptsEnvironmentCredentials(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, err := providerconfig.NewCipher("provider-test-secret")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(repo, cipher, config.Config{
		Providers:           map[string]providerkit.Settings{"bytedance": {"api_key": "secret-bytedance-key", "base_url": "https://example.com/api/v3"}},
		ProviderHTTPTimeout: time.Second,
	})
	if err := manager.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(repo.records) != len(manager.definitions()) {
		t.Fatalf("provider rows = %d, want %d", len(repo.records), len(manager.definitions()))
	}
	byteDance := repo.records["bytedance"]
	if !byteDance.Enabled || byteDance.CredentialSource != "environment" {
		t.Fatalf("unexpected environment seed: %#v", byteDance)
	}
	if strings.Contains(string(byteDance.CredentialsCiphertext), "secret-bytedance-key") {
		t.Fatal("API key was persisted as plaintext")
	}
	if !repo.records["mock"].Enabled || repo.records["alibaba"].Enabled {
		t.Fatal("unexpected default enabled states")
	}
}

func TestManagerUpdateValidatesAndDisablePreservesCredential(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("provider-test-secret")
	manager := NewManager(repo, cipher, config.Config{
		ProviderHTTPTimeout: time.Second,
	})
	manager.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{"data":[{"id":"seedream-4-0-250828"}]}`
		if request.Header.Get("Authorization") != "Bearer valid-key" {
			status, body = http.StatusUnauthorized, `{"error":{"message":"unauthorized"}}`
		}
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}
	if err := manager.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	enabled := true
	key, endpoint := "valid-key", "https://provider.example/api/v3"
	provider, err := manager.Update(context.Background(), "bytedance", ProviderUpdate{Enabled: &enabled, APIKey: &key, BaseURL: &endpoint})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.Enabled || !provider.Configured || provider.CredentialHint != "••••-key" {
		t.Fatalf("unexpected configured provider: %#v", provider)
	}
	before := append([]byte(nil), repo.records["bytedance"].CredentialsCiphertext...)
	disabled := false
	if _, err := manager.Update(context.Background(), "bytedance", ProviderUpdate{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if len(repo.records["bytedance"].CredentialsCiphertext) == 0 {
		t.Fatal("disabling removed stored credentials")
	}
	providerAdapter := manager.ManagedProviders()["bytedance"]
	enabledState, configured, err := providerAdapter.(interface {
		Availability(context.Context) (bool, bool, error)
	}).Availability(context.Background())
	if err != nil || enabledState || !configured {
		t.Fatalf("availability = %v, %v, %v", enabledState, configured, err)
	}
	if len(before) == 0 {
		t.Fatal("configured credentials were not stored")
	}
	_, err = manager.resolve(context.Background(), "bytedance", true)
	if !errors.Is(err, ErrProviderDisabled) {
		t.Fatalf("resolve error = %v, want disabled", err)
	}
}

// Connection settings must work without account-balance permissions. Separate
// billing credentials remain available for the actual-charge reconciliation job.
func TestProviderChecksDoNotReadFinancialData(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("provider-test-secret")
	manager := NewManager(repo, cipher, config.Config{ProviderHTTPTimeout: time.Second})
	owner := identity.WithOwnerID(context.Background(), "provider-billing-owner")
	allowBills, billCalls := false, 0
	manager.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[{"id":"seedream-4-0-250828"}]}`
		if r.URL.Host != "provider.example" {
			if !allowBills || r.URL.Query().Get("Action") != "ListBillDetail" {
				t.Errorf("unexpected financial request: %s", r.URL.Path)
				return nil, errors.New("financial access not allowed")
			}
			billCalls++
			body = `{"Result":{"Total":0,"List":[]}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	enabled := true
	key, endpoint := "inference-key", "https://provider.example/api/v3"
	if _, err := manager.Update(owner, "bytedance", ProviderUpdate{Enabled: &enabled, APIKey: &key, BaseURL: &endpoint}); err != nil {
		t.Fatal(err)
	}
	access, secret := "billing-key", "billing-secret"
	if _, err := manager.Update(owner, "bytedance", ProviderUpdate{BillingAccessKeyID: &access}); err == nil {
		t.Fatal("partial billing credentials accepted")
	}
	info, err := manager.Update(owner, "bytedance", ProviderUpdate{BillingAccessKeyID: &access, BillingAccessKeySecret: &secret})
	if err != nil || !info.BillingConfigured || !info.SupportsBillingCredentials {
		t.Fatalf("billing settings: %+v, %v", info, err)
	}
	info, err = manager.Check(owner, "bytedance")
	if err != nil || info.Status != "connected" {
		t.Fatalf("inference check: %+v, %v", info, err)
	}
	wire, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(wire, &fields); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"balance", "billing_portal_url"} {
		if _, ok := fields[removed]; ok {
			t.Fatalf("obsolete field %s returned", removed)
		}
	}
	allowBills = true
	bills, fingerprint, err := manager.FetchBills(owner, "bytedance", "2026-09")
	if err != nil || len(bills) != 0 || fingerprint == "" || billCalls == 0 {
		t.Fatalf("bill reconciliation: %v %q %v (%d calls)", bills, fingerprint, err, billCalls)
	}
}

func TestProviderUpdateClearsCredentials(t *testing.T) {
	for _, ownerID := range []string{identity.SystemOwnerID, "personal-owner"} {
		t.Run(ownerID, func(t *testing.T) {
			repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
			cipher, _ := providerconfig.NewCipher("provider-test-secret")
			manager := NewManager(repo, cipher, config.Config{ProviderHTTPTimeout: time.Second})
			ctx := identity.WithOwnerID(context.Background(), ownerID)
			manager.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"data":[]}`
				if r.Method == http.MethodPost {
					if r.Header.Get("Authorization") != "Bearer replacement-api" {
						t.Fatal("generation did not use replacement API key")
					}
					body = `{"id":"fixture-task"}`
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			enabled := true
			key, endpoint, access, secret := "old-api", "https://provider.example/api/v3", "billing-id", "billing-secret"
			if _, err := manager.Update(ctx, "bytedance", ProviderUpdate{Enabled: &enabled, APIKey: &key, BaseURL: &endpoint, BillingAccessKeyID: &access, BillingAccessKeySecret: &secret}); err != nil {
				t.Fatal(err)
			}
			key = "new-api"
			info, err := manager.Update(ctx, "bytedance", ProviderUpdate{APIKey: &key})
			if err != nil || !info.BillingConfigured {
				t.Fatalf("omitted billing keys must be preserved: %v", err)
			}
			var update ProviderUpdate
			if err := json.Unmarshal([]byte(`{"api_key":"replacement-api","billing_access_key_id":"","billing_access_key_secret":""}`), &update); err != nil {
				t.Fatal(err)
			}
			info, err = manager.Update(ctx, "bytedance", update)
			if err != nil || info.BillingConfigured || !info.Enabled || !info.Configured {
				t.Fatalf("clearing billing keys: %+v, %v", info, err)
			}
			adapter, err := manager.resolve(ctx, "bytedance", true)
			if err != nil {
				t.Fatal(err)
			}
			task, err := adapter.Generate(ctx, core.MediaRequest{Model: "seedance-model", Prompt: "test"})
			if err != nil || task.ID != "fixture-task" {
				t.Fatalf("generation without billing keys: %v", err)
			}
			_, _, err = manager.FetchBills(ctx, "bytedance", "2026-09")
			if !errors.Is(err, ErrBillingUnconfigured) {
				t.Fatalf("missing billing keys should skip reconciliation: %v", err)
			}
			record, _ := repo.Get(ctx, "bytedance")
			credentials, err := manager.decryptCredentials(record.CredentialsCiphertext)
			if err != nil || credentials.APIKey != "replacement-api" || credentials.BillingAccessKeyID != "" || credentials.BillingAccessKeySecret != "" {
				t.Fatal("credential changes were not persisted")
			}
			if _, err := manager.Update(ctx, "bytedance", ProviderUpdate{BillingAccessKeyID: &access, BillingAccessKeySecret: &secret}); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(`{"enabled":true,"api_key":"","billing_access_key_id":"","billing_access_key_secret":""}`), &update); err != nil {
				t.Fatal(err)
			}
			info, err = manager.Update(ctx, "bytedance", update)
			if err != nil || info.Enabled || info.Configured || info.BillingConfigured {
				t.Fatalf("clearing all keys must disconnect: %+v, %v", info, err)
			}
			record, _ = repo.Get(ctx, "bytedance")
			credentials, err = manager.decryptCredentials(record.CredentialsCiphertext)
			if err != nil || credentials.APIKey != "" || credentials.BillingAccessKeyID != "" || credentials.BillingAccessKeySecret != "" {
				t.Fatal("cleared keys remain stored")
			}
			_, err = manager.resolve(ctx, "bytedance", true)
			if !errors.Is(err, ErrProviderDisabled) {
				t.Fatalf("cleared provider is usable: %v", err)
			}
		})
	}
}

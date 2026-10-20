package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/config"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	registry "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
)

type extensionAdapter struct{}

func (extensionAdapter) Name() string { return "extension" }
func (extensionAdapter) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{{ID: "extension-image", MediaType: "image"}}, nil
}
func (extensionAdapter) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	return core.ProviderTask{ID: "extension-task"}, nil
}
func (extensionAdapter) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{Status: core.ProviderSucceeded, ResultURL: "https://example.test/image.png"}, nil
}

func TestModuleWithUnknownCredentialAndRemoval(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("registry-test-secret")
	manager := NewManager(repo, cipher, config.Config{})
	calls := 0
	definition := providerkit.Definition{ID: "extension", Name: "Extension", DefaultModel: "extension-image", Fields: []providerkit.Field{{Key: "tenant_token", Label: "Tenant token", Secret: true, Required: true}, {Key: "workspace", Label: "Workspace", Default: "default"}}, Factory: func(c Credentials, s providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
		calls++
		if c.Get("tenant_token") != "extension-secret" || c.Get("workspace") != "default" {
			t.Fatal("settings lost")
		}
		return extensionAdapter{}, nil
	}, Quote: func(_ string, _ core.MediaModel, _ core.MediaRequest, now time.Time) (core.TaskCost, error) {
		return core.TaskCost{ExpectedUSD: 123, Status: "estimated", QuotedAt: now}, nil
	}}
	manager.registry = registry.MustRegistry(definition)
	owner := identity.WithOwnerID(context.Background(), "extension-owner")
	if err := manager.Bootstrap(owner); err != nil {
		t.Fatal(err)
	}
	var update ProviderUpdate
	if err := json.Unmarshal([]byte(`{"enabled":true,"tenant_token":"extension-secret"}`), &update); err != nil {
		t.Fatal(err)
	}
	info, err := manager.Update(owner, "extension", update)
	if err != nil || !info.Configured || len(info.Fields) != 2 || info.Values["workspace"] != "default" || info.Hints["tenant_token"] == "" {
		t.Fatalf("descriptor %+v %v", info, err)
	}
	wire, _ := json.Marshal(info)
	if strings.Contains(string(wire), "extension-secret") {
		t.Fatal("secret exposed")
	}
	recordsBefore, _ := repo.List(owner)
	for _, record := range recordsBefore {
		if strings.Contains(string(record.CredentialsCiphertext), "extension-secret") {
			t.Fatal("unencrypted credential")
		}
	}
	p := manager.ManagedProviders()["extension"]
	_, fingerprint, err := p.(core.CostQuoter).Quote(owner, core.MediaModel{}, core.MediaRequest{})
	if err != nil || len(fingerprint) != 64 {
		t.Fatal(fingerprint, err)
	}
	if _, err = p.Generate(owner, core.MediaRequest{ProviderIdentity: fingerprint}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("adapter not cached", calls)
	}
	// Unknown fields fail before persistence or adapter execution.
	if _, err = manager.Update(owner, "extension", ProviderUpdate{Extra: map[string]string{"undeclared": "secret"}}); err == nil {
		t.Fatal("undeclared credential accepted")
	}
	// Removing the definition hides it and rejects new work while retaining data.
	manager.registry = registry.MustRegistry()
	if len(manager.ManagedProviders()) != 0 {
		t.Fatal("removed module still exposed")
	}
	if _, err = manager.Get(owner, "extension"); !errors.Is(err, ErrUnsupportedProvider) {
		t.Fatal(err)
	}
	recordsAfter, _ := repo.List(owner)
	if len(recordsAfter) != len(recordsBefore) {
		t.Fatal("removal deleted stored credentials")
	}
	manager.registry = registry.MustRegistry(definition)
	if _, err = manager.Get(owner, "extension"); err != nil {
		t.Fatal("reinstall failed", err)
	}
}

func TestLegacyEncryptedCredentialIdentity(t *testing.T) {
	repo := &memoryProviderRepository{records: map[string]providerconfig.Record{}}
	cipher, _ := providerconfig.NewCipher("registry-test-secret")
	manager := NewManager(repo, cipher, config.Config{})
	// This is the exact pre-registry plaintext structure inside encrypted records.
	raw := []byte(`{"api_key":"saved-key","base_url":"https://api.openai.com/v1"}`)
	encrypted, _ := cipher.Encrypt(raw)
	c, err := manager.decryptCredentials(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := registry.Find("openai")
	if d.Identity(c) != legacyFingerprint("openai", c.BaseURL, c.APIKey) {
		t.Fatal("accepted task identity changed")
	}
}
func legacyFingerprint(id, endpoint, key string) string {
	hash := sha256.Sum256([]byte(id + "\x00" + endpoint + "\x00" + key))
	return hex.EncodeToString(hash[:])
}

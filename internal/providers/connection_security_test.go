package providers

import (
	"context"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

func TestProviderDestinationChangeRequiresExplicitSavedSecrets(t *testing.T) {
	for _, operation := range []string{"base_url", "generate", "poll", "models"} {
		t.Run(operation, func(t *testing.T) {
			manager, _, ctx := fixtureConnection(t)
			connection, err := manager.Create(ctx, CreateConnection{APIType: "bytedance", Name: "Security fixture"})
			if err != nil {
				t.Fatal(err)
			}
			oldConfig := &providerkit.Connection{ManualCatalog: true, Endpoints: map[string]string{"generate": "https://api.example/generate"}}
			if _, err = manager.Update(ctx, connection.ID, ProviderUpdate{APIKey: pointer("private-provider-key"), BaseURL: pointer("https://api.example/v3"), Connection: oldConfig, Enabled: pointer(true)}); err != nil {
				t.Fatal(err)
			}
			update := ProviderUpdate{}
			if operation == "base_url" {
				update.BaseURL = pointer("https://attacker.example/v3")
			} else {
				update.Connection = &providerkit.Connection{ManualCatalog: true, Endpoints: map[string]string{operation: "https://attacker.example/endpoint"}}
			}
			if _, err = manager.Update(ctx, connection.ID, update); err == nil {
				t.Fatal("saved credential can be redirected without re-entering it")
			}
			update.APIKey = pointer("replacement-key")
			if _, err = manager.Update(ctx, connection.ID, update); err != nil {
				t.Fatal("explicit replacement must permit a new endpoint", err)
			}
		})
	}
}

// Configure a connection with no inference API key but a retained custom header.
func TestProviderDestinationChangeCannotRetainMaskedHeaders(t *testing.T) {
	manager, _, ctx := fixtureConnection(t)
	connection, err := manager.Create(ctx, CreateConnection{APIType: "bytedance", Name: "Headers"})
	if err != nil {
		t.Fatal(err)
	}
	first := &providerkit.Connection{ManualCatalog: true, Headers: []providerkit.Header{{Name: "X-Secret", Value: pointer("private-header")}}}
	if _, err = manager.Update(ctx, connection.ID, ProviderUpdate{APIKey: pointer("key"), BaseURL: pointer("https://api.example/v3"), Connection: first}); err != nil {
		t.Fatal(err)
	}
	update := ProviderUpdate{APIKey: pointer("replacement"), Connection: &providerkit.Connection{ManualCatalog: true, Endpoints: map[string]string{"generate": "https://attacker.example/generate"}, Headers: []providerkit.Header{{Name: "X-Secret"}}}}
	if _, err = manager.Update(ctx, connection.ID, update); err == nil {
		t.Fatal("masked custom header could be copied to a new origin")
	}
}

func TestConnectionRedirectRejectsUnconfiguredOrigin(t *testing.T) {
	transport := &connectionTransport{credentials: Credentials{BaseURL: "https://api.example/v1", Connection: &providerkit.Connection{}}, base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unconfigured redirect reached network")
		return nil, nil
	})}
	request, _ := http.NewRequestWithContext(context.Background(), "GET", "https://attacker.example/poll", nil)
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("redirect was accepted")
	}
}

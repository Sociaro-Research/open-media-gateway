package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func TestFailedPollingResultsRedactConfiguredAndBuiltinSecrets(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint("connection=", configured), func(t *testing.T) {
			manager, _, _ := fixtureConnection(t)
			credentials := Credentials{APIKey: "synthetic-api-secret", MusicAPIKey: "synthetic-music-secret", BaseURL: "https://provider.example.test/api/v3"}
			secrets := []string{credentials.APIKey, credentials.MusicAPIKey}
			if configured {
				credentials.Connection = &providerkit.Connection{Headers: []providerkit.Header{{Name: "X-Private", Value: pointer("synthetic-header-secret")}}}
				secrets = append(secrets, "synthetic-header-secret")
			}
			manager.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("unexpected paid request method: %s", r.Method)
				}
				data, _ := json.Marshal(map[string]any{"status": "failed", "error": map[string]string{"message": "upstream rejected " + strings.Join(secrets, " ")}})
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
			})
			adapter, err := manager.buildAdapter("bytedance", credentials)
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.GetTask(context.Background(), "accepted-task")
			if err != nil || result.Status != core.ProviderFailed {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			stored := core.Task{Status: core.TaskFailed, Error: &result.Error}
			public, err := json.Marshal(core.GenerationView(stored))
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range secrets {
				if strings.Contains(result.Error, secret) || strings.Contains(string(public), secret) {
					t.Fatal("synthetic saved secret escaped into persisted/public failure text")
				}
			}
			if !strings.Contains(result.Error, "upstream rejected") || !strings.Contains(result.Error, "[redacted]") {
				t.Fatal("safe failure context was lost")
			}
		})
	}
}

func TestConnectionRedactionPreservesValidationClassification(t *testing.T) {
	manager, _, _ := fixtureConnection(t)
	manager.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid options must be rejected before any provider request")
		return nil, errors.New("unexpected request")
	})
	adapter, err := manager.buildAdapter("openai", Credentials{APIKey: "synthetic-key", BaseURL: "https://api.openai.com/v1", Connection: &providerkit.Connection{}})
	if err != nil {
		t.Fatal(err)
	}
	err = adapter.(core.RequestValidator).ValidateRequest(context.Background(), core.MediaRequest{Model: "gpt-image-2.5-flare", Prompt: "fixture", Options: core.MediaOptions{Background: "transparent", Format: "jpeg"}})
	if !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatalf("validation identity lost: %v", err)
	}
	wrapper := &connectionAdapter{credentials: Credentials{APIKey: "synthetic-key", Connection: &providerkit.Connection{}}}
	sanitized := wrapper.redact(fmt.Errorf("%w: rejected synthetic-key", core.ErrOptionsInvalid))
	if !errors.Is(sanitized, core.ErrOptionsInvalid) || strings.Contains(sanitized.Error(), "synthetic-key") {
		t.Fatalf("classification/redaction contract lost: %v", sanitized)
	}
}

type warningCheckProvider struct {
	core.MediaProvider
	check providerkit.CredentialCheck
}

func (p warningCheckProvider) CredentialStatus() providerkit.CredentialCheck { return p.check }

func TestCredentialCheckWarningsRedactConfiguredSecrets(t *testing.T) {
	wrapper := &connectionAdapter{
		MediaProvider: warningCheckProvider{check: providerkit.CredentialCheck{Warning: "Optional details unavailable: synthetic-key synthetic-header-secret"}},
		credentials:   Credentials{APIKey: "synthetic-key", Connection: &providerkit.Connection{Headers: []providerkit.Header{{Name: "X-Private", Value: pointer("synthetic-header-secret")}}}},
	}
	check := wrapper.CredentialStatus()
	if check.Err != nil || strings.Contains(check.Warning, "synthetic-key") || strings.Contains(check.Warning, "synthetic-header-secret") || check.Warning != "Optional details unavailable: [redacted] [redacted]" {
		t.Fatal("warning redaction failed", check)
	}
}

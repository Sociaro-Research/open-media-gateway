package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/modelaccess"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providers"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

func TestAdministrativeModelPickersRetainReleaseMetadata(t *testing.T) {
	model := core.MediaModel{ID: "native-image", DisplayName: "Studio Image", MediaType: "image", Metadata: &core.ModelMetadata{ReleaseDate: "2026-01-02", VersionDate: "2026-09-01"}}
	svc := openAIFixture()
	svc.catalog = []core.ProviderDescriptor{{ID: "openai", Name: "OpenAI", Models: []core.MediaModel{model}}}
	policy := &modelAccessMemory{}
	if err := policy.Ensure(context.Background(), []modelaccess.Ref{{Provider: "openai", Model: model.ID, Name: model.DisplayName}}); err != nil {
		t.Fatal(err)
	}
	policy.models[0].PublicID = "studio-image"
	policy.models[0].EnabledByDefault = false
	iconURL := "/provider-assets/openai/icon.svg?v=fixture"
	s := &Server{service: svc, modelAccess: policy, system: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"admin": true}}, providerAdmin: &providerAdminStub{provider: providers.AdminProvider{ID: "openai", Name: "OpenAI", IconURL: iconURL, IconMonochrome: true, Models: []core.MediaModel{model}}}}
	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
	}{
		{"/v1/system/markups/catalog", s.markupCatalog},
		{"/v1/system/model-settings", s.modelSettings},
	} {
		for _, actor := range []string{"admin", "member"} {
			r := httptest.NewRequest("GET", endpoint.path, nil)
			r = r.WithContext(identity.WithOwnerID(r.Context(), actor))
			w := httptest.NewRecorder()
			endpoint.handler(w, r)
			if actor == "member" {
				if w.Code != 403 {
					t.Fatalf("%s exposed administrative models: %d", endpoint.path, w.Code)
				}
				continue
			}
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			var result struct {
				Data []struct {
					Provider               string            `json:"provider"`
					ProviderName           string            `json:"provider_name"`
					ProviderIconURL        string            `json:"provider_icon_url"`
					ProviderIconMonochrome bool              `json:"provider_icon_monochrome"`
					IconURL                string            `json:"icon_url"`
					IconMonochrome         bool              `json:"icon_monochrome"`
					Capabilities           *core.MediaModel  `json:"capabilities"`
					Models                 []core.MediaModel `json:"models"`
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			var found *core.MediaModel
			for _, item := range result.Data {
				if item.Capabilities != nil {
					found = item.Capabilities
					if item.ProviderName != "OpenAI" {
						t.Fatal("provider display name missing")
					}
					if item.ProviderIconURL != iconURL || !item.ProviderIconMonochrome {
						t.Fatal("organization picker lost provider artwork")
					}
				}
				for _, m := range item.Models {
					if m.ID == model.ID {
						found = &m
						if item.IconURL == "" || !item.IconMonochrome {
							t.Fatal("markup picker lost provider artwork")
						}
					}
				}
			}
			if found == nil || found.PublicID != "studio-image" || found.Metadata == nil || found.Metadata.ReleaseDate != "2026-01-02" || found.Metadata.VersionDate != "2026-09-01" {
				t.Fatalf("%s lost public ID or release metadata: %s", endpoint.path, w.Body)
			}
		}
	}
}

func TestMarkupCatalogIncludesConcreteSharedConnections(t *testing.T) {
	const connection = "openai--11111111-1111-4111-8111-111111111111"
	svc := openAIFixture()
	svc.catalog = []core.ProviderDescriptor{{ID: connection, Name: "Studio connection", IconURL: "/provider-assets/openai/icon.svg", IconMonochrome: true, Models: []core.MediaModel{{ID: "shared-model", DisplayName: "Studio model", MediaType: "image"}}}}
	s := &Server{service: svc, system: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"admin": true}}}
	r := httptest.NewRequest("GET", "/v1/system/markups/catalog", nil)
	r = r.WithContext(identity.WithOwnerID(r.Context(), "admin"))
	w := httptest.NewRecorder()
	s.markupCatalog(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	var result struct {
		Data []struct {
			ID             string            `json:"id"`
			Name           string            `json:"name"`
			IconURL        string            `json:"icon_url"`
			IconMonochrome bool              `json:"icon_monochrome"`
			Models         []core.MediaModel `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range result.Data {
		if seen[p.ID] {
			t.Fatalf("duplicate provider %q", p.ID)
		}
		seen[p.ID] = true
		if p.ID == connection {
			if p.Name != "Studio connection" || p.IconURL == "" || !p.IconMonochrome || len(p.Models) != 1 || p.Models[0].ID != "shared-model" {
				t.Fatalf("connection identity or descriptors lost: %+v", p)
			}
		}
	}
	if !seen[connection] || !seen["openai"] || seen["mock"] {
		t.Fatalf("concrete connection or legacy choices incorrect: %s", w.Body)
	}
}

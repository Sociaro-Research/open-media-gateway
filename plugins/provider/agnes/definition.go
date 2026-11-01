// Package agnes adapts Agnes AI image generation and asynchronous video jobs.
package agnes

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const DefaultBaseURL = "https://apihub.agnes-ai.com/v1"
const DefaultModel = "agnes-image-2.5-flash"

func Definition() providerkit.Definition {
	return providerkit.Definition{
		CatalogSources: catalogSources(),
		CatalogReviews: catalogDocumentReviews(),
		PublicCatalog:  true,
		ID:             "agnes", Name: "Agnes AI", DefaultModel: DefaultModel, ModelEnv: "AGNES_MODEL",
		Description: "Image generation, editing and composition; video generation with keyframes and media references.",
		Fields: []providerkit.Field{
			{Key: "api_key", Label: "API key", Secret: true, Required: true, Env: "AGNES_API_KEY"},
			{Key: "base_url", Label: "API endpoint", Default: DefaultBaseURL, Env: "AGNES_BASE_URL"},
		},
		Profiles:    catalog,
		BillingHelp: "Published prices are estimates, including temporary zero-price offers. Agnes account bills confirm charges; no public billing-detail API is documented. The model catalog is public and does not verify API keys.",
		Factory: func(c providerkit.Credentials, s providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
			return New(c.Get("api_key"), c.Get("base_url"), client, client.Timeout)
		},
	}
}

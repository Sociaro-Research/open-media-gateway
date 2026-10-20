package openai

import (
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
	"net/http"
)

func Definition() providerkit.Definition {
	return providerkit.Definition{
		CatalogSources: catalogSources(),
		CatalogReviews: catalogDocumentReviews(),
		CatalogAliases: tariffAliases(),
		LegacyIdentity: true,
		Profiles: func() []core.MediaModel {
			var result []core.MediaModel
			for _, id := range modelIDs {
				result = append(result, describeModel(id))
			}
			return result
		},
		ID: "openai", Name: "OpenAI", Description: "GPT Image generation and editing, text-to-speech, and spoken GPT Audio responses.", DefaultModel: "gpt-image-2.5-flare", ModelEnv: "OPENAI_MODEL",
		Fields: []providerkit.Field{{Key: "api_key", Label: "API key", Secret: true, Required: true, Env: "OPENAI_API_KEY"}, {Key: "base_url", Label: "API endpoint", Default: "https://api.openai.com/v1", Env: "OPENAI_BASE_URL"}},
		Icon:   Icon(), IconType: "image/svg+xml", IconMonochrome: true,
		Factory: func(c providerkit.Credentials, s providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
			return New(c.APIKey, c.BaseURL, client, client.Timeout)
		},
	}
}

func (p *Adapter) DisplayName() string { return Definition().Name }
func (p *Adapter) IsFree() bool        { return Definition().Free }
func (p *Adapter) FailureIsFree() bool { return Definition().FailureNoCharge }

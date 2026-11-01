// Package kling implements the direct Kling API's reviewed video routes.
package kling

import (
	"net/http"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const DefaultBaseURL = "https://api-singapore.klingai.com"

// Gateway operation IDs distinguish serializers and exact upstream versions.
const TextToVideo = "kling-3.0-text-to-video"
const ImageToVideo = "kling-3.0-image-to-video"
const TextToVideo4 = "kling-4.0-text-to-video"
const ImageToVideo4 = "kling-4.0-image-to-video"
const docsBase = "https://kling.ai/document-api/"
const priceSource = docsBase + "pricing/base/video"
const priceMarkdownSource = priceSource + ".md"

func Definition() providerkit.Definition {
	aliases := map[string]string{}
	for _, path := range []string{"api/get-started/authentication", "api/video/3-0-omni/text-to-video", "api/video/3-0-omni/image-to-video", "api/video/4-0/text-to-video", "api/video/4-0/image-to-video", "pricing/base/video"} {
		aliases[docsBase+path] = docsBase + path + ".md"
	}
	return providerkit.Definition{
		ID: "kling", Name: "Kling AI", DefaultModel: TextToVideo4, ModelEnv: "KLING_MODEL",
		Description:        "Direct Kling 4.0 and 3.0 text-to-video and image-to-video, with published price estimates and a free authentication check.",
		PublicCatalog:      true,
		AuthenticatedCheck: true,
		Fields: []providerkit.Field{
			{Key: "api_key", Label: "API key (current API)", Secret: true, Required: true, Env: "KLING_API_KEY"},
			{Key: "base_url", Label: "API endpoint", Default: DefaultBaseURL, Env: "KLING_BASE_URL"},
			{Key: "kling4_720p_price", Label: "4.0 720p override (USD/s, optional)", Env: "KLING4_720P_PRICE"},
			{Key: "kling4_1080p_price", Label: "4.0 1080p override (USD/s, optional)", Env: "KLING4_1080P_PRICE"},
		},
		CatalogSources: []catalogsync.Source{
			{ID: "authentication", URL: docsBase + "api/get-started/authentication.md", Parser: "document"},
			{ID: "account-check", URL: docsBase + "api/assets/account-usage.md", Parser: "document"},
			{ID: "error-codes", URL: docsBase + "api/get-started/error-codes.md", Parser: "document"},
			{ID: "text-to-video", URL: docsBase + "api/video/3-0-omni/text-to-video.md", Parser: "document"},
			{ID: "image-to-video", URL: docsBase + "api/video/3-0-omni/image-to-video.md", Parser: "document"},
			{ID: "kling4-text-to-video", URL: docsBase + "api/video/4-0/text-to-video.md", Parser: "document"},
			{ID: "kling4-image-to-video", URL: docsBase + "api/video/4-0/image-to-video.md", Parser: "document"},
			{ID: "pricing", URL: priceMarkdownSource, Parser: "document"},
		},
		CatalogInterpret:     interpretPrices,
		CatalogSourceAliases: aliases,
		// Model versions live in routes, not a JSON model field. The generic
		// manual-model override cannot safely substitute these upstream IDs.
		BillingHelp: "Published USD list prices supply estimates automatically; optional account overrides are preserved. Prepaid units, trials, discounts and CNY are not converted to USD or treated as confirmed bills. Check connection uses the free authenticated account query without retaining balances; success verifies authentication, not model entitlement or funded generation. Fresh queries are limited to one per second per gateway process; successful checks are cached for five minutes.",
		Factory: func(c providerkit.Credentials, _ providerkit.Settings, client *http.Client) (core.MediaProvider, error) {
			a, err := New(c.Get("api_key"), c.Get("base_url"), client)
			if err != nil {
				return nil, err
			}
			if err = a.setPrices(c); err != nil {
				return nil, err
			}
			return a, nil
		},
	}
}

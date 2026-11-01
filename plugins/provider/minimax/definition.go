// Package minimax implements MiniMax's direct image and video APIs.
package minimax

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
	"net/http"
)

const ContextIR = "MiniMax-H3-Context-IR"

const DefaultBaseURL = "https://api.minimax.io"
const docsBase = "https://platform.minimax.io/docs/"
const priceSource = docsBase + "pricing/overview"

func Definition() providerkit.Definition {
	sources := []catalogsync.Source{}
	for _, p := range []string{"faq/models", "guides/video-generation", "api-reference/api-overview", "api-reference/video-generation-v2-create", "api-reference/video-generation-v2-query", "api-reference/video-generation-v2-list", "api-reference/video-generation-v2-delete", "api-reference/video-generation-v2-h3-context-ir", "api-reference/video-generation-v2-regeneration", "api-reference/video-generation-t2v", "api-reference/video-generation-i2v", "api-reference/video-generation-fl2v", "api-reference/video-generation-s2v", "api-reference/image-generation-t2i", "api-reference/image-generation-i2i", "api-reference/video-agent-create", "faq/video-agent-templates", "api-reference/file-management-upload", "api-reference/file-management-list", "api-reference/file-management-retrieve", "api-reference/file-management-retrieve-content", "api-reference/file-management-delete", "api-reference/video-generation-query", "api-reference/video-generation-download", "api-reference/video-agent-query", "api-reference/models/openai/list-models", "pricing/overview"} {
		sources = append(sources, catalogsync.Source{ID: p, URL: docsBase + p + ".md", Parser: "document"})
	}
	return providerkit.Definition{
		ID: "minimax", Name: "MiniMax", DefaultModel: "MiniMax-H3", ModelEnv: "MINIMAX_MODEL",
		Description:   "Direct MiniMax H3 multimodal video, documented legacy video, image generation and Video Agent templates.",
		Fields:        []providerkit.Field{{Key: "api_key", Label: "Pay-as-you-go API key", Secret: true, Required: true, Env: "MINIMAX_API_KEY"}, {Key: "base_url", Label: "API endpoint", Default: DefaultBaseURL, Env: "MINIMAX_BASE_URL"}},
		PublicCatalog: true, AuthenticatedCheck: true, CatalogSources: sources, CatalogInterpret: interpretPrices,
		BillingHelp: "Connection checks authenticate through the free models-list endpoint; they do not verify image/video entitlement, whitelist access, balance or successful generation. Published USD prices remain estimates; H3 charges output seconds, input video seconds and images beyond the free allowance separately. Unknown tariffs remain unavailable; no public invoice-detail API is documented.",
		Factory: func(c providerkit.Credentials, _ providerkit.Settings, h *http.Client) (core.MediaProvider, error) {
			return New(c.Get("api_key"), c.Get("base_url"), h)
		},
	}
}

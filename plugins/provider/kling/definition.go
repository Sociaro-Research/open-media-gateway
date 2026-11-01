// Package kling implements the direct Kling API's reviewed video routes.
package kling

import (
	"net/http"
	"strings"

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
	for _, source := range reviewedSources() {
		aliases[strings.TrimSuffix(source.URL, ".md")] = source.URL
	}
	return providerkit.Definition{
		ID: "kling", Name: "Kling AI", DefaultModel: TextToVideo4, ModelEnv: "KLING_MODEL",
		Description:        "Direct Kling video, Omni references, images, audio, editing and managed resources, with typed capabilities, reviewed pricing and a free authentication check.",
		PublicCatalog:      true,
		AuthenticatedCheck: true,
		Fields: []providerkit.Field{
			{Key: "api_key", Label: "API key (current API)", Secret: true, Required: true, Env: "KLING_API_KEY"},
			{Key: "base_url", Label: "API endpoint", Default: DefaultBaseURL, Env: "KLING_BASE_URL"},
			{Key: "kling4_720p_price", Label: "4.0 720p override (USD/s, optional)", Env: "KLING4_720P_PRICE"},
			{Key: "kling4_1080p_price", Label: "4.0 1080p override (USD/s, optional)", Env: "KLING4_1080P_PRICE"},
			{Key: "selection_init_price", Label: "Confirmed selection initialization tariff (USD/request, optional)", Env: "KLING_SELECTION_INIT_PRICE"},
			{Key: "selection_add_price", Label: "Confirmed add-selection tariff (USD/request, optional)", Env: "KLING_SELECTION_ADD_PRICE"},
			{Key: "selection_delete_price", Label: "Confirmed delete-selection tariff (USD/request, optional)", Env: "KLING_SELECTION_DELETE_PRICE"},
			{Key: "selection_clear_price", Label: "Confirmed clear-selection tariff (USD/request, optional)", Env: "KLING_SELECTION_CLEAR_PRICE"},
			{Key: "selection_preview_price", Label: "Confirmed selection preview tariff (USD/request, optional)", Env: "KLING_SELECTION_PREVIEW_PRICE"},
			{Key: "element_multi_image_price", Label: "Confirmed image-element creation tariff (USD/request, optional)", Env: "KLING_ELEMENT_MULTI_IMAGE_PRICE"},
			{Key: "element_video_character_price", Label: "Confirmed video-element creation tariff (USD/request, optional)", Env: "KLING_ELEMENT_VIDEO_CHARACTER_PRICE"},
			{Key: "avatar_std_price", Label: "Confirmed Avatar std tariff (USD/request, optional)", Env: "KLING_AVATAR_STD_PRICE"},
			{Key: "avatar_pro_price", Label: "Confirmed Avatar pro tariff (USD/request, optional)", Env: "KLING_AVATAR_PRO_PRICE"},
			{Key: "lip_sync_price", Label: "Confirmed lip-sync tariff (USD/request, optional)", Env: "KLING_LIP_SYNC_PRICE"},
			{Key: "multi_edit_std_price", Label: "Confirmed multi-edit std tariff (USD/request, optional)", Env: "KLING_MULTI_EDIT_STD_PRICE"},
			{Key: "multi_edit_pro_price", Label: "Confirmed multi-edit pro tariff (USD/request, optional)", Env: "KLING_MULTI_EDIT_PRO_PRICE"},
			{Key: "effect_prices", Label: "Confirmed Effects tariffs (scene to USD/request JSON, optional)", Env: "KLING_EFFECT_PRICES"},
		},
		CatalogSources:       reviewedSources(),
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
			if err = a.setOperationPrices(c); err != nil {
				return nil, err
			}
			if err = a.setEffectPrices(c); err != nil {
				return nil, err
			}
			return a, nil
		},
	}
}

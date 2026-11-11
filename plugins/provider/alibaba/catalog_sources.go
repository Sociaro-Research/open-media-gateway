package alibaba

import "github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"

func lifecycleSources() []catalogsync.Source {
	return []catalogsync.Source{{ID: "retirements", URL: "https://www.alibabacloud.com/en/notice/model_studio_notice_of_retirement_for_selected_legacy_models_7d9", Parser: "alibaba-retirements"}}
}

func catalogSources() []catalogsync.Source {
	return append([]catalogsync.Source{
		{ID: "model-list", URL: "https://www.alibabacloud.com/help/en/model-studio/model-studio-model-list/", Parser: "document"},
		{ID: "image-model-list", URL: "https://www.alibabacloud.com/help/en/model-studio/model-list-image-generation", Parser: "document"},
		{ID: "video-model-list", URL: "https://www.alibabacloud.com/help/en/model-studio/model-list-video-generation", Parser: "document"},
		{ID: "speech-model-list", URL: "https://www.alibabacloud.com/help/en/model-studio/model-list-speech-synthesis", Parser: "document"},
		{ID: "music-models", URL: "https://www.alibabacloud.com/help/en/model-studio/fun-music", Parser: "document"},
		// The maintained policy distinguishes new-user restrictions, EoM and EoS.
		// The fixed-notice retirement parser must not collapse those checkpoints.
		{ID: "lifecycle-policy", URL: "https://docs.modelstudio.console.alibabacloud.com/en/model-studio/model-depreciation.md", Parser: "document"},
		{ID: "releases", URL: "https://www.alibabacloud.com/help/en/model-studio/newly-released-models", Parser: "alibaba-releases"},
		{ID: "pricing", URL: alibabaPriceSource, Parser: "alibaba-pricing"},
		{ID: "image-capabilities", URL: "https://www.alibabacloud.com/help/en/model-studio/image-model", Parser: "alibaba-image-models"},
		{ID: "retirements", URL: "https://www.alibabacloud.com/en/notice/model_studio_notice_of_retirement_for_selected_legacy_models_7d9", Parser: "alibaba-retirements"},
	}, videoAPISources...)
}

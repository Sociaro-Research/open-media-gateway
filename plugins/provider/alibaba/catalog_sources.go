package alibaba

import "github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"

func catalogSources() []catalogsync.Source {
	return append([]catalogsync.Source{
		{ID: "releases", URL: "https://www.alibabacloud.com/help/en/model-studio/newly-released-models", Parser: "alibaba-releases"},
		{ID: "pricing", URL: alibabaPriceSource, Parser: "alibaba-pricing"},
		{ID: "image-capabilities", URL: "https://www.alibabacloud.com/help/en/model-studio/image-model", Parser: "alibaba-image-models"},
		{ID: "retirements", URL: "https://www.alibabacloud.com/en/notice/model_studio_notice_of_retirement_for_selected_legacy_models_7d9", Parser: "alibaba-retirements"},
	}, videoAPISources...)
}

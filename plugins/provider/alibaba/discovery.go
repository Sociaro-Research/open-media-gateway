package alibaba

import (
	"context"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func (a *Adapter) DiscoverModelIDs(ctx context.Context) ([]string, error) {
	ids := []string{}
	for page := 1; page <= 100; page++ {
		var response struct {
			Output struct {
				Total  int `json:"total"`
				Models []struct {
					ID           string   `json:"model"`
					Capabilities []string `json:"capabilities"`
					Inference    struct {
						Response []string `json:"response_modality"`
					} `json:"inference_metadata"`
				} `json:"models"`
			} `json:"output"`
		}
		if err := a.do(ctx, a.baseURL, a.apiKey, "GET", fmt.Sprintf("/models?page_no=%d&page_size=200", page), nil, false, &response); err != nil {
			return nil, err
		}
		for _, m := range response.Output.Models {
			if containsFold(m.Inference.Response, "Image") || containsFold(m.Inference.Response, "Video") || containsFold(m.Inference.Response, "Audio") || containsFold(m.Capabilities, "IG") || containsFold(m.Capabilities, "VG") || containsFold(m.Capabilities, "TTS") {
				ids = append(ids, m.ID)
			}
		}
		if len(response.Output.Models) == 0 || page*200 >= response.Output.Total {
			return core.MediaDiscoveryIDs(ids, a.ReviewedCatalogModels()), nil
		}
	}
	return nil, fmt.Errorf("model discovery exceeded pagination limit")
}

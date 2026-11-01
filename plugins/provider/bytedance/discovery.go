package bytedance

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
	"net/http"
	"strings"
)

func (a *Adapter) DiscoverModelIDs(ctx context.Context) ([]string, error) {
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := a.do(ctx, http.MethodGet, "/models", nil, &response); err != nil {
		return nil, err
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, m := range response.Data {
		if strings.Contains(m.ID, "seedance") || strings.Contains(m.ID, "seedream") {
			id := modelcatalog.CanonicalID("bytedance", m.ID)
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
	}
	return core.MediaDiscoveryIDs(ids, Definition().Profiles()), nil
}

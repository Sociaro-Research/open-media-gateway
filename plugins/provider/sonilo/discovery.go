package sonilo

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
)

func (a *Adapter) DiscoverModelIDs(ctx context.Context) ([]string, error) {
	data, err := a.do(ctx, "GET", "/account/services", nil, "")
	if err != nil {
		return nil, err
	}
	var response struct {
		Services []string `json:"available_services"`
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, service := range response.Services {
		found := false
		for _, op := range operations {
			if op.service == strings.ReplaceAll(service, "-", "_") {
				ids = append(ids, op.id)
				found = true
			}
		}
		if !found {
			ids = append(ids, service)
		}
	}
	return core.MediaDiscoveryIDs(ids, catalog()), nil
}

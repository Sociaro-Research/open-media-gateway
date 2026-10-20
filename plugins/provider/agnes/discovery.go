package agnes

import (
	"context"
	"encoding/json"
	"net/http"
)

func (a *Adapter) DiscoverModelIDs(ctx context.Context) ([]string, error) {
	data, _, err := a.do(ctx, http.MethodGet, "/models", nil, false)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err = json.Unmarshal(data, &response); err != nil {
		return nil, err
	}
	ids := []string{}
	for _, m := range response.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

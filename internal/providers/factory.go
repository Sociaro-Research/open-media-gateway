package providers

import (
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/httpclient"
	registry "github.com/Sociaro-Research/media-gateway-dev/plugins/provider"

	"github.com/Sociaro-Research/media-gateway-dev/internal/config"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func New(cfg config.Config) (core.MediaProvider, error) {
	available, err := NewAll(cfg)
	if err != nil {
		return nil, err
	}
	provider, ok := available[cfg.MediaProvider]
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", cfg.MediaProvider)
	}
	return provider, nil
}

func NewAll(cfg config.Config) (map[string]core.MediaProvider, error) {
	result := map[string]core.MediaProvider{}
	m := &Manager{cfg: cfg, client: httpclient.NewPublic(cfg.ProviderHTTPTimeout, cfg.OutboundPrivateHosts...)}
	for _, d := range registry.All() {
		c := m.environmentCredentials(d.ID)
		if !d.Configured(c) {
			continue
		}
		p, e := m.buildAdapter(d.ID, c)
		if e != nil {
			return nil, e
		}
		result[d.ID] = p
	}
	return result, nil
}

package providers

import (
	"context"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerassets"
)

func (m *Manager) AssetLibrary(ctx context.Context, id string) (providerassets.Client, error) {
	spec, ok := m.findSpec(id)
	if !ok || spec.AssetLibrary == nil {
		return nil, providerassets.ErrUnavailable
	}
	record, err := m.loadRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	c, err := m.decryptCredentials(record.CredentialsCiphertext)
	if err != nil {
		return nil, err
	}
	// Asset management deliberately uses its own fixed origin and credentials,
	// never inference connection headers, endpoint overrides or billing keys.
	return spec.AssetLibrary(m.withDefaults(id, c), m.client)
}

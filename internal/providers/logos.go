package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

func (m *Manager) Logo(ctx context.Context, id string) ([]byte, error) {
	repo, ok := m.repo.(providerconfig.LogoRepository)
	if !ok {
		return nil, providerconfig.ErrNotFound
	}
	if _, err := m.repo.Get(ctx, id); err != nil {
		return nil, err
	}
	return repo.Logo(ctx, id)
}

func (m *Manager) SaveLogo(ctx context.Context, id string, data []byte) (string, error) {
	if err := providerconfig.ValidateLogo(data); err != nil {
		return "", err
	}
	if _, err := m.loadRecord(ctx, id); err != nil {
		return "", err
	}
	repo, ok := m.repo.(providerconfig.LogoRepository)
	if !ok {
		return "", errors.New("provider logo storage is unavailable")
	}
	if err := repo.SaveLogo(ctx, id, data); err != nil {
		return "", err
	}
	return logoURL(ctx, id, data), nil
}

func logoURL(ctx context.Context, id string, data []byte) string {
	digest := sha256.Sum256(data)
	result := "/provider-assets/" + url.PathEscape(identity.ProviderOwnerID(ctx)) + "/" + url.PathEscape(id) + "/icon.png?v=" + hex.EncodeToString(digest[:])
	// Catalog images must retain the key's resource and organization grants,
	// even when the same browser also has a personal management session.
	if identity.APIKeyID(ctx) != nil {
		result += "&generation=1"
	}
	return result
}

func (m *Manager) logoURL(ctx context.Context, id string) string {
	data, err := m.Logo(ctx, id)
	if err != nil || len(data) == 0 {
		return ""
	}
	return logoURL(ctx, id, data)
}

package auth

import (
	"context"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"time"
)

type googleRuntime struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	profile  *oidc.Provider
	version  int64
}

func (m *Manager) googleRuntime(ctx context.Context) (googleRuntime, error) {
	if m.providers == nil {
		return googleRuntime{}, ErrLoginProviderUnavailable
	}
	r, err := m.providers.record(ctx, "google")
	if err != nil {
		return googleRuntime{}, err
	}
	if !r.Enabled || !m.providers.view(r).Configured {
		return googleRuntime{}, ErrLoginProviderUnavailable
	}
	secret, err := m.providers.cipher.Decrypt(r.SecretCiphertext)
	if err != nil {
		return googleRuntime{}, err
	}
	// Only public OIDC metadata is cached; credentials are read afresh on each request.
	m.googleMu.Lock()
	defer m.googleMu.Unlock()
	if m.profile == nil {
		discoveryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		m.profile, err = m.discoverGoogle(discoveryCtx)
		if err != nil {
			return googleRuntime{}, err
		}
	}
	return googleRuntime{oauth: oauth2.Config{ClientID: r.ClientID, ClientSecret: string(secret), RedirectURL: m.providers.redirectURL, Endpoint: m.profile.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"}}, verifier: m.profile.Verifier(&oidc.Config{ClientID: r.ClientID}), profile: m.profile, version: r.Version}, nil
}

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

var ErrLoginProviderSettings = errors.New("invalid login provider settings")
var ErrLoginProviderUnavailable = errors.New("login provider is unavailable")

// LoginProviderRecord is storage-only; credentials never cross the HTTP boundary.
type LoginProviderRecord struct {
	Provider         string
	Enabled          bool
	Connected        bool
	ClientID         string
	SecretCiphertext []byte
	Version          int64
}
type LoginProviderRepository interface {
	LoginProviders(context.Context) ([]LoginProviderRecord, error)
	SaveLoginProvider(context.Context, string, LoginProviderRecord) (LoginProviderRecord, error)
	BootstrapGoogleLogin(context.Context, LoginProviderRecord) error
}
type LoginProviderSettings struct {
	Provider         string `json:"provider"`
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	Connected        bool   `json:"connected"`
	Configured       bool   `json:"configured"`
	ClientID         string `json:"client_id,omitempty"`
	SecretConfigured bool   `json:"secret_configured"`
	RedirectURL      string `json:"redirect_url,omitempty"`
	Version          int64  `json:"version"`
}
type LoginProviderUpdate struct {
	Enabled      *bool   `json:"enabled"`
	ClientID     *string `json:"client_id,omitempty"`
	ClientSecret string  `json:"client_secret,omitempty"`
	Version      int64   `json:"version"`
}
type LoginProviderManager struct {
	repo        LoginProviderRepository
	cipher      *providerconfig.Cipher
	redirectURL string
}

func NewLoginProviderManager(repo LoginProviderRepository, cipher *providerconfig.Cipher, publicURL string) *LoginProviderManager {
	return &LoginProviderManager{repo: repo, cipher: cipher, redirectURL: strings.TrimRight(publicURL, "/") + "/auth/google/callback"}
}
func (m *Manager) LoginProviders() *LoginProviderManager { return m.providers }
func loginProviderName(kind string) string {
	switch kind {
	case "google":
		return "Google"
	case "email":
		return "Email and password"
	}
	return ""
}
func (m *LoginProviderManager) view(r LoginProviderRecord) LoginProviderSettings {
	v := LoginProviderSettings{Provider: r.Provider, Name: loginProviderName(r.Provider), Enabled: r.Enabled, Connected: r.Connected, ClientID: r.ClientID, SecretConfigured: len(r.SecretCiphertext) > 0, Version: r.Version}
	v.Configured = r.Connected
	if r.Provider == "google" {
		v.RedirectURL = m.redirectURL
		v.Configured = r.Connected && r.ClientID != "" && v.SecretConfigured
	}
	return v
}
func (m *LoginProviderManager) Settings(ctx context.Context) ([]LoginProviderSettings, error) {
	records, err := m.repo.LoginProviders(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]LoginProviderSettings, 0, len(records))
	for _, r := range records {
		result = append(result, m.view(r))
	}
	return result, nil
}
func (m *LoginProviderManager) record(ctx context.Context, kind string) (LoginProviderRecord, error) {
	records, err := m.repo.LoginProviders(ctx)
	if err != nil {
		return LoginProviderRecord{}, err
	}
	for _, r := range records {
		if r.Provider == kind {
			return r, nil
		}
	}
	return LoginProviderRecord{}, ErrLoginProviderSettings
}
func validGoogleCredentials(id, secret string) bool {
	return len(id) > 0 && len(id) <= 512 && strings.HasSuffix(id, ".apps.googleusercontent.com") && len(secret) > 0 && len(secret) <= 512 && !strings.ContainsAny(id+secret, " \r\n\t*")
}
func (m *LoginProviderManager) Update(ctx context.Context, actor, kind string, in LoginProviderUpdate) (LoginProviderSettings, error) {
	if loginProviderName(kind) == "" || in.Version < 1 || in.Enabled == nil {
		return LoginProviderSettings{}, ErrLoginProviderSettings
	}
	r, err := m.record(ctx, kind)
	if err != nil {
		return LoginProviderSettings{}, err
	}
	if kind == "email" {
		if in.ClientID != nil || in.ClientSecret != "" {
			return LoginProviderSettings{}, ErrLoginProviderSettings
		}
	} else {
		if in.ClientID != nil {
			r.ClientID = strings.TrimSpace(*in.ClientID)
		}
		plain, err := m.cipher.Decrypt(r.SecretCiphertext)
		if err != nil {
			return LoginProviderSettings{}, err
		}
		secret := string(plain)
		if in.ClientSecret != "" {
			secret = strings.TrimSpace(in.ClientSecret)
		}
		if !validGoogleCredentials(r.ClientID, secret) {
			return LoginProviderSettings{}, ErrLoginProviderSettings
		}
		if in.ClientSecret != "" {
			r.SecretCiphertext, err = m.cipher.Encrypt([]byte(secret))
			if err != nil {
				return LoginProviderSettings{}, err
			}
		}
	}
	r.Connected = true
	r.Enabled = *in.Enabled
	r.Version = in.Version
	r, err = m.repo.SaveLoginProvider(ctx, actor, r)
	return m.view(r), err
}
func (m *LoginProviderManager) Bootstrap(ctx context.Context, id, secret, redirect string) error {
	if id == "" && secret == "" {
		return nil
	}
	r, err := m.record(ctx, "google")
	if err != nil {
		return err
	}
	if r.Connected || len(r.SecretCiphertext) > 0 || r.Version != 1 {
		return nil
	}
	if !validGoogleCredentials(id, secret) {
		return ErrLoginProviderSettings
	}
	// Do not silently move an existing OAuth callback to another origin.
	if redirect != "" {
		a, err := url.Parse(redirect)
		if err != nil || a.String() != m.redirectURL {
			return errors.New("existing Google callback must match AUTH_PUBLIC_URL/auth/google/callback")
		}
	}
	r.ClientID = id
	r.Connected = true
	r.SecretCiphertext, err = m.cipher.Encrypt([]byte(secret))
	if err != nil {
		return err
	}
	return m.repo.BootstrapGoogleLogin(ctx, r)
}
func (m *LoginProviderManager) LoginMethods(ctx context.Context) (LoginMethods, error) {
	rows, err := m.repo.LoginProviders(ctx)
	v := LoginMethods{}
	for _, r := range rows {
		if r.Provider == "google" {
			v.Google = r.Enabled && m.view(r).Configured
		}
		if r.Provider == "email" {
			v.Email = r.Enabled && r.Connected
		}
	}
	return v, err
}

// JSON audit payloads must use the public view, never LoginProviderRecord.
func (r LoginProviderRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{"provider": r.Provider, "enabled": r.Enabled, "connected": r.Connected, "client_id": r.ClientID, "secret_configured": len(r.SecretCiphertext) > 0, "version": r.Version})
}

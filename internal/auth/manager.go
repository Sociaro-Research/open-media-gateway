package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
)

const (
	sessionCookieName = "media_gateway_session"
	stateCookieName   = "media_gateway_oauth_state"
)

var ErrUnauthenticated = errors.New("user is not authenticated")

type Repository interface {
	UpsertGoogleUser(context.Context, string, string, string, string) (identity.User, error)
	CreateSession(context.Context, []byte, string, time.Time) error
	UserBySession(context.Context, []byte, time.Time) (identity.User, error)
	DeleteSession(context.Context, []byte) error
}

type Config struct {
	LoginMethods   LoginMethodsReader
	LoginProviders *LoginProviderManager
	SessionSecret  string
	SessionTTL     time.Duration
	PublicURL      string
	UIURL          string
	EmailSender    EmailSender
	SetupCipher    *providerconfig.Cipher
}

type Manager struct {
	methods        LoginMethodsReader
	email          *emailAuth
	setup          SetupRepository
	repo           Repository
	providers      *LoginProviderManager
	googleMu       sync.Mutex
	profile        *oidc.Provider
	discoverGoogle func(context.Context) (*oidc.Provider, error)
	signingKey     []byte
	sessionTTL     time.Duration
	cookieSecure   bool
	uiURL          string
}

type statePayload struct {
	State         string `json:"state"`
	Nonce         string `json:"nonce"`
	ExpiresAt     int64  `json:"expires_at"`
	ConfigVersion int64  `json:"config_version"`
}

func New(ctx context.Context, repo Repository, cfg Config) (*Manager, error) {
	if len(cfg.SessionSecret) < 32 {
		return nil, errors.New("session signing secret must contain at least 32 characters")
	}
	ttl := cfg.SessionTTL
	if ttl <= 0 {
		ttl = 30 * 24 * time.Hour
	}
	origin := cfg.PublicURL
	if origin == "" {
		return nil, errors.New("AUTH_PUBLIC_URL is required")
	}
	m := &Manager{methods: cfg.LoginMethods, providers: cfg.LoginProviders, repo: repo, signingKey: []byte(cfg.SessionSecret), sessionTTL: ttl, cookieSecure: strings.HasPrefix(origin, "https://"), discoverGoogle: func(ctx context.Context) (*oidc.Provider, error) {
		return oidc.NewProvider(ctx, "https://accounts.google.com")
	}}
	if cfg.LoginProviders != nil {
		m.methods = cfg.LoginProviders
	}
	m.uiURL = strings.TrimRight(cfg.UIURL, "/")
	if m.uiURL == "" {
		m.uiURL = strings.TrimRight(origin, "/")
	}
	if err := m.configureEmail(cfg.EmailSender, m.uiURL); err != nil {
		return nil, err
	}
	if cfg.SetupCipher != nil {
		if repo, ok := repo.(SetupRepository); ok {
			m.setup = repo
			if _, err := InstallationSetupCode(ctx, repo, cfg.SetupCipher); err != nil && !errors.Is(err, ErrSetupClosed) {
				return nil, err
			}
		}
	}
	return m, nil
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	if !m.allowLogin(w, r, "google") {
		return
	}
	google, err := m.googleRuntime(r.Context())
	if err != nil {
		emailError(w, 503, "auth_unavailable", "Google sign-in is temporarily unavailable.")
		return
	}
	state, err := randomToken(32)
	if err != nil {
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	nonce, err := randomToken(32)
	if err != nil {
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	payload := statePayload{State: state, Nonce: nonce, ExpiresAt: time.Now().Add(10 * time.Minute).Unix(), ConfigVersion: google.version}
	sealed, err := m.sealState(payload)
	if err != nil {
		http.Error(w, "authentication unavailable", http.StatusServiceUnavailable)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName, Value: sealed, Path: "/auth/google/callback", MaxAge: 600,
		HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, google.oauth.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.SetAuthURLParam("prompt", "select_account")), http.StatusFound)
}

func (m *Manager) Callback(w http.ResponseWriter, r *http.Request) {
	if !m.allowLogin(w, r, "google") {
		return
	}
	google, err := m.googleRuntime(r.Context())
	if err != nil {
		emailError(w, 503, "auth_unavailable", "Google sign-in is temporarily unavailable.")
		return
	}
	cookie, err := r.Cookie(stateCookieName)
	if err != nil {
		m.redirectFailure(w, r)
		return
	}
	payload, err := m.openState(cookie.Value)
	if err != nil || payload.ExpiresAt < time.Now().Unix() || payload.ConfigVersion != google.version || !constantEqual(payload.State, r.URL.Query().Get("state")) {
		m.redirectFailure(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, Value: "", Path: "/auth/google/callback", MaxAge: -1, HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteLaxMode})
	if r.URL.Query().Get("error") != "" {
		m.redirectFailure(w, r)
		return
	}
	token, err := google.oauth.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		m.redirectFailure(w, r)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		m.redirectFailure(w, r)
		return
	}
	idToken, err := google.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		m.redirectFailure(w, r)
		return
	}
	var claims struct {
		Subject       string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		Nonce         string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" || claims.Email == "" || !claims.EmailVerified || !constantEqual(claims.Nonce, payload.Nonce) {
		m.redirectFailure(w, r)
		return
	}
	if claims.Picture == "" && google.profile != nil {
		// Some ID tokens omit optional profile claims; UserInfo supplies them.
		profileCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		info, profileErr := google.profile.UserInfo(profileCtx, oauth2.StaticTokenSource(token))
		cancel()
		if profileErr == nil && info.Subject == claims.Subject {
			var profile struct {
				Picture string `json:"picture"`
			}
			if info.Claims(&profile) == nil {
				claims.Picture = profile.Picture
			}
		}
	}
	user, err := m.repo.UpsertGoogleUser(r.Context(), claims.Subject, strings.ToLower(claims.Email), claims.Name, claims.Picture)
	if err != nil {
		m.redirectFailure(w, r)
		return
	}
	sessionToken, err := randomToken(32)
	if err != nil {
		m.redirectFailure(w, r)
		return
	}
	expiresAt := time.Now().UTC().Add(m.sessionTTL)
	if err := m.repo.CreateSession(r.Context(), tokenHash(sessionToken), user.ID, expiresAt); err != nil {
		m.redirectFailure(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: sessionToken, Path: "/", Expires: expiresAt, MaxAge: int(m.sessionTTL.Seconds()),
		HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, m.uiURL+"/", http.StatusFound)
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		if err := m.repo.DeleteSession(r.Context(), tokenHash(cookie.Value)); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":"logout_unavailable","message":"Sign out could not be completed. Please try again."}}`))
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.cookieSecure, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func (m *Manager) Authenticate(r *http.Request) (identity.User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return identity.User{}, ErrUnauthenticated
	}
	user, err := m.repo.UserBySession(r.Context(), tokenHash(cookie.Value), time.Now().UTC())
	if err != nil {
		return identity.User{}, ErrUnauthenticated
	}
	// An opaque, non-credential identifier binds temporary browser keys to this login.
	binding := sha256.Sum256([]byte("console-key-storage:" + cookie.Value))
	user.SessionID = base64.RawURLEncoding.EncodeToString(binding[:])
	return user, nil
}

func (m *Manager) redirectFailure(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, m.uiURL+"/?auth_error=1", http.StatusFound)
}

func (m *Manager) sealState(payload statePayload) (string, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	value := base64.RawURLEncoding.EncodeToString(encoded)
	mac := hmac.New(sha256.New, m.signingKey)
	_, _ = mac.Write([]byte(value))
	return value + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (m *Manager) openState(value string) (statePayload, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 {
		return statePayload{}, errors.New("invalid OAuth state")
	}
	mac := hmac.New(sha256.New, m.signingKey)
	_, _ = mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !constantEqual(expected, parts[1]) {
		return statePayload{}, errors.New("invalid OAuth state signature")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return statePayload{}, err
	}
	var payload statePayload
	if err := json.Unmarshal(decoded, &payload); err != nil {
		return statePayload{}, err
	}
	return payload, nil
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenHash(token string) []byte {
	hash := sha256.Sum256([]byte(token))
	return hash[:]
}

func constantEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

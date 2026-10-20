package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/go-jose/go-jose/v4"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/coreos/go-oidc/v3/oidc"
)

type loginProviderFixture struct {
	rows []LoginProviderRecord
	err  error
}

func (f *loginProviderFixture) LoginProviders(context.Context) ([]LoginProviderRecord, error) {
	return append([]LoginProviderRecord(nil), f.rows...), f.err
}
func (f *loginProviderFixture) SaveLoginProvider(_ context.Context, _ string, next LoginProviderRecord) (LoginProviderRecord, error) {
	for i, r := range f.rows {
		if r.Provider == next.Provider {
			if next.Version != r.Version {
				return next, system.ErrConflict
			}
			next.Version++
			f.rows[i] = next
			return next, nil
		}
	}
	return next, ErrLoginProviderSettings
}
func (f *loginProviderFixture) BootstrapGoogleLogin(_ context.Context, next LoginProviderRecord) error {
	for i, r := range f.rows {
		if r.Provider == "google" && !r.Connected && r.Version == 1 {
			next.Version++
			f.rows[i] = next
		}
	}
	return nil
}
func loginFixture(t *testing.T) (*LoginProviderManager, *loginProviderFixture) {
	t.Helper()
	c, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
	repo := &loginProviderFixture{rows: []LoginProviderRecord{{Provider: "google", Enabled: true, Version: 1}, {Provider: "email", Connected: true, Enabled: true, Version: 1}}}
	return NewLoginProviderManager(repo, c, "https://gateway.test"), repo
}
func TestLoginProviderSecretsBootstrapAndRotation(t *testing.T) {
	ctx := context.Background()
	m, repo := loginFixture(t)
	if err := m.Bootstrap(ctx, "old.apps.googleusercontent.com", "secret-one", "https://gateway.test/auth/google/callback"); err != nil {
		t.Fatal(err)
	}
	enabled := false
	v, err := m.Update(ctx, "admin", "google", LoginProviderUpdate{Enabled: &enabled, Version: 2})
	if err != nil || v.Enabled || !v.SecretConfigured {
		t.Fatal(v, err)
	}
	if err = m.Bootstrap(ctx, "obsolete.apps.googleusercontent.com", "obsolete", ""); err != nil {
		t.Fatal(err)
	}
	r, _ := m.record(ctx, "google")
	if r.ClientID != "old.apps.googleusercontent.com" || r.Enabled {
		t.Fatal("bootstrap overwrote edited settings", r)
	}
	if strings.Contains(string(r.SecretCiphertext), "secret-one") {
		t.Fatal("plaintext persisted")
	}
	for _, object := range []any{r, v} {
		body, _ := json.Marshal(object)
		if strings.Contains(string(body), "secret-one") || strings.Contains(string(body), "SecretCiphertext") {
			t.Fatal("secret leaked", string(body))
		}
	}
	enabled = true
	id := "new.apps.googleusercontent.com"
	v, err = m.Update(ctx, "admin", "google", LoginProviderUpdate{Enabled: &enabled, ClientID: &id, ClientSecret: "secret-two", Version: v.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Update(ctx, "admin", "google", LoginProviderUpdate{Enabled: &enabled, Version: v.Version - 1}); !errors.Is(err, system.ErrConflict) {
		t.Fatal("stale save", err)
	}
	if _, err = m.Update(ctx, "admin", "email", LoginProviderUpdate{Enabled: &enabled, ClientSecret: "not-allowed", Version: 1}); !errors.Is(err, ErrLoginProviderSettings) {
		t.Fatal(err)
	}
	if _, err = m.Update(ctx, "admin", "github", LoginProviderUpdate{Enabled: &enabled, Version: 1}); !errors.Is(err, ErrLoginProviderSettings) {
		t.Fatal(err)
	}
	repo.err = errors.New("database unavailable")
	if _, err = m.LoginMethods(ctx); err == nil {
		t.Fatal("must fail closed")
	}
}
func TestGoogleRuntimeUsesFreshCredentialsAndRejectsChangedFlow(t *testing.T) {
	providers, _ := loginFixture(t)
	ctx := context.Background()
	if err := providers.Bootstrap(ctx, "first.apps.googleusercontent.com", "secret-one", ""); err != nil {
		t.Fatal(err)
	}
	discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"issuer": "https://accounts.google.com", "authorization_endpoint": "https://accounts.google.com/o/oauth2/auth", "token_endpoint": "https://oauth2.googleapis.com/token", "jwks_uri": "https://www.googleapis.com/oauth2/v3/certs"})
	}))
	defer discovery.Close()
	m := &Manager{methods: providers, providers: providers, signingKey: []byte(strings.Repeat("s", 32)), discoverGoogle: func(ctx context.Context) (*oidc.Provider, error) {
		return oidc.NewProvider(oidc.InsecureIssuerURLContext(ctx, "https://accounts.google.com"), discovery.URL)
	}}
	w := httptest.NewRecorder()
	m.Login(w, httptest.NewRequest("GET", "/auth/google", nil))
	if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "first.apps.googleusercontent.com") {
		t.Fatal(w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	payload, err := m.openState(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	id := "second.apps.googleusercontent.com"
	enabled := true
	if _, err = providers.Update(ctx, "admin", "google", LoginProviderUpdate{ClientID: &id, ClientSecret: "secret-two", Enabled: &enabled, Version: 2}); err != nil {
		t.Fatal(err)
	}
	runtime, err := m.googleRuntime(ctx)
	if err != nil || runtime.oauth.ClientID != id || runtime.oauth.ClientSecret != "secret-two" {
		t.Fatal("stale credentials", err)
	}
	req := httptest.NewRequest("GET", "/auth/google/callback?state="+payload.State+"&code=unspent-code", nil)
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	m.Callback(w, req)
	if w.Header().Get("Location") != "/?auth_error=1" {
		t.Fatal("changed flow accepted", w.Code, w.Body)
	}
}

type oauthSessionFixture struct {
	Repository
	users, sessions int
}

func (f *oauthSessionFixture) UpsertGoogleUser(_ context.Context, subject, email, name, picture string) (identity.User, error) {
	f.users++
	return identity.User{ID: "signed-in-user"}, nil
}
func (f *oauthSessionFixture) CreateSession(context.Context, []byte, string, time.Time) error {
	f.sessions++
	return nil
}
func TestGoogleCallbackWithDynamicCredentials(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	var nonce string
	verified := true
	audience := "client.apps.googleusercontent.com"
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"issuer": "https://accounts.google.com", "authorization_endpoint": server.URL + "/authorize", "token_endpoint": server.URL + "/token", "jwks_uri": server.URL + "/keys"})
		case "/keys":
			json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "fixture", Algorithm: "RS256", Use: "sig"}}})
		case "/token":
			id, secret, ok := r.BasicAuth()
			if !ok || id != "client.apps.googleusercontent.com" || secret != "client-secret" {
				t.Error("wrong credentials used for exchange")
				w.WriteHeader(401)
				return
			}
			claims, _ := json.Marshal(map[string]any{"iss": "https://accounts.google.com", "sub": "google-subject", "aud": audience, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "user@example.test", "email_verified": verified, "name": "Test User", "picture": "https://example.test/avatar", "nonce": nonce})
			signed, e := signer.Sign(claims)
			if e != nil {
				t.Error(e)
				return
			}
			token, e := signed.CompactSerialize()
			if e != nil {
				t.Error(e)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "access", "token_type": "Bearer", "id_token": token})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	providers, _ := loginFixture(t)
	if err = providers.Bootstrap(context.Background(), "client.apps.googleusercontent.com", "client-secret", ""); err != nil {
		t.Fatal(err)
	}
	repo := &oauthSessionFixture{}
	m := &Manager{uiURL: "https://ui.example.test", methods: providers, providers: providers, repo: repo, sessionTTL: time.Hour, signingKey: []byte(strings.Repeat("s", 32)), discoverGoogle: func(ctx context.Context) (*oidc.Provider, error) {
		return oidc.NewProvider(oidc.InsecureIssuerURLContext(ctx, "https://accounts.google.com"), server.URL)
	}}
	for _, tc := range []struct {
		name          string
		emailVerified bool
		aud           string
		badNonce      bool
		success       bool
	}{{"valid", true, "client.apps.googleusercontent.com", false, true}, {"unverified email", false, "client.apps.googleusercontent.com", false, false}, {"wrong audience", true, "other.apps.googleusercontent.com", false, false}, {"wrong nonce", true, "client.apps.googleusercontent.com", true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			verified = tc.emailVerified
			audience = tc.aud
			w := httptest.NewRecorder()
			m.Login(w, httptest.NewRequest("GET", "/auth/google", nil))
			if w.Code != 302 {
				t.Fatal(w.Code, w.Body)
			}
			cookie := w.Result().Cookies()[0]
			payload, e := m.openState(cookie.Value)
			if e != nil {
				t.Fatal(e)
			}
			nonce = payload.Nonce
			if tc.badNonce {
				nonce = "wrong"
			}
			before := repo.sessions
			req := httptest.NewRequest("GET", "/auth/google/callback?state="+payload.State+"&code=fixture", nil)
			req.AddCookie(cookie)
			w = httptest.NewRecorder()
			m.Callback(w, req)
			if tc.success {
				if w.Header().Get("Location") != "https://ui.example.test/" || repo.sessions != before+1 {
					t.Fatal("valid callback failed", w.Code, w.Body)
				}
			} else if w.Header().Get("Location") != "https://ui.example.test/?auth_error=1" || repo.sessions != before {
				t.Fatal("invalid identity accepted", w.Code, w.Body)
			}
		})
	}
	if repo.users != 1 || repo.sessions != 1 {
		t.Fatal("unexpected account/session writes", repo.users, repo.sessions)
	}
}

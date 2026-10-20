package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"net/http/httptest"
	"strings"
	"testing"
)

type loginSettingsFixture struct {
	systemFixture
	row    auth.LoginProviderRecord
	writes int
}

func (f *loginSettingsFixture) LoginProviders(context.Context) ([]auth.LoginProviderRecord, error) {
	return []auth.LoginProviderRecord{f.row}, nil
}
func (f *loginSettingsFixture) SaveLoginProvider(_ context.Context, _ string, in auth.LoginProviderRecord) (auth.LoginProviderRecord, error) {
	if in.Version != f.row.Version {
		return in, system.ErrConflict
	}
	in.Version++
	f.row = in
	f.writes++
	return in, nil
}
func (f *loginSettingsFixture) BootstrapGoogleLogin(context.Context, auth.LoginProviderRecord) error {
	return nil
}
func TestLoginSettingsRequireAdministratorAndVersionedInput(t *testing.T) {
	repo := &loginSettingsFixture{systemFixture: systemFixture{admins: map[string]bool{"root": true}}, row: auth.LoginProviderRecord{Provider: "google", Version: 1}}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
	server := &Server{system: repo, loginProviders: auth.NewLoginProviderManager(repo, cipher, "https://gateway.test")}
	for _, tc := range []struct {
		actor, method, body string
		code                int
	}{
		{"user", "GET", "", 403}, {"user", "PUT", `{"enabled":true,"version":1}`, 403},
		{"root", "PUT", `{"version":1}`, 400}, {"root", "PUT", `{"enabled":true,"version":0}`, 400},
		{"root", "PUT", `{"enabled":true,"version":1,"extra":1}`, 400},
		{"root", "PUT", `{"enabled":true,"version":1,"client_id":"client.apps.googleusercontent.com","client_secret":"fixture-secret"}`, 200},
		{"root", "PUT", `{"enabled":false,"version":1}`, 409}, {"root", "GET", "", 200},
	} {
		r := httptest.NewRequest(tc.method, "/v1/system/login-providers/google", strings.NewReader(tc.body))
		r.SetPathValue("provider", "google")
		r = r.WithContext(identity.WithOwnerID(r.Context(), tc.actor))
		w := httptest.NewRecorder()
		server.loginMethodSettings(w, r)
		if w.Code != tc.code {
			t.Fatal(tc, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), "fixture-secret") {
			t.Fatal("secret leaked")
		}
		if w.Code == 200 && tc.method == "GET" {
			var data map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data["data"] == nil {
				t.Fatal(w.Body)
			}
		}
	}
	if repo.writes != 1 {
		t.Fatal(repo.writes)
	}
}

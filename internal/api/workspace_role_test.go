package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func configuredCentralTestContext(t *testing.T) context.Context {
	t.Helper()
	m, e := installation.Describe(installation.DefaultCentralOrigin)
	if e != nil {
		t.Fatal(e)
	}
	return installation.WithContext(context.Background(), m)
}
func TestWorkspaceHostingTrustedConfiguration(t *testing.T) {
	db := workspaceDatabase(t)
	repo := postgres.NewSystemRepository(db)
	db.Exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`)
	cipher, _ := providerconfig.NewCipher(strings.Repeat("h", 32))
	gateway := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, WorkspaceAuthorization: wa.New(repo, cipher)})
	for _, origin := range []string{"", "https://console-staging.sociaro.ai", "https://console.sociaro.ai:8443", "https://CONSOLE.SOCIARO.AI:443/"} {
		t.Run(origin, func(t *testing.T) {
			h, err := WithPublicAPI(gateway, "", origin, "")
			if err != nil {
				t.Fatal(err)
			}
			device, _ := wa.DeviceCode()
			proof, _ := wa.DeviceCode()
			body := fmt.Sprintf(`{"device_code":%q,"installation_proof":%q,"client_name":"Fixture","client_origin":"http://localhost"}`, device, proof)
			r := httptest.NewRequest("POST", "https://console.sociaro.ai/auth/workspace/start", strings.NewReader(body))
			r.Host = "console.sociaro.ai"
			r.Header.Set("Forwarded", "host=console.sociaro.ai;proto=https")
			r.Header.Set("X-Forwarded-Host", "console.sociaro.ai")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			meta, _ := installation.Describe(origin)
			want := 403
			if meta.Role == "central" {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("trusted origin %s status %d: %s", origin, w.Code, w.Body)
			}
		})
	}
	for _, bad := range []string{"https://console.sociaro.ai/path", "https://console.sociaro.ai?role=central", "https://user@console.sociaro.ai", " https://console.sociaro.ai"} {
		if _, err := WithPublicAPI(gateway, "", bad, ""); err == nil {
			t.Fatal("invalid origin accepted", bad)
		}
	}
}
func TestCustomerHostingOverrideRejected(t *testing.T) {
	s, _ := newPersonalTestServer()
	f := newLimitsFixture()
	s.system = f
	h := s.handler()
	for _, value := range []string{"true", "false"} {
		for _, actor := range []string{"alice", "bob"} {
			w := sendTest(h, "PUT", "/v1/system/settings", `{"generation_ui":true,"customer_workspaces":`+value+`,"version":1}`, actor, "")
			want := 400
			if actor == "bob" {
				want = 403
			}
			if w.Code != want || f.settings.Version != 1 {
				t.Fatal("writable hosting override", w.Code)
			}
		}
	}
}
func TestAuthorizationForbiddenProvenance(t *testing.T) {
	db := workspaceDatabase(t)
	repo := postgres.NewSystemRepository(db)
	u, err := postgres.NewAuthRepository(db).UpsertGoogleUser(context.Background(), uuid.NewString(), uuid.NewString()+"@fixture.test", "Owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetAdmin(context.Background(), "", u.ID, true, true); err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("e", 32))
	for _, tc := range []struct {
		name, body string
		disabled   bool
	}{{"known", `{"error":{"code":"workspace_onboarding_disabled"}}`, true}, {"other", `{"error":{"code":"forbidden","message":"private upstream detail"}}`, false}, {"html", "<html>private</html>", false}, {"truncated", `{"error":{"code":"workspace_onboarding_disabled"`, false}, {"trailing", `{"error":{"code":"workspace_onboarding_disabled"}} x`, false}, {"oversized", `{"error":{"code":"workspace_onboarding_disabled"},"padding":"` + strings.Repeat("x", 4096) + `"}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); fmt.Fprint(w, tc.body) }))
			defer host.Close()
			manager := remote.New(repo, cipher, host.Client())
			_, err := manager.BeginAuthorization(context.Background(), u.ID, host.URL, "Fixture", "http://localhost", 0)
			if tc.disabled != errors.Is(err, wa.ErrDisabled) || !tc.disabled && !errors.Is(err, wa.ErrForbidden) {
				t.Fatalf("classification: %v", err)
			}
			attempt, e := repo.AuthorizationAttempt(context.Background(), u.ID)
			if e != nil {
				t.Fatal(e)
			}
			if e = repo.ClearAuthorizationAttempt(context.Background(), u.ID, attempt.ID); e != nil {
				t.Fatal(e)
			}
		})
	}
}

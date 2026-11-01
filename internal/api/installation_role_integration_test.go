package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func TestConfiguredCentralReceivesDedicatedInstallation(t *testing.T) {
	ctx := context.Background()
	centralDB, localDB := workspaceDatabase(t), workspaceDatabase(t)
	centralPeople, localPeople := postgres.NewAuthRepository(centralDB), postgres.NewAuthRepository(localDB)
	centralSystem, localSystem := postgres.NewSystemRepository(centralDB), postgres.NewSystemRepository(localDB)
	user := func(repo *postgres.AuthRepository, name string) identity.User {
		t.Helper()
		u, e := repo.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	operator, customer, localOwner := user(centralPeople, "Central operator"), user(centralPeople, "External customer"), user(localPeople, "Local administrator")
	if err := centralSystem.SetAdmin(ctx, "", operator.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if err := localSystem.SetAdmin(ctx, "", localOwner.ID, true, true); err != nil {
		t.Fatal(err)
	}
	settings, err := centralSystem.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.CustomerWorkspaces = true
	settings.SharedResources = true
	settings.PersonalProviders = false
	if _, err = centralSystem.SaveSettings(ctx, operator.ID, settings); err != nil {
		t.Fatal(err)
	}
	existing, err := centralSystem.CreateOrganization(ctx, operator.ID, "Central operator company", operator.ID, "operator.fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("z", 32))
	identityManager, err := auth.New(ctx, centralPeople, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: installation.DefaultCentralOrigin})
	if err != nil {
		t.Fatal(err)
	}
	service := &accountService{}
	gateway := NewGateway(Dependencies{Service: service, Health: healthStub{}, Auth: identityManager, System: centralSystem, Keys: postgres.NewAPIKeyRepository(centralDB), WorkspaceAuthorization: wa.New(centralSystem, cipher)})
	// The real UI public origin is trusted configuration. Loopback is only the
	// disposable API transport; no request Host/Forwarded header asserts the role.
	var active atomic.Pointer[http.Handler]
	central := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := active.Load()
		if h == nil {
			http.Error(w, "initializing", 503)
			return
		}
		(*h).ServeHTTP(w, r)
	}))
	defer central.Close()
	wrapped, err := WithPublicAPI(gateway, central.URL, installation.DefaultCentralOrigin, "")
	if err != nil {
		t.Fatal(err)
	}
	active.Store(&wrapped)
	response, err := central.Client().Get(central.URL + "/site.json")
	if err != nil {
		t.Fatal(err)
	}
	var site struct {
		Role   string `json:"installation_role"`
		Origin string `json:"central_origin"`
	}
	err = json.NewDecoder(response.Body).Decode(&site)
	response.Body.Close()
	if err != nil || site.Role != "central" || site.Origin != installation.DefaultCentralOrigin {
		t.Fatal("fixture is not a configured central deployment", err)
	}
	session, _ := wa.DeviceCode()
	if err = centralPeople.CreateSession(ctx, wa.Digest(session), customer.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	call := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", central.URL+path, strings.NewReader(string(data)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: session})
		w := httptest.NewRecorder()
		wrapped.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("central customer operation %s status %d", path, w.Code)
		}
		return w
	}
	manager := remote.New(localSystem, cipher, central.Client())
	view, err := manager.BeginAuthorization(ctx, localOwner.ID, central.URL, "External installation", "http://external.fixture.lan", 0)
	if err != nil {
		t.Fatal(err)
	}
	inspected := call("/v1/workspace-authorizations/inspect", map[string]string{"user_code": view.Code})
	var inspection struct {
		Request   wa.Challenge `json:"request"`
		Companies []wa.Company `json:"companies"`
	}
	if err = json.Unmarshal(inspected.Body.Bytes(), &inspection); err != nil || inspection.Request.Provisioning != "new" || len(inspection.Companies) != 0 || inspection.Request.Company != "" {
		t.Fatal("existing central company offered", err)
	}
	approved := call("/v1/workspace-authorizations/approve", map[string]any{"user_code": view.Code, "consent": true})
	var consent wa.Challenge
	if err = json.Unmarshal(approved.Body.Bytes(), &consent); err != nil || consent.Company == "" || consent.Company == existing.ID {
		t.Fatal("new installation did not receive separate company", err)
	}
	record, err := manager.PollAuthorization(ctx, localOwner.ID, view.ID)
	if err != nil || record.OrganizationID != consent.Company || record.Provisioning != "dedicated" {
		t.Fatal("verified company binding lost", err)
	}
	var role string
	if err = centralDB.QueryRow(`SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2`, record.OrganizationID, customer.ID).Scan(&role); err != nil || role != "supermanager" {
		t.Fatal("initiating customer lacks company authority", err)
	}
	var credits int
	if err = centralDB.QueryRow(`SELECT count(*) FROM organization_credits`).Scan(&credits); err != nil || credits != 0 {
		t.Fatal("central onboarding issued funds", err)
	}
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		res, _, _, e := manager.Proxy(ctx, localOwner.ID, method, path, "", []byte(body), uuid.NewString(), record.SnapshotID())
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil || res.StatusCode != status {
			t.Fatalf("company-scoped proxy %s status %d: %v", path, res.StatusCode, e)
		}
		return data
	}
	request("GET", "/v1/models", "", 200)
	request("POST", "/v1/generations/estimate", `{"prompt":"Synthetic central estimate","model":"mock/mock-video-v1"}`, 200)
	request("POST", "/v1/generations", `{"prompt":"Synthetic central task","model":"mock/mock-video-v1"}`, 202)
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.tasks) != 1 || service.tasks[0].OwnerID != record.OrganizationID || service.tasks[0].APIKeyID == nil || *service.tasks[0].APIKeyID != record.KeyID {
		t.Fatal("central generation lost dedicated company/key snapshot")
	}
	keyView := request("GET", "/v1/key", "", 200)
	if !strings.Contains(string(keyView), record.OrganizationID) || strings.Contains(string(keyView), existing.ID) {
		t.Fatal("central balance belonged to another company")
	}
	grants, err := centralSystem.Grants(ctx, operator.ID)
	if err != nil || len(grants) != 0 {
		t.Fatal("central owner personal consent was incorrectly required", err)
	}
}

package companyinterface_test

import (
	"bufio"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/playgroundkey"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/google/uuid"
)

func customerHandler(t *testing.T, f *activeHTTPFixture, authority ci.CustomerAuthority, services ...api.Service) http.Handler {
	t.Helper()
	a, e := auth.New(context.Background(), postgres.NewAuthRepository(f.centralDB), auth.Config{SessionSecret: fixtureMaster, PublicURL: fixtureCentral})
	if e != nil {
		t.Fatal(e)
	}
	lifecycle, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
	if e != nil {
		t.Fatal(e)
	}
	keys := postgres.NewAPIKeyRepository(f.centralDB)
	managed, e := playgroundkey.New(keys, keys, fixtureMaster)
	if e != nil {
		t.Fatal(e)
	}
	var service api.Service
	if len(services) > 0 {
		service = services[0]
	}
	h, e := api.WithPublicAPI(api.NewGateway(api.Dependencies{Service: service, CompanyInterface: lifecycle, CompanyCustomer: authority, CompanyCentralOrigin: fixtureCentral, Auth: a, System: postgres.NewSystemRepository(f.centralDB), Keys: keys, PlaygroundKeys: managed}), "", fixtureCentral, "")
	if e != nil {
		t.Fatal(e)
	}
	return fixtureReadyCentral(h)
}
func customerRequest(t *testing.T, h http.Handler, v ci.ActiveContext, browser bool, method, path, body, key string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	prefix := ci.CustomerSDKPrefix
	if browser {
		prefix = ci.CustomerBrowserPrefix
	}
	r := httptest.NewRequest(method, fixtureCentral+prefix+strings.TrimPrefix(path, "/"), strings.NewReader(body))
	r.Header.Set(ci.InstallationHeader, v.Tokens.InstallationCredential)
	r.Header.Set(ci.LocalOriginHeader, fixtureLocal)
	if browser {
		r.Header.Set(ci.SessionHeader, v.Tokens.AccessToken)
		r.Header.Set(ci.LocalSessionHeader, hex.EncodeToString(v.Tokens.LocalSessionHash[:]))
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestCompanyCustomerActualHandlersPreserveCompanyCeiling(t *testing.T) {
	f := activeHTTP(t)
	v := f.current(t)
	ctx := context.Background()
	authority := postgres.NewCompanyInterfaceAuthority(f.centralDB)
	h := customerHandler(t, f, authority)
	sys := postgres.NewSystemRepository(f.centralDB)
	if e := sys.SetAdmin(ctx, "", v.Tokens.Actor, true, true); e != nil {
		t.Fatal(e)
	}
	foreign, e := sys.CreateOrganization(ctx, v.Tokens.Actor, "Other company", v.Tokens.Actor, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	// Actual globally privileged account remains its actual company Supermanager.
	me := customerRequest(t, h, v, true, "GET", "/v1/me", "", "", nil)
	expectFixture(t, me, 200)
	var profile struct {
		ID        string `json:"id"`
		Admin     bool   `json:"is_system_admin"`
		Sales     bool   `json:"is_client_manager"`
		SalesRole string `json:"sales_role"`
	}
	if e = json.Unmarshal(me.Body.Bytes(), &profile); e != nil || profile.ID != v.Tokens.Actor || profile.Admin || profile.Sales || profile.SalesRole != "" {
		t.Fatalf("unsafe profile: %s", me.Body.String())
	}
	for _, secret := range []string{v.Tokens.InstallationCredential, v.Tokens.AccessToken, v.Tokens.RefreshToken} {
		if strings.Contains(me.Body.String(), secret) {
			t.Fatal("profile secret")
		}
	}
	list := customerRequest(t, h, v, true, "GET", "/v1/organizations", "", "", nil)
	expectFixture(t, list, 200)
	var companies struct {
		Data []struct{ ID, Role string } `json:"data"`
	}
	if e = json.Unmarshal(list.Body.Bytes(), &companies); e != nil || len(companies.Data) != 1 || companies.Data[0].ID != v.Tokens.Company || companies.Data[0].Role != "supermanager" {
		t.Fatalf("company ceiling: %s", list.Body.String())
	}
	expectFixture(t, customerRequest(t, h, v, true, "GET", "/v1/organizations/"+foreign.ID, "", "", nil), 403)
	expectFixture(t, customerRequest(t, h, v, true, "GET", "/v1/organizations?all=true", "", "", nil), 403)
	expectFixture(t, customerRequest(t, h, v, true, "GET", "/v1/system/templates/library", "", "", nil), 403)
	// Existing browser cookie behavior is unchanged outside the new boundary.
	direct := httptest.NewRequest("GET", fixtureCentral+"/v1/me", nil)
	direct.AddCookie(f.centralCookie)
	dw := httptest.NewRecorder()
	h.ServeHTTP(dw, direct)
	expectFixture(t, dw, 200)
	if !strings.Contains(dw.Body.String(), `"is_system_admin":true`) {
		t.Fatal("direct admin behavior changed")
	}
	// The managed key path uses the actual handler and ordinary saved-key policy.
	restored := customerRequest(t, h, v, true, "POST", "/v1/playground-key/restore", `{"organization_id":"`+v.Tokens.Company+`"}`, "", nil)
	expectFixture(t, restored, 200)
	var managed struct {
		Secret string `json:"secret"`
	}
	json.Unmarshal(restored.Body.Bytes(), &managed)
	if managed.Secret != v.Tokens.PlaygroundKey {
		t.Fatal("managed key replaced")
	}
	// A globally scoped original key still has only the bound-company ceiling.
	keys := postgres.NewAPIKeyRepository(f.centralDB)
	k, secret, e := apikey.Generate(apikey.CreateInput{Name: "global fixture", OrganizationID: v.Tokens.Company, ResourceMode: "shared", StorageMode: "shared", Scopes: []string{apikey.Generation, apikey.KeysManage, apikey.SystemManage}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = keys.Create(ctx, v.Tokens.Actor, k, apikey.Hash(secret)); e != nil {
		t.Fatal(e)
	}
	expectFixture(t, customerRequest(t, h, v, false, "GET", "/v1/organizations", "", secret, nil), 200)
	expectFixture(t, customerRequest(t, h, v, false, "GET", "/v1/organizations/"+foreign.ID, "", secret, nil), 403)
	expectFixture(t, customerRequest(t, h, v, false, "GET", "/v1/system/templates/library", "", secret, nil), 403)
	before := countFixture(t, f.centralDB, "user_api_keys")
	for _, scope := range []string{apikey.SystemManage, apikey.ProvidersManage, apikey.StorageManage} {
		body := `{"name":"forbidden","organization_id":"` + v.Tokens.Company + `","scopes":["generation","` + scope + `"]}`
		expectFixture(t, customerRequest(t, h, v, true, "POST", "/v1/api-keys", body, "", nil), 403)
	}
	if countFixture(t, f.centralDB, "user_api_keys") != before {
		t.Fatal("privileged child key created")
	}
	// Ordinary recipient use is independent of creator rank and browser identity.
	_, coworker, _ := fixtureAuth(t, f.centralDB, fixtureCentral, false)
	if _, e = f.centralDB.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'member')`, v.Tokens.Company, coworker.ID); e != nil {
		t.Fatal(e)
	}
	memberKey, memberSecret, e := apikey.Generate(apikey.CreateInput{Name: "coworker", UserID: coworker.ID, OrganizationID: v.Tokens.Company, ResourceMode: "shared", StorageMode: "shared"}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = keys.Create(ctx, v.Tokens.Actor, memberKey, apikey.Hash(memberSecret)); e != nil {
		t.Fatal(e)
	}
	used := customerRequest(t, h, v, true, "GET", "/v1/key", "", memberSecret, nil)
	expectFixture(t, used, 200)
	var usedKey struct {
		Key apikey.Key `json:"key"`
	}
	json.Unmarshal(used.Body.Bytes(), &usedKey)
	if usedKey.Key.UserID != coworker.ID || usedKey.Key.CreatedBy != v.Tokens.Actor {
		t.Fatal("creator impersonation or recipient restriction")
	}
	// Browser session is mandatory even with a valid original key.
	expectFixture(t, customerRequest(t, h, v, true, "GET", "/v1/key", "", memberSecret, func(r *http.Request) { r.Header.Set(ci.SessionHeader, strings.Repeat("x", 43)) }), 403)
	expectFixture(t, customerRequest(t, h, v, true, "GET", "/v1/me", "", "", func(r *http.Request) { r.Header.Set("Authorization", "Bearer invalid") }), 403)
	for _, name := range []string{"Cookie", "Origin", "Sec-Fetch-Site"} {
		expectFixture(t, customerRequest(t, h, v, false, "GET", "/v1/key", "", memberSecret, func(r *http.Request) { r.Header[name] = []string{""} }), 403)
	}
	// Invalid tenant/body/header shapes cannot reach the existing handlers.
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/v1/api-keys?organization_id=" + foreign.ID, ""},
		{"POST", "/v1/playground-key/restore", `{"organization_id":"` + foreign.ID + `"}`},
		{"PATCH", "/v1/me", `{"is_system_admin":true}`},
		{"GET", "/v1/me", "{}"},
		{"GET", "/v1/%6de", ""},
	} {
		expectFixture(t, customerRequest(t, h, v, true, tc.method, tc.path, tc.body, "", nil), 403)
	}
	expectFixture(t, customerRequest(t, h, v, true, "GET", "/v1/me", "", "", func(r *http.Request) { r.Header.Add(ci.InstallationHeader, v.Tokens.InstallationCredential) }), 403)
	expectFixture(t, customerRequest(t, h, v, true, "GET", "/v1/me", "", "", func(r *http.Request) { r.Header.Set(ci.LocalOriginHeader, "https://foreign.fixture.test") }), 403)
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, v.Tokens.Actor); e != nil {
		t.Fatal(e)
	}
	limited := customerRequest(t, h, v, true, "GET", "/v1/organizations", "", "", nil)
	expectFixture(t, limited, 200)
	json.Unmarshal(limited.Body.Bytes(), &companies)
	if len(companies.Data) != 1 || companies.Data[0].Role != "member" {
		t.Fatal("global admin overrode member role")
	}
	expectFixture(t, customerRequest(t, h, v, true, "POST", "/v1/playground-key/restore", `{"organization_id":"`+v.Tokens.Company+`"}`, "", nil), 200)
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET role='supermanager' WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, coworker.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.centralDB.Exec(`UPDATE user_api_keys SET scopes='["generation","keys:manage"]'::jsonb WHERE id=$1`, memberKey.ID); e != nil {
		t.Fatal(e)
	}
	crafted := `{"name":"borrowed manager","organization_id":"` + v.Tokens.Company + `","resource_mode":"shared"}`
	before = countFixture(t, f.centralDB, "user_api_keys")
	expectFixture(t, customerRequest(t, h, v, true, "POST", "/v1/api-keys", crafted, memberSecret, nil), 403)
	if countFixture(t, f.centralDB, "user_api_keys") != before {
		t.Fatal("browser borrowed coworker management authority")
	}
	if _, e = f.centralDB.Exec(`UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, v.Tokens.Company, coworker.ID); e != nil {
		t.Fatal(e)
	}
	// Sales role remains ineligible at production admission, even if a member.
	if _, e = f.centralDB.Exec(`UPDATE users SET is_system_admin=false,is_client_manager=true WHERE id=$1`, coworker.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = authority.Membership(ctx, v.Tokens.Company, coworker.ID); e == nil {
		t.Fatal("sales authority admitted")
	}
	bound := identity.WithCompanyBoundary(ctx, v.Tokens.Company)
	role, e := sys.OrganizationAccess(bound, coworker.ID, v.Tokens.Company)
	if e != nil || role != "member" {
		t.Fatalf("internal sales override %q %v", role, e)
	}
	items, e := sys.Organizations(bound, coworker.ID, true)
	if e != nil || len(items) != 1 || items[0].Role != "member" {
		t.Fatalf("internal sales list: %+v %v", items, e)
	}
	if _, e = sys.OrganizationAccess(bound, coworker.ID, foreign.ID); e == nil {
		t.Fatal("foreign internal company allowed")
	}
}

// The verified projection happens before the inner real handler, and the
// after-body authority check must suppress a response after concurrent revocation.
type revokeOnProjection struct {
	ci.CustomerAuthority
	revoke func()
}

func (a revokeOnProjection) User(ctx context.Context, id string) (identity.User, error) {
	u, e := a.CustomerAuthority.User(ctx, id)
	if e == nil {
		a.revoke()
	}
	return u, e
}
func TestCompanyCustomerAfterBodyAuthorityFence(t *testing.T) {
	f := activeHTTP(t)
	v := f.current(t)
	authority := revokeOnProjection{CustomerAuthority: postgres.NewCompanyInterfaceAuthority(f.centralDB), revoke: func() {
		if _, e := f.centralDB.Exec(`UPDATE company_interface_sessions SET revoked_at=clock_timestamp(),session_epoch=session_epoch+1 WHERE id=$1`, v.Tokens.SessionID); e != nil {
			t.Fatal(e)
		}
	}}
	h := customerHandler(t, f, authority)
	response := customerRequest(t, h, v, true, "GET", "/v1/me", "", "", nil)
	expectFixture(t, response, 409)
	if strings.Contains(response.Body.String(), v.Tokens.Actor) || strings.Contains(response.Body.String(), `"email"`) {
		t.Fatal("late revoked profile released")
	}
}

func TestCompanyCustomerStalledBodyUsesTotalDeadline(t *testing.T) {
	f := activeHTTP(t)
	v := f.current(t)
	h := customerHandler(t, f, postgres.NewCompanyInterfaceAuthority(f.centralDB))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 150*time.Millisecond)
		defer cancel()
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	defer server.Close()
	conn, e := net.Dial("tcp", strings.TrimPrefix(server.URL, "http://"))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, e = fmt.Fprintf(conn, "PATCH %sv1/me HTTP/1.1\r\nHost: console.sociaro.ai\r\nContent-Type: application/json\r\nContent-Length: 2\r\n%s: %s\r\n%s: %s\r\n%s: %s\r\n%s: %s\r\n\r\n{", ci.CustomerBrowserPrefix, ci.InstallationHeader, v.Tokens.InstallationCredential, ci.LocalOriginHeader, fixtureLocal, ci.SessionHeader, v.Tokens.AccessToken, ci.LocalSessionHeader, hex.EncodeToString(v.Tokens.LocalSessionHash[:]))
	if e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	response, e := http.ReadResponse(bufio.NewReader(conn), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != 403 || time.Since(started) > time.Second {
		t.Fatalf("stalled body escaped total deadline: %d %v", response.StatusCode, time.Since(started))
	}
}

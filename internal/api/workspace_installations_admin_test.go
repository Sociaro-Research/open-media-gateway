package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func TestOrganizationInstallationAdministration(t *testing.T) {
	db := workspaceDatabase(t)
	ctx := installation.WithContext(context.Background(), installation.Metadata{Role: "central", CentralOrigin: installation.DefaultCentralOrigin})
	people, system := postgres.NewAuthRepository(db), postgres.NewSystemRepository(db)
	user := func(name string) identity.User {
		t.Helper()
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	admin, issuer, other, manager, member := user("Administrator"), user("Issuer"), user("Other issuer"), user("Manager"), user("Member")
	if e := system.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("t", 32))
	authorization := wa.New(system, cipher)
	issue := func(u identity.User, proof, access string) wa.Challenge {
		t.Helper()
		device, _ := wa.DeviceCode()
		challenge, e := authorization.Start(ctx, device, wa.Challenge{Label: "Fixture installation", Origin: "https://fixture.local", InstallationProof: proof, Access: access})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = system.Approve(ctx, u.ID, challenge.Code, wa.Approval{Consent: true, Access: access}); e != nil {
			t.Fatal(e)
		}
		issued, _, e := authorization.Poll(ctx, device)
		if e != nil {
			t.Fatal(e)
		}
		return issued
	}
	proof, _ := wa.DeviceCode()
	otherProof, _ := wa.DeviceCode()
	permanent := issue(issuer, proof, wa.CompanyAccess)
	expiring := issue(issuer, proof, wa.GenerationAccess)
	foreign := issue(other, otherProof, wa.CompanyAccess)
	expired := issue(issuer, proof, wa.GenerationAccess)
	if _, e := db.Exec(`UPDATE user_api_keys SET expires_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, expired.KeyID); e != nil {
		t.Fatal(e)
	}
	// Historical records without a proof are still administered through their
	// exact company/key association; no legacy-to-permanent migration occurs.
	if _, e := db.Exec(`UPDATE workspace_authorizations SET installation_hash=NULL WHERE key_id=$1`, expiring.KeyID); e != nil {
		t.Fatal(e)
	}
	for _, entry := range []struct {
		u    identity.User
		role string
	}{{manager, "admin"}, {member, "member"}} {
		if _, e := db.Exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,$3)`, permanent.Company, entry.u.ID, entry.role); e != nil {
			t.Fatal(e)
		}
	}
	service, e := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: installation.DefaultCentralOrigin})
	if e != nil {
		t.Fatal(e)
	}
	gateway := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, Auth: service, System: system, Keys: postgres.NewAPIKeyRepository(db), WorkspaceAuthorization: authorization})
	host, e := WithPublicAPI(gateway, installation.DefaultCentralOrigin, installation.DefaultCentralOrigin, "")
	if e != nil {
		t.Fatal(e)
	}
	local, e := WithPublicAPI(gateway, "https://local.fixture.test", "https://local.fixture.test", "")
	if e != nil {
		t.Fatal(e)
	}
	cookies := map[string]*http.Cookie{}
	for _, u := range []identity.User{admin, issuer, other, manager, member} {
		token, _ := wa.DeviceCode()
		if e = people.CreateSession(ctx, wa.Digest(token), u.ID, time.Now().Add(time.Hour)); e != nil {
			t.Fatal(e)
		}
		cookies[u.ID] = &http.Cookie{Name: "media_gateway_session", Value: token}
	}
	call := func(h http.Handler, origin, method, path, actor, bearer string, cross bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, origin+path, nil)
		if actor != "" {
			r.AddCookie(cookies[actor])
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cross {
			r.Header.Set("Origin", "https://attacker.invalid")
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, status int) {
		t.Helper()
		if w.Code != status {
			t.Fatalf("got %d want %d: %s", w.Code, status, w.Body)
		}
	}
	base := "/v1/system/organizations/" + permanent.Company + "/installation-access"
	for _, u := range []identity.User{issuer, other, manager, member} {
		expect(call(host, installation.DefaultCentralOrigin, "GET", base, u.ID, "", false), 403)
		expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+permanent.KeyID, u.ID, "", false), 403)
	}
	expect(call(local, "https://local.fixture.test", "GET", base, admin.ID, "", false), 403)
	expect(call(local, "https://local.fixture.test", "DELETE", base+"/"+permanent.KeyID, admin.ID, "", false), 403)
	_, adminKey, e := system.IssueAdministratorKey(ctx, admin.ID, "Fixture admin key")
	if e != nil {
		t.Fatal(e)
	}
	expect(call(host, installation.DefaultCentralOrigin, "GET", base, "", adminKey, false), 403)
	expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+permanent.KeyID, "", adminKey, false), 403)
	expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+permanent.KeyID, admin.ID, "invalid", false), 401)
	expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+permanent.KeyID, admin.ID, "", true), 403)
	for _, u := range []identity.User{admin, issuer, manager, member} {
		expect(call(host, installation.DefaultCentralOrigin, "GET", "/v1/workspace-installations", u.ID, "", false), 403)
		expect(call(host, installation.DefaultCentralOrigin, "DELETE", "/v1/workspace-installations/"+permanent.KeyID, u.ID, "", false), 403)
	}
	summary := call(host, installation.DefaultCentralOrigin, "GET", "/v1/system/installation-access", admin.ID, "", false)
	expect(summary, 200)
	var counts struct {
		Data []wa.InstallationAccessCount `json:"data"`
	}
	if e = json.Unmarshal(summary.Body.Bytes(), &counts); e != nil {
		t.Fatal(e)
	}
	active := map[string]int{}
	for _, item := range counts.Data {
		active[item.Company] = item.Active
	}
	if active[permanent.Company] != 2 || active[foreign.Company] != 1 {
		t.Fatal("wrong active counts", counts)
	}
	expect(call(host, installation.DefaultCentralOrigin, "GET", "/v1/system/installation-access", issuer.ID, "", false), 403)
	expect(call(local, "https://local.fixture.test", "GET", "/v1/system/installation-access", admin.ID, "", false), 403)
	w := call(host, installation.DefaultCentralOrigin, "GET", base, admin.ID, "", false)
	expect(w, 200)
	var result struct {
		Data []wa.InstallationGrant `json:"data"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Data) != 3 {
		t.Fatal("wrong company grant list", result)
	}
	byID := map[string]wa.InstallationGrant{}
	for _, g := range result.Data {
		byID[g.ID] = g
		if g.Company != permanent.Company || g.Prefix == "" {
			t.Fatal("wrong association", g)
		}
	}
	if byID[permanent.KeyID].Expires != nil || byID[expiring.KeyID].Expires == nil || byID[expiring.KeyID].Provisioning != "legacy" {
		t.Fatal("mixed lifetimes lost", result)
	}
	expect(call(host, installation.DefaultCentralOrigin, "HEAD", base, admin.ID, "", false), 200)
	for _, method := range []string{"POST", "PUT", "PATCH", "OPTIONS"} {
		expect(call(host, installation.DefaultCentralOrigin, method, base, admin.ID, "", false), 405)
	}
	expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+foreign.KeyID, admin.ID, "", false), 410)
	expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+uuid.NewString(), admin.ID, "", false), 410)
	// An ordinary company key is not an installation key and must not be revoked.
	plain, _, e := system.IssueAdministratorKey(ctx, admin.ID, "Unrelated key")
	if e != nil {
		t.Fatal(e)
	}
	expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+plain.ID, admin.ID, "", false), 410)
	for range 2 {
		expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+permanent.KeyID, admin.ID, "", false), 200)
	}
	var revoked bool
	var cleared bool
	if e = db.QueryRow(`SELECT k.revoked_at IS NOT NULL,a.token_ciphertext IS NULL FROM user_api_keys k JOIN workspace_authorizations a ON a.key_id=k.id WHERE k.id=$1`, permanent.KeyID).Scan(&revoked, &cleared); e != nil || !revoked || !cleared {
		t.Fatal("revocation missing", e)
	}
	for _, id := range []string{expiring.KeyID, foreign.KeyID, plain.ID} {
		if e = db.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE id=$1`, id).Scan(&revoked); e != nil || revoked {
			t.Fatal("unrelated key changed", id, e)
		}
	}
	w = call(host, installation.DefaultCentralOrigin, "GET", base, admin.ID, "", false)
	expect(w, 200)
	if e = json.Unmarshal(w.Body.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if len(result.Data) != 3 {
		t.Fatal("history lost")
	}
	var companies, bindings int
	db.QueryRow(`SELECT count(*) FROM organizations`).Scan(&companies)
	db.QueryRow(`SELECT count(*) FROM workspace_installations`).Scan(&bindings)
	if companies != 2 || bindings != 2 {
		t.Fatal("company/binding deleted", companies, bindings)
	}
	// Current role is rechecked even with the same previously valid cookie.
	if _, e = db.Exec(`UPDATE users SET is_system_admin=FALSE WHERE id=$1`, admin.ID); e != nil {
		t.Fatal(e)
	}
	expect(call(host, installation.DefaultCentralOrigin, "GET", base, admin.ID, "", false), 403)
	expect(call(host, installation.DefaultCentralOrigin, "DELETE", base+"/"+expiring.KeyID, admin.ID, "", false), 403)
}

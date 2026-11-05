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

// The UI predicate is based on this existing authorized summary, not names or
// the existence of unrelated company keys. All state is disposable test data.
func TestInstallationSummaryExcludesOrdinaryAndInactiveCompanies(t *testing.T) {
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
	admin, issuer := user("Administrator"), user("Issuer")
	if e := system.SetAdmin(ctx, "", admin.ID, true, true); e != nil {
		t.Fatal(e)
	}
	cipher, e := providerconfig.NewCipher(strings.Repeat("f", 32))
	if e != nil {
		t.Fatal(e)
	}
	manager := wa.New(system, cipher)
	issue := func(proof, access string) wa.Challenge {
		t.Helper()
		device, _ := wa.DeviceCode()
		ch, e := manager.Start(ctx, device, wa.Challenge{Label: "Same visible name", Origin: "https://fixture.local", InstallationProof: proof, Access: access})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = system.Approve(ctx, issuer.ID, ch.Code, wa.Approval{Consent: true, Access: access}); e != nil {
			t.Fatal(e)
		}
		ch, _, e = manager.Poll(ctx, device)
		if e != nil {
			t.Fatal(e)
		}
		return ch
	}
	proof, _ := wa.DeviceCode()
	permanent := issue(proof, wa.CompanyAccess)
	legacy := issue(proof, wa.GenerationAccess)
	legacyProof, _ := wa.DeviceCode()
	legacyOnly := issue(legacyProof, wa.GenerationAccess)
	revokedProof, _ := wa.DeviceCode()
	revoked := issue(revokedProof, wa.CompanyAccess)
	expiredProof, _ := wa.DeviceCode()
	expired := issue(expiredProof, wa.GenerationAccess)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`UPDATE workspace_authorizations SET installation_hash=NULL WHERE key_id IN ($1,$2)`, legacy.KeyID, legacyOnly.KeyID)
	exec(`UPDATE user_api_keys SET revoked_at=NOW() WHERE id=$1`, revoked.KeyID)
	exec(`UPDATE user_api_keys SET expires_at=NOW()-INTERVAL '1 hour' WHERE id=$1`, expired.KeyID)
	ordinary := uuid.NewString()
	exec(`INSERT INTO organizations(id,name) VALUES($1,'fixture.local')`, ordinary)
	plainKey := uuid.NewString()
	exec(`INSERT INTO user_api_keys(id,user_id,created_by,organization_id,name,prefix,token_hash,scopes,resource_mode,storage_mode) VALUES($1,$2,$2,$3,'Same visible name','fixture',$4,'["generation"]','shared','shared')`, plainKey, issuer.ID, ordinary, wa.Digest("synthetic-unrelated-key"))
	service, e := auth.New(ctx, people, auth.Config{SessionSecret: strings.Repeat("s", 32), PublicURL: installation.DefaultCentralOrigin})
	if e != nil {
		t.Fatal(e)
	}
	gateway := NewGateway(Dependencies{Service: &accountService{}, Health: healthStub{}, Auth: service, System: system, Keys: postgres.NewAPIKeyRepository(db), WorkspaceAuthorization: manager})
	host, e := WithPublicAPI(gateway, installation.DefaultCentralOrigin, installation.DefaultCentralOrigin, "")
	if e != nil {
		t.Fatal(e)
	}
	token, _ := wa.DeviceCode()
	if e = people.CreateSession(ctx, wa.Digest(token), admin.ID, time.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	summary := func() map[string]int {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, installation.DefaultCentralOrigin+"/v1/system/installation-access", nil)
		r.AddCookie(&http.Cookie{Name: "media_gateway_session", Value: token})
		w := httptest.NewRecorder()
		host.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var data struct {
			Data []wa.InstallationAccessCount `json:"data"`
		}
		if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
			t.Fatal(e)
		}
		out := map[string]int{}
		for _, v := range data.Data {
			out[v.Company] = v.Active
		}
		return out
	}
	got := summary()
	if len(got) != 2 || got[permanent.Company] != 2 || got[legacyOnly.Company] != 1 {
		t.Fatal("wrong active installation classification", got)
	}
	for _, id := range []string{ordinary, revoked.Company, expired.Company} {
		if _, present := got[id]; present {
			t.Fatal("ordinary/inactive company advertised as revocable", id)
		}
	}
	for _, id := range []string{permanent.KeyID, legacy.KeyID} {
		if e = system.RevokeOrganizationInstallation(ctx, admin.ID, permanent.Company, id); e != nil {
			t.Fatal(e)
		}
	}
	got = summary()
	if len(got) != 1 || got[legacyOnly.Company] != 1 {
		t.Fatal("all-revoked company remains active or another installation changed", got)
	}
	history, e := system.OrganizationInstallationGrants(ctx, admin.ID, permanent.Company)
	if e != nil || len(history) != 2 {
		t.Fatal("history not retained", len(history), e)
	}
	var plainRevoked bool
	if e = db.QueryRow(`SELECT revoked_at IS NOT NULL FROM user_api_keys WHERE id=$1`, plainKey).Scan(&plainRevoked); e != nil || plainRevoked {
		t.Fatal("ordinary company key changed", e)
	}
	t.Log("HTTP summary: permanent+legacy, legacy-only, revoked-only, expired-only and ordinary same-name/key controls; last grant revoke removes only its active summary entry while retaining history")
}

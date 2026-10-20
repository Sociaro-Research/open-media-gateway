package postgres

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/api"
	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
)

// Reproduce the reported privilege escalation through real HTTP authentication
// and PostgreSQL authorization; fixtures that always return "admin" miss it.
func TestPersonalKeyCannotInheritSystemOrganizationAuthority(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "key_security_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec("DROP SCHEMA " + schema + " CASCADE")
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthRepository(db)
	user := func(name string) identity.User {
		u, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), name+"@example.test", name, "")
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	admin, member, target := user("system"), user("member"), user("target")
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	repo := NewSystemRepository(db)
	foreign, err := repo.CreateOrganization(ctx, admin.ID, "Foreign", member.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := repo.CreateOrganization(ctx, admin.ID, "Own", admin.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	keys := NewAPIKeyRepository(db)
	key := func(owner, organization string, scopes ...string) (apikey.Key, string) {
		k, secret, err := apikey.Generate(apikey.CreateInput{Name: "Security test", OrganizationID: organization, Scopes: append([]string{apikey.Generation}, scopes...)}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = keys.Create(ctx, owner, k, apikey.Hash(secret)); err != nil {
			t.Fatal(err)
		}
		return k, secret
	}
	restrictedKey, restricted := key(admin.ID, "", apikey.OrganizationsManage)
	_, privileged := key(admin.ID, "", apikey.OrganizationsManage, apikey.SystemManage)
	_, memberKey := key(member.ID, "", apikey.OrganizationsManage)
	_, boundKey := key(admin.ID, owned.ID, apikey.OrganizationsManage)
	_, generationKey := key(admin.ID, "")
	h := api.NewWithSystemAdministration(nil, nil, "", nil, "", 0, nil, nil, nil, repo, keys)
	call := func(method, path, body, secret string, want int) {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+secret)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, want, w.Body.String())
		}
	}
	path := "/v1/organizations/" + foreign.ID
	for _, secret := range []string{restricted, boundKey, generationKey} {
		call("GET", path+"/members", "", secret, 403)
		call("PUT", path+"/members/"+target.ID, `{"role":"admin"}`, secret, 403)
		call("POST", path+"/invitations", `{"role":"admin"}`, secret, 403)
		call("DELETE", path+"/members/"+member.ID, "", secret, 403)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM organization_members WHERE organization_id=$1 AND user_id=$2`, foreign.ID, target.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unauthorized membership created: %d %v", count, err)
	}
	call("GET", "/v1/organizations?all=true", "", restricted, 403)
	call("POST", "/v1/organizations", `{"name":"Forbidden"}`, restricted, 403)
	// Assigning a nonmember is a system operation; organization administrators
	// can still manage existing members and invite new ones.
	call("PUT", "/v1/organizations/"+owned.ID+"/members/"+target.ID, `{"role":"member"}`, privileged, 204)
	call("PUT", path+"/members/"+target.ID, `{"role":"member"}`, privileged, 204)
	// Legitimate organization administration remains available through the key.
	call("PUT", "/v1/organizations/"+owned.ID+"/members/"+target.ID, `{"role":"member"}`, restricted, 204)
	call("PUT", path+"/members/"+target.ID, `{"role":"member"}`, memberKey, 204)
	call("PUT", path+"/members/"+target.ID, `{"role":"admin"}`, privileged, 204)
	// Membership role must not be promoted by the account's system role.
	if _, err = db.Exec(`UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, owned.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	call("PUT", "/v1/organizations/"+owned.ID+"/members/"+target.ID, `{"role":"admin"}`, restricted, 403)
	// Revoking the account role takes effect even for an existing system-scoped key.
	if _, err = db.Exec(`UPDATE users SET is_system_admin=FALSE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	call("PUT", path+"/members/"+target.ID, `{"role":"admin"}`, privileged, 403)
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if role, err := repo.OrganizationAccess(ctx, admin.ID, foreign.ID); err != nil || role != "system_admin" {
		t.Fatalf("browser authority lost: %s %v", role, err)
	}
	// Repository access is fail-closed even if the API middleware omitted the grant.
	keyCtx := identity.WithAPIKeyID(identity.WithUser(ctx, admin), restrictedKey.ID)
	if _, err = repo.OrganizationAccess(keyCtx, admin.ID, foreign.ID); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("repository bypass: %v", err)
	}
	listed, err := repo.Organizations(keyCtx, admin.ID, true)
	if err != nil || len(listed) != 1 || listed[0].ID != owned.ID || listed[0].MarkupPercent != nil || listed[0].EffectiveMarkupPercent != nil {
		t.Fatalf("restricted list leaked system view: %+v %v", listed, err)
	}
	if _, err = repo.CreateOrganization(keyCtx, admin.ID, "Forbidden direct call", "", uuid.NewString()+".fixture.test"); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("system mutation bypass: %v", err)
	}
}

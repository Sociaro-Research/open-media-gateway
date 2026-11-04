package postgres

import (
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"reflect"
	"strings"
	"testing"
)

func TestWorkspaceCompanyKeyExplicitConsentAndRecovery(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := centralWorkspaceTestContext(t)
	repo := NewSystemRepository(db)
	user, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Company customer", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.SetAdmin(ctx, "", user.ID, true, true); err != nil {
		t.Fatal(err)
	}
	settings, err := repo.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.CustomerWorkspaces = true
	settings.SharedResources = true
	settings.PersonalProviders = false
	if _, err = repo.SaveSettings(ctx, user.ID, settings); err != nil {
		t.Fatal(err)
	}
	cipher, err := providerconfig.NewCipher(strings.Repeat("x", 32))
	if err != nil {
		t.Fatal(err)
	}
	manager := wa.New(repo, cipher)
	proof, _ := wa.DeviceCode()
	device, _ := wa.DeviceCode()
	input := wa.Challenge{Access: wa.CompanyAccess, InstallationProof: proof, Label: "Synthetic company UI", Origin: "https://local.fixture.test"}
	challenge, err := manager.Start(ctx, device, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Approve(ctx, user.ID, challenge.Code, wa.Approval{Consent: true}); !errors.Is(err, wa.ErrForbidden) {
		t.Fatal("old consent widened", err)
	}
	input.Access = wa.GenerationAccess
	if _, err = manager.Start(ctx, device, input); !errors.Is(err, wa.ErrConflict) {
		t.Fatal("same proof changed intent", err)
	}
	input.Access = wa.CompanyAccess
	approved, err := repo.Approve(ctx, user.ID, challenge.Code, wa.Approval{Consent: true, Access: wa.CompanyAccess})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Approve(ctx, user.ID, challenge.Code, wa.Approval{Consent: true, Access: wa.CompanyAccess}); err != nil {
		t.Fatal(err)
	}
	poll := func() (wa.Challenge, string, error) {
		t.Helper()
		if _, e := db.Exec(`UPDATE workspace_authorizations SET next_poll_at=NOW()-INTERVAL '1 second' WHERE device_hash=$1`, wa.Digest(device)); e != nil {
			t.Fatal(e)
		}
		return manager.Poll(ctx, device)
	}
	issued, secret, err := poll()
	if err != nil {
		t.Fatal(err)
	}
	repeated, again, err := poll()
	if err != nil || again != secret || repeated.KeyID != issued.KeyID {
		t.Fatal("lost reply duplicated key", err)
	}
	subject, key, err := NewAPIKeyRepository(db).Authenticate(ctx, apikey.Hash(secret))
	if err != nil || subject.ID != user.ID || key.OrganizationID != approved.Company || !reflect.DeepEqual(key.Scopes, []string{apikey.Generation, apikey.OrganizationsManage, apikey.KeysManage}) {
		t.Fatal("wrong scope or subject", key, err)
	}
	var expiring bool
	if err = db.QueryRow(`SELECT expires_at IS NOT NULL FROM user_api_keys WHERE id=$1`, issued.KeyID).Scan(&expiring); err != nil || expiring {
		t.Fatal("company connection expires", err)
	}
	grants, err := repo.Grants(ctx, user.ID)
	if err != nil || len(grants) != 1 || grants[0].Expires != nil {
		t.Fatal("null grant expiry", grants, err)
	}
	var companies, keys int
	db.QueryRow(`SELECT count(*) FROM workspace_installations`).Scan(&companies)
	db.QueryRow(`SELECT count(*) FROM user_api_keys`).Scan(&keys)
	if companies != 1 || keys != 1 {
		t.Fatal("repeated approval created duplicates", companies, keys)
	}
	// Application rollback retains additive consent evidence; re-upgrade must
	// preserve the same key, company and permanent lifetime.
	if err = goose.DownTo(db, "migrations", 95); err != nil {
		t.Fatal(err)
	}
	if err = goose.UpTo(db, "migrations", 96); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE user_api_keys SET created_at=NOW()-INTERVAL '367 days' WHERE id=$1`, issued.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, after, e := NewAPIKeyRepository(db).Authenticate(ctx, apikey.Hash(secret)); e != nil || after.ID != issued.KeyID || after.OrganizationID != approved.Company {
		t.Fatal("permanent key lost after year/upgrade", e)
	}
	// A fresh explicit reconnect recovers the existing dedicated company.
	nextDevice, _ := wa.DeviceCode()
	next, err := manager.Start(ctx, nextDevice, input)
	if err != nil {
		t.Fatal(err)
	}
	nextApproval, err := repo.Approve(ctx, user.ID, next.Code, wa.Approval{Consent: true, Access: wa.CompanyAccess})
	if err != nil || nextApproval.Company != approved.Company {
		t.Fatal("reconnect changed company", err)
	}
	if err = repo.Revoke(ctx, user.ID, issued.KeyID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = poll(); !errors.Is(err, wa.ErrUnavailable) {
		t.Fatal("revoked key recovered", err)
	}
}

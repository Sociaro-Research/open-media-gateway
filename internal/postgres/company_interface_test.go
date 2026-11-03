package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pressly/goose/v3"
)

func companyInterfaceSeed(t *testing.T, db *sql.DB) (ci.Grant, ci.Credentials, string) {
	t.Helper()
	actor, company, grant, session := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,email,display_name) VALUES($1,$2,'Fixture central member')`, actor, actor+"@fixture.test")
	exec(`INSERT INTO organizations(id,name) VALUES($1,'Dedicated company fixture')`, company)
	exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'member')`, company, actor)
	c := ci.Credentials{Installation: strings.Repeat("i", 32), Session: strings.Repeat("s", 32), LocalSessionHash: sha256.Sum256([]byte("local session fixture"))}
	installation := sha256.Sum256([]byte("installation proof fixture"))
	token := sha256.Sum256([]byte(c.Installation))
	access := sha256.Sum256([]byte(c.Session))
	refresh := sha256.Sum256([]byte("refresh fixture"))
	exec(`INSERT INTO company_interface_installations(installation_hash,organization_id,central_origin) VALUES($1,$2,'https://central.example')`, installation[:], company)
	exec(`INSERT INTO company_interface_grants(id,installation_hash,organization_id,central_origin,approved_local_origin,purpose,protocol_version,credential_hash) VALUES($1,$2,$3,'https://central.example','https://local.example','company-interface',1,$4)`, grant, installation[:], company, token[:])
	// The current protocol requires a real private central source-session anchor.
	sourceHash := sha256.Sum256([]byte("foundation source " + actor))
	if err := NewAuthRepository(db).CreateSession(context.Background(), sourceHash[:], actor, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var source string
	if err := db.QueryRow(`SELECT session_id::text FROM user_sessions WHERE token_hash=$1`, sourceHash[:]).Scan(&source); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO company_interface_sessions(id,grant_id,organization_id,actor_id,local_session_hash,grant_epoch,access_hash,access_expires_at,refresh_hash,refresh_expires_at,source_session_id) VALUES($1,$2,$3,$4,$5,1,$6,NOW()+INTERVAL '1 hour',$7,NOW()+INTERVAL '7 days',$8)`, session, grant, company, actor, c.LocalSessionHash[:], access[:], refresh[:], source)
	g, err := NewCompanyInterfaceAuthority(db).Installation(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return g, c, actor
}

func TestCompanyInterfaceAuthorityRechecksPersistedMembershipAndEpoch(t *testing.T) {
	db := auditDatabase(t, true)
	grant, c, actor := companyInterfaceSeed(t, db)
	gate := ci.Gate{Authority: NewCompanyInterfaceAuthority(db), CentralOrigin: "https://central.example"}
	ctx := context.Background()
	now := time.Now()
	p, err := gate.AuthenticateBrowser(ctx, c, now)
	if err != nil || p.Actor != actor || p.Role != "member" || p.Grant.ExpiresAt != nil {
		t.Fatal(p, err)
	}
	for _, days := range []int{31, 366} {
		if grant.Validate(now.AddDate(0, 0, days)) != nil {
			t.Fatal("permanent grant expired")
		}
	}
	if _, err = gate.AuthenticateBrowser(ctx, c, now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired user access accepted")
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE organization_members SET role='admin' WHERE organization_id=$1 AND user_id=$2`, grant.Company, actor)
	p, err = gate.AuthenticateBrowser(ctx, c, now)
	if err != nil || p.Role != "admin" {
		t.Fatal("role not live", p, err)
	}
	exec(`UPDATE organization_members SET removed_at=NOW() WHERE organization_id=$1 AND user_id=$2`, grant.Company, actor)
	if _, err = gate.AuthenticateBrowser(ctx, c, now); err == nil {
		t.Fatal("removed member admitted")
	}
	exec(`UPDATE organization_members SET removed_at=NULL WHERE organization_id=$1 AND user_id=$2`, grant.Company, actor)
	exec(`UPDATE company_interface_grants SET revocation_epoch=revocation_epoch+1 WHERE id=$1`, grant.ID)
	if _, err = gate.AuthenticateBrowser(ctx, c, now); err == nil {
		t.Fatal("stale session epoch admitted")
	}
	// A fresh repository instance sees durable revocation, not an in-memory cache.
	exec(`UPDATE company_interface_grants SET revoked_at=NOW(),revocation_epoch=revocation_epoch+1 WHERE id=$1`, grant.ID)
	gate.Authority = NewCompanyInterfaceAuthority(db)
	if _, err = gate.AuthenticateBrowser(ctx, c, now); err == nil {
		t.Fatal("revoked grant admitted after reopen")
	}
}

func TestCompanyInterfaceSchemaRejectsRebindingAndResurrection(t *testing.T) {
	db := auditDatabase(t, true)
	g, c, _ := companyInterfaceSeed(t, db)
	access := sha256.Sum256([]byte(c.Session))
	s, err := NewCompanyInterfaceAuthority(db).Session(context.Background(), access)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE company_interface_installations SET central_origin='https://attacker.example'`,
		`UPDATE company_interface_installations SET organization_id='00000000-0000-0000-0000-000000000001'`,
		`UPDATE company_interface_grants SET approved_local_origin='https://attacker.example'`,
		`UPDATE company_interface_grants SET central_origin='https://attacker.example'`,
		`UPDATE company_interface_grants SET organization_id='00000000-0000-0000-0000-000000000001'`,
		`UPDATE company_interface_grants SET purpose='generation'`,
		`UPDATE company_interface_grants SET protocol_version=2`,
		`UPDATE company_interface_grants SET expires_at=NOW()+INTERVAL '1 day'`,
		`UPDATE company_interface_grants SET revoked_at=NOW()`,
		`UPDATE company_interface_sessions SET actor_id='00000000-0000-0000-0000-000000000001'`,
		`UPDATE company_interface_sessions SET local_session_hash=decode(repeat('01',32),'hex')`,
		`UPDATE company_interface_sessions SET grant_epoch=2`,
		`UPDATE company_interface_sessions SET revoked_at=NOW()`,
	} {
		if _, err := db.Exec(q); err == nil {
			t.Fatal("unsafe write accepted", q)
		}
	}
	if _, err := db.Exec(`UPDATE company_interface_sessions SET revoked_at=NOW(),session_epoch=session_epoch+1 WHERE id=$1`, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE company_interface_sessions SET revoked_at=NULL WHERE id=$1`, s.ID); err == nil {
		t.Fatal("session resurrected")
	}
	if _, err := db.Exec(`UPDATE company_interface_grants SET revoked_at=NOW(),revocation_epoch=revocation_epoch+1 WHERE id=$1`, g.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE company_interface_grants SET revoked_at=NULL WHERE id=$1`, g.ID); err == nil {
		t.Fatal("grant resurrected")
	}
	if _, err := db.Exec(`UPDATE company_interface_grants SET revocation_epoch=1 WHERE id=$1`, g.ID); err == nil {
		t.Fatal("grant epoch decreased")
	}
}

func TestCompanyInterfaceMigrationPreservesLegacyAndRevocationAcrossRollback(t *testing.T) {
	db := auditDatabase(t, false)
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 87); err != nil {
		t.Fatal(err)
	}
	actor, company, key := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,email,display_name) VALUES($1,'legacy-ci@fixture.test','Legacy fixture')`, actor)
	exec(`INSERT INTO organizations(id,name) VALUES($1,'Existing legacy company')`, company)
	exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, company, actor)
	hash := sha256.Sum256([]byte("old generation only credential"))
	exec(`INSERT INTO user_api_keys(id,user_id,created_by,organization_id,name,prefix,token_hash,scopes,resource_mode,storage_mode,expires_at) VALUES($1,$2,$2,$3,'Old generation-only grant','fixture',$4,'["generation"]','shared','shared',NOW()+INTERVAL '30 days')`, key, actor, company, hash[:])
	exec(`INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,external_id) VALUES($1,$2,$3,1234567,'promo','original')`, uuid.NewString(), company, actor)
	exec(`INSERT INTO remote_workspaces(owner_id,version,active,origin,organization_id,remote_key_id) VALUES($1,3,TRUE,'https://central.example',$2,$3)`, actor, company, key)
	preserved := newResourceLegacyProjection(t, db, "users", "organizations", "organization_members", "organization_credits", "user_api_keys", "remote_workspaces", "workspace_installations", "workspace_authorizations", "system_settings")
	before := preserved.snapshot(t, db, "before-company-interface")
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if before != preserved.snapshot(t, db, "after-company-interface") {
		t.Fatal("migration modified legacy data")
	}
	var count int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM company_interface_grants)+(SELECT count(*) FROM company_interface_sessions)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration auto-created authority", count, err)
	}
	g, _, _ := companyInterfaceSeed(t, db)
	exec(`UPDATE company_interface_grants SET revoked_at=NOW(),revocation_epoch=2 WHERE id=$1`, g.ID)
	newRecords := newResourceLegacyProjection(t, db, "company_interface_installations", "company_interface_grants", "company_interface_sessions")
	retained := newRecords.snapshot(t, db, "before-rollback")
	if err := goose.DownTo(db, "migrations", 87); err != nil {
		t.Fatal(err)
	}
	if retained != newRecords.snapshot(t, db, "after-rollback") {
		t.Fatal("rollback lost identity/revocation")
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	if retained != newRecords.snapshot(t, db, "after-reupgrade") {
		t.Fatal("reupgrade changed identity/revocation")
	}
}

func TestCompanyInterfaceFreshGrantRetainsRevokedHistoryAndCompany(t *testing.T) {
	db := auditDatabase(t, true)
	old, c, _ := companyInterfaceSeed(t, db)
	nextID := uuid.NewString()
	nextSecret := strings.Repeat("n", 32)
	nextHash := sha256.Sum256([]byte(nextSecret))
	insert := `INSERT INTO company_interface_grants(id,installation_hash,organization_id,central_origin,approved_local_origin,purpose,protocol_version,credential_hash) SELECT $2,installation_hash,organization_id,central_origin,approved_local_origin,purpose,protocol_version,$3 FROM company_interface_grants WHERE id=$1`
	if _, err := db.Exec(insert, old.ID, nextID, nextHash[:]); err == nil {
		t.Fatal("second active grant accepted")
	}
	if _, err := db.Exec(`UPDATE company_interface_grants SET revoked_at=NOW(),revocation_epoch=2 WHERE id=$1`, old.ID); err != nil {
		t.Fatal(err)
	}
	// Represents NEW explicit approval, not revival of the previous credential.
	// Production approval/issuance is a later transaction, not implemented here.
	if _, err := db.Exec(insert, old.ID, nextID, nextHash[:]); err != nil {
		t.Fatal("cannot recover original company with fresh grant", err)
	}
	authority := NewCompanyInterfaceAuthority(db)
	next, err := authority.Installation(context.Background(), nextHash)
	if err != nil || next.ID == old.ID || next.Company != old.Company || next.InstallationHash != old.InstallationHash || next.Validate(time.Now()) != nil {
		t.Fatal(next, err)
	}
	oldHash := sha256.Sum256([]byte(c.Installation))
	retained, err := authority.Installation(context.Background(), oldHash)
	if err != nil || !retained.Revoked || retained.Epoch != 2 {
		t.Fatal("old revocation lost", retained, err)
	}
	c.Installation = nextSecret
	if _, err := (ci.Gate{Authority: authority, CentralOrigin: old.CentralOrigin}).AuthenticateBrowser(context.Background(), c, time.Now()); err == nil {
		t.Fatal("old session adopted new grant")
	}
	foreign := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Foreign company')`, foreign); err != nil {
		t.Fatal(err)
	}
	// Free the active-grant slot so this negative cannot pass merely because of
	// the one-active-grant constraint: require the immutable binding FK to reject.
	if _, err := db.Exec(`UPDATE company_interface_grants SET revoked_at=NOW(),revocation_epoch=2 WHERE id=$1`, next.ID); err != nil {
		t.Fatal(err)
	}
	foreignHash := sha256.Sum256([]byte("foreign credential"))
	if _, err := db.Exec(`INSERT INTO company_interface_grants(id,installation_hash,organization_id,central_origin,approved_local_origin,purpose,protocol_version,credential_hash) VALUES($1,$2,$3,$4,$5,'company-interface',1,$6)`, uuid.NewString(), old.InstallationHash[:], foreign, old.CentralOrigin, old.LocalOrigin, foreignHash[:]); err == nil {
		t.Fatal("installation rebound to foreign company")
	} else {
		var pgerr *pgconn.PgError
		if !errors.As(err, &pgerr) || pgerr.Code != "23503" {
			t.Fatal("foreign binding failed for unrelated reason", err)
		}
	}
}

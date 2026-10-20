package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/audit"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func auditDatabase(t *testing.T, migrate bool) *sql.DB {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	base, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := "audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close(); base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); base.Close() })
	if migrate {
		if err = Migrate(db); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func allAudit() audit.Filter {
	return audit.Filter{From: time.Unix(0, 0), Until: time.Now().Add(time.Hour), Limit: 100}
}
func TestAuditAtomicChangesRedactionAndNoOps(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	people := NewAuthRepository(db)
	admin, err := people.UpsertGoogleUser(ctx, uuid.NewString(), "audit-admin@example.test", "Before name", "")
	if err != nil {
		t.Fatal(err)
	}
	sys := NewSystemRepository(db)
	if err = sys.SetAdmin(ctx, "", admin.ID, true, true); err != nil {
		t.Fatal(err)
	}
	ctx = audit.WithCorrelation(identity.WithResourceOwners(identity.WithUser(ctx, admin), identity.SharedOwnerID, identity.SharedOwnerID), "audit-correlation")
	providers := NewProviderSettingsRepository(db)
	rec := providerconfig.Record{ProviderID: "mock", Name: "Test connection", Enabled: true, CredentialSource: "console", CredentialsCiphertext: []byte("never-log-this-cipher")}
	if err = providers.Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if err = NewStorageSettingsRepository(db).Save(ctx, storage.SettingsRecord{Driver: "local"}); err != nil {
		t.Fatal(err)
	}
	name := "After name"
	if _, err = people.UpdateProfile(ctx, admin.ID, auth.ProfileUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	roleFilter := allAudit()
	roleFilter.Action = "administrator_role"
	roleFilter.ObjectID = admin.ID
	roles, err := sys.AuditEvents(ctx, roleFilter)
	if err != nil || len(roles) != 1 || roles[0].ObjectName != "Before name" {
		t.Fatalf("role target label rewritten after profile rename: %+v %v", roles, err)
	}
	f := allAudit()
	f.Action = "profile"
	before, err := sys.AuditEvents(ctx, f)
	if err != nil || len(before) != 1 {
		t.Fatalf("profile diff: %+v %v", before, err)
	}
	if before[0].ActorName != "After name" || before[0].CorrelationID != "audit-correlation" || !strings.Contains(string(before[0].Details), "Before name") {
		t.Fatalf("snapshot/diff missing: %+v", before[0])
	}
	if _, err = people.UpdateProfile(ctx, admin.ID, auth.ProfileUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	after, _ := sys.AuditEvents(ctx, f)
	if len(after) != len(before) {
		t.Fatal("no-op produced audit")
	}
	f = allAudit()
	f.Action = "provider_settings"
	events, err := sys.AuditEvents(ctx, f)
	if err != nil || len(events) != 1 {
		t.Fatalf("provider event: %+v %v", events, err)
	}
	if !strings.Contains(string(events[0].Details), "credential_change") || strings.Contains(string(events[0].Details), "cipher") {
		t.Fatal("credential change missing or leaked", string(events[0].Details))
	}
	// If history cannot be written, the configuration update must roll back too.
	if _, err = db.Exec(`CREATE FUNCTION reject_test_audit() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='provider_settings' THEN RAISE EXCEPTION 'test audit failure';END IF;RETURN NEW;END $$;CREATE TRIGGER reject_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_test_audit()`); err != nil {
		t.Fatal(err)
	}
	rec.Name = "Must roll back"
	if err = providers.Upsert(ctx, rec); err == nil {
		t.Fatal("save survived failed audit")
	}
	stored, _ := providers.Get(ctx, rec.ProviderID)
	if stored.Name != "Test connection" {
		t.Fatal("configuration committed without audit")
	}
	if _, err = db.Exec(`DROP TRIGGER reject_audit ON audit_events`); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{`UPDATE audit_events SET action='tampered'`, `DELETE FROM audit_events`, `TRUNCATE audit_events`} {
		if _, err = db.Exec(statement); err == nil {
			t.Fatal("history mutable", statement)
		}
	}
	tx, err := beginAuditTx(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if err = appendAudit(ctx, tx, "", admin.ID, "test", "system", "test", map[string]any{"api_key": "sensitive", "nested": map[string]any{"prompt": "sensitive", "safe": true}, "headers": map[string]any{"Authorization": "sensitive"}}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	all, _ := sys.AuditEvents(ctx, allAudit())
	encoded, _ := json.Marshal(all)
	for _, secret := range []string{"never-log-this-cipher", "sensitive"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("audit leaked secret", secret)
		}
	}
}
func TestAuditRevocationSnapshotsCascadesAndLateCosts(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	people := NewAuthRepository(db)
	admin, _ := people.UpsertGoogleUser(ctx, uuid.NewString(), "admin@example.test", "Administrator", "")
	member, _ := people.UpsertGoogleUser(ctx, uuid.NewString(), "member@example.test", "Member", "")
	sys := NewSystemRepository(db)
	if err := sys.SetAdmin(ctx, "", admin.ID, true, true); err != nil {
		t.Fatal(err)
	}
	org, err := sys.CreateOrganization(ctx, admin.ID, "Original company", admin.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	if err = sys.SetOrganizationMember(ctx, admin.ID, org.ID, member.ID, "member", nil, false, false); err != nil {
		t.Fatal(err)
	}
	keys := NewAPIKeyRepository(db)
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Original alias", Scopes: []string{apikey.Generation}}, time.Now())
	hash := apikey.Hash(secret)
	if err != nil {
		t.Fatal(err)
	}
	key.UserID = member.ID
	key.OrganizationID = org.ID
	key.ResourceMode = "shared"
	key.StorageMode = "shared"
	if err = keys.Create(ctx, admin.ID, key, hash); err != nil {
		t.Fatal(err)
	}
	first, second := uuid.NewString(), uuid.NewString()
	if _, err = db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,api_key_id,status,provider,model,prompt,provider_owner_id,storage_owner_id,cost) VALUES($1,$3,$4,$5,'succeeded','mock','mock-image-v1','private prompt',$4,$3,'{"metered_usd":0.012345,"markup_percent":20}'),($2,$3,$4,$5,'running','mock','mock-image-v1','private prompt',$4,$3,NULL)`, first, second, org.ID, member.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if err = sys.SetOrganizationMember(ctx, admin.ID, org.ID, member.ID, "member", nil, false, true); err != nil {
		t.Fatal(err)
	}
	revoked, err := sys.RevokedKeys(ctx, allAudit())
	if err != nil || len(revoked) != 1 {
		t.Fatalf("cascade omitted: %+v %v", revoked, err)
	}
	snapshot := string(revoked[0].Snapshot)
	var fields map[string]any
	json.Unmarshal(revoked[0].Snapshot, &fields)
	if fields["spent_micros"] != float64(14814) || fields["pending_requests"] != float64(1) || fields["revoked_by"] != admin.ID || fields["name"] != "Original alias" || fields["revocation_reason"] != "membership_removed" || fields["correlation_id"] == "" {
		t.Fatal("incorrect snapshot", snapshot)
	}
	// Direct repeat is harmless even after membership removal (Administrator can revoke).
	if err = keys.RevokeForActor(ctx, admin.ID, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE tasks SET status='succeeded',cost='{"metered_usd":0.02}' WHERE id=$1;`, second); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE organizations SET name='Renamed company' WHERE id=$1`, org.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE user_api_keys SET name='Renamed alias' WHERE id=$1`, key.ID); err != nil {
		t.Fatal(err)
	}
	revoked, err = sys.RevokedKeys(ctx, allAudit())
	if err != nil || string(revoked[0].Snapshot) != snapshot || revoked[0].CurrentSpentMicros != 34814 || revoked[0].PendingRequests != 0 {
		t.Fatal("revocation history changed", revoked, err)
	}
	f := allAudit()
	f.Action = "key_revoked"
	events, _ := sys.AuditEvents(ctx, f)
	if len(events) != 1 {
		t.Fatal("repeat revocation audited twice")
	}
	if _, err = db.Exec(`DELETE FROM revoked_key_history`); err == nil {
		t.Fatal("revocation snapshot mutable")
	}
	if _, _, err = keys.Authenticate(ctx, hash); err == nil {
		t.Fatal("revoked key authenticated")
	}
}
func TestAuditAcceptedIdentitySurvivesRenamesAndReplay(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	people := NewAuthRepository(db)
	admin, _ := people.UpsertGoogleUser(ctx, uuid.NewString(), "old@example.test", "Original person", "")
	sys := NewSystemRepository(db)
	if err := sys.SetAdmin(ctx, "", admin.ID, true, true); err != nil {
		t.Fatal(err)
	}
	org, err := sys.CreateOrganization(ctx, admin.ID, "Original company", admin.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	ctx = identity.WithOrganization(identity.WithUser(ctx, admin), org.ID, true)
	repo := NewTaskRepository(db)
	now := time.Now().UTC()
	task := core.Task{ID: uuid.NewString(), OrganizationID: org.ID, ActorID: admin.ID, Provider: "mock", Model: "mock-image-v1", MediaType: "image", Prompt: "private prompt", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now}
	accepted, created, err := repo.CreateOrGet(ctx, task, "identity-test", "same-hash")
	if err != nil || !created {
		t.Fatal("acceptance", err)
	}
	if _, err = db.Exec(`UPDATE users SET display_name='Renamed person',email='new@example.test' WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE organizations SET name='Renamed company' WHERE id=$1`, org.ID); err != nil {
		t.Fatal(err)
	}
	replay, created, err := repo.CreateOrGet(ctx, task, "identity-test", "same-hash")
	if err != nil || created || string(replay.IdentitySnapshot) != string(accepted.IdentitySnapshot) {
		t.Fatal("replay changed identities", err)
	}
	rows, err := repo.CostReport(ctx, core.ReportFilter{From: now.Add(-time.Hour), Until: now.Add(time.Hour), UserEmail: "old@example.test", Limit: 10})
	if err != nil || len(rows) != 1 || rows[0].UserEmail != "old@example.test" || rows[0].OrganizationName != "Original company" || !rows[0].IdentityHistorical {
		t.Fatalf("historical report: %+v %v", rows, err)
	}
	if _, err = db.Exec(`UPDATE tasks SET identity_snapshot='{}' WHERE id=$1`, task.ID); err == nil {
		t.Fatal("accepted identity mutable")
	}
	all, _ := sys.AuditEvents(ctx, allAudit())
	b, _ := json.Marshal(all)
	if strings.Contains(string(b), "private prompt") {
		t.Fatal("audit retained request content")
	}
}
func TestAuditMigrationPreservesLegacyAndUnknownFacts(t *testing.T) {
	db := auditDatabase(t, false)
	ctx := context.Background()
	goose.SetBaseFS(migrations)
	goose.SetDialect("postgres")
	if err := goose.UpTo(db, "migrations", 50); err != nil {
		t.Fatal(err)
	}
	// Seed the old schema directly: the current repository reads columns added
	// after version 50, which this migration test intentionally has not applied.
	personID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO users(id,google_subject,email,display_name) VALUES($1,$2,'legacy@example.test','Today name')`, personID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	orgID := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Today company')`, orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO system_audit_log(actor,action,target,details,created_at) VALUES('Server command','settings','installation','{"before":false,"after":true,"secret":"must not migrate"}','2026-01-02T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO organization_events(organization_id,actor_id,action,details,created_at)VALUES($1,$2,'member','{"role":"member"}','2026-01-01T12:00:00Z')`, orgID, personID); err != nil {
		t.Fatal(err)
	}
	legacyKey := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO user_api_keys(id,user_id,created_by,name,prefix,token_hash,scopes,created_at,revoked_at) VALUES($1,$2,$2,'Old revoked key','old-prefix',$3,'["generation"]','2026-01-01T12:00:00Z','2026-02-01T12:00:00Z')`, legacyKey, personID, []byte("old-test-hash")); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	events, err := NewSystemRepository(db).AuditEvents(ctx, allAudit())
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	if events[0].LegacySource != "system_audit_log" || !events[0].At.After(events[1].At) {
		t.Fatal("imported events lost chronological order", events)
	}
	for _, e := range events {
		if e.LegacyID == nil || e.CorrelationID != "" || e.OrganizationName != "" || strings.Contains(string(e.Details), "must not migrate") {
			t.Fatal("invented/leaked legacy data", e)
		}
	}
	keys, err := NewSystemRepository(db).RevokedKeys(ctx, allAudit())
	if err != nil || len(keys) != 1 || keys[0].ID != legacyKey {
		t.Fatal("legacy revoked key lost", keys, err)
	}
	var snapshot map[string]any
	if err = json.Unmarshal(keys[0].Snapshot, &snapshot); err != nil || snapshot["history_complete"] != false || snapshot["spent_micros"] != nil || snapshot["revoked_by"] != nil {
		t.Fatal("invented revocation facts", snapshot, err)
	}
	for _, table := range []string{"system_audit_log", "organization_events"} {
		var exists bool
		if err := db.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil || exists {
			t.Fatal("legacy journal still present", table, err)
		}
	}
}

package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	"github.com/pressly/goose/v3"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

func TestDedicatedCompanyForEachExternalInstallation(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	customer, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "External customer", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`); err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("i", 32))
	manager := wa.New(repo, cipher)
	companies := map[string]bool{}
	for range 2 {
		name := "Identical installation display name"
		device, _ := wa.DeviceCode()
		proof, _ := wa.DeviceCode()
		request, err := manager.Start(ctx, device, wa.Challenge{InstallationProof: proof, Label: name, Origin: "http://same-untrusted-display.lan"})
		if err != nil {
			t.Fatal(err)
		}
		approved, err := repo.Approve(ctx, customer.ID, request.Code, wa.Approval{Consent: true})
		if err != nil {
			t.Fatalf("distinct installation must create its own company: %v", err)
		}
		if approved.Company == "" || companies[approved.Company] {
			t.Fatal("installations shared a company")
		}
		companies[approved.Company] = true
		var role string
		if err = db.QueryRow(`SELECT role FROM organization_members WHERE organization_id=$1 AND user_id=$2`, approved.Company, customer.ID).Scan(&role); err != nil || role != "supermanager" {
			t.Fatal("initiating customer not supermanager", err)
		}
	}
	var credits int
	if err = db.QueryRow(`SELECT count(*) FROM organization_credits`).Scan(&credits); err != nil || credits != 0 {
		t.Fatal("provisioning funded a company", err)
	}
}

func dedicatedFixture(t *testing.T) (*sql.DB, *SystemRepository, *wa.Manager, identity.User) {
	t.Helper()
	db := auditDatabase(t, true)
	ctx := context.Background()
	repo := NewSystemRepository(db)
	customer, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Customer", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`); err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("x", 32))
	return db, repo, wa.New(repo, cipher), customer
}
func dedicatedStart(t *testing.T, m *wa.Manager, proof, replacement string) (string, wa.Challenge) {
	t.Helper()
	device, _ := wa.DeviceCode()
	v, err := m.Start(context.Background(), device, wa.Challenge{InstallationProof: proof, Label: "Same display label", Origin: "http://same-display.lan", ReplaceKey: replacement})
	if err != nil {
		t.Fatal(err)
	}
	return device, v
}
func dedicatedIssue(t *testing.T, db *sql.DB, repo *SystemRepository, m *wa.Manager, actor, device string, v wa.Challenge) wa.Challenge {
	t.Helper()
	ctx := context.Background()
	approved, err := repo.Approve(ctx, actor, v.Code, wa.Approval{Consent: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE workspace_authorizations SET next_poll_at=NOW()-INTERVAL '1 second' WHERE device_hash=$1`, wa.Digest(device)); err != nil {
		t.Fatal(err)
	}
	issued, _, err := m.Poll(ctx, device)
	if err != nil || issued.Company != approved.Company {
		t.Fatal("issuance changed company", err)
	}
	return issued
}
func TestDedicatedInstallationRetryCancellationExpiryAndSubstitution(t *testing.T) {
	db, repo, m, customer := dedicatedFixture(t)
	ctx := context.Background()
	proof, _ := wa.DeviceCode()
	device, v := dedicatedStart(t, m, proof, "")
	issued := dedicatedIssue(t, db, repo, m, customer.ID, device, v)
	if err := repo.Cancel(ctx, wa.Digest(device)); err != nil {
		t.Fatal(err)
	}
	// Cancellation/expiry can discard a device request, never the installation binding.
	expired, request := dedicatedStart(t, m, proof, "")
	if _, err := repo.Approve(ctx, customer.ID, request.Code, wa.Approval{Consent: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE workspace_authorizations SET expires_at=NOW()-INTERVAL '1 second' WHERE device_hash=$1`, wa.Digest(expired)); err != nil {
		t.Fatal(err)
	}
	device, v = dedicatedStart(t, m, proof, "")
	described, companies, create, err := repo.Describe(ctx, customer.ID, v.Code)
	if err != nil || create || len(companies) != 0 || described.Provisioning != "renewal" || described.Company != issued.Company {
		t.Fatal("reconnect lost original company", err)
	}
	// A new process/key lifetime uses the same proof, not the transient request ID.
	resumed := wa.New(repo, m.Cipher)
	renewed := dedicatedIssue(t, db, repo, resumed, customer.ID, device, v)
	if renewed.Company != issued.Company {
		t.Fatal("renewal created a new company")
	}
	other, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Another customer", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, other.ID); err != nil {
		t.Fatal(err)
	}
	_, foreign := dedicatedStart(t, m, proof, "")
	if _, _, _, err = repo.Describe(ctx, other.ID, foreign.Code); !errors.Is(err, wa.ErrForbidden) {
		t.Fatal("system visibility replaced initiating customer", err)
	}
	if _, err = repo.Approve(ctx, other.ID, foreign.Code, wa.Approval{Consent: true}); !errors.Is(err, wa.ErrForbidden) {
		t.Fatal("cross-user proof replay", err)
	}
	// Owning two companies does not allow swapping their installation credentials.
	proof2, _ := wa.DeviceCode()
	device2, v2 := dedicatedStart(t, m, proof2, "")
	second := dedicatedIssue(t, db, repo, m, customer.ID, device2, v2)
	_, replacement := dedicatedStart(t, m, proof, second.KeyID)
	if _, err = repo.Approve(ctx, customer.ID, replacement.Code, wa.Approval{Consent: true, Replace: true}); !errors.Is(err, wa.ErrForbidden) {
		t.Fatal("same-customer cross-installation key replacement", err)
	}
	if _, err = repo.Approve(ctx, customer.ID, replacement.Code, wa.Approval{Consent: true, Company: second.Company}); !errors.Is(err, wa.ErrForbidden) {
		t.Fatal("explicit company substitution", err)
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM workspace_installations`).Scan(&n); err != nil || n != 2 {
		t.Fatal("retry duplicated company binding", n, err)
	}
	public, _ := json.Marshal(renewed)
	if bytes.Contains(public, []byte(proof)) || bytes.Contains(public, renewed.InstallationHash) {
		t.Fatal("proof leaked into browser projection")
	}
}
func TestDedicatedProvisioningRollbackBeforeBinding(t *testing.T) {
	db, repo, m, customer := dedicatedFixture(t)
	ctx := context.Background()
	proof, _ := wa.DeviceCode()
	_, request := dedicatedStart(t, m, proof, "")
	// Fail after company/member inserts. The transaction must leave no partial tenant.
	if _, err := db.Exec(`CREATE FUNCTION fail_installation_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic binding failure'; END $$; CREATE TRIGGER fail_installation_fixture BEFORE INSERT ON workspace_installations FOR EACH ROW EXECUTE FUNCTION fail_installation_fixture()`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Approve(ctx, customer.ID, request.Code, wa.Approval{Consent: true}); err == nil {
		t.Fatal("injected binding failure was ignored")
	}
	for _, table := range []string{"organizations", "organization_members", "workspace_installations", "organization_credits", "user_api_keys"} {
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatal("partial provisioning survived", table, n, err)
		}
	}
	if _, err := db.Exec(`DROP TRIGGER fail_installation_fixture ON workspace_installations`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Approve(ctx, customer.ID, request.Code, wa.Approval{Consent: true}); err != nil {
		t.Fatal("same intent did not recover after failure", err)
	}
}
func TestDedicatedCompanyBudgetAndHistoryIsolation(t *testing.T) {
	db, repo, m, customer := dedicatedFixture(t)
	ctx := context.Background()
	grants := []wa.Challenge{}
	for range 2 {
		proof, _ := wa.DeviceCode()
		device, v := dedicatedStart(t, m, proof, "")
		grants = append(grants, dedicatedIssue(t, db, repo, m, customer.ID, device, v))
	}
	for _, g := range grants {
		var zero int64
		if err := db.QueryRow(`SELECT COALESCE(SUM(amount_micros),0) FROM organization_credits WHERE organization_id=$1`, g.Company).Scan(&zero); err != nil || zero != 0 {
			t.Fatal("nonzero initial credit", err)
		}
	}
	// Synthetic fixture funding only; no payment service or media provider is used.
	if _, err := db.Exec(`UPDATE organization_members SET weekly_limit_micros=NULL WHERE user_id=$1`, customer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,external_id) VALUES($1,$2,$3,10000000,'promo','synthetic-budget')`, uuid.NewString(), grants[0].Company, customer.ID); err != nil {
		t.Fatal(err)
	}
	task := uuid.NewString()
	if _, err := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,api_key_id,status,provider,model,prompt,provider_owner_id,storage_owner_id,cost) VALUES($1,$2,$3,$4,'succeeded','mock','mock-image-v1','Isolated fixture',$5,$5,'{"expected_usd":1,"actual_usd":1,"metered_usd":1,"status":"confirmed"}')`, task, grants[0].Company, customer.ID, grants[0].KeyID, identity.SharedOwnerID); err != nil {
		t.Fatal(err)
	}
	for i, g := range grants {
		_, remaining, err := readOrganizationBudget(ctx, db, g.Company, g.KeyID, time.Now(), "", false)
		want := money.USD(0)
		if i == 0 {
			want = 9000000
		}
		if err != nil || remaining == nil || *remaining != want {
			t.Fatal("company balance mixed", i, remaining, err)
		}
	}
	if _, _, err := readOrganizationBudget(ctx, db, grants[0].Company, grants[1].KeyID, time.Now(), "", false); !errors.Is(err, core.ErrKeyUnavailable) {
		t.Fatal("foreign company key read budget", err)
	}
	tasks := NewTaskRepository(db)
	own := identity.WithOrganization(identity.WithUser(ctx, customer), grants[0].Company, true)
	foreign := identity.WithOrganization(identity.WithUser(ctx, customer), grants[1].Company, true)
	if _, err := tasks.Get(own, task); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Get(foreign, task); !errors.Is(err, core.ErrTaskNotFound) {
		t.Fatal("history crossed installation company", err)
	}
}
func TestInstallationIdentitySurvivesDisconnectAndLocalAdministratorChange(t *testing.T) {
	db, repo, _, customer := dedicatedFixture(t)
	ctx := context.Background()
	other, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", "Second local administrator", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id IN($1,$2)`, customer.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	first, err := repo.InstallationIdentity(ctx, customer.ID, "https://central.fixture.test", []byte("synthetic-encrypted-proof"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.InstallationIdentity(ctx, other.ID, "https://central.fixture.test", []byte("different-candidate"))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("same physical installation changed identity", err)
	}
	recovered, err := repo.InstallationIdentity(ctx, customer.ID, "https://central.fixture.test", nil)
	if err != nil || !bytes.Equal(first, recovered) {
		t.Fatal("proof did not persist", err)
	}
	another, err := repo.InstallationIdentity(ctx, customer.ID, "https://another.fixture.test", []byte("different-central-proof"))
	if err != nil || bytes.Equal(first, another) {
		t.Fatal("central origins shared a proof", err)
	}
}
func TestDedicatedMigrationPreservesLegacyCompanyAndGrant(t *testing.T) {
	db := auditDatabase(t, false)
	ctx := context.Background()
	goose.SetBaseFS(migrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, "migrations", 81); err != nil {
		t.Fatal(err)
	}
	user, company, key, task := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	device, _ := wa.DeviceCode()
	cipher, _ := providerconfig.NewCipher(strings.Repeat("g", 32))
	encrypted, _ := cipher.Encrypt([]byte("synthetic-legacy-token"))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,email,display_name,is_system_admin) VALUES($1,'legacy@fixture.test','Legacy customer',TRUE)`, user)
	exec(`INSERT INTO organizations(id,name) VALUES($1,'Existing company')`, company)
	exec(`INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'supermanager')`, company, user)
	exec(`INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,external_id) VALUES($1,$2,$3,1234567,'promo','original')`, uuid.NewString(), company, user)
	exec(`INSERT INTO user_api_keys(id,user_id,created_by,organization_id,name,prefix,token_hash,scopes,resource_mode,storage_mode,expires_at) VALUES($1,$2,$2,$3,'Legacy installation','fixture',$4,'["generation"]','shared','shared',NOW()+INTERVAL '1 day')`, key, user, company, apikey.Hash("synthetic-legacy-token"))
	exec(`INSERT INTO workspace_authorizations(device_hash,user_code,client_name,client_origin,status,expires_at,actor_id,organization_id,key_id,token_ciphertext) VALUES($1,$2,'Legacy installation','http://legacy.lan','issued',NOW()+INTERVAL '5 minutes',$3,$4,$5,$6)`, wa.Digest(device), wa.UserCode(device), user, company, key, encrypted)
	exec(`INSERT INTO customer_workspace_owners(user_id,organization_id) VALUES($1,$2)`, user, company)
	exec(`INSERT INTO remote_workspaces(owner_id,version,active,origin,organization_id,organization_name,remote_user_id,remote_key_id,token_hash,token_ciphertext) VALUES($1::uuid,1,TRUE,'https://central.fixture.test',$2,'Existing company',$1::text,$3,$4,$5)`, user, company, key, apikey.Hash("synthetic-legacy-token"), encrypted)
	exec(`INSERT INTO tasks(id,owner_id,actor_id,api_key_id,status,provider,model,prompt,provider_owner_id,storage_owner_id,storage_key,cost) VALUES($1,$2,$3,$4,'succeeded','mock','mock-image-v1','Unmoved legacy task',$5,$5,'legacy/result.png','{"expected_usd":0.1,"actual_usd":0.1}')`, task, company, user, key, identity.SharedOwnerID)
	preserved := newResourceLegacyProjection(t, db, "users", "organizations", "organization_members", "organization_credits", "user_api_keys", "workspace_authorizations", "customer_workspace_owners", "remote_workspaces", "tasks")
	before := preserved.snapshot(t, db, "dedicated81-before-upgrade")
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	assertResourceBindingsEmptyDefault(t, db)
	if preserved.snapshot(t, db, "dedicated84-after-upgrade") != before {
		t.Fatal("migration changed an old field, data count or ledger")
	}
	repo := NewSystemRepository(db)
	record, err := repo.RemoteWorkspace(ctx, user)
	if err != nil || record.Provisioning != "legacy" || record.OrganizationID != company {
		t.Fatal("legacy connection changed", err)
	}
	grants, err := repo.Grants(ctx, user)
	if err != nil || len(grants) != 1 || grants[0].Provisioning != "legacy" || grants[0].Company != company {
		t.Fatal("legacy management lost", err)
	}
	exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`)
	recovered, token, err := wa.New(repo, cipher).Poll(ctx, device)
	if err != nil || token != "synthetic-legacy-token" || len(recovered.InstallationHash) != 0 {
		t.Fatal("issued legacy credential no longer recoverable", err)
	}
	// An active legacy connection cannot silently start new-company provisioning.
	manager := remote.New(repo, cipher, &http.Client{})
	if _, err = manager.BeginAuthorization(ctx, user, "https://central.fixture.test", "Legacy installation", "http://legacy.lan", 1); !errors.Is(err, wa.ErrLegacy) {
		t.Fatal("legacy silently reprovisioned", err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM workspace_installations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("migration created a dedicated company", err)
	}
	// Exercise the populated combined 081→084→081→084 window. Additive Down
	// paths retain both Kling artifacts and installation proofs/billing snapshots.
	exec(`UPDATE system_settings SET customer_workspaces=TRUE,shared_resources=TRUE`)
	proof, _ := wa.DeviceCode()
	authority := wa.New(repo, cipher)
	freshDevice, request := dedicatedStart(t, authority, proof, "")
	dedicatedIssue(t, db, repo, authority, user, freshDevice, request)
	proofCipher, _ := cipher.Encrypt([]byte(proof))
	if _, err = repo.InstallationIdentity(ctx, user, "https://central.fixture.test", proofCipher); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE tasks SET result_artifacts='[{"index":0,"url":"https://fixture.invalid/result.png"}]',template_snapshot='{"prompt":"Unmoved legacy task","company":"original"}' WHERE id=$1`, task)
	resourceSeed := seedResourcePreservationMetadata(t, db, company, identity.SharedOwnerID)
	protected := func() string {
		t.Helper()
		var value string
		for _, table := range []string{"users", "organizations", "organization_members", "organization_credits", "user_api_keys", "workspace_authorizations", "customer_workspace_owners", "remote_workspaces", "workspace_installations", "remote_installation_identities", "tasks", "provider_resources", "provider_resource_jobs"} {
			var rows string
			if err := db.QueryRow(`SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]')::text FROM ` + table + ` t`).Scan(&rows); err != nil {
				t.Fatal(err)
			}
			value += rows
		}
		return value
	}
	for range 2 {
		prior := protected()
		if err = goose.DownTo(db, "migrations", 81); err != nil {
			t.Fatal(err)
		}
		if protected() != prior {
			t.Fatal("combined rollback lost accepted artifacts, proof bindings, legacy data or billing snapshots")
		}
		// Real old-column updates/inserts preserve populated resource metadata.
		assertResourceMetadataSurvivesOldWriter(t, db, resourceSeed)
		afterOldWrite := protected()
		if err = goose.UpTo(db, "migrations", 84); err != nil {
			t.Fatal(err)
		}
		if protected() != afterOldWrite {
			t.Fatal("reupgrade recreated or changed retained installation/accepted task data")
		}
	}
}

func TestLegacyOriginSpellingRenewalPreservesOperationScope(t *testing.T) {
	db, repo, _, customer := dedicatedFixture(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, customer.ID); err != nil {
		t.Fatal(err)
	}
	original := remote.Record{Owner: customer.ID, Active: true, Provisioning: "legacy", Origin: "https://CENTRAL.fixture.test:443", OrganizationID: uuid.NewString(), UserID: uuid.NewString(), KeyID: uuid.NewString(), TokenHash: []byte("old-fixture-hash"), Ciphertext: []byte("old-fixture-ciphertext")}
	first, err := repo.SaveRemoteWorkspace(ctx, original, 0)
	if err != nil {
		t.Fatal(err)
	}
	replacement := original
	replacement.Origin = "https://central.fixture.test"
	replacement.KeyID = uuid.NewString()
	replacement.TokenHash = []byte("new-fixture-hash")
	renewed, err := repo.SaveRemoteWorkspace(ctx, replacement, 1)
	if err != nil || renewed.Origin != first.Origin || renewed.OperationScopeID() != first.OperationScopeID() || renewed.Provisioning != "legacy" {
		t.Fatal("legacy spelling renewal changed operation scope", err)
	}
	repeated, err := repo.SaveRemoteWorkspace(ctx, replacement, 1)
	if err != nil || repeated.Version != renewed.Version {
		t.Fatal("legacy canonical retry changed saved result", err)
	}
}

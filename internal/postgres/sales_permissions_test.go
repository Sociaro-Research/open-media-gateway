package postgres

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/markup"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"testing"
	"time"
)

func TestDelegatedSalesPermissions(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r := NewSystemRepository(db)
	people := NewAuthRepository(db)
	users := map[string]string{}
	for _, name := range []string{"admin", "region", "other", "lead", "manager", "direct", "owner"} {
		u, e := people.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		users[name] = u.ID
	}
	admin := users["admin"]
	if e := r.SetAdmin(ctx, "", admin, true, true); e != nil {
		t.Fatal(e)
	}
	place := func(name, role, parent string) {
		t.Helper()
		s, e := r.SalesStructure(ctx, admin)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = r.PlaceSalesMember(ctx, admin, users[name], system.SalesPlacement{Role: role, SupervisorID: users[parent], Version: s.Version}); e != nil {
			t.Fatal(e)
		}
	}
	place("region", system.RegionalClientManager, "")
	place("other", system.RegionalClientManager, "")
	place("lead", system.LeadClientManager, "region")
	place("manager", system.ClientManager, "lead")
	place("direct", system.ClientManager, "region")
	company, e := r.CreateOrganization(ctx, admin, "Delegation company", users["owner"], uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.AssignClientManager(ctx, admin, company.ID, users["manager"]); e != nil {
		t.Fatal(e)
	}
	change := func(actor, target, p string, on bool) error {
		t.Helper()
		s, e := r.SalesPermissions(ctx, users[actor])
		if e != nil {
			return e
		}
		return r.SetSalesPermission(ctx, users[actor], users[target], p, system.SalesPermissionChange{Enabled: on, Version: s.Version})
	}
	allow := func(actor, target, p string) {
		t.Helper()
		if e := change(actor, target, p, true); e != nil {
			t.Fatalf("grant %s -> %s/%s: %v", actor, target, p, e)
		}
	}
	denied := func(e error) {
		t.Helper()
		if !errors.Is(e, system.ErrForbidden) {
			t.Fatalf("want forbidden, got %v", e)
		}
	}
	check := func(actor, p string, want bool) {
		t.Helper()
		e := r.CommercialPermission(ctx, users[actor], company.ID, p)
		if want && e != nil {
			t.Fatal(e)
		}
		if !want {
			denied(e)
		}
	}
	for _, p := range []string{"pricing", "models", "topup"} {
		check("manager", p, false)
	}
	denied(change("lead", "manager", "pricing", true))
	denied(change("region", "region", "pricing", true))
	allow("admin", "region", "pricing")
	allow("region", "lead", "pricing")
	allow("lead", "manager", "pricing")
	check("manager", "pricing", true)
	check("manager", "models", false)
	check("lead", "pricing", true)
	check("region", "pricing", true)
	check("other", "pricing", false)
	denied(change("other", "manager", "pricing", true))
	denied(change("manager", "lead", "pricing", false))
	policy, e := r.Markups(ctx, users["manager"], company.ID)
	if e != nil {
		t.Fatal(e)
	}
	rate := money.Percent(1250)
	policy.Company.Default = &rate
	if _, e = r.SaveMarkups(ctx, users["manager"], company.ID, policy.Company); e != nil {
		t.Fatal(e)
	}
	if _, e = r.SaveMarkups(ctx, users["manager"], "", markup.Config{Default: &rate, Version: 1}); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("global pricing", e)
	}
	// Revoke and regrant cannot resurrect the previous grant lineage.
	if e = change("admin", "region", "pricing", false); e != nil {
		t.Fatal(e)
	}
	check("lead", "pricing", false)
	check("manager", "pricing", false)
	allow("admin", "region", "pricing")
	check("manager", "pricing", false)
	allow("region", "lead", "pricing")
	allow("lead", "manager", "pricing")
	// A higher supervisor may revoke even without owning this permission.
	allow("admin", "manager", "models")
	if e = change("lead", "manager", "models", false); e != nil {
		t.Fatal(e)
	}
	allow("admin", "manager", "models")
	if e = r.Ensure(ctx, []modelaccess.Ref{{Provider: "mock", Model: "fixture", Name: "Fixture"}}); e != nil {
		t.Fatal(e)
	}
	models, e := r.List(ctx, company.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, e := r.Organization(ctx, users["manager"], company.ID)
	if e != nil {
		t.Fatal(e)
	}
	a.ModelIDs = []string{models[0].ID}
	if _, e = r.SaveOrganization(ctx, users["manager"], company.ID, a); e != nil {
		t.Fatal(e)
	}
	// Moving a whole branch invalidates outside grants, preserving administrator grants.
	place("lead", system.LeadClientManager, "other")
	check("manager", "pricing", false)
	check("manager", "models", true)
	place("lead", system.LeadClientManager, "region")
	check("manager", "pricing", false)
	allow("admin", "region", "topup")
	allow("region", "lead", "topup")
	allow("lead", "manager", "topup")
	credit := org.Credit{Amount: 20 * money.USD(money.Scale), Kind: "payment", Reason: "Received invoice 123", ExternalID: uuid.NewString()}
	saved, e := r.AddOrganizationCredit(ctx, users["manager"], company.ID, credit)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.AddOrganizationCredit(ctx, users["manager"], company.ID, credit)
	if e != nil || saved.ID != again.ID {
		t.Fatal("idempotency", e)
	}
	// Permission is sufficient: neither large nor repeated payments have an amount ceiling.
	for i := 0; i < 2; i++ {
		large := credit
		large.Amount = 2_000_000 * money.USD(money.Scale)
		large.ExternalID = uuid.NewString()
		if _, e = r.AddOrganizationCredit(ctx, users["manager"], company.ID, large); e != nil {
			t.Fatal("uncapped payment", e)
		}
	}
	for _, bad := range []org.Credit{{Amount: money.USD(money.Scale), Kind: "payment", Reason: " "}, {Amount: -money.USD(money.Scale), Kind: "adjustment", Reason: "negative"}, {Amount: money.USD(money.Scale), Kind: "promo", Reason: "promo"}} {
		bad.ExternalID = uuid.NewString()
		if _, e = r.AddOrganizationCredit(ctx, users["manager"], company.ID, bad); !errors.Is(e, org.ErrInvalid) {
			t.Fatalf("invalid topup: %+v %v", bad, e)
		}
	}
	if _, e = r.AddOrganizationCredit(ctx, users["other"], company.ID, credit); !errors.Is(e, org.ErrForbidden) {
		t.Fatal("foreign topup", e)
	}
	// A write queued behind revocation must recheck authorization after acquiring the lock.
	tx, e := beginAuditTx(ctx, db)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		t.Fatal(e)
	}
	result := make(chan error, 1)
	go func() {
		_, err := r.AddOrganizationCredit(ctx, users["manager"], company.ID, org.Credit{Amount: 1, Kind: "payment", Reason: "racing", ExternalID: uuid.NewString()})
		result <- err
	}()
	if _, e = tx.ExecContext(ctx, `UPDATE sales_permission_grants SET revoked_at=clock_timestamp() WHERE employee_id=$1 AND permission='topup' AND revoked_at IS NULL`, users["lead"]); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-result:
		if !errors.Is(e, org.ErrForbidden) {
			t.Fatal("revocation race", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("revocation race blocked")
	}
	// An old permission snapshot cannot overwrite a later administrator decision.
	stale, e := r.SalesPermissions(ctx, admin)
	if e != nil {
		t.Fatal(e)
	}
	allow("admin", "manager", "models")
	if e = r.SetSalesPermission(ctx, admin, users["manager"], "models", system.SalesPermissionChange{Version: stale.Version}); !errors.Is(e, system.ErrConflict) {
		t.Fatal("stale grant write", e)
	}
	// Missing lead is supported, and permissions follow current customer assignment.
	allow("region", "direct", "topup")
	check("direct", "topup", false)
	if e = r.AssignClientManager(ctx, admin, company.ID, users["direct"]); e != nil {
		t.Fatal(e)
	}
	check("direct", "topup", true)
	check("manager", "topup", false)
	if e = r.SetUserRole(ctx, admin, users["region"], "user"); e != nil {
		t.Fatal(e)
	}
	check("direct", "topup", false)
	var events int
	if e = db.QueryRow(`SELECT count(*) FROM audit_events WHERE action='sales_permission' AND actor_id<>''`).Scan(&events); e != nil || events < 10 {
		t.Fatal("grant audit", events, e)
	}
	var reason string
	if e = db.QueryRow(`SELECT details->'after'->>'reason' FROM audit_events WHERE action='credit' AND object_id=$1`, saved.ID).Scan(&reason); e != nil || reason != credit.Reason {
		t.Fatal("credit reason audit", reason, e)
	}
	state, e := r.SalesPermissions(ctx, users["manager"])
	if e != nil || len(state.Members) != 1 {
		t.Fatal("scoped directory", e)
	}
	key := uuid.NewString()
	keyCtx := identity.WithAPIKeyID(ctx, key)
	if _, e = r.SalesPermissions(keyCtx, admin); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("key grant management", e)
	}
}

// Existing grants survive the upgrade, retaining historical metadata without enforcing it.
func TestSalesPermissionLimitRemovalMigration(t *testing.T) {
	db := auditDatabase(t, false)
	ctx := context.Background()
	goose.SetBaseFS(migrations)
	if e := goose.SetDialect("postgres"); e != nil {
		t.Fatal(e)
	}
	if e := goose.UpTo(db, "migrations", 57); e != nil {
		t.Fatal(e)
	}
	r := NewSystemRepository(db)
	users := map[string]string{}
	for _, name := range []string{"admin", "lead", "manager", "owner"} {
		// Seed the historical schema independently of current profile columns.
		u := identity.User{ID: uuid.NewString()}
		_, e := db.Exec(`INSERT INTO users(id,email,display_name) VALUES($1,$2,$3)`, u.ID, uuid.NewString()+"@fixture.test", name)
		if e != nil {
			t.Fatal(e)
		}
		users[name] = u.ID
	}
	if _, e := db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, users["admin"]); e != nil {
		t.Fatal(e)
	}
	// Seed the historical hierarchy directly: current placement code reads the
	// latest settings schema. The migration below is the behavior under test.
	for _, v := range [][3]string{{"lead", system.LeadClientManager, ""}, {"manager", system.ClientManager, "lead"}} {
		if _, e := db.Exec(`UPDATE users SET is_client_manager=TRUE,sales_role=$2,sales_supervisor_id=NULLIF($3,'')::uuid WHERE id=$1`, users[v[0]], v[1], users[v[2]]); e != nil {
			t.Fatal(e)
		}
	}
	root, child := uuid.NewString(), uuid.NewString()
	if _, e := db.Exec(`INSERT INTO sales_permission_grants(id,employee_id,permission,issuer_id,limit_micros) VALUES($1,$2,'topup',$3,100000000)`, root, users["lead"], users["admin"]); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO sales_permission_grants(id,employee_id,permission,issuer_id,parent_id,limit_micros) VALUES($1,$2,'topup',$3,$4,50000000)`, child, users["manager"], users["lead"], root); e != nil {
		t.Fatal(e)
	}
	if e := Migrate(db); e != nil {
		t.Fatal(e)
	}
	state, e := r.SalesPermissions(ctx, users["admin"])
	if e != nil || len(state.Grants) != 2 {
		t.Fatal("lost historical grants", state, e)
	}
	var oldLimit int64
	if e = db.QueryRow(`SELECT limit_micros FROM sales_permission_grants WHERE id=$1`, child).Scan(&oldLimit); e != nil || oldLimit != 50000000 {
		t.Fatal("lost historical metadata", oldLimit, e)
	}
	company, e := r.CreateOrganization(ctx, users["admin"], "Upgrade client", users["owner"], uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.AssignClientManager(ctx, users["admin"], company.ID, users["manager"]); e != nil {
		t.Fatal(e)
	}
	if _, e = r.AddOrganizationCredit(ctx, users["manager"], company.ID, org.Credit{Amount: 2_000_000 * money.USD(money.Scale), Kind: "payment", Reason: "Received invoice", ExternalID: uuid.NewString()}); e != nil {
		t.Fatal("old grant still capped", e)
	}
	state, e = r.SalesPermissions(ctx, users["lead"])
	if e != nil {
		t.Fatal(e)
	}
	if e = r.SetSalesPermission(ctx, users["lead"], users["manager"], "topup", system.SalesPermissionChange{Enabled: true, Version: state.Version}); e != nil {
		t.Fatal("boolean delegation", e)
	}
	if e = r.CommercialPermission(ctx, users["manager"], company.ID, "topup"); e != nil {
		t.Fatal(e)
	}
}

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func TestSalesProfitAndBonusLedger(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	r := NewSystemRepository(db)
	auth := NewAuthRepository(db)
	users := map[string]string{}
	for _, name := range []string{"admin", "region", "lead", "manager", "replacement", "outsider", "owner"} {
		u, e := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@fixture.test", name, "")
		if e != nil {
			t.Fatal(e)
		}
		users[name] = u.ID
	}
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(r.SetAdmin(ctx, "", users["admin"], true, true))
	for _, v := range [][3]string{{"region", system.RegionalClientManager, ""}, {"lead", system.LeadClientManager, "region"}, {"manager", system.ClientManager, "lead"}, {"replacement", system.ClientManager, "region"}, {"outsider", system.ClientManager, ""}} {
		s, e := r.SalesStructure(ctx, users["admin"])
		must(e)
		_, e = r.PlaceSalesMember(ctx, users["admin"], users[v[0]], system.SalesPlacement{Role: v[1], SupervisorID: users[v[2]], Version: s.Version})
		must(e)
	}
	company, e := r.CreateOrganization(ctx, users["admin"], "Profit client", users["owner"], uuid.NewString()+".fixture.test")
	must(e)
	assign := func(name string) { must(r.AssignClientManager(ctx, users["admin"], company.ID, users[name])) }
	assign("manager")
	insert := func(cost, status, owner, providerOwner string, old bool) string {
		t.Helper()
		id := uuid.NewString()
		at := time.Now().UTC()
		if old {
			at = at.Add(-time.Hour)
		}
		_, e := db.Exec(`INSERT INTO tasks(id,owner_id,actor_id,status,provider,model,prompt,provider_owner_id,storage_owner_id,cost,created_at) VALUES($1,$2,$3,$4,'mock','fixture','fixture',$5,$5,$6::jsonb,$7)`, id, owner, users["owner"], status, providerOwner, cost, at)
		must(e)
		return id
	}
	update := func(id, cost string) {
		t.Helper()
		_, e := db.Exec(`UPDATE tasks SET cost=$2::jsonb,status='succeeded' WHERE id=$1`, id, cost)
		must(e)
	}
	check := func(id, state string, profit *int64, manager, supervisor string) {
		t.Helper()
		var got string
		var amount *int64
		var m, s *string
		must(db.QueryRow(`SELECT state,profit_micros,manager_id,supervisor_id FROM generation_profits WHERE task_id=$1`, id).Scan(&got, &amount, &m, &s))
		if got != state || (profit == nil) != (amount == nil) || profit != nil && *amount != *profit {
			t.Fatalf("profit %s: %s %v", id, got, amount)
		}
		if manager != "" && (m == nil || *m != users[manager]) {
			t.Fatalf("manager %v", m)
		}
		if supervisor != "" && (s == nil || *s != users[supervisor]) {
			t.Fatalf("supervisor %v", s)
		}
	}
	value := func(n int64) *int64 { return &n }
	balance := func(name string, want money.USD) {
		t.Helper()
		d, e := r.SalesBonuses(ctx, users[name])
		must(e)
		for _, p := range d.People {
			if p.ID == users[name] {
				if p.Balance != want {
					t.Fatalf("%s balance %d want %d", name, p.Balance, want)
				}
				return
			}
		}
		t.Fatal("own balance absent")
	}
	pending := `{"metered_usd":100,"markup_percent":20,"billing_required":true}`
	verified := `{"metered_usd":100,"markup_percent":20,"billing_required":true,"actual_usd":90,"status":"confirmed","invoice_ids":["invoice-1"]}`
	id := insert(pending, "running", company.ID, identity.SharedOwnerID, false)
	update(id, pending)
	check(id, "awaiting_billing", nil, "", "")
	balance("manager", 0)
	assign("replacement")
	_, e = db.Exec(`UPDATE tasks SET provider_task_id='billed-job',provider_identity='profit-credentials' WHERE id=$1`, id)
	must(e)
	paid := money.USD(90000000)
	bill := core.BillRecord{ID: "profit-bill", InstanceID: "billed-job", Currency: "USD", PaidUSD: &paid, Settled: true}
	must(NewTaskRepository(db).importBills(ctx, identity.SharedOwnerID, "mock", time.Now().UTC().Format("2006-01"), "profit-credentials", []core.BillRecord{bill}))

	check(id, "recognized", value(30000000), "replacement", "region")
	balance("replacement", 1500000)
	balance("region", 300000)
	update(id, verified)
	balance("replacement", 1500000)
	// First recognition snapshots rates and recipients; corrections cannot reroute them.
	dashboard, e := r.SalesBonuses(ctx, users["admin"])
	must(e)
	settings := dashboard.Settings
	if settings.ManagerPercent != 500 || settings.SupervisorPercent != 100 {
		t.Fatal(settings)
	}
	settings.ManagerPercent = 1000
	settings.SupervisorPercent = 200
	saved, e := r.SaveBonusSettings(ctx, users["admin"], settings)
	must(e)
	if _, e = r.SaveBonusSettings(ctx, users["admin"], settings); !errors.Is(e, system.ErrConflict) {
		t.Fatal("stale settings", e)
	}
	if _, e = r.SaveBonusSettings(ctx, users["lead"], saved); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("non-admin settings", e)
	}
	invalid := saved
	invalid.ManagerPercent = 10000
	invalid.SupervisorPercent = 1
	if _, e = r.SaveBonusSettings(ctx, users["admin"], invalid); !errors.Is(e, system.ErrBonusInput) {
		t.Fatal("oversized combined rates", e)
	}
	assign("manager")
	update(id, `{"metered_usd":100,"markup_percent":20,"billing_required":true,"actual_usd":110,"status":"confirmed","invoice_ids":["invoice-1"]}`)
	check(id, "recognized", value(10000000), "replacement", "region")
	balance("replacement", 500000)
	balance("region", 100000)
	update(id, pending)
	check(id, "awaiting_billing", nil, "replacement", "region")
	balance("replacement", 0)
	update(id, verified)
	balance("replacement", 1500000)
	update(id, `{"metered_usd":100,"markup_percent":20,"billing_required":true,"actual_usd":130,"status":"confirmed","invoice_ids":["invoice-1"]}`)
	check(id, "recognized", value(-10000000), "replacement", "region")
	balance("replacement", 0)
	update(id, verified)
	immediate := insert(`{"metered_usd":100,"markup_percent":20}`, "succeeded", company.ID, identity.SharedOwnerID, false)
	check(immediate, "recognized", value(20000000), "manager", "lead")
	balance("manager", 2000000)
	balance("lead", 400000)
	unknown := insert(`{"expected_usd":100}`, "succeeded", company.ID, identity.SharedOwnerID, false)
	check(unknown, "awaiting_cost", nil, "", "")
	free := insert(`{"actual_source":"provider_failed_no_charge","billing_required":true}`, "failed", company.ID, identity.SharedOwnerID, false)
	check(free, "recognized", value(0), "manager", "lead")
	for _, id := range []string{insert(verified, "succeeded", company.ID, identity.SharedOwnerID, true), insert(verified, "succeeded", users["owner"], identity.SharedOwnerID, false), insert(verified, "succeeded", company.ID, users["owner"], false)} {
		var n int
		must(db.QueryRow(`SELECT count(*) FROM generation_profits WHERE task_id=$1`, id).Scan(&n))
		if n != 0 {
			t.Fatal("historical/personal/BYOK request recognized")
		}
	}
	// Profit is exactly the sum of recognized rows in the organization report.
	reports, e := NewTaskRepository(db).CostReport(identity.WithOwnerID(ctx, company.ID), core.ReportFilter{From: time.Now().Add(-time.Minute), Until: time.Now().Add(time.Minute)})
	must(e)
	var total money.USD
	for _, row := range reports {
		if row.ProfitUSD != nil {
			total += *row.ProfitUSD
		}
	}
	if total != 50000000 {
		t.Fatal("report profit", total)
	}
	payout := system.BonusPayout{Amount: 3 * money.USD(money.Scale), PaidAt: "2026-10-13", Method: "Bank transfer", Reason: "Rounded payment", ExternalID: uuid.NewString()}
	if _, e = r.RecordBonusPayout(ctx, users["lead"], users["manager"], payout); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("ungranted payout", e)
	}
	grant, e := r.SalesPermissions(ctx, users["admin"])
	must(e)
	must(r.SetSalesPermission(ctx, users["admin"], users["lead"], "bonuses", system.SalesPermissionChange{Enabled: true, Version: grant.Version}))
	for _, bad := range []system.BonusPayout{{Amount: 0, PaidAt: payout.PaidAt, Method: "Bank", ExternalID: uuid.NewString()}, {Amount: 1, PaidAt: "2026-02-30", Method: "Bank", ExternalID: uuid.NewString()}, {Amount: 1, PaidAt: payout.PaidAt, Method: " ", ExternalID: uuid.NewString()}} {
		if _, e = r.RecordBonusPayout(ctx, users["lead"], users["manager"], bad); !errors.Is(e, system.ErrBonusInput) {
			t.Fatal("invalid payout", e)
		}
	}
	first, e := r.RecordBonusPayout(ctx, users["lead"], users["manager"], payout)
	must(e)
	second, e := r.RecordBonusPayout(ctx, users["lead"], users["manager"], payout)
	must(e)
	if first != second {
		t.Fatal("duplicate payout")
	}
	balance("manager", -1000000)
	payout.Amount++
	if _, e = r.RecordBonusPayout(ctx, users["lead"], users["manager"], payout); !errors.Is(e, system.ErrConflict) {
		t.Fatal("changed retry", e)
	}
	payout.ExternalID = uuid.NewString()
	for _, target := range []string{"lead", "outsider", "region"} {
		if _, e = r.RecordBonusPayout(ctx, users["lead"], users[target], payout); !errors.Is(e, system.ErrForbidden) {
			t.Fatal("outside/self payout", target, e)
		}
	}
	if _, e = r.BonusEntries(ctx, users["outsider"], users["manager"], 0); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("foreign history", e)
	}
	entries, e := r.BonusEntries(ctx, users["lead"], users["manager"], 0)
	must(e)
	if len(entries) != 2 || entries[0].PaidAt != "2026-10-13" || entries[0].Method != "Bank transfer" || entries[0].ActorName != "lead" {
		t.Fatal(entries)
	}
	if _, e = db.Exec(`UPDATE sales_bonus_entries SET amount_micros=0 WHERE id=$1`, first); e == nil {
		t.Fatal("ledger mutable")
	}
	// Revoke while a payout waits on the authorization lock.
	tx, e := beginAuditTx(ctx, db)
	must(e)
	_, e = readSystemSettings(ctx, tx, true)
	must(e)
	result := make(chan error, 1)
	go func() { _, e := r.RecordBonusPayout(ctx, users["lead"], users["manager"], payout); result <- e }()
	_, e = tx.Exec(`UPDATE sales_permission_grants SET revoked_at=clock_timestamp() WHERE employee_id=$1 AND permission='bonuses'`, users["lead"])
	must(e)
	must(tx.Commit())
	select {
	case e = <-result:
		if !errors.Is(e, system.ErrForbidden) {
			t.Fatal("revoked payout", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("payout deadlock")
	}
}

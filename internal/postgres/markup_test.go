package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/google/uuid"
)

func TestOrganizationMarkupAccounting(t *testing.T) {
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL")
	}
	ctx := context.Background()
	db, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = Migrate(db); err != nil {
		t.Fatal(err)
	}
	auth := NewAuthRepository(db)
	sys := NewSystemRepository(db)
	tasks := NewTaskRepository(db)
	admin, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "System", "")
	if err != nil {
		t.Fatal(err)
	}
	member, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Member", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=true WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	o, err := sys.CreateOrganization(ctx, admin.ID, "Markup test", member.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM generation_profits WHERE organization_id=$1`, `DELETE FROM tasks WHERE owner_id=$1`, `DELETE FROM user_api_keys WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, o.ID); e != nil {
				t.Error(e)
			}
		}
		if _, e := db.Exec(`DELETE FROM users WHERE id IN ($1,$2)`, admin.ID, member.ID); e != nil {
			t.Error(e)
		}
	}()
	before, err := sys.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`UPDATE system_settings SET markup_basis_points=$1 WHERE id=true`, before.MarkupPercent)
	setGlobal := func(p money.Percent) {
		t.Helper()
		s, e := sys.Settings(ctx)
		if e != nil {
			t.Fatal(e)
		}
		s.MarkupPercent = p
		if _, e = sys.SaveSettings(ctx, admin.ID, s); e != nil {
			t.Fatal(e)
		}
	}
	setGlobal(2500)
	override := money.Percent(1000)
	zero := money.Percent(0)
	if err = sys.OrganizationSettings(ctx, member.ID, o.ID, o.Name, 0, nil, false, &override); !errors.Is(err, org.ErrForbidden) {
		t.Fatalf("org admin changed markup: %v", err)
	}
	if err = sys.OrganizationSettings(ctx, admin.ID, o.ID, o.Name, 100*money.USD(money.Scale), nil, true); err != nil {
		t.Fatal(err)
	}
	if err = sys.SetOrganizationMember(ctx, admin.ID, o.ID, member.ID, "supermanager", nil, true, false); err != nil {
		t.Fatal(err)
	}
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Markup", OrganizationID: o.ID, ResourceMode: "shared"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = NewAPIKeyRepository(db).Create(ctx, member.ID, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	c := identity.WithOrganization(identity.WithAPIKeyID(identity.WithUser(ctx, member), key.ID), o.ID, true)
	newTask := func() core.Task {
		return core.Task{ID: uuid.NewString(), OrganizationID: o.ID, OwnerID: o.ID, ActorID: member.ID, APIKeyID: &key.ID, ProviderOwnerID: identity.SharedOwnerID, StorageOwnerID: o.ID, Provider: "mock", Model: "mock-image-v1", MediaType: "image", Prompt: "Markup", Status: core.TaskQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Cost: &core.TaskCost{ExpectedUSD: money.USD(money.Scale), Status: "estimated"}}
	}
	estimate := newTask()
	if err = tasks.ApplyMarkup(c, &estimate); err != nil {
		t.Fatal(err)
	}
	expected, err := estimate.Cost.CustomerExpected()
	if err != nil || expected != 1250000 {
		t.Fatalf("estimate %v %v", expected, err)
	}
	// A higher rate between estimate and acceptance requires reconfirmation.
	setGlobal(3000)
	blocked := newTask()
	blocked.MaxExpectedUSD = &expected
	if _, _, err = tasks.CreateOrGet(c, blocked, blocked.ID, "same"); !errors.Is(err, core.ErrEstimateChanged) {
		t.Fatalf("increased quote admitted: %v", err)
	}
	setGlobal(2500)
	first := newTask()
	got, _, err := tasks.CreateOrGet(c, first, "replay", "same")
	if err != nil || got.Cost.MarkupPercent != 2500 {
		t.Fatalf("accept %v %v", got.Cost, err)
	}
	if err = sys.OrganizationSettings(ctx, admin.ID, o.ID, o.Name, 0, nil, false, &override); err != nil {
		t.Fatal(err)
	}
	if err = sys.OrganizationSettings(ctx, member.ID, o.ID, "Renamed", 0, nil, false); err != nil {
		t.Fatal(err)
	}
	next := newTask()
	if err = tasks.ApplyMarkup(c, &next); err != nil || next.Cost.MarkupPercent != 1000 {
		t.Fatalf("override lost %v %v", next.Cost, err)
	}
	retry := newTask()
	retry.MaxExpectedUSD = new(money.USD)
	got, created, err := tasks.CreateOrGet(c, retry, "replay", "same")
	if err != nil || created || got.Cost.MarkupPercent != 2500 {
		t.Fatalf("replay repriced: %v %v", got, err)
	}
	// Bill reconciliation changes the base, not the snapshotted markup.
	if _, err = db.Exec(`UPDATE tasks SET status='succeeded',cost=cost || '{"metered_usd":1,"tariff_usd":2,"actual_usd":0,"package_units":2}'::jsonb WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	report, err := tasks.CostReport(c, core.ReportFilter{From: first.CreatedAt.Add(-time.Hour), Until: time.Now().Add(time.Hour), Limit: 50})
	if err != nil || len(report) != 1 || report[0].ConsumedUSD == nil || *report[0].ConsumedUSD != 2500000 || *report[0].Cost.TariffUSD != 2000000 || *report[0].Cost.ActualUSD != 0 {
		t.Fatalf("report %+v %v", report, err)
	}
	_, remaining, err := tasks.Budget(c, key.ID, time.Now())
	if err != nil || remaining == nil || *remaining != 97500000 {
		t.Fatalf("balance %v %v", remaining, err)
	}
	weekly := money.USD(3000000)
	if err = sys.SetOrganizationMember(ctx, admin.ID, o.ID, member.ID, "supermanager", &weekly, true, false); err != nil {
		t.Fatal(err)
	}
	_, remaining, err = tasks.Budget(c, key.ID, time.Now())
	if err != nil || remaining == nil || *remaining != 500000 {
		t.Fatalf("weekly markup: %v %v", remaining, err)
	}
	if err = sys.SetOrganizationMember(ctx, admin.ID, o.ID, member.ID, "supermanager", nil, true, false); err != nil {
		t.Fatal(err)
	}
	usage, err := NewAPIKeyRepository(db).Usage(c, member.ID, time.Now())
	if err != nil || len(usage) != 1 || usage[0].ConsumedUSD != 2500000 {
		t.Fatalf("key usage %+v %v", usage, err)
	}
	// Explicit zero overrides; null resumes inheritance; personal usage is exempt.
	if err = sys.OrganizationSettings(ctx, admin.ID, o.ID, o.Name, 0, nil, false, &zero); err != nil {
		t.Fatal(err)
	}
	next = newTask()
	if err = tasks.ApplyMarkup(c, &next); err != nil || next.Cost.MarkupPercent != 0 {
		t.Fatal("zero override", err)
	}
	if err = sys.OrganizationSettings(ctx, admin.ID, o.ID, o.Name, 0, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	setGlobal(4000)
	next = newTask()
	if err = tasks.ApplyMarkup(c, &next); err != nil || next.Cost.MarkupPercent != 4000 {
		t.Fatal("inheritance", err)
	}
	next.ProviderOwnerID = member.ID
	if err = tasks.ApplyMarkup(c, &next); err != nil || next.Cost.MarkupPercent != 0 {
		t.Fatal("personal marked up", err)
	}
	// No historical repricing and explicit no-charge takes precedence over usage.
	if _, err = db.Exec(`UPDATE tasks SET cost=cost-'markup_percent' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	_, remaining, err = tasks.Budget(c, key.ID, time.Now())
	if err != nil || *remaining != 98000000 {
		t.Fatal("historical repricing", remaining, err)
	}
	if err = tasks.ConfirmNoCharge(c, first.ID); err != nil {
		t.Fatal(err)
	}
	_, remaining, err = tasks.Budget(c, key.ID, time.Now())
	if err != nil || *remaining != 100000000 {
		t.Fatal("free charge", remaining, err)
	}
	list, err := sys.Organizations(ctx, member.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(list)
	if string(b) == "" {
		t.Fatal("empty")
	}
	for _, item := range list {
		if item.MarkupPercent != nil || item.EffectiveMarkupPercent != nil {
			t.Fatal("non-system settings disclosed")
		}
	}
}

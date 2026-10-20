package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/markup"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func TestMarkupRulesPersistenceAndPricing(t *testing.T) {
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
	sys := NewSystemRepository(db)
	tasks := NewTaskRepository(db)
	auth := NewAuthRepository(db)
	admin, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Pricing admin", "")
	if err != nil {
		t.Fatal(err)
	}
	member, err := auth.UpsertGoogleUser(ctx, uuid.NewString(), uuid.NewString()+"@example.test", "Company admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=TRUE WHERE id=$1`, admin.ID); err != nil {
		t.Fatal(err)
	}
	o, err := sys.CreateOrganization(ctx, admin.ID, "Pricing matrix", member.ID, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	before, err := sys.Markups(ctx, admin.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		rules, _ := json.Marshal(before.System.Exceptions)
		db.Exec(`UPDATE system_settings SET markup_basis_points=$1,markup_exceptions=$2 WHERE id=TRUE`, before.System.Default, rules)
		for _, q := range []string{`DELETE FROM generation_profits WHERE organization_id=$1`, `DELETE FROM tasks WHERE owner_id=$1`, `DELETE FROM user_api_keys WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, err := db.Exec(q, o.ID); err != nil {
				t.Error(err)
			}
		}
		db.Exec(`DELETE FROM users WHERE id IN ($1,$2)`, admin.ID, member.ID)
	}()
	rate := func(n money.Percent) *money.Percent { return &n }
	config := before.System
	config.Default = rate(2000)
	config.Exceptions = []markup.Rule{{Provider: "openai", Percent: rate(2500)}, {Provider: "openai", Model: "X", Percent: rate(3000)}}
	if _, err = sys.SaveMarkups(ctx, member.ID, "", config); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("company admin wrote system rules: %v", err)
	}
	if _, err = sys.Markups(ctx, member.ID, o.ID); !errors.Is(err, system.ErrForbidden) {
		t.Fatalf("company admin read rules: %v", err)
	}
	if _, err = sys.SaveMarkups(ctx, admin.ID, "", config); err != nil {
		t.Fatal(err)
	}
	if _, err = sys.SaveMarkups(ctx, admin.ID, "", config); !errors.Is(err, system.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	// Updating other system settings must retain all exceptions.
	settings, err := sys.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sys.SaveSettings(ctx, admin.ID, settings); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		company markup.Config
		want    [3]money.Percent
	}{
		{markup.Config{}, [3]money.Percent{3000, 2500, 2000}},
		{markup.Config{Default: rate(0)}, [3]money.Percent{0, 0, 0}},
		{markup.Config{Exceptions: []markup.Rule{{Provider: "openai", Percent: rate(0)}}}, [3]money.Percent{0, 0, 2000}},
		{markup.Config{Exceptions: []markup.Rule{{Provider: "openai", Model: "X", Percent: rate(0)}}}, [3]money.Percent{0, 2500, 2000}},
		{markup.Config{Default: rate(1000), Exceptions: []markup.Rule{{Provider: "openai", Percent: rate(500)}, {Provider: "openai", Model: "X", Percent: rate(100)}}}, [3]money.Percent{100, 500, 1000}},
	} {
		p, err := sys.Markups(ctx, admin.ID, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		tt.company.Version = p.Company.Version
		if _, err = sys.SaveMarkups(ctx, admin.ID, o.ID, tt.company); err != nil {
			t.Fatal(err)
		}
		if _, err = sys.SaveMarkups(ctx, admin.ID, o.ID, tt.company); !errors.Is(err, system.ErrConflict) {
			t.Fatalf("organization stale version: %v", err)
		}
		for i, target := range [][2]string{{"openai", "X"}, {"openai", "Y"}, {"alibaba", "X"}} {
			task := core.Task{OwnerID: o.ID, ProviderOwnerID: identity.SharedOwnerID, Provider: target[0], Model: target[1], Cost: &core.TaskCost{ExpectedUSD: 1_000_000}}
			if err := tasks.ApplyMarkup(ctx, &task); err != nil {
				t.Fatal(err)
			}
			if task.Cost.MarkupPercent != tt.want[i] {
				t.Fatalf("%v: got %v, want %v", target, task.Cost.MarkupPercent, tt.want[i])
			}
			task.ProviderOwnerID = member.ID
			if err := tasks.ApplyMarkup(ctx, &task); err != nil || task.Cost.MarkupPercent != 0 {
				t.Fatal("personal credentials marked up", err)
			}
		}
	}
	// Legacy default updates preserve model exceptions and invalidate stale editors.
	old, err := sys.Markups(ctx, admin.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = sys.OrganizationSettings(ctx, admin.ID, o.ID, o.Name, 0, nil, false, rate(4000)); err != nil {
		t.Fatal(err)
	}
	next, err := sys.Markups(ctx, admin.ID, o.ID)
	if err != nil || next.Company.Version != old.Company.Version+1 || next.Resolve("openai", "X").Percent != 100 {
		t.Fatal(next, err)
	}
	if _, err = sys.SaveMarkups(ctx, admin.ID, o.ID, old.Company); !errors.Is(err, system.ErrConflict) {
		t.Fatal(err)
	}
	// Accepted tasks and idempotent retries keep the chosen model exception.
	if err = sys.OrganizationSettings(ctx, admin.ID, o.ID, o.Name, 100_000_000, nil, true); err != nil {
		t.Fatal(err)
	}
	if err = sys.SetOrganizationMember(ctx, admin.ID, o.ID, member.ID, "supermanager", nil, true, false); err != nil {
		t.Fatal(err)
	}
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Pricing test", OrganizationID: o.ID, ResourceMode: "shared"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = NewAPIKeyRepository(db).Create(ctx, member.ID, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	task := core.Task{ID: uuid.NewString(), OrganizationID: o.ID, OwnerID: o.ID, ActorID: member.ID, APIKeyID: &key.ID, ProviderOwnerID: identity.SharedOwnerID, StorageOwnerID: o.ID, Provider: "openai", Model: "X", MediaType: "image", Prompt: "Pricing fixture", Status: core.TaskQueued, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Cost: &core.TaskCost{ExpectedUSD: 1_000_000, Status: "estimated"}}
	taskCtx := identity.WithOrganization(identity.WithAPIKeyID(identity.WithUser(ctx, member), key.ID), o.ID, true)
	accepted, _, err := tasks.CreateOrGet(taskCtx, task, "pricing-snapshot", "same")
	if err != nil || accepted.Cost.MarkupPercent != 100 {
		t.Fatalf("model exception admission %+v %v", accepted.Cost, err)
	}
	next, err = sys.Markups(ctx, admin.ID, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	next.Company.Exceptions = nil
	next.Company.Default = rate(0)
	if _, err = sys.SaveMarkups(ctx, admin.ID, o.ID, next.Company); err != nil {
		t.Fatal(err)
	}
	replay, created, err := tasks.CreateOrGet(taskCtx, task, "pricing-snapshot", "same")
	if err != nil || created || replay.Cost.MarkupPercent != 100 {
		t.Fatalf("repriced existing task %+v %v", replay.Cost, err)
	}

}

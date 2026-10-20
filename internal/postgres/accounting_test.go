package postgres

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/google/uuid"
)

func TestCompletedSpendingAndInvoiceOverrun(t *testing.T) {
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
	owner, err := NewAuthRepository(db).UpsertGoogleUser(ctx, "budget-"+uuid.NewString(), "budget@example.test", "Budget", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.ExecContext(ctx, `DELETE FROM tasks WHERE owner_id=$1`, owner.ID)
		db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, owner.ID)
	}()
	limit, _ := money.Parse("0.51")
	expected, _ := money.Parse("0.50")
	actual, _ := money.Parse("0.55")
	defer budgetOrganization(t, db, owner.ID, limit)()
	keys := NewAPIKeyRepository(db)
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Bot", OrganizationID: owner.ID, ResourceMode: "shared"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Create(ctx, owner.ID, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	ctx = identity.WithOrganization(identity.WithAPIKeyID(identity.WithUser(ctx, owner), key.ID), owner.ID, true)
	repo := NewTaskRepository(db)
	now := time.Now().UTC()
	makeTask := func() core.Task {
		return core.Task{ID: uuid.NewString(), OwnerID: owner.ID, ProviderOwnerID: identity.SharedOwnerID, APIKeyID: &key.ID, Provider: "alibaba", Model: "wan3.0-video-prime", MediaType: "video", Status: core.TaskQueued, Prompt: "budget test", CreatedAt: now, UpdatedAt: now, RequireCost: true, ProviderIdentity: "fingerprint", Cost: &core.TaskCost{ExpectedUSD: expected, Status: "estimated", Pricing: core.PricingSnapshot{Currency: "USD", Price: expected, Per: 1, Unit: "second"}}}
	}
	var wg sync.WaitGroup
	accepted := make(chan core.Task, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			task := makeTask()
			got, _, err := repo.CreateOrGet(ctx, task, task.ID, "same-hash")
			if err == nil {
				accepted <- got
			} else if !errors.Is(err, core.ErrBudgetExceeded) {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(accepted)
	close(failures)
	for err := range failures {
		t.Errorf("unexpected admission error: %v", err)
	}
	if len(accepted) != 12 {
		t.Fatalf("parallel requests accepted %d tasks; want 12", len(accepted))
	}
	task := <-accepted
	// Pending tasks leave the full budget available, regardless of their estimates.
	_, left, e := repo.Budget(ctx, key.ID, now)
	if e != nil || left == nil || *left != limit {
		t.Fatalf("pending tasks occupied budget: %v %v", left, e)
	}
	expensive := makeTask()
	expensive.Cost.ExpectedUSD = money.USD(100 * money.Scale)
	if _, _, e = repo.CreateOrGet(ctx, expensive, "larger-than-budget", "expensive"); e != nil {
		t.Fatalf("estimate blocked admission: %v", e)
	}
	// Idempotent retries do not create another task.
	replay := makeTask()
	zero := money.USD(0)
	replay.MaxExpectedUSD = &zero
	got, created, err := repo.CreateOrGet(ctx, replay, task.ID, "same-hash")
	if err != nil || created || got.ID != task.ID {
		t.Fatalf("idempotency: %v", err)
	}
	if err = repo.MarkRunning(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetProviderTaskID(ctx, task.ID, "provider-task"); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetProviderUsage(ctx, task.ID, core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "output_video_duration", Quantity: 1.1}}}); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkSucceeded(ctx, task.ID, core.StoredMedia{}, ""); err != nil {
		t.Fatal(err)
	}
	// A model/day aggregate must never be assigned to this request.
	bill := core.BillRecord{ID: "invoice-item", InstanceID: "model;day;all-requests", Currency: "USD", PaidUSD: &actual, Settled: true}
	if err = repo.importBills(ctx, identity.SharedOwnerID, "alibaba", now.Format("2006-01"), "fingerprint", []core.BillRecord{bill}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, task.ID)
	if got.Cost.ActualUSD != nil {
		t.Fatal("aggregate bill was falsely attributed")
	}
	bill.InstanceID = "provider-task"
	if err = repo.importBills(ctx, identity.SharedOwnerID, "alibaba", now.Format("2006-01"), "fingerprint", []core.BillRecord{bill}); err != nil {
		t.Fatal(err)
	}
	// The same snapshot is a replacement, never an additional charge.
	if err = repo.importBills(ctx, identity.SharedOwnerID, "alibaba", now.Format("2006-01"), "fingerprint", []core.BillRecord{bill}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cost.ExpectedUSD != expected || got.Cost.ActualUSD == nil || *got.Cost.ActualUSD != actual {
		t.Fatalf("cost history: %+v", got.Cost)
	}
	_, remaining, err := repo.Budget(ctx, key.ID, now)
	if err != nil || remaining == nil || *remaining != -40000 {
		t.Fatalf("remaining %v, error %v", remaining, err)
	}
	next := makeTask()
	if _, _, err = repo.CreateOrGet(ctx, next, "another", "other"); !errors.Is(err, core.ErrBudgetExceeded) {
		t.Fatalf("overdrawn budget accepted: %v", err)
	}
	usage, err := keys.Usage(ctx, owner.ID, now)
	if err != nil || len(usage) != 1 || usage[0].ActualUSD != actual || usage[0].ConsumedUSD != actual {
		t.Fatalf("usage %+v: %v", usage, err)
	}
	// Exhausted budgets block new admissions, including free tasks.
	free := makeTask()
	free.Provider = "mock"
	freeCost := core.FreeCost(now)
	free.Cost = &freeCost
	if _, _, err = repo.CreateOrGet(ctx, free, "free-overdrawn", "free"); !errors.Is(err, core.ErrBudgetExceeded) {
		t.Fatalf("exhausted budget admitted a task: %v", err)
	}
	// Calendar months and additional keys do not refill organization credit.
	start, _ := monthBounds(now)
	next = makeTask()
	next.CreatedAt = start.AddDate(0, 1, 0)
	next.UpdatedAt = next.CreatedAt
	if _, _, err = repo.CreateOrGet(ctx, next, "next-month", "next"); !errors.Is(err, core.ErrBudgetExceeded) {
		t.Fatalf("month restored credit: %v", err)
	}
	second, secret, err := apikey.Generate(apikey.CreateInput{Name: "Second", OrganizationID: owner.ID, ResourceMode: "shared"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Create(ctx, owner.ID, second, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	next = makeTask()
	next.APIKeyID = &second.ID
	if _, _, err = repo.CreateOrGet(identity.WithAPIKeyID(ctx, second.ID), next, "second-key", "second"); !errors.Is(err, core.ErrBudgetExceeded) {
		t.Fatalf("new key restored credit: %v", err)
	}
	// Unknown tariffs and consent ceilings are enforced independently of organization credit.
	next = makeTask()
	next.APIKeyID = &second.ID
	next.Cost = nil
	if _, _, err = repo.CreateOrGet(ctx, next, "unpriced", "unpriced"); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatalf("unpriced task: %v", err)
	}
	next = makeTask()
	next.APIKeyID = &second.ID
	next.MaxExpectedUSD = &zero
	if _, _, err = repo.CreateOrGet(ctx, next, "consent", "consent"); !errors.Is(err, core.ErrEstimateChanged) {
		t.Fatalf("price ceiling: %v", err)
	}
}

func budgetOrganization(t *testing.T, db *sql.DB, owner string, credit money.USD) func() {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO organizations(id,name,credit_limit_micros,default_weekly_limit_micros) VALUES($1,'Budget test',$2,NULL)`, owner, credit); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO organization_members(organization_id,user_id,role,weekly_limit_micros) VALUES($1,$1,'admin',NULL)`, owner); err != nil {
		t.Fatal(err)
	}
	return func() {
		db.Exec(`DELETE FROM provider_bill_records WHERE owner_id=$1 OR owner_id=$2`, owner, identity.SharedOwnerID)
		for _, q := range []string{`DELETE FROM generation_profits WHERE organization_id=$1`, `DELETE FROM tasks WHERE owner_id=$1`, `DELETE FROM user_api_keys WHERE organization_id=$1`, `DELETE FROM organization_members WHERE organization_id=$1`, `DELETE FROM organization_credits WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, err := db.Exec(q, owner); err != nil {
				t.Error(err)
			}
		}
	}
}

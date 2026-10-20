package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/google/uuid"
)

func TestPackageBillReconcilesBudgetAndRevisions(t *testing.T) {
	for _, media := range []string{"image", "video"} {
		t.Run(media, func(t *testing.T) { testPackageBillReconciliation(t, media) })
	}
}
func testPackageBillReconciliation(t *testing.T, media string) {
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
	owner, err := NewAuthRepository(db).UpsertGoogleUser(ctx, "image-bill-"+uuid.NewString(), "image-bill@example.test", "Billing", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.ExecContext(ctx, `DELETE FROM tasks WHERE owner_id=$1`, owner.ID)
		db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, owner.ID)
	}()
	ctx = identity.WithUser(ctx, owner)
	amount, limit := money.USD(40000), money.USD(100000)
	model, billModel, metric, unit := "seedream-4-5-251128", "seedream-4-5", "image_count", 1.0
	audio := true
	price := core.PricingSnapshot{Currency: "USD", Unit: "image", Price: amount, Per: 1}
	if media == "video" {
		amount, model, billModel, metric, unit = 93120, "seedance-1-5-pro-251215", "seedance-1-5-pro", "completion_tokens", 38800
		price = core.PricingSnapshot{Currency: "USD", Unit: "token", Price: 2400000, Per: 1000000}
	}
	defer budgetOrganization(t, db, owner.ID, limit)()
	ctx = identity.WithOrganization(ctx, owner.ID, true)
	key, secret, err := apikey.Generate(apikey.CreateInput{Name: "Image budget", OrganizationID: owner.ID, ResourceMode: "shared"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = NewAPIKeyRepository(db).Create(ctx, owner.ID, key, apikey.Hash(secret)); err != nil {
		t.Fatal(err)
	}
	ctx = identity.WithAPIKeyID(ctx, key.ID)
	repo := NewTaskRepository(db)
	now := time.Now().UTC()
	start, end := now.Add(-time.Minute), now.Add(time.Minute)
	task := core.Task{ID: uuid.NewString(), OwnerID: owner.ID, ProviderOwnerID: identity.SharedOwnerID, APIKeyID: &key.ID, Provider: "bytedance", ProviderIdentity: "fingerprint", Model: model, MediaType: media, Options: core.MediaOptions{GenerateAudio: &audio}, Prompt: "billing regression", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now, Cost: &core.TaskCost{ExpectedUSD: amount, Pricing: price}}
	if _, _, err = repo.CreateOrGet(ctx, task, task.ID, "hash"); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkRunning(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetProviderUsage(ctx, task.ID, core.ProviderUsage{Metrics: []core.UsageMetric{{Name: metric, Quantity: unit}}}); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkSucceeded(ctx, task.ID, core.StoredMedia{}, ""); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	zero := money.USD(0)
	bill := core.BillRecord{ID: "image-detail", InstanceID: "model-instance", Model: billModel, MediaType: media, Currency: "USD", UsageStart: &start, UsageEnd: &end, Units: &unit, PackageUnits: &unit, TariffUSD: &amount, PaidUSD: &zero, Settled: true}
	month := now.Format("2006-01")
	for i := 0; i < 2; i++ {
		if err = repo.importBills(ctx, identity.SharedOwnerID, "bytedance", month, "fingerprint", []core.BillRecord{bill}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.UpdatedAt.Equal(completed.UpdatedAt) {
		t.Fatal("billing changed generation completion time")
	}
	if got.Cost.ActualUSD == nil || *got.Cost.ActualUSD != 0 || got.Cost.TariffUSD == nil || *got.Cost.TariffUSD != amount || got.Cost.ExpectedUSD != amount || got.Cost.BillingMatch != "model_usage_window" {
		t.Fatalf("cost=%+v", got.Cost)
	}
	_, remaining, err := repo.Budget(ctx, key.ID, now)
	if err != nil || remaining == nil || *remaining != limit-amount {
		t.Fatalf("remaining=%v err=%v", remaining, err)
	}
	usage, err := NewAPIKeyRepository(db).Usage(ctx, owner.ID, now)
	if err != nil || len(usage) != 1 || usage[0].ConsumedUSD != amount || usage[0].ActualUSD != 0 || usage[0].PackageCoveredUSD != amount {
		t.Fatalf("covered usage=%+v err=%v", usage, err)
	}
	filter := core.ReportFilter{From: now.Add(-time.Hour), Until: now.Add(time.Hour), KeyID: key.ID, Limit: 50}
	report, err := repo.CostReport(ctx, filter)
	if err != nil || len(report) != 1 || report[0].ConsumedUSD == nil || *report[0].ConsumedUSD != amount || report[0].Cost.ActualUSD == nil || *report[0].Cost.ActualUSD != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	filter.GenerationID = task.ID
	matched, err := repo.CostReport(ctx, filter)
	if err != nil || len(matched) != 1 || matched[0].ID != task.ID {
		t.Fatalf("generation filter: %+v %v", matched, err)
	}
	count := 0
	if err = repo.WalkCostReport(ctx, filter, func(row core.CostReportRow) error {
		count++
		if row.ID != task.ID {
			t.Fatal(row.ID)
		}
		return nil
	}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	filter.GenerationID = uuid.NewString()
	matched, err = repo.CostReport(ctx, filter)
	if err != nil || len(matched) != 0 {
		t.Fatalf("unknown generation: %+v %v", matched, err)
	}
	filter.GenerationID = task.ID
	matched, err = repo.CostReport(identity.WithOwnerID(context.Background(), uuid.NewString()), filter)
	if err != nil || len(matched) != 0 {
		t.Fatal("generation ID bypassed owner scope", matched, err)
	}
	filter.GenerationID = ""
	summary, err := core.SummarizeCostReport(ctx, repo, filter, "day")
	if err != nil || summary.Requests != 1 || summary.Spend != amount || len(summary.PublicModels) != 1 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
	hidden, err := core.SummarizeCostReport(identity.WithOwnerID(context.Background(), uuid.NewString()), repo, filter, "day")
	if err != nil || hidden.Requests != 0 || hidden.Spend != 0 {
		t.Fatalf("cross-owner summary=%+v err=%v", hidden, err)
	}
	alien, err := repo.CostReport(identity.WithOwnerID(context.Background(), uuid.NewString()), filter)
	if err != nil || len(alien) != 0 {
		t.Fatalf("cross-owner report=%+v err=%v", alien, err)
	}
	filter.KeyID = uuid.NewString()
	empty, err := repo.CostReport(ctx, filter)
	if err != nil || len(empty) != 0 {
		t.Fatalf("key filter=%+v err=%v", empty, err)
	}
	filter.KeyID = key.ID
	filter.From = now.Add(time.Hour)
	filter.Until = now.Add(2 * time.Hour)
	empty, err = repo.CostReport(ctx, filter)
	if err != nil || len(empty) != 0 {
		t.Fatalf("date filter=%+v err=%v", empty, err)
	}
	// A provider revision changes cash settlement without consuming the budget twice.
	bill.PaidUSD = &amount
	uncovered := 0.0
	bill.PackageUnits = &uncovered
	if err = repo.importBills(ctx, identity.SharedOwnerID, "bytedance", month, "fingerprint", []core.BillRecord{bill}); err != nil {
		t.Fatal(err)
	}
	_, remaining, err = repo.Budget(ctx, key.ID, now)
	if err != nil || remaining == nil || *remaining != limit-amount {
		t.Fatalf("revised remaining=%v err=%v", remaining, err)
	}
	// A removed bill cannot leave a stale confirmed payment or free allowance.
	if err = repo.importBills(ctx, identity.SharedOwnerID, "bytedance", month, "fingerprint", nil); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, task.ID)
	if err != nil || got.Cost.ActualUSD != nil || got.Cost.Status != "pending_billing" {
		t.Fatalf("removed bill cost=%+v err=%v", got.Cost, err)
	}
	if err = repo.importBills(ctx, identity.SharedOwnerID, "bytedance", month, "fingerprint", []core.BillRecord{bill}); err != nil {
		t.Fatal(err)
	}
	got, err = repo.Get(ctx, task.ID)
	if err != nil || got.Cost.ActualUSD == nil {
		t.Fatalf("restored bill cost=%+v err=%v", got.Cost, err)
	}
}

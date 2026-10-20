package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/bytedance"
	"github.com/google/uuid"
)

func TestSeedreamInexactCountImportAndRevision(t *testing.T) {
	db := auditDatabase(t, true)
	ctx := context.Background()
	owner, err := NewAuthRepository(db).UpsertGoogleUser(ctx, uuid.NewString(), "bill-count@example.test", "Bill count", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx = identity.WithUser(ctx, owner)
	repo := NewTaskRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	task := core.Task{
		ID: uuid.NewString(), OwnerID: owner.ID, ProviderOwnerID: owner.ID,
		Provider: "bytedance", ProviderIdentity: "count-fixture", Model: "seedream-4-5-251128",
		MediaType: "image", Status: core.TaskQueued, CreatedAt: now, UpdatedAt: now,
		Cost: &core.TaskCost{ExpectedUSD: 40000, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "image", Price: 40000, Per: 1}},
	}
	if _, _, err = repo.CreateOrGet(ctx, task, task.ID, "count-hash"); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkRunning(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetProviderUsage(ctx, task.ID, core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "image_count", Quantity: 1}}}); err != nil {
		t.Fatal(err)
	}
	if err = repo.MarkSucceeded(ctx, task.ID, core.StoredMedia{}, ""); err != nil {
		t.Fatal(err)
	}
	completed, err := repo.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("billing", 8*60*60)
	row := map[string]any{
		"BillDetailId": "count-detail", "InstanceNo": "model-service",
		"BillCategory": "Consumption-usage", "Product": "Smart_Drawing_T2I",
		"ExpandField": "seedream-4-5", "Element": "Seedream 4.5-Piece-Generate",
		"ExpenseBeginTime": now.Add(-time.Minute).In(zone).Format("2006-01-02 15:04:05"),
		"ExpenseEndTime":   now.Add(time.Minute).In(zone).Format("2006-01-02 15:04:05"),
		"DeductionCount":   "1", "Unit": "Piece", "PriceUnit": "Piece",
		"BillingFunction": "Fixed Price", "Price": "0.040000",
		"PaidAmount": "0", "UnpaidAmount": "0", "Currency": "USD",
	}
	// Invalid first import, valid correction, invalid revision, valid restoration.
	for _, count := range []string{"1.0000000000000001", "1", "0.99999999999999999", "1"} {
		row["Count"] = count
		bill, err := bytedance.Definition().NormalizeBill(now.Format("2006-01"), row)
		if err != nil {
			t.Fatal(err)
		}
		if err = repo.importBills(ctx, owner.ID, "bytedance", now.Format("2006-01"), task.ProviderIdentity, []core.BillRecord{bill}); err != nil {
			t.Fatal(err)
		}
		got, err := repo.Get(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if count == "1" {
			if got.Cost.ActualUSD == nil || *got.Cost.ActualUSD != 0 || got.Cost.TariffUSD == nil || *got.Cost.TariffUSD != 40000 || got.Cost.PackageUnits == nil || *got.Cost.PackageUnits != 1 || got.Cost.BillingMatch != "model_usage_window" {
				t.Errorf("valid correction not confirmed: %+v", got.Cost)
			}
		} else if got.Cost.ActualUSD != nil || got.Cost.TariffUSD != nil || got.Cost.PackageUnits != nil || got.Cost.BillingMatch != "" || got.Cost.Status != "pending_billing" {
			t.Errorf("inexact Count %q persisted confirmed cost: %+v", count, got.Cost)
		}
		if !got.UpdatedAt.Equal(completed.UpdatedAt) || got.Cost.ExpectedUSD != 40000 || got.Cost.MeteredUSD == nil || *got.Cost.MeteredUSD != 40000 {
			t.Errorf("import changed completion/estimate/metering: %+v", got)
		}
		var storedCount string
		var rows int
		if err = db.QueryRowContext(ctx, `SELECT COUNT(*),MIN(payload->'raw'->>'Count') FROM provider_bill_records WHERE owner_id=$1 AND provider='bytedance'`, owner.ID).Scan(&rows, &storedCount); err != nil || rows != 1 || storedCount != count {
			t.Fatalf("snapshot did not retain exactly the original record: rows=%d Count=%q err=%v", rows, storedCount, err)
		}
	}
}

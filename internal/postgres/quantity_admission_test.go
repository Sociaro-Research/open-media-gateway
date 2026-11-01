package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/google/uuid"
)

type quantityQueue struct{ enqueued int }

func (q *quantityQueue) Enqueue(context.Context, string) error { q.enqueued++; return nil }
func (*quantityQueue) Dequeue(context.Context) (string, error) { return "", nil }
func (*quantityQueue) Ack(context.Context, string) error       { return nil }
func (*quantityQueue) Requeue(context.Context, string) error   { return nil }
func (*quantityQueue) Recover(context.Context) error           { return nil }

type quantityProvider struct{}

func (quantityProvider) Name() string { return "quantity" }
func (quantityProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{{ID: "inferred-video", MediaType: "video", NoSeed: true}}, nil
}
func (quantityProvider) Generate(context.Context, core.MediaRequest) (core.ProviderTask, error) {
	return core.ProviderTask{}, errors.New("fixture must not submit")
}
func (quantityProvider) GetTask(context.Context, string) (core.ProviderResult, error) {
	return core.ProviderResult{}, errors.New("fixture must not poll")
}
func (quantityProvider) Quote(context.Context, core.MediaModel, core.MediaRequest) (core.TaskCost, string, error) {
	return core.TaskCost{QuantityUnknown: true, Status: "quantity_unknown", QuotedAt: time.Now(), Pricing: core.PricingSnapshot{Currency: "USD", Unit: "token", Per: 1000000, Source: "https://pricing.example.test/verified-token-tariff", Components: []core.PricingComponent{{Metric: "input_tokens", Price: 1000000, Per: 1000000}, {Metric: "output_tokens", Price: 2000000, Per: 1000000}}}}, "quantity-accepted-revision", nil
}
func TestQuantityUnknownAdmissionNeverSatisfiesExplicitCostCap(t *testing.T) {
	db := auditDatabase(t, true)
	repo := NewTaskRepository(db)
	queue := &quantityQueue{}
	svc := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"quantity": quantityProvider{}}, map[string]string{"quantity": "inferred-video"}, "quantity")
	svc.EnableAccounting()
	ctx := identity.WithOwnerID(context.Background(), uuid.NewString())
	for _, cap := range []money.USD{0, 1, 1000000000} {
		input := core.CreateMediaInput{Prompt: "Synthetic known rate", MaxExpectedUSD: &cap}
		if _, err := svc.CreateMediaRequest(ctx, input, uuid.NewString()); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatalf("unknown total satisfied cap %d: %v", cap, err)
		}
	}
	if queue.enqueued != 0 {
		t.Fatal("unprovable capped request dispatched", queue.enqueued)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM tasks WHERE owner_id=$1`, identity.OwnerID(ctx)).Scan(&count); err != nil || count != 0 {
		t.Fatal("rejected capped admission persisted", count, err)
	}
	accepted, err := svc.CreateMediaRequest(ctx, core.CreateMediaInput{Prompt: "Synthetic known rate"}, "uncapped-context")
	if err != nil || queue.enqueued != 1 {
		t.Fatal(accepted, err, queue.enqueued)
	}
	var raw string
	if err = db.QueryRow(`SELECT cost::text FROM tasks WHERE id=$1`, accepted.ID).Scan(&raw); err != nil || strings.Contains(raw, `"expected_usd"`) || strings.Contains(raw, `"customer_expected_usd"`) || !strings.Contains(raw, `"quantity_unknown": true`) {
		t.Fatal(raw, err)
	}
	stored, err := repo.Get(ctx, accepted.ID)
	if err != nil || stored.MediaType != "video" || stored.Cost == nil || !stored.Cost.QuantityUnknown || len(stored.Cost.Pricing.Components) != 2 {
		t.Fatal(stored, err)
	}
	if err = repo.MarkRunning(ctx, accepted.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetProviderUsage(ctx, accepted.ID, core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "input_tokens", Quantity: 1000}, {Name: "output_tokens", Quantity: 100}}}); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.Get(ctx, accepted.ID)
	if err != nil || stored.Cost.MeteredUSD == nil || *stored.Cost.MeteredUSD != 1200 || stored.Cost.ActualSource == "free_provider" {
		t.Fatal("known tariff not metered from actual quantities", stored.Cost, err)
	}
}

package core

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestMultiMetricTariffSurvivesSnapshotAndRequiresCompleteUsage(t *testing.T) {
	c := TaskCost{Pricing: PricingSnapshot{Unit: "multi_metric", Components: []PricingComponent{
		{Metric: "text_input_tokens", Price: money.USD(5 * money.Scale), Per: 1000000},
		{Metric: "image_input_tokens", Price: money.USD(8 * money.Scale), Per: 1000000},
		{Metric: "image_output_tokens", Price: money.USD(30 * money.Scale), Per: 1000000},
	}}}
	saved, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var restored TaskCost
	if err = json.Unmarshal(saved, &restored); err != nil {
		t.Fatal(err)
	}
	u := ProviderUsage{Metrics: []UsageMetric{{Name: "text_input_tokens", Quantity: 10}, {Name: "image_input_tokens", Quantity: 20}}}
	if restored.MeterUsage(u) || restored.MeteredUSD != nil {
		t.Fatal("partial usage was priced")
	}
	u.Metrics = append(u.Metrics, UsageMetric{Name: "image_output_tokens", Quantity: 100})
	if !restored.MeterUsage(u) || restored.MeteredUSD.String() != "0.003210" || restored.ActualUSD != nil || restored.Status != "pending_billing" {
		t.Fatal(restored)
	}
	u.Metrics[0].Quantity = -1
	if restored.MeterUsage(u) {
		t.Fatal("negative usage was priced")
	}
}

func TestImageControlsValidateAgainstCatalog(t *testing.T) {
	s := &TaskService{}
	m := MediaModel{MediaType: "image", Qualities: []string{"medium", "high"}, Backgrounds: []string{"opaque", "transparent"}}
	options, err := s.normalizeOptions(m, MediaOptions{})
	if err != nil || options.Quality != "medium" || options.Background != "opaque" {
		t.Fatal(options, err)
	}
	if _, err = s.normalizeOptions(m, MediaOptions{Quality: "max"}); err == nil {
		t.Fatal("unsupported quality")
	}
	if _, err = s.normalizeOptions(MediaModel{MediaType: "image"}, MediaOptions{Background: "opaque"}); err == nil {
		t.Fatal("control silently ignored by another provider")
	}
}

func TestEmptyCatalogDoesNotInventDefaultVideoModel(t *testing.T) {
	service := NewTaskServiceWithProviders(&memoryRepository{}, &memoryQueue{}, map[string]MediaProvider{"catalog": catalogProvider{}}, map[string]string{"catalog": "gpt-image-2.5-flare"}, "catalog")
	if len(service.ListProviders(context.Background())) != 0 {
		t.Fatal("empty catalog advertised a fabricated model")
	}
	if _, found := service.findModel(context.Background(), "catalog", "gpt-image-2.5-flare"); found {
		t.Fatal("unavailable default model accepted")
	}
}

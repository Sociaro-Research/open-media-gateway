package sonilo

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func (a *Adapter) setPrices(c providerkit.Credentials) error {
	for _, op := range operations {
		if op.priceField == "" || c.Get(op.priceField) == "" {
			continue
		}
		price, err := money.Parse(c.Get(op.priceField))
		if err != nil || price < 0 {
			return fmt.Errorf("Sonilo %s requires a nonnegative USD amount", op.priceField)
		}
		a.prices[op.id] = price
	}
	return nil
}

func (a *Adapter) Quote(ctx context.Context, _ core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.TaskCost{}, "", err
	}
	op, _ := findOperation(r.Model)
	price, configured := a.prices[op.id]
	source := "https://platform.sonilo.com/pricing"
	if !configured {
		if op.rate == "" {
			return core.TaskCost{}, "", fmt.Errorf("%w: configure the account-confirmed Sonilo %s tariff", core.ErrPricingUnavailable, op.priceField)
		}
		price, _ = money.Parse(op.rate)
	} else {
		source = "Operator-confirmed Sonilo account tariff"
	}
	quantity := float64(op.maxDuration)
	description := "Reserves the maximum supported input duration; actual duration is reported after generation. Includes the standard minimum billed duration; account discounts and trials are not assumed."
	if op.id == "text-to-music" {
		quantity = float64(duration(r.Options))
		description = "Requested music duration with a 10-second billing floor; account discounts and trials are not assumed."
	}
	if op.id == "text-to-sfx" {
		p, _ := core.NormalizeParameters(modelFor(op.id).Parameters, r.Options.Parameters)
		quantity = p["duration_seconds"].(float64)
		description = "Requested effect duration with a 3-second billing floor; account discounts and trials are not assumed."
	}
	quantity = math.Max(quantity, float64(op.floor))
	expected, err := price.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	snapshot := core.PricingSnapshot{Currency: "USD", Unit: "second", Price: price, Per: 1, Source: source, Version: "2026-10-09", CheckedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), EstimateDescription: description}
	return core.TaskCost{ExpectedUSD: expected, Status: "estimated", Pricing: snapshot, EstimatedUnits: quantity, QuotedAt: time.Now().UTC()}, "", nil
}

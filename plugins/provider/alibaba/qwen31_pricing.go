package alibaba

import (
	"time"
	"unicode"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// Independent exact-model standard tariffs; no 3.0/realtime family aliases.
// https://www.alibabacloud.com/help/en/model-studio/model-pricing
func qwen31Quote(endpoint string, model core.MediaModel, request core.MediaRequest, now time.Time) (core.TaskCost, error) {
	region, ok := qwen31Region(endpoint)
	if !ok || !isQwen31(request.Model) || model.ID != request.Model {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	if err := qwen31Validate(endpoint, request); err != nil {
		return core.TaskCost{}, err
	}
	p := core.PricingSnapshot{Currency: "USD", Unit: "token", Per: 1_000_000, Region: region,
		Source: alibabaPriceSource, Version: "qwen-audio-3.1/2026-11-08",
		CheckedAt: time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)}
	cost := core.TaskCost{Status: "estimated", Pricing: p, QuotedAt: now}
	if request.Model == "qwen-audio-3.1-tts-plus" {
		cost.Pricing.Unit, cost.Pricing.Per = "character", 10_000
		cost.Pricing.Price = money.USD(300_000) // USD 0.30 per 10,000 characters.
		if region == "Beijing" {
			cost.Pricing.Price = money.USD(200_000)
		}
		for _, r := range request.Prompt {
			cost.EstimatedUnits++
			if unicode.Is(unicode.Han, r) {
				cost.EstimatedUnits++
			}
		}
		var err error
		cost.ExpectedUSD, err = cost.Pricing.Price.Multiply(cost.EstimatedUnits, cost.Pricing.Per)
		return cost, err
	}
	input, output := money.USD(230_000), money.USD(1_870_000)
	if request.Model == qwen31Next {
		input = money.USD(930_000)
	}
	if region == "Beijing" {
		input, output = money.USD(212_000), money.USD(1_696_000)
		if request.Model == qwen31Next {
			input = money.USD(848_000)
		}
	}
	cost.QuantityUnknown = true
	cost.Pricing.Components = []core.PricingComponent{
		{Metric: "input_tokens", Price: input, Per: 1_000_000},
		{Metric: "output_tokens", Price: output, Per: 1_000_000},
	}
	cost.Pricing.EstimateDescription = "Verified input/output token tariffs; provider quantities are unknown. Duration is not converted to tokens."
	return cost, nil
}

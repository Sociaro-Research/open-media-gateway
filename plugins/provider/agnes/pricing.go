package agnes

import (
	"context"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

const priceSource = "https://www.agnes-ai.com/en/docs/pricing"

func (a *Adapter) Quote(ctx context.Context, _ core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.TaskCost{}, "", err
	}
	m := describeModel(r.Model)
	price := core.PricingSnapshot{Currency: "USD", Per: 1, Source: priceSource, Version: "2026-09-30", CheckedAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), Region: "International"}
	quantity := 1.0
	if m.MediaType == "image" || r.Model == "agnes-video-2.5-flash" {
		price.Unit = "request"
		price.EstimateDescription = "Published temporary $0 promotion; account eligibility and final billing are unverified."
	} else {
		price.Unit = "second"
		rate := "0.025"
		switch valueOr(r.Options.Resolution, "720P") {
		case "1080P", "1K":
			rate = "0.040"
		case "2K":
			rate = "0.055"
		}
		price.Price, _ = money.Parse(rate)
		duration := r.Options.Duration
		if duration == 0 {
			duration = 5
		}
		quantity = float64(duration)
		if len(r.Options.Videos()) > 0 {
			// Native URL references do not provide duration before acceptance.
			// Reserve the documented 12-second maximum instead of underquoting.
			quantity += 12
			price.EstimateDescription = "Includes a 12-second upper bound for the input video; actual reference duration is unverified."
			price.Components = []core.PricingComponent{{Metric: "output_video_duration", Price: price.Price, Per: 1}, {Metric: "input_video_duration", Price: price.Price, Per: 1}}
		}
		extra, _ := money.Parse("0.005")
		price.FixedUSD, _ = extra.Multiply(float64(max(0, len(r.Options.ImageURLs)-5)), 1)
	}
	expected, err := price.Price.Multiply(quantity, price.Per)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	return core.TaskCost{ExpectedUSD: expected + price.FixedUSD, Status: "estimated", Pricing: price, EstimatedUnits: quantity, QuotedAt: time.Now().UTC()}, "", nil
}

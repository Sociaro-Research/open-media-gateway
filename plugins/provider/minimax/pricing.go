package minimax

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"time"
)

type tariff struct {
	output, input, image money.USD
	free                 int
}

func h3Tariff(model, res string) tariff {
	t := tariff{free: 5}
	switch model {
	case "MiniMax-H3":
		t = tariff{80000, 80000, 40000, 5}
		if res == "2k" {
			t.output = 130000
			t.input = 130000
		}
	case "MiniMax-H3-Highspeed":
		t = tariff{60000, 60000, 30000, 5}
		if res == "480p" {
			t.output = 40000
			t.input = 40000
		}
	case "MiniMax-H3-Lite":
		t = tariff{40000, 40000, 20000, 5}
		if res == "480p" {
			t.output = 25000
			t.input = 25000
		}
	case "MiniMax-H3-Max":
		t = tariff{80000, 143000, 74000, 2}
		if res == "480p" {
			t.output = 50000
			t.input = 55300
		}
	}
	return t
}
func (a *Adapter) Quote(ctx context.Context, _ core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if r.Model == Regeneration {
		return a.quoteRegeneration(ctx, r)
	}
	if e := a.ValidateRequest(ctx, r); e != nil {
		return core.TaskCost{}, "", e
	}
	op, _ := operationFor(r.Model)
	m := describe(op)
	res := resolution(m, r.Options)
	dur := duration(m, r.Options)
	snapshot := core.PricingSnapshot{Currency: "USD", Source: priceSource, Version: "2026-10-31", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), Region: "Global", Per: 1, EstimateDescription: "Published list-price estimate; account discounts, packages and invoices are separate."}
	quantity := 1.
	var amount money.USD
	switch op.kind {
	case "context":
		return core.TaskCost{Status: "unknown", QuotedAt: time.Now().UTC(), Pricing: ContextIRTariff()}, "", core.ErrPricingUnavailable
	case "image":
		if r.Model != "image-01" {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		quantity = float64(imageCount(r.Options))
		snapshot.Unit = "image"
		snapshot.Price = 3500
		amount, _ = snapshot.Price.Multiply(quantity, 1)
	case "h3":
		t := h3Tariff(r.Model, res)
		quantity = float64(dur)
		input := 0.
		if len(r.Options.Videos()) > 0 {
			if measured, known := r.Options.InputVideoDuration(); known {
				input = measured
				snapshot.EstimateDescription += " Input-video quantity uses owner-inspected duration; final provider usage remains authoritative."
			} else {
				input = 15
				snapshot.EstimateDescription += " Reserves the documented 15-second total input-video limit until final provider usage is available."
			}
		}
		images := float64(max(0, len(r.Options.ImageURLs)-t.free))
		snapshot.Unit = "second"
		snapshot.Price = t.output
		snapshot.Components = []core.PricingComponent{{Metric: "output_video_duration", Price: t.output, Per: 1, EstimatedUnits: quantity}, {Metric: "input_video_duration", Price: t.input, Per: 1, EstimatedUnits: input}, {Metric: "billable_input_images", Price: t.image, Per: 1, EstimatedUnits: images}}
		for _, c := range snapshot.Components {
			part, e := c.Price.Multiply(c.EstimatedUnits, c.Per)
			if e != nil {
				return core.TaskCost{}, "", e
			}
			amount += part
		}
	case "legacy":
		switch r.Model {
		case "MiniMax-Hailuo-2.3", "MiniMax-Hailuo-02":
			amount = 280000
			if dur == 10 {
				amount = 560000
			}
			if res == "1080p" {
				amount = 490000
			}
			if res == "512p" {
				amount = 100000
				if dur == 10 {
					amount = 150000
				}
			}
		case "MiniMax-Hailuo-2.3-Fast":
			amount = 190000
			if dur == 10 {
				amount = 320000
			}
			if res == "1080p" {
				amount = 330000
			}
		default:
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		snapshot.Unit = "request"
		snapshot.Price = amount
	default:
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: snapshot}, "", nil
}

// ContextIRTariff is token-priced; no prompt-to-token conversion or output-token
// ceiling is published, so a pre-generation total must remain unknown.
func ContextIRTariff() core.PricingSnapshot {
	return core.PricingSnapshot{Currency: "USD", Unit: "token", Per: 1_000_000, Source: priceSource, Version: "2026-10-31", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), Components: []core.PricingComponent{{Metric: "prompt_tokens", Price: 900000, Per: 1_000_000}, {Metric: "completion_tokens", Price: 3600000, Per: 1_000_000}}}
}
func RegenerationTariff() core.PricingSnapshot {
	return core.PricingSnapshot{Currency: "USD", Unit: "second", Per: 1, Source: priceSource, Version: "2026-10-31", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), Components: []core.PricingComponent{{Metric: "output_video_duration", Price: 50000, Per: 1}, {Metric: "input_video_duration", Price: 50000, Per: 1}, {Metric: "billable_input_images", Price: 25000, Per: 1}}, EstimateDescription: "Regeneration is billed separately; original input materials are billed again, with the first five images free. The 768P base output is not an original reference-video charge."}
}

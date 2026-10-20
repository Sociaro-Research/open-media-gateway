package alibaba

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const tariffVersion = "2026-10-12.1"
const alibabaPriceSource = "https://www.alibabacloud.com/help/en/model-studio/model-pricing"

// Exact standard online prices reviewed against the complete public page.
// Regional IDs are retained; unknown protocols cannot use these price records.
//
//go:embed reviewed_tariffs.json
var reviewedTariffData []byte
var reviewedTariffs = func() map[string][]catalogsync.Rate {
	var rates map[string][]catalogsync.Rate
	if err := json.Unmarshal(reviewedTariffData, &rates); err != nil {
		panic(err)
	}
	return rates
}()

func quotePublic(endpoint string, model core.MediaModel, request core.MediaRequest, now time.Time) (core.TaskCost, error) {
	id, o := request.Model, request.Options
	if _, ok := describeDashModel(id, id); !ok {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	region := ""
	switch parsed.Hostname() {
	case "dashscope-intl.aliyuncs.com":
		region = "Singapore"
	case "dashscope.aliyuncs.com":
		region = "Beijing"
	}
	if region == "" {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	audio := model.AudioAlwaysOn || o.GenerateAudio != nil && *o.GenerateAudio
	hasVideo := len(o.Videos()) > 0
	var selected *catalogsync.Rate
	for _, rate := range reviewedTariffs[id] {
		if rate.Region != region || rate.Resolution != "" && rate.Resolution != o.Resolution || rate.Audio != nil && *rate.Audio != audio || rate.VideoInput != nil && *rate.VideoInput != hasVideo {
			continue
		}
		copy := rate
		selected = &copy
		break
	}
	if selected == nil {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	price := core.PricingSnapshot{Currency: "USD", Unit: selected.Unit, Per: selected.Per, Price: selected.Price, Source: alibabaPriceSource, Version: tariffVersion, CheckedAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Region: region}
	quantity := 1.0
	switch model.MediaType {
	case "image":
		if selected.InputImages != nil {
			price.FixedUSD, err = selected.InputImages.Price.Multiply(float64(max(0, len(o.ImageURLs)-selected.InputImages.Free)), 1)
			if err != nil {
				return core.TaskCost{}, err
			}
		}
	case "speech":
		quantity = 0
		for _, c := range request.Prompt {
			quantity++
			if unicode.Is(unicode.Han, c) {
				quantity++
			}
		}
	case "video":
		billInput := false
		price.BillInputVideo = &billInput
		quantity = float64(o.Duration)
		if model.DurationFromInput {
			quantity = float64(model.MaxDuration)
			price.EstimateDescription = "Reservation uses the maximum supported source duration; metering uses provider-reported usage."
		}
		if hasVideo {
			switch {
			case strings.HasPrefix(id, "wan2.7-r2v"), strings.HasPrefix(id, "wan2.6-r2v"):
				billInput = true
				bound := 5.0
				if strings.HasPrefix(id, "wan2.6-") {
					bound *= float64(len(o.Videos())) / float64(len(o.Videos())+len(o.ImageURLs))
				}
				quantity += bound
				price.EstimateDescription = "Reservation includes the maximum billable reference-video duration under the reviewed truncation rule; metering uses provider-reported input and output duration."
			case strings.HasPrefix(id, "wan3.0-"):
				billInput = true
				quantity += min(15.0, max(0.0, 30.0-quantity))
				price.EstimateDescription = "Reservation includes up to 15 seconds of combined input video, with at most 30 seconds of input and output combined; metering uses provider-reported usage."
			case id == "wan2.7-videoedit":
				billInput = true
				quantity *= 2
			case strings.HasPrefix(id, "wan2.7-i2v"), strings.Contains(id, "animate"), id == "happyhorse-1.0-video-edit":
				// Continuation is billed for the final output; animation input is free.
			default:
				return core.TaskCost{}, core.ErrPricingUnavailable
			}
		}
	default:
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	expected, err := price.Price.Multiply(quantity, price.Per)
	if err != nil {
		return core.TaskCost{}, fmt.Errorf("estimate cost: %w", err)
	}
	expected += price.FixedUSD
	return core.TaskCost{ExpectedUSD: expected, Status: "estimated", Pricing: price, EstimatedUnits: quantity, QuotedAt: now}, nil
}

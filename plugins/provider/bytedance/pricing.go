package bytedance

import (
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const tariffVersion = "2026-10-12.1"
const alibabaPriceSource = "https://www.alibabacloud.com/help/en/model-studio/model-pricing"
const bytePriceSource = "https://docs.byteplus.com/en/docs/modelark/1544106"

func quotePublic(endpoint string, model core.MediaModel, request core.MediaRequest, now time.Time) (core.TaskCost, error) {
	checked := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	price := core.PricingSnapshot{Currency: "USD", Per: 1, Version: tariffVersion, CheckedAt: checked}
	quantity := 1.0
	var rate string
	o, id := request.Options, request.Model

	u, _ := url.Parse(endpoint)
	if u == nil || u.Hostname() != "ark.ap-southeast.bytepluses.com" {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	price.Source, price.Region = bytePriceSource, "Singapore"
	if model.MediaType == "image" {
		price.Unit = "image"
		switch id {
		case "seedream-4-0-250828":
			rate = "0.03"
		case "seedream-4-5-251128":
			rate = "0.04"
		case "seedream-5-0-260128", "seedream-5-0-lite-260128":
			rate = "0.035"
		case "dola-seedream-5-0-flash-260915":
			rate = "0.018"
		case "dola-seedream-5-0-pro-260628":
			switch o.Resolution {
			case "1K", "1.5K":
				rate = "0.045"
			case "2K":
				rate = "0.09"
			}
			inputRate, _ := money.Parse("0.003")
			price.FixedUSD, _ = inputRate.Multiply(float64(max(0, len(o.ImageURLs)-1)), 1)
		}
	} else if model.Pricing.USDPerMillionTokens > 0 {
		if o.Ratio == "adaptive" || strings.Contains(id, "-lite-") {
			return core.TaskCost{}, core.ErrPricingUnavailable
		}
		price.Unit, price.Per = "token", 1_000_000
		value := model.Pricing.USDPerMillionByResolution[o.Resolution]
		if value == 0 {
			value = model.Pricing.USDPerMillionTokens
		}
		if o.GenerateAudio != nil && *o.GenerateAudio && model.Pricing.USDPerMillionAudioTokens > 0 {
			value = model.Pricing.USDPerMillionAudioTokens
		}
		rate = strconv.FormatFloat(value, 'f', 6, 64)
		size, ok := videoDimensions[o.Resolution][o.Ratio]
		if !ok {
			return core.TaskCost{}, core.ErrPricingUnavailable
		}
		frames := o.Duration * 24
		if id == "seedance-1-5-pro-251215" {
			// Observed Seedance 1.5 output includes the endpoint frame: a
			// four-second 640x640 clip has 97 frames and bills 38,800 tokens.
			// This remains an estimate; completion_tokens is authoritative.
			frames++
		}
		quantity = float64(frames*size[0]*size[1]) / 1024
		if strings.HasPrefix(id, "dreamina-seedance-2-") {
			var referenceRate string
			var estimateErr error
			quantity, referenceRate, estimateErr = video2xUnits(id, o)
			if estimateErr != nil {
				return core.TaskCost{}, estimateErr
			}
			if referenceRate != "" {
				rate = referenceRate
				maxInput := 15
				if id == "dreamina-seedance-2-5-260628" {
					maxInput = 30
				}
				price.EstimateDescription = fmt.Sprintf("Conservative reserve assumes %d seconds of total reference video and includes the provider token minimum. Metering uses actual completion_tokens.", maxInput)
			}
		} else if len(o.Videos()) > 0 {
			return core.TaskCost{}, core.ErrPricingUnavailable
		}
		// BytePlus converts draft token consumption, not the token tariff.
		// Returned completion_tokens already includes this conversion.
		if o.Draft != nil && *o.Draft {
			factor := model.Pricing.DraftSilentFactor
			if o.GenerateAudio != nil && *o.GenerateAudio {
				factor = model.Pricing.DraftAudioFactor
			}
			if factor <= 0 || factor > 1 {
				return core.TaskCost{}, core.ErrPricingUnavailable
			}
			quantity *= factor
		}
	}

	if rate == "" {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	parsed, err := money.Parse(rate)
	if err != nil || parsed <= 0 {
		return core.TaskCost{}, core.ErrPricingUnavailable
	}
	price.Price = parsed
	expected, err := price.Price.Multiply(quantity, price.Per)
	if err != nil {
		return core.TaskCost{}, fmt.Errorf("estimate cost: %w", err)
	}
	expected += price.FixedUSD
	return core.TaskCost{ExpectedUSD: expected, Status: "estimated", Pricing: price, EstimatedUnits: quantity, QuotedAt: now}, nil
}

var videoDimensions = map[string]map[string][2]int{
	"480p":  {"16:9": {864, 480}, "9:16": {480, 864}, "4:3": {736, 544}, "3:4": {544, 736}, "1:1": {640, 640}, "21:9": {960, 416}},
	"720p":  {"16:9": {1248, 704}, "9:16": {704, 1248}, "4:3": {1120, 832}, "3:4": {832, 1120}, "1:1": {960, 960}, "21:9": {1504, 640}},
	"1080p": {"16:9": {1920, 1088}, "9:16": {1088, 1920}, "4:3": {1664, 1248}, "3:4": {1248, 1664}, "1:1": {1440, 1440}, "21:9": {2176, 928}},
	"4k":    {"16:9": {3840, 2160}, "9:16": {2160, 3840}, "4:3": {3328, 2496}, "3:4": {2496, 3328}, "1:1": {2880, 2880}, "21:9": {4352, 1856}},
}

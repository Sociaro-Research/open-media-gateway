package bytedance

import (
	"math"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

// Reviewed against both official minimum-token tables on 2026-10-12:
// https://bytedance.larkoffice.com/wiki/H0fUwHPxtiHayOk6CVpcZzKqnHJ
// 192 Seedance 2.0 rows and 339 Seedance 2.5 rows (324 distinct configurations).
// Every row equals round((outputSeconds + ceil(2*outputSeconds/3))*pixels*24/1024).
// Keep the separate dimensions: legacy Seedance sizes differ from the 2.x tables.
var video2xDimensions = map[string]map[string][2]int{
	"480p":  {"16:9": {864, 496}, "9:16": {496, 864}, "4:3": {752, 560}, "3:4": {560, 752}, "1:1": {640, 640}, "21:9": {992, 432}},
	"720p":  {"16:9": {1280, 720}, "9:16": {720, 1280}, "4:3": {1112, 834}, "3:4": {834, 1112}, "1:1": {960, 960}, "21:9": {1470, 630}},
	"1080p": {"16:9": {1920, 1080}, "9:16": {1080, 1920}, "4:3": {1664, 1248}, "3:4": {1248, 1664}, "1:1": {1440, 1440}, "21:9": {2206, 946}},
	"4k":    {"16:9": {3840, 2160}, "9:16": {2160, 3840}, "4:3": {3326, 2494}, "3:4": {2494, 3326}, "1:1": {2880, 2880}, "21:9": {4398, 1886}},
}

func video2xUnits(id string, o core.MediaOptions) (float64, string, error) {
	maxDuration, maxInput := 15, 15
	var inputRate string
	switch id {
	case "dreamina-seedance-2-0-260128":
		switch o.Resolution {
		case "480p", "720p":
			inputRate = "4.3"
		case "1080p":
			inputRate = "4.7"
		case "4k":
			inputRate = "2.4"
		}
	case "dreamina-seedance-2-0-fast-260128":
		if o.Resolution == "480p" || o.Resolution == "720p" {
			inputRate = "3.3"
		}
	case "dreamina-seedance-2-0-mini-260615":
		if o.Resolution == "480p" || o.Resolution == "720p" {
			inputRate = "2.1"
		}
	case "dreamina-seedance-2-5-260628":
		maxDuration, maxInput = 30, 30
		switch o.Resolution {
		case "480p", "720p":
			inputRate = "6.4"
		case "1080p":
			inputRate = "7.0"
		}
	default:
		return 0, "", core.ErrPricingUnavailable
	}
	size, ok := video2xDimensions[o.Resolution][o.Ratio]
	if !ok || inputRate == "" || o.Duration < 4 || o.Duration > maxDuration || o.Draft != nil && *o.Draft {
		return 0, "", core.ErrPricingUnavailable
	}
	if id == "dreamina-seedance-2-5-260628" && o.Resolution == "480p" && (o.Ratio == "16:9" || o.Ratio == "9:16") {
		size = [2]int{854, 480}
	}
	unitsPerSecond := float64(size[0]*size[1]*24) / 1024
	if len(o.Videos()) == 0 {
		return unitsPerSecond * float64(o.Duration), "", nil
	}
	// Clip metadata is not trusted/probed during quoting. Reserve for the
	// maximum combined reference duration accepted by the provider (15/30 s).
	// MeterUsage later uses actual completion_tokens with the frozen tariff.
	floor := minimumVideoTokens(o.Duration, unitsPerSecond)
	return math.Max(floor, unitsPerSecond*float64(o.Duration+maxInput)), inputRate, nil
}

func minimumVideoTokens(duration int, unitsPerSecond float64) float64 {
	return math.Round(float64(duration+(2*duration+2)/3) * unitsPerSecond)
}

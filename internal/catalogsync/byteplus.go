package catalogsync

import (
	"regexp"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

var byteScalar = regexp.MustCompile(`^\$?([0-9]+(?:\.[0-9]+)?)\s*(?:USD)?$`)

func bytePlusTariff(id, unit string, cells []string) Entry {
	entry := Entry{ID: id, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Published tariff", Value: strings.Join(cells[1:], " | ")}}}}
	valid := true
	add := func(value, resolution string, audio *bool, input *ImageInputTariff) bool {
		price, err := money.Parse(value)
		if err != nil {
			valid = false
			return false
		}
		per := int64(1)
		if unit == "token" {
			per = 1_000_000
		}
		entry.Rates = append(entry.Rates, Rate{Region: "Singapore", Resolution: resolution, Audio: audio, InputImages: input, Unit: unit, Per: per, Price: price})
		return true
	}
	if strings.Contains(id, "seedance") && unit == "token" {
		if match := byteScalar.FindStringSubmatch(cells[1]); match != nil {
			add(match[1], "", nil, nil)
		} else if matches := regexp.MustCompile(`^Video with audio: ([0-9.]+) Video without audio: ([0-9.]+)$`).FindStringSubmatch(cells[1]); matches != nil {
			for i, audio := range []bool{true, false} {
				add(matches[i+1], "", &audio, nil)
			}
		} else {
			r := regexp.MustCompile(`(?i)For ([0-9pKk ,and]+) outputs?:\s*Input without video:\s*([0-9]+(?:\.[0-9]+)?)(?:\s*Input with video:\s*([0-9]+(?:\.[0-9]+)?))?`)
			for _, match := range r.FindAllStringSubmatch(cells[1], -1) {
				for _, res := range regexp.MustCompile(`\d+p|[24][kK]`).FindAllString(match[1], -1) {
					add(match[2], strings.ToLower(res), nil, nil)
					if match[3] != "" && add(match[3], strings.ToLower(res), nil, nil) {
						on := true
						entry.Rates[len(entry.Rates)-1].VideoInput = &on
					}
				}
			}
		}
	} else if unit == "image" {
		if match := byteScalar.FindStringSubmatch(cells[2]); match != nil && (strings.Trim(cells[1], " -") == "" || cells[1] == "Free") {
			add(match[1], "", nil, nil)
		} else if id == "dola-seedream-5-0-pro-260628" {
			input := regexp.MustCompile(`^First image: Free From the 2nd image: ([0-9.]+)$`).FindStringSubmatch(cells[1])
			// Only single-image generation is exposed by the adapter. Preserve
			// layer prices as reviewed evidence; changes still require review.
			output := regexp.MustCompile(`^Single image generation: ≤ 2\.61 million pixels \(1\.5K or lower\): ([0-9.]+) > 2\.61 million pixels \(higher than 1\.5K\): ([0-9.]+) Layer decomposition: ≤ 2\.61 million pixels \(1\.5K or lower\): ([0-9.]+) > 2\.61 million pixels \(higher than 1\.5K\): ([0-9.]+)$`).FindStringSubmatch(cells[2])
			if input != nil && output != nil {
				p, err := money.Parse(input[1])
				if err == nil {
					tariff := &ImageInputTariff{Free: 1, Price: p}
					add(output[1], "1K", nil, tariff)
					add(output[1], "1.5K", nil, tariff)
					add(output[2], "2K", nil, tariff)
					entry.Metadata.Facts = append(entry.Metadata.Facts, core.ModelFact{Label: "Provider: layer decomposition tariff (USD/image; operation unavailable)", Value: output[3] + " / " + output[4]})
				}
			}
		}
	}
	if !valid {
		entry.Rates = nil
	}
	videoRates := 0
	for _, rate := range entry.Rates {
		if rate.VideoInput != nil && *rate.VideoInput {
			videoRates++
		}
	}
	if len(entry.Rates) > 0 && strings.Contains(cells[1], "Input with video") && videoRates == 0 {
		entry.Metadata.Facts = append(entry.Metadata.Facts, core.ModelFact{Label: "Published tariff condition", Value: "Video-reference estimates need input clip durations and BytePlus minimum-token tables: " + cells[1]})
	}
	return entry
}

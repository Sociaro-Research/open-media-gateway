package catalogsync

import (
	"reflect"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// The redesigned media tables use page-level units and two header rows.
// Read only the explicit online columns; offline prices are not substitutes.
func bytePlusMediaTables(text string) ([]Entry, bool) {
	tokenUnit := strings.Contains(clean(text), "Token prices are USD per million tokens.")
	imageUnit := strings.Contains(clean(text), "Image generation prices are per image.")
	lines := strings.Split(text, "\n")
	section, inputMode := "", ""
	handled := false
	var entries []Entry
	index := map[string]int{}
	invalid := map[string]bool{}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		plain := clean(line)
		if strings.HasPrefix(line, "## ") {
			section = plain
			inputMode = ""
		}
		if strings.HasPrefix(line, "### ") {
			inputMode = ""
		}
		if line == "**Input without video**" {
			inputMode = "without"
		}
		if line == "**Input with video**" {
			inputMode = "with"
		}
		if plain == "Retiring models" {
			inputMode = ""
		}
		if !strings.HasPrefix(line, "|") {
			continue
		}
		var rows [][]string
		for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|"); i++ {
			raw := strings.Split(strings.Trim(strings.TrimSpace(lines[i]), "|"), "|")
			for j := range raw {
				raw[j] = clean(raw[j])
			}
			if strings.Trim(strings.Join(raw, ""), "-: ") == "" {
				continue
			}
			rows = append(rows, raw)
		}
		i--
		if len(rows) < 2 {
			continue
		}
		header := rows[0]
		video := section == "## Video generation models" && len(header) > 1 && header[0] == "Model"
		image := section == "## Image generation models" && reflect.DeepEqual(header, []string{"Model ID", "Input image", "Output image"})
		if !video && !image {
			continue
		}
		handled = true
		start := 1
		if video {
			start = 2
		}
		for _, cells := range rows[start:] {
			if len(cells) == 0 {
				continue
			}
			id := modelcatalog.CanonicalID("bytedance", mediaID.FindString(cells[0]))
			if id == "" {
				continue
			}
			entry := Entry{ID: id, Metadata: &core.ModelMetadata{Facts: []core.ModelFact{{Label: "Published tariff", Value: strings.Join(cells[1:], " | ")}}}}
			valid := false
			if image && imageUnit && len(cells) == 3 {
				normalized := append([]string(nil), cells...)
				if id == "dola-seedream-5-0-pro-260628" {
					normalizer := strings.NewReplacer("From the second image: $", "From the 2nd image: ", "> Pricing varies by image generation scenario.", "", "Single-image generation:", "Single image generation:", "Up to 2.61 million pixels", "≤ 2.61 million pixels", "More than 2.61 million pixels", "> 2.61 million pixels", "$", "")
					normalized[1] = strings.Join(strings.Fields(normalizer.Replace(cells[1])), " ")
					normalized[2] = strings.Join(strings.Fields(normalizer.Replace(cells[2])), " ")
				}
				parsed := bytePlusTariff(id, "image", normalized)
				entry.Rates = parsed.Rates
				if len(parsed.Metadata.Facts) > 1 {
					entry.Metadata.Facts = append(entry.Metadata.Facts, parsed.Metadata.Facts[1:]...)
				}
				valid = len(entry.Rates) > 0
			}
			if video && tokenUnit && len(rows) > 1 && len(cells) == 5 {
				var ok bool
				entry.Rates, ok = bytePlusVideoRow(id, header, rows[1], cells, inputMode)
				valid = ok
			}
			if !valid {
				entry.Rates = nil
				invalid[id] = true
			}
			if pos, exists := index[id]; exists {
				entries[pos].Metadata.Facts = append(entries[pos].Metadata.Facts, entry.Metadata.Facts...)
				for _, rate := range entry.Rates {
					duplicate := false
					for _, old := range entries[pos].Rates {
						if rateKey(old) == rateKey(rate) {
							duplicate = true
							if !reflect.DeepEqual(old, rate) {
								invalid[id] = true
							}
						}
					}
					if !duplicate {
						entries[pos].Rates = append(entries[pos].Rates, rate)
					}
				}
			} else {
				index[id] = len(entries)
				entries = append(entries, entry)
			}
		}
	}
	for i := range entries {
		if invalid[entries[i].ID] {
			entries[i].Rates = nil
		}
	}
	return entries, handled
}

func bytePlusVideoRow(id string, header, labels, cells []string, inputMode string) ([]Rate, bool) {
	var rates []Rate
	add := func(value, resolution string, audio *bool) bool {
		if value == "Not supported" {
			return true
		}
		match := byteScalar.FindStringSubmatch(value)
		if match == nil {
			return false
		}
		price, err := money.Parse(match[1])
		if err != nil {
			return false
		}
		rate := Rate{Region: "Singapore", Unit: "token", Per: 1000000, Price: price, Resolution: resolution, Audio: audio}
		if inputMode == "with" {
			on := true
			rate.VideoInput = &on
		}
		rates = append(rates, rate)
		return true
	}
	if reflect.DeepEqual(header, []string{"Model", "Online inference", "", "Offline inference"}) && reflect.DeepEqual(labels, []string{"", "Video with audio", "Video without audio", "Video with audio", "Video without audio"}) {
		for i, on := range []bool{true, false} {
			if !add(cells[i+1], "", &on) {
				return nil, false
			}
		}
		return rates, len(rates) > 0
	}
	if !reflect.DeepEqual(header, []string{"Model", "Online inference", "", "", "Offline inference"}) || !reflect.DeepEqual(labels, []string{"", "480p and 720p", "1080p", "4K", ""}) || (inputMode != "with" && inputMode != "without") {
		return nil, false
	}
	// These two documented rows span all online resolutions. Empty cells on a
	// resolution-specific model are incomplete data, never free/merged tariffs.
	if (id == "seedance-1-0-pro-250528" || id == "seedance-1-0-pro-fast-251015") && inputMode == "without" && cells[2] == "" && cells[3] == "" {
		return ratesWithScalar(cells[1])
	}
	for i, resolutions := range [][]string{{"480p", "720p"}, {"1080p"}, {"4k"}} {
		for _, res := range resolutions {
			if !add(cells[i+1], res, nil) {
				return nil, false
			}
		}
	}
	return rates, len(rates) > 0
}

func ratesWithScalar(value string) ([]Rate, bool) {
	match := byteScalar.FindStringSubmatch(value)
	if match == nil {
		return nil, false
	}
	price, err := money.Parse(match[1])
	if err != nil {
		return nil, false
	}
	return []Rate{{Region: "Singapore", Unit: "token", Per: 1000000, Price: price}}, true
}

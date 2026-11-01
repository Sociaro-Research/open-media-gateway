package kling

import (
	"errors"
	"regexp"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

var priceCell = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)? Units? \(\$([0-9]+(?:\.[0-9]+)?)\) /s$`)

// Interpret only exact reviewed routes and their output-duration tariff. Dollar
// amounts are explicit Balance prices, never a conversion from package units.
func interpretPrices(source catalogsync.Source, text string) ([]catalogsync.Entry, error) {
	if source.URL != priceMarkdownSource && source.URL != priceSource {
		return nil, nil
	}
	if !strings.Contains(text, "> Source: "+priceSource) || !strings.Contains(text, "> Locale: en") || strings.Contains(strings.ToLower(text), "preview") {
		return nil, errors.New("Kling price source is incomplete, unreviewed or preview; retain last confirmed rates")
	}
	rows := map[string][]catalogsync.Rate{}
	seen := map[string]bool{}
	header := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.Join(strings.Fields(cells[i]), " ")
		}
		if len(cells) == 6 && cells[0] == "Model" {
			if strings.Join(cells, "|") != "Model|Features|Billing Type|Price (720P)|Price (1080P)|Price (4K)" {
				return nil, errors.New("Kling pricing columns changed")
			}
			header = true
			continue
		}
		if !header || len(cells) != 6 {
			continue
		}
		var ids []string
		var audio *bool
		key := ""
		switch {
		case cells[0] == "Kling 4.0" && strings.HasPrefix(cells[1], "No Input Video ("):
			ids = []string{TextToVideo4, ImageToVideo4}
			key = "4-no-video"
			if cells[5] != "-" {
				return nil, errors.New("Kling 4.0 price source adds an unreviewed resolution")
			}
		case cells[0] == "Kling 3.0" && cells[1] == "No Native Audio":
			ids = []string{TextToVideo, ImageToVideo}
			value := false
			audio = &value
			key = "3-silent"
		case cells[0] == "Kling 3.0" && cells[1] == "With Native Audio":
			ids = []string{TextToVideo, ImageToVideo}
			value := true
			audio = &value
			key = "3-native"
		default:
			continue
		}
		if seen[key] || cells[2] != "Per second (Total duration = Output duration)" {
			return nil, errors.New("Kling pricing is duplicated or its billing rule changed")
		}
		seen[key] = true
		for i, res := range []string{"720p", "1080p", "4k"} {
			if key == "4-no-video" && res == "4k" {
				continue
			}
			match := priceCell.FindStringSubmatch(cells[3+i])
			if len(match) != 2 {
				return nil, errors.New("Kling Balance USD/second price is missing or changed")
			}
			price, err := money.Parse(match[1])
			if err != nil || price <= 0 {
				return nil, errors.New("Kling Balance price is invalid")
			}
			video := false
			for _, id := range ids {
				rows[id] = append(rows[id], catalogsync.Rate{Region: "Singapore", Resolution: res, Audio: audio, VideoInput: &video, Unit: "second", Per: 1, Price: price, Source: source.URL})
			}
		}
	}
	if len(seen) != 3 {
		return nil, errors.New("Kling supported tariff rows are missing; retain last confirmed rates")
	}
	result := []catalogsync.Entry{}
	for _, id := range []string{TextToVideo, ImageToVideo, TextToVideo4, ImageToVideo4} {
		result = append(result, catalogsync.Entry{ID: id, Rates: rows[id]})
	}
	return result, nil
}

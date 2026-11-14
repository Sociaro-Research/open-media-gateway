package catalogsync

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func openAIPriceFixture(id string, rows [][]any, headings []any) []byte {
	props := map[string]any{"headings": []any{0, headings}, "groups": []any{0, []any{map[string]any{"model": []any{0, id}, "rows": []any{0, rows}}}}}
	body, _ := json.Marshal(props)
	return []byte(`<html><main><div class="pricing-section-meta">Prices per 1M tokens unless noted.</div><astro-island component-export="GroupedPricingTable" props="` + html.EscapeString(string(body)) + `"></astro-island></main></html>`)
}
func TestOpenAICharacterTariffsAndUnsupportedChanges(t *testing.T) {
	headings := []any{"Model", "Modality", "Input", "Cached input", "Output / cost"}
	for _, tc := range []struct {
		id, cell string
		want     money.USD
	}{{"tts-1", "$15.00 / 1M characters", 15_000_000}, {"tts-1-hd", "$30.00 / 1M characters", 30_000_000}} {
		body := openAIPriceFixture(tc.id, [][]any{{"Text", tc.cell, "-", "-"}}, headings)
		entries, _, err := parseOpenAIPrices(body)
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 1 {
			t.Fatal(entries, err)
		}
		r := entries[0].Rates[0]
		if r.Unit != "character" || r.Per != 1_000_000 || r.Price != tc.want || len(r.Components) != 0 {
			t.Fatal(r)
		}
		for _, unit := range []string{"1K characters", "1M bytes", "minute"} {
			entries, _, err = parseOpenAIPrices([]byte(strings.ReplaceAll(string(body), "1M characters", unit)))
			if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 || entries[0].Metadata == nil {
				t.Fatal("unknown unit silently accepted", unit, entries, err)
			}
		}
	}
	for _, rows := range [][][]any{
		{{"Text", 5.0, "-", 10.0}, {"Image", "$8 per image", "-", 30.0}},
		{{"Text", 5.0, "-", 10.0}, {"Video", 8.0, "-", 30.0}},
		{{"Text", 5.0, "-", 10.0, 42.0}},
	} {
		entries, _, err := parseOpenAIPrices(openAIPriceFixture("gpt-image-1.5", rows, headings))
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 || entries[0].Metadata == nil {
			t.Fatal("partial tariff silently accepted", entries, err)
		}
	}
	for _, hs := range [][]any{{"Model", "Modality", "Output", "Cached input", "Output / cost"}, nil} {
		entries, _, err := parseOpenAIPrices(openAIPriceFixture("gpt-image-1", [][]any{{"Text", 5.0, "-", 40.0}}, hs))
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 {
			t.Fatal("unknown columns accepted", entries, err)
		}
	}
	standard := openAIPriceFixture("gpt-image-1", [][]any{{"Text", 5.0, "-", 40.0}}, headings)
	batch := strings.ReplaceAll(string(standard), "40", "20")
	body := []byte(strings.Replace(string(standard), "</main>", `<div data-value="batch">`+batch+`</div></main>`, 1))
	entries, _, err := parseOpenAIPrices(body)
	if err != nil || len(entries) != 1 || entries[0].Rates[0].Components[1].Price != 40_000_000 {
		t.Fatal("batch price mixed into standard", entries, err)
	}
}

func TestOpenAIMultimodalHeadingUnit(t *testing.T) {
	// Multimodal section of the 2026-11-14 pricing page (full download SHA-256
	// 099c9496f75ffe6f08a57f30b007c5abf79f6eb26ecba5ddecf50ffdf4ec8028). Astro
	// props are verbatim; rendered table markup, scripts and icons were removed.
	body, err := os.ReadFile("testdata/openai-pricing-20261114.html")
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := parseOpenAIPrices(body)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		parts := []string{}
		for _, r := range e.Rates {
			if r.Unit == "character" {
				parts = append(parts, fmt.Sprintf("character=%g", r.Price.Float64()))
			}
			for _, c := range r.Components {
				parts = append(parts, fmt.Sprintf("%s=%g", c.Metric, c.Price.Float64()))
			}
		}
		got[e.ID] = strings.Join(parts, " ")
	}
	image2 := "image_input_tokens=8 image_output_tokens=30 text_input_tokens=5"
	image15 := "image_input_tokens=8 image_output_tokens=32 text_input_tokens=5 text_output_tokens=10"
	audio := "audio_input_tokens=32 audio_output_tokens=64 text_input_tokens=2.5 text_output_tokens=10"
	want := map[string]string{
		"gpt-image-2.6-sunburst": image2,
		"gpt-image-2.6-flare":    image2,
		"gpt-image-2.5-sunburst": image2,
		"gpt-image-2.5-flare":    image2,
		"gpt-image-2":            image2,
		"gpt-image-1.5":          image15,
		"chatgpt-image-latest":   image15,
		"gpt-image-1-mini":       "image_input_tokens=2.5 image_output_tokens=8 text_input_tokens=2",
		"gpt-audio-1.5":          audio,
		"gpt-audio":              audio,
		"gpt-audio-mini":         "audio_input_tokens=10 audio_output_tokens=20 text_input_tokens=0.6 text_output_tokens=2.4",
		"gpt-4o-mini-tts":        "audio_output_tokens=12 text_input_tokens=0.6",
		"tts-1":                  "character=15",
		"tts-1-hd":               "character=30",
	}
	if len(entries) != len(want) || !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	realtime := `<div class="pricing-subsection pricing-section-heading pricing-multimodal-subsection"><div class="anchor-heading-wrapper"><h4 id="audio-tokens"`
	for _, change := range [][2]string{
		{"Prices per 1M tokens unless noted.", "Prices per 1K tokens unless noted."},
		{realtime, "<h2>Video</h2>" + realtime},
		{realtime, `<p class="pricing-section-meta">Prices per second.</p>` + realtime},
	} {
		changed := strings.Replace(string(body), change[0], change[1], 1)
		if changed == string(body) {
			t.Fatal("fixture no longer matches", change[0])
		}
		if _, _, err := parseOpenAIPrices([]byte(changed)); err == nil {
			t.Fatal("unit accepted without its own section note", change[1])
		}
	}
}

func TestOpenAIReorderedPriceColumns(t *testing.T) {
	for _, tc := range []struct {
		id   string
		rows [][]any
	}{
		{"gpt-image-1.5", [][]any{{"Image", 8.0, 2.0, 32.0}, {"Text", 5.0, 1.25, 10.0}}},
		{"gpt-audio", [][]any{{"Audio", 32.0, "-", 64.0}, {"Text", 2.5, "-", 10.0}}},
		{"gpt-4o-mini-tts", [][]any{{"Audio", "-", "-", 12.0}, {"Text", 0.6, "-", "-"}}},
		{"tts-1", [][]any{{"Text", "$15.00 / 1M characters", "-", "-"}}},
		{"tts-1-hd", [][]any{{"Text", "$30.00 / 1M characters", "-", "-"}}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			baseline, _, err := parseOpenAIPrices(openAIPriceFixture(tc.id, tc.rows, []any{"Model", "Modality", "Input", "Cached input", "Output"}))
			if err != nil || len(baseline) != 1 || len(baseline[0].Rates) != 1 {
				t.Fatal(baseline, err)
			}
			for _, order := range [][3]int{{1, 3, 2}, {3, 2, 1}, {2, 1, 3}} {
				labels := []string{"Modality", "Input", "Cached input", "Output / cost"}
				headings := []any{"Model", "Modality"}
				rows := [][]any{}
				for _, column := range order {
					headings = append(headings, map[string]any{"__pricingTooltipHeading": []any{0, map[string]any{"label": []any{0, labels[column]}}}})
				}
				for _, row := range tc.rows {
					rows = append(rows, []any{row[0], row[order[0]], row[order[1]], row[order[2]]})
				}
				entries, _, err := parseOpenAIPrices(openAIPriceFixture(tc.id, rows, headings))
				if err != nil || !reflect.DeepEqual(entries, baseline) {
					t.Fatal(order, entries, baseline, err)
				}
			}
		})
	}
	for _, headings := range [][]any{
		{"Model", "Modality", "Input", "Output", "Discount"},
		{"Model", "Modality", "Input", "Output", "Output / cost"},
		{"Model", "Modality", "Input", "Input", "Cached input"},
		{"Model", "Modality", "Input", "Output"},
	} {
		entries, _, err := parseOpenAIPrices(openAIPriceFixture("gpt-image-1", [][]any{{"Image", 8.0, 32.0, 2.0}}, headings))
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 || entries[0].Metadata == nil {
			t.Fatal("ambiguous columns accepted", entries, err)
		}
	}
}

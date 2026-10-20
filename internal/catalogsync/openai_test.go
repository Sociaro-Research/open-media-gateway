package catalogsync

import (
	"encoding/json"
	"html"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
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
	for _, hs := range [][]any{{"Model", "Modality", "Output", "Cached input", "Input"}, nil} {
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

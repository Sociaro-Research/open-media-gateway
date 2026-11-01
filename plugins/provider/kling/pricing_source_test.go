package kling

import (
	"os"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestPublishedPriceInterpretationUsesBalanceDollarsAndExactCapabilities(t *testing.T) {
	raw, err := os.ReadFile("testdata/pricing.md")
	if err != nil {
		t.Fatal(err)
	}
	source := catalogsync.Source{URL: priceMarkdownSource, Parser: "document"}
	entries, err := interpretPrices(source, string(raw))
	if err != nil || len(entries) != 4 {
		t.Fatal(entries, err)
	}
	for _, entry := range entries {
		want := map[string]money.USD{"720p": 157000, "1080p": 235000}
		if !isKling4(entry.ID) {
			want = map[string]money.USD{"720p": 84000, "1080p": 112000, "4k": 420000}
		}
		expected := 2
		if !isKling4(entry.ID) {
			expected = 6
		}
		if len(entry.Rates) != expected {
			t.Fatal(entry)
		}
		for _, r := range entry.Rates {
			price := want[r.Resolution]
			if r.Audio != nil && *r.Audio && r.Resolution != "4k" {
				price = price * 3 / 2
			}
			if r.Price != price || r.Per != 1 || r.Unit != "second" || r.Region != "Singapore" || r.VideoInput == nil || *r.VideoInput || r.Source != priceMarkdownSource {
				t.Fatal(entry)
			}
			if isKling4(entry.ID) && r.Audio != nil {
				t.Fatal("unreviewed 4.0 audio switch")
			}
		}
	}
	changed := strings.Replace(string(raw), "($0.235)", "($0.240)", 1)
	entries, err = interpretPrices(source, changed)
	if err != nil || entries[2].Rates[1].Price != 240000 {
		t.Fatal("new explicit USD price not imported", entries, err)
	}
	for name, text := range map[string]string{
		"HTML shell":       "<html><body>JavaScript required</body></html>",
		"wrong currency":   strings.ReplaceAll(string(raw), "($0.235)", "(CNY0.235)"),
		"units only":       strings.ReplaceAll(string(raw), " ($0.235)", ""),
		"missing row":      strings.Replace(string(raw), "| Kling 3.0 | No Native Audio", "| Unknown | No Native Audio", 1),
		"changed header":   strings.Replace(string(raw), "Price (720P)", "Price (480P)", 1),
		"changed billing":  strings.Replace(string(raw), "Total duration = Output duration)", "Total duration = Input duration)", 1),
		"preview":          string(raw) + "\nPreview prices",
		"untrusted locale": strings.Replace(string(raw), "> Locale: en", "> Locale: zh", 1),
		"duplicate":        string(raw) + "\n" + strings.Split(string(raw), "\n")[5],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := interpretPrices(source, text); err == nil {
				t.Fatal("unsafe pricing accepted")
			}
		})
	}
	if entries, err := interpretPrices(catalogsync.Source{URL: "https://other.example/prices.md"}, string(raw)); err != nil || len(entries) != 0 {
		t.Fatal("unknown source parsed")
	}
}

package catalogsync

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"strings"
	"testing"
)

// Reduced HTML fixtures preserve the cell wording published by Alibaba on
// 2026-11-20: https://www.alibabacloud.com/help/en/model-studio/model-pricing.
func TestAlibabaReviewedPriceNotation(t *testing.T) {
	for _, tc := range []struct{ name, header, value string }{
		{"legacy", "Output unit price", "$0.24/second"},
		{"seconds abbreviation", "Output unit price", "$0.24/s"},
		{"plural combined header", "Input and output unit prices", "$0.24/s"},
		{"original price", "Input and output unit prices", "Original$0.24/ s (30% off for a limited time)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "<h2>Singapore</h2><table><tr><th>Model ID</th><th>Resolution</th><th>" + tc.header + "</th></tr><tr><td>happyhorse-1.0-i2v</td><td>1080p</td><td>" + tc.value + "</td></tr></table>"
			entries, _, err := parseAlibabaPrices([]byte(body))
			if err != nil || len(entries) != 1 || len(entries[0].Rates) != 1 {
				t.Fatalf("rates not parsed: %+v, %v", entries, err)
			}
			rate := entries[0].Rates[0]
			want, _ := money.Parse("240")
			if rate.Region != "Singapore" || rate.Resolution != "1080p" || rate.Unit != "second" || rate.Per != 1000 || rate.Price != want {
				t.Fatalf("incorrect tariff: %+v", rate)
			}
			if !strings.Contains(entries[0].Metadata.Facts[0].Value, tc.value) {
				t.Fatal("original source evidence lost")
			}
		})
	}
}

func TestAlibabaUnreviewedPriceNotationRemainsEvidence(t *testing.T) {
	for _, value := range []string{
		"$0.24/minute", "€0.24/s", "$0.24/s after 100 seconds", "Original$0.24/s (new billing condition)",
		"Original$0.24/s (30% off for a limited time) plus $0.01/s", "$0.24/s (30% off for a limited time)",
		"Original$0.24/s (130% off for a limited time)",
	} {
		t.Run(value, func(t *testing.T) {
			body := "<h2>Singapore</h2><table><tr><th>Model ID</th><th>Input and output unit prices</th></tr><tr><td>happyhorse-1.0-i2v</td><td>" + value + "</td></tr></table>"
			entries, _, err := parseAlibabaPrices([]byte(body))
			if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 || entries[0].Metadata == nil {
				t.Fatalf("unsafe tariff acceptance or evidence loss: %+v %v", entries, err)
			}
		})
	}
	for _, extra := range []string{"<th>New billing condition</th>", "<th>Output video mode</th>"} {
		body := "<h2>Singapore</h2><table><tr><th>Model ID</th><th>Input and output unit prices</th>" + extra + "</tr><tr><td>wan3.0-video</td><td>$0.24/s</td><td>unreviewed</td></tr></table>"
		entries, _, err := parseAlibabaPrices([]byte(body))
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 {
			t.Fatalf("unknown conditions accepted: %+v %v", entries, err)
		}
	}
}

func TestAlibabaDigitalHumanPriceNotation(t *testing.T) {
	for _, unit := range []string{"second", " s"} {
		for _, suffix := range []string{"", " plus scene surcharge", "econdary"} {
			value := "<p>Output video:</p><ul><li><p>480P: $0.071677/" + unit + "</p></li><li><p>720P: $0.129018/" + unit + "</p></li></ul>" + suffix
			entries, _, err := parseAlibabaPrices([]byte("<h2>China (Beijing)</h2><table><tr><th>Model ID</th><th>Unit price</th></tr><tr><td>wan2.2-s2v</td><td>" + value + "</td></tr></table>"))
			if err != nil || len(entries) != 1 {
				t.Fatal(entries, err)
			}
			wantCount := 2
			if suffix != "" {
				wantCount = 0
			}
			if len(entries[0].Rates) != wantCount {
				t.Fatalf("%s %s: %+v", unit, suffix, entries[0].Rates)
			}
			if wantCount == 2 {
				for i, amount := range []string{"71.677", "129.018"} {
					want, _ := money.Parse(amount)
					r := entries[0].Rates[i]
					if r.Price != want || r.Unit != "second" || r.Per != 1000 || r.Region != "Beijing" {
						t.Fatalf("incorrect rate: %+v", r)
					}
				}
			}
		}
	}
}

func TestAlibabaDigitalHumanUnreviewedDimensions(t *testing.T) {
	for _, header := range []string{"Output video mode", "Input price", "Resolution", "New billing condition"} {
		body := "<h2>China (Beijing)</h2><table><tr><th>Model ID</th><th>Unit price</th><th>" + header + "</th></tr><tr><td>wan2.2-s2v</td><td>Output video: 480P: $0.071677/s 720P: $0.129018/s</td><td>unreviewed</td></tr></table>"
		entries, _, err := parseAlibabaPrices([]byte(body))
		if err != nil || len(entries) != 1 || len(entries[0].Rates) != 0 {
			t.Fatalf("%s accepted: %+v %v", header, entries, err)
		}
	}
}

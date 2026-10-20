package bytedance

import (
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func TestVideoReferenceReserveAndActualTokenMetering(t *testing.T) {
	for _, tc := range []struct{ id, resolution, rate, reserve string }{
		{"dreamina-seedance-2-0-260128", "480p", "4.300000", "0.863784"},
		{"dreamina-seedance-2-0-260128", "720p", "4.300000", "1.857600"},
		{"dreamina-seedance-2-0-260128", "1080p", "4.700000", "4.568400"},
		{"dreamina-seedance-2-0-260128", "4k", "2.400000", "9.331200"},
		{"dreamina-seedance-2-0-fast-260128", "480p", "3.300000", "0.662904"},
		{"dreamina-seedance-2-0-mini-260615", "720p", "2.100000", "0.907200"},
		{"dreamina-seedance-2-5-260628", "480p", "6.400000", "2.152080"},
		{"dreamina-seedance-2-5-260628", "720p", "6.400000", "4.838400"},
		{"dreamina-seedance-2-5-260628", "1080p", "7.000000", "11.907000"},
	} {
		t.Run(tc.id+tc.resolution, func(t *testing.T) {
			request := core.MediaRequest{Model: tc.id, Options: core.MediaOptions{Duration: 5, Resolution: tc.resolution, Ratio: "16:9", VideoURLs: []string{"https://example.test/one.mp4", "https://example.test/two.mp4"}}}
			cost, err := quotePublic("https://ark.ap-southeast.bytepluses.com", describeModel(tc.id), request, time.Now())
			if err != nil || cost.Pricing.Price.String() != tc.rate || cost.ExpectedUSD.String() != tc.reserve || !strings.Contains(cost.Pricing.EstimateDescription, "Conservative reserve") {
				t.Fatal(cost, err)
			}
			before := cost.ExpectedUSD
			if !cost.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "completion_tokens", Quantity: 90_000}}}) || cost.ExpectedUSD != before || cost.BilledUnits == nil || *cost.BilledUnits != 90_000 {
				t.Fatal("metering reused the conservative estimate", cost)
			}
		})
	}
}

func TestVideoMinimumTableDimensionsAndReserveCoverage(t *testing.T) {
	// Independent published floor samples from every resolution/aspect group,
	// including fractional-token rounding and the 30-second Seedance 2.5 limit.
	for _, tc := range []struct {
		resolution, ratio string
		duration, floor   int
	}{
		{"480p", "16:9", 5, 90396}, {"480p", "4:3", 5, 88830}, {"480p", "1:1", 5, 86400}, {"480p", "21:9", 5, 90396},
		{"720p", "16:9", 5, 194400}, {"720p", "4:3", 5, 195625}, {"720p", "1:1", 5, 194400}, {"720p", "21:9", 5, 195349},
		{"1080p", "16:9", 5, 437400}, {"1080p", "4:3", 5, 438048}, {"1080p", "1:1", 5, 437400}, {"1080p", "21:9", 5, 440200},
		{"4k", "16:9", 5, 1749600}, {"4k", "4:3", 5, 1749736}, {"4k", "1:1", 5, 1749600}, {"4k", "21:9", 5, 1749648},
	} {
		o := core.MediaOptions{Resolution: tc.resolution, Ratio: tc.ratio, Duration: tc.duration, VideoURL: "https://example.test/ref.mp4"}
		units, _, err := video2xUnits("dreamina-seedance-2-0-260128", o)
		if err != nil || units < float64(tc.floor) {
			t.Fatal("reserve below published minimum", tc, units, err)
		}
		o.VideoURL = ""
		normal, _, err := video2xUnits("dreamina-seedance-2-0-260128", o)
		if err != nil || minimumVideoTokens(o.Duration, normal/float64(o.Duration)) != float64(tc.floor) {
			t.Fatal("reviewed minimum differs from published table", tc, normal, err)
		}
	}
	for _, id := range []string{"dreamina-seedance-2-0-260128", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-mini-260615", "dreamina-seedance-2-5-260628"} {
		m := describeModel(id)
		for _, res := range m.Resolutions {
			for _, ratio := range []string{"16:9", "9:16", "4:3", "3:4", "1:1", "21:9"} {
				for d := m.MinDuration; d <= m.MaxDuration; d++ {
					if _, _, err := video2xUnits(id, core.MediaOptions{Resolution: res, Ratio: ratio, Duration: d, VideoURL: "https://example.test/ref.mp4"}); err != nil {
						t.Fatal(id, res, ratio, d, err)
					}
				}
			}
		}
	}
	for _, o := range []core.MediaOptions{{Resolution: "4k", Ratio: "16:9", Duration: 5}, {Resolution: "720p", Ratio: "adaptive", Duration: 5}, {Resolution: "720p", Ratio: "16:9", Duration: 31}} {
		if _, _, err := video2xUnits("dreamina-seedance-2-5-260628", o); err == nil {
			t.Fatal("unreviewed configuration admitted", o)
		}
	}
}

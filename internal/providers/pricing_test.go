package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	registry "github.com/Sociaro-Research/media-gateway-dev/plugins/provider"
	"testing"
	"time"
)

func TestRegionalQuotesAndFrozenMetering(t *testing.T) {
	tests := []struct {
		id, media, endpoint, resolution, prompt, expected string
		duration                                          int
		images                                            []string
	}{
		{"wan3.0-video-prime", "video", "https://dashscope-intl.aliyuncs.com/api/v1", "480p", "test", "0.476000", 7, nil},
		{"wan3.0-video-prime", "video", "https://dashscope.aliyuncs.com/api/v1", "480p", "test", "0.445200", 7, nil},
		{"qwen-image-3.0-pro", "image", "https://dashscope-intl.aliyuncs.com/api/v1", "2048*2048", "test", "0.081000", 0, []string{"a", "b"}},
		{"qwen3-tts-flash", "speech", "https://dashscope-intl.aliyuncs.com/api/v1", "", "Hello", "0.000050", 0, nil},
		{"qwen3-tts-flash", "speech", "https://dashscope-intl.aliyuncs.com/api/v1", "", "中A文123", "0.000080", 0, nil},
	}
	for _, tt := range tests {
		t.Run(tt.id+tt.endpoint, func(t *testing.T) {
			quote, err := quotePublic("alibaba", tt.endpoint, core.MediaModel{MediaType: tt.media}, core.MediaRequest{Model: tt.id, Prompt: tt.prompt, Options: core.MediaOptions{Duration: tt.duration, Resolution: tt.resolution, ImageURLs: tt.images}}, time.Now())
			if err != nil || quote.ExpectedUSD.String() != tt.expected {
				t.Fatalf("quote=%+v err=%v", quote, err)
			}
			if tt.media == "video" {
				if !quote.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "output_video_duration", Quantity: 8}, {Name: "duration", Quantity: 8}}}) {
					t.Fatal("no metering")
				}
				if quote.ActualUSD != nil || quote.ExpectedUSD.String() != tt.expected {
					t.Fatal("metered cost overwritten expectation or pretended to be invoice")
				}
			}
		})
	}
	_, err := quotePublic("alibaba", "https://proxy.example/api", core.MediaModel{MediaType: "video"}, core.MediaRequest{Model: "wan3.0-video-prime"}, time.Now())
	if !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("invented a proxy price")
	}
}

func TestBytePlusUsageUsesTokensNotDurationGuess(t *testing.T) {
	model := core.MediaModel{MediaType: "video", Pricing: core.MediaModelPricing{Known: true, USDPerMillionTokens: 5.6}}
	cost, err := quotePublic("bytedance", "https://ark.ap-southeast.bytepluses.com/api/v3", model, core.MediaRequest{Model: "dreamina-seedance-2-0-fast-260128", Options: core.MediaOptions{Duration: 7, Resolution: "480p", Ratio: "16:9"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	expected := cost.ExpectedUSD
	if !cost.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "completion_tokens", Quantity: 70726}, {Name: "total_tokens", Quantity: 99999}}}) {
		t.Fatal("metering failed")
	}
	if cost.MeteredUSD.String() != "0.396066" || cost.ExpectedUSD != expected || cost.ActualUSD != nil {
		t.Fatalf("wrong cost: %+v", cost)
	}
}

func TestBillNormalizationRequiresPaidUSD(t *testing.T) {
	tests := []struct {
		currency, paid, unpaid string
		settled                bool
	}{{"USD", "0.55", "0", true}, {"USD", "0", "0.55", false}, {"CNY", "0.55", "0", false}, {"USD", "0.0000001", "0", false}, {"USD", "0.55", "", false}}
	for _, tt := range tests {
		bill, err := normalizeBill("bytedance", "2026-09", map[string]any{"BillDetailId": "detail-1", "InstanceNo": "task-1", "Currency": tt.currency, "PaidAmount": tt.paid, "UnpaidAmount": tt.unpaid})
		if err != nil || bill.Settled != tt.settled {
			t.Fatalf("%+v: %v", bill, err)
		}
	}
	// BSS numbers must survive the raw JSON decoder without the generated float32 conversion.
	bill, err := normalizeBill("alibaba", "2026-09", map[string]any{"InstanceID": "task-1", "BillingItemCode": "tokens", "Currency": "USD", "PaymentAmount": json.Number("12345.123456"), "OutstandingAmount": json.Number("0")})
	if err != nil || bill.PaidUSD == nil || *bill.PaidUSD != money.USD(12345123456) {
		t.Fatalf("BSS precision: %+v %v", bill, err)
	}
}

// Regression from a real 640x640, 24fps Seedance 1.5 result: 97 frames,
// completion_tokens=38800 and a bill for 38.8 K tokens at $0.0024/K.
func TestSeedance15FrameEstimateAndDraftMetering(t *testing.T) {
	for _, draft := range []bool{false, true} {
		t.Run(fmt.Sprintf("draft_%v", draft), func(t *testing.T) {
			audio := true
			model := core.MediaModel{MediaType: "video", Pricing: core.MediaModelPricing{Known: true, USDPerMillionTokens: 1.2, USDPerMillionAudioTokens: 2.4, DraftAudioFactor: .6}}
			cost, err := quotePublic("bytedance", "https://ark.ap-southeast.bytepluses.com/api/v3", model, core.MediaRequest{Model: "seedance-1-5-pro-251215", Options: core.MediaOptions{Duration: 4, Resolution: "480p", Ratio: "1:1", GenerateAudio: &audio, Draft: &draft}}, time.Now())
			units, amount := 38800.0, money.USD(93120)
			if draft {
				units, amount = 23280, 55872
			}
			if err != nil || cost.EstimatedUnits != units || cost.ExpectedUSD != amount || cost.Pricing.Price != 2400000 {
				t.Fatalf("quote=%+v err=%v", cost, err)
			}
			if !cost.MeterUsage(*core.NormalizeProviderUsage(map[string]any{"completion_tokens": units, "total_tokens": units + 100})) || *cost.MeteredUSD != amount {
				t.Fatalf("draft conversion applied twice or wrong token counter: %+v", cost)
			}
			if cost.ActualUSD != nil {
				t.Fatal("token metering pretended to be a settled bill")
			}
		})
	}
}

func quotePublic(id, endpoint string, m core.MediaModel, r core.MediaRequest, t time.Time) (core.TaskCost, error) {
	d, _ := registry.Find(id)
	return d.Quote(endpoint, m, r, t)
}
func normalizeBill(id, month string, row map[string]any) (core.BillRecord, error) {
	d, _ := registry.Find(id)
	return d.NormalizeBill(month, row)
}

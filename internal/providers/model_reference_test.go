package providers

import (
	"context"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/openai"
	"testing"
	"time"
)

func TestOpenAIReferenceUsesRequestEstimator(t *testing.T) {
	adapter, err := openai.New("fixture", "https://api.openai.com/v1", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"gpt-image-2": "0.053340", "gpt-image-2.5-flare": "0.013830", "gpt-image-2.5-sunburst": "0.013830", "gpt-image-1-mini": "0.011264"}
	for id, want := range cases {
		m := core.MediaModel{ID: id, MediaType: "image", Resolutions: []string{"1024x1024"}, Qualities: []string{"medium"}}
		request, _ := referenceRequest(m)
		cost, _, err := adapter.Quote(context.Background(), m, request)
		if err != nil || cost.ExpectedUSD.String() != want {
			t.Fatalf("%s: %s %v", id, cost.ExpectedUSD.String(), err)
		}
	}
}

func TestAuditedPricesAndUnsafeReferenceConfigurations(t *testing.T) {
	cases := []struct {
		provider, id, media, region, resolution, want string
		duration                                      int
		images                                        []string
	}{
		{"alibaba", "wan2.6-image", "image", "https://dashscope-intl.aliyuncs.com/api/v1", "1280*1280", "0.030000", 0, nil},
		{"alibaba", "qwen-image-2.1-pro", "image", "https://dashscope.aliyuncs.com/api/v1", "1024*1024", "0.035333", 0, []string{"fixture"}},
		{"alibaba", "happyhorse-1.1-t2v", "video", "https://dashscope.aliyuncs.com/api/v1", "480p", "0.309423", 5, nil},
		{"bytedance", "dola-seedream-5-0-pro-260628", "image", "https://ark.ap-southeast.bytepluses.com/api/v3", "1K", "0.051000", 0, []string{"a", "b", "c"}},
	}
	for _, c := range cases {
		cost, err := quotePublic(c.provider, c.region, core.MediaModel{MediaType: c.media}, core.MediaRequest{Model: c.id, Options: core.MediaOptions{Resolution: c.resolution, Duration: c.duration, ImageURLs: c.images}}, time.Now())
		if err != nil || cost.ExpectedUSD.String() != c.want {
			t.Fatalf("%s: %s %v", c.id, cost.ExpectedUSD.String(), err)
		}
	}
	for _, provider := range []string{"bytedance", "alibaba"} {
		endpoint, id := "https://dashscope-intl.aliyuncs.com/api/v1", "wan3.0-video"
		if provider == "bytedance" {
			endpoint, id = "https://ark.ap-southeast.bytepluses.com/api/v3", "dreamina-seedance-2-0-260128"
		}
		m := core.MediaModel{MediaType: "video", Pricing: core.MediaModelPricing{Known: true, USDPerMillionTokens: 7}}
		for _, o := range []core.MediaOptions{{VideoURL: "https://test/video"}, {VideoURLs: []string{"https://test/video"}}} {
			o.Duration, o.Resolution, o.Ratio = 5, "480p", "16:9"
			if _, err := quotePublic(provider, endpoint, m, core.MediaRequest{Model: id, Options: o}, time.Now()); err != nil {
				t.Fatal("video-input quote does not match reviewed provider support")
			}
		}
	}
	// Metering remains based on published token rates, even when Mini's planning
	// estimate comes from the provider's separate per-image table.
	a, _ := openai.New("fixture", "https://api.openai.com/v1", nil, 0)
	c, _, _ := a.Quote(context.Background(), core.MediaModel{}, core.MediaRequest{Model: "gpt-image-1-mini", Prompt: "test"})
	if !c.MeterUsage(core.ProviderUsage{Metrics: []core.UsageMetric{{Name: "text_input_tokens", Quantity: 0}, {Name: "image_input_tokens", Quantity: 0}, {Name: "image_output_tokens", Quantity: 1000}}}) || *c.MeteredUSD != money.USD(8000) {
		t.Fatal("metered Mini cost did not use actual tokens")
	}
}

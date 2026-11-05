package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestImage26DiscoveryIsExactAndRequiresAccountListing(t *testing.T) {
	for _, body := range []string{`{"data":[]}`, `{"data":[{"id":"gpt-image-2.6-flare"},{"id":"gpt-image-2.6-sunburst"},{"id":"gpt-image-2.6-flare-2026-11-04"},{"id":"gpt-image-2.6"}]}`} {
		a := adapter(t, func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" || r.URL.Path != "/v1/models" {
				t.Fatal(r.URL)
			}
			return response(200, "application/json", body), nil
		})
		models, err := a.ListModels(context.Background())
		want := 0
		if strings.Contains(body, "flare") {
			want = 2
		}
		if err != nil || len(models) != want {
			t.Fatal(models, err)
		}
	}
	if DefaultModel != "gpt-image-2.5-flare" || Definition().DefaultModel != DefaultModel {
		t.Fatal("existing default changed")
	}
}

func TestImage26BoundedContractAndSerialization(t *testing.T) {
	for _, id := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		m := describeModel(id)
		if !reflect.DeepEqual(m.Resolutions, []string{"1024x1024", "1536x1024", "1024x1536"}) || !reflect.DeepEqual(m.Qualities, []string{"medium", "low", "high"}) || !reflect.DeepEqual(m.Backgrounds, []string{"opaque"}) {
			t.Fatal(m)
		}
		for _, edit := range []bool{false, true} {
			for _, complexity := range []string{"", "low", "medium", "high"} {
				for _, format := range m.Formats {
					a := adapter(t, func(r *http.Request) (*http.Response, error) {
						var p map[string]any
						if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
							t.Fatal(err)
						}
						path := "/v1/images/generations"
						if edit {
							path = "/v1/images/edits"
						}
						if r.Method != "POST" || r.URL.Path != path || r.Header.Get("Content-Type") != "application/json" || p["model"] != id || p["size"] != "1536x1024" || p["quality"] != "low" || p["complexity"] != valueOr(complexity, "high") || p["output_format"] != format || p["background"] != "opaque" || p["n"] != float64(1) {
							t.Fatal(r.URL, p)
						}
						wantKeys := 8
						if edit {
							wantKeys++
							refs := p["images"].([]any)
							if len(refs) != 2 || refs[0].(map[string]any)["image_url"] != "https://example.test/a.png" || refs[1].(map[string]any)["image_url"] != "data:image/png;base64,YQ==" {
								t.Fatal(p)
							}
						}
						if len(p) != wantKeys {
							t.Fatal("unexpected wire control", p)
						}
						return response(200, "application/json", `{"data":[{"b64_json":"aW1hZ2U="}]}`), nil
					})
					r := core.MediaRequest{Model: id, Prompt: "Synthetic fixture", Options: core.MediaOptions{Resolution: "1536x1024", Quality: "low", Format: format}}
					if complexity != "" {
						r.Options.Parameters = map[string]any{"complexity": complexity}
					}
					if edit {
						r.Options.ImageURLs = []string{"https://example.test/a.png", "data:image/png;base64,YQ=="}
					}
					result, err := a.Generate(context.Background(), r)
					if err != nil || result.Usage != nil || result.ResultURL != "data:image/"+format+";base64,aW1hZ2U=" {
						t.Fatal(result, err)
					}
				}
			}
		}
	}
}

func TestImage26RejectsUnreviewedControlsBeforeNetwork(t *testing.T) {
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached network", r.URL)
		return nil, nil
	})
	for _, options := range []core.MediaOptions{
		{Resolution: "2048x2048"}, {Resolution: "1536x864"}, {Resolution: "auto"}, {Quality: "xhigh"}, {Quality: "max"}, {Quality: "auto"},
		{Background: "transparent"}, {Background: "auto"}, {Format: "gif"}, {Format: "svg"},
		{Parameters: map[string]any{"complexity": "max"}}, {Parameters: map[string]any{"complexity": nil}}, {Parameters: map[string]any{"complexity": 1}},
		{Parameters: map[string]any{"markup": "annotation"}}, {Parameters: map[string]any{"frame_count": 9}}, {Parameters: map[string]any{"input_fidelity": "high"}},
		{ImageURLs: make([]string, 17)}, {ImageURLs: []string{"data:image/gif;base64,YQ=="}}, {ImageURLs: []string{"https://user:pass@example.test/a.png"}},
	} {
		r := core.MediaRequest{Model: "gpt-image-2.6-flare", Prompt: "fixture", Options: options}
		if _, err := a.Generate(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal(options, err)
		}
	}
	for _, id := range []string{"gpt-image-2.6-flare-2026-11-04", "gpt-image-2.6", "gpt-image-2.7-flare"} {
		if _, err := a.Generate(context.Background(), core.MediaRequest{Model: id, Prompt: "fixture"}); err == nil {
			t.Fatal("unknown model accepted", id)
		}
	}
	for _, id := range []string{DefaultModel, "gpt-image-2", "tts-1"} {
		if err := a.ValidateRequest(context.Background(), core.MediaRequest{Model: id, Prompt: "fixture", Options: core.MediaOptions{Parameters: map[string]any{"complexity": "high"}}}); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal("2.6 control leaked to legacy", id, err)
		}
	}
	// The JSON URL limit includes the full encoded data URL and is inclusive.
	base := "https://example.test/"
	for _, n := range []int{20_971_520, 20_971_521} {
		r := core.MediaRequest{Model: "gpt-image-2.6-flare", Prompt: "fixture", Options: core.MediaOptions{ImageURLs: []string{base + strings.Repeat("a", n-len(base))}}}
		if err := a.ValidateRequest(context.Background(), r); (err == nil) != (n == 20_971_520) {
			t.Fatal(n, err)
		}
	}
}

func TestImage26CalculatorAndMetering(t *testing.T) {
	a := adapter(t, nil)
	for _, id := range []string{"gpt-image-2.6-flare", "gpt-image-2.6-sunburst"} {
		for _, tc := range []struct {
			size, quality string
			tokens        float64
		}{
			{"1024x1024", "low", 823}, {"1024x1024", "medium", 1449}, {"1024x1024", "high", 1756},
			{"1536x1024", "low", 664}, {"1536x1024", "medium", 1132}, {"1536x1024", "high", 1372},
			{"1024x1536", "low", 664}, {"1024x1536", "medium", 1132}, {"1024x1536", "high", 1372},
		} {
			r := core.MediaRequest{Model: id, Prompt: "x", Options: core.MediaOptions{Resolution: tc.size, Quality: tc.quality}}
			c, _, err := a.Quote(context.Background(), describeModel(id), r)
			if err != nil || outputTokens(id, tc.size, tc.quality) != tc.tokens || c.Pricing.Version != "openai-image-2.6-2026-11-05" || !strings.Contains(c.Pricing.EstimateDescription, "no separate fee") {
				t.Fatal(tc, c, err)
			}
			want, _ := money.USD(30_000_000).Multiply(tc.tokens, 1_000_000)
			text, _ := money.USD(5_000_000).Multiply(33, 1_000_000)
			if c.ExpectedUSD != want+text {
				t.Fatal(tc, c.ExpectedUSD, want+text)
			}
		}
		c, _, _ := a.Quote(context.Background(), describeModel(id), core.MediaRequest{Model: id, Prompt: "x"})
		for _, raw := range []string{
			`{"input_tokens":10,"output_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":0}}`,
			`{"input_tokens":10,"output_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":0},"output_tokens_details":{"text_tokens":0,"image_tokens":100}}`,
		} {
			if !c.MeterUsage(*imageUsage(json.RawMessage(raw), id)) || *c.MeteredUSD != money.USD(3050) || c.ActualUSD != nil {
				t.Fatal(c, raw)
			}
		}
		for _, raw := range []string{
			`{}`, `null`, `{"output_tokens":100}`, `{"input_tokens":11,"output_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":0}}`,
			`{"output_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":0},"output_tokens_details":{"image_tokens":100}}`,
			`{"output_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":0},"output_tokens_details":{"text_tokens":1,"image_tokens":99}}`,
			`{"output_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":0},"output_tokens_details":{"text_tokens":0,"image_tokens":99}}`,
		} {
			c.MeteredUSD = nil
			u := imageUsage(json.RawMessage(raw), id)
			if u != nil && c.MeterUsage(*u) || c.MeteredUSD != nil {
				t.Fatal("unknown usage invented cost", c, raw)
			}
		}
	}
}

func TestImage26UnexpectedAuthoringUsageIsNotPricedAsRaster(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return response(200, "application/json", `{"data":[{"b64_json":"aW1hZ2U="}],"svg_usage":{"model":"unreviewed-author","input_tokens":10,"output_tokens":20},"usage":{"input_tokens":10,"output_tokens":100,"input_tokens_details":{"text_tokens":10,"image_tokens":0}}}`), nil
	})
	result, err := a.Generate(context.Background(), core.MediaRequest{Model: "gpt-image-2.6-flare", Prompt: "fixture"})
	if err != nil || result.ResultURL == "" || result.Usage != nil {
		t.Fatal("unpriced authoring tokens settled as raster", result, err)
	}
}

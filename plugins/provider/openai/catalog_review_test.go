package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestReviewedSpeechSnapshotsUseExactWireIDAndTariff(t *testing.T) {
	for id, parent := range tariffAliases() {
		if strings.Contains(id, "image") {
			continue
		}
		t.Run(id, func(t *testing.T) {
			a := adapter(t, func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["model"] != id {
					t.Fatal("snapshot wire ID lost", payload)
				}
				if strings.HasPrefix(id, "gpt-audio") {
					if r.URL.Path != "/v1/chat/completions" {
						t.Fatal(r.URL)
					}
					return response(200, "application/json", `{"choices":[{"finish_reason":"stop","message":{"audio":{"data":"YXVkaW8="}}}],"usage":{"prompt_tokens":10,"completion_tokens":100,"completion_tokens_details":{"audio_tokens":80}}}`), nil
				}
				if r.URL.Path != "/v1/audio/speech" {
					t.Fatal(r.URL)
				}
				return response(200, "audio/mpeg", "audio"), nil
			})
			req := core.MediaRequest{Model: id, Prompt: "Hello"}
			if _, err := a.Generate(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			cost, _, err := a.Quote(context.Background(), describeModel(id), req)
			req.Model = parent
			base, _, baseErr := a.Quote(context.Background(), describeModel(parent), req)
			if err != nil || baseErr != nil || cost.ExpectedUSD != base.ExpectedUSD {
				t.Fatal(cost, base, err, baseErr)
			}
			if parent == "tts-1-hd" && cost.Pricing.Price != 30_000_000 {
				t.Fatal("HD snapshot charged standard TTS price")
			}
		})
	}
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("invalid model reached network", r.URL)
		return nil, nil
	})
	for _, id := range []string{"gpt-4o-mini-tts-2099-01-01", "gpt-audio-2099-01-01", "gpt-image-2-2099-01-01", "tts-1-hd-2099", "sora-2", "sora-2-pro"} {
		if _, err := a.Generate(context.Background(), core.MediaRequest{Model: id, Prompt: "x"}); err == nil {
			t.Fatal("unreviewed or retired model accepted", id)
		}
	}
}

func TestLegacyImageTextReasoningUsageIsPricedWithoutGuessing(t *testing.T) {
	a := adapter(t, nil)
	for _, id := range []string{"gpt-image-1.5", "chatgpt-image-latest", "gpt-image-1.5-2025-12-16"} {
		cost, _, err := a.Quote(context.Background(), describeModel(id), core.MediaRequest{Model: id, Prompt: "draw"})
		if err != nil {
			t.Fatal(err)
		}
		usage := imageUsage(json.RawMessage(`{"input_tokens":10,"output_tokens":120,"input_tokens_details":{"text_tokens":10,"image_tokens":0},"output_tokens_details":{"image_tokens":100,"text_tokens":20}}`), id)
		if !cost.MeterUsage(*usage) || *cost.MeteredUSD != money.USD(3450) {
			t.Fatal("reasoning output missing or charged twice", cost, usage)
		}
		for _, raw := range []string{
			`{"output_tokens":120,"input_tokens_details":{"text_tokens":10,"image_tokens":0}}`,
			`{"output_tokens":120,"input_tokens_details":{"text_tokens":10,"image_tokens":0},"output_tokens_details":{"image_tokens":100}}`,
			`{"output_tokens":120,"input_tokens_details":{"text_tokens":10,"image_tokens":0},"output_tokens_details":{"image_tokens":100,"text_tokens":30}}`,
		} {
			cost.MeteredUSD = nil
			if cost.MeterUsage(*imageUsage(json.RawMessage(raw), id)) || cost.MeteredUSD != nil {
				t.Fatal("incomplete output breakdown invented a cost", raw)
			}
		}
	}
}

func TestMiniPlanningUsesItsPublishedSizeQualityTable(t *testing.T) {
	a := adapter(t, nil)
	for quality, amounts := range map[string][2]money.USD{"low": {5000, 6000}, "medium": {11000, 15000}, "high": {36000, 52000}} {
		for _, size := range []string{"1024x1024", "1536x1024", "1024x1536"} {
			cost, _, err := a.Quote(context.Background(), describeModel("gpt-image-1-mini"), core.MediaRequest{Model: "gpt-image-1-mini", Prompt: "x", Options: core.MediaOptions{Resolution: size, Quality: quality}})
			if err != nil {
				t.Fatal(err)
			}
			var output money.USD
			for _, c := range cost.Pricing.Components {
				if c.Metric == "image_output_tokens" {
					output, err = c.Price.Multiply(c.EstimatedUnits, c.Per)
				}
			}
			index := 0
			if size != "1024x1024" {
				index = 1
			}
			if err != nil || output != amounts[index] {
				t.Fatal(size, quality, output, amounts[index], err)
			}
		}
	}
}

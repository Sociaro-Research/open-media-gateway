package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

const priceSource = "https://developers.openai.com/api/docs/pricing"

func rates(id string) map[string]float64 {
	id = canonicalModel(id)
	switch id {
	case "gpt-image-1-mini":
		return map[string]float64{"text_input_tokens": 2, "image_input_tokens": 2.5, "image_output_tokens": 8}
	case "gpt-image-1":
		return map[string]float64{"text_input_tokens": 5, "image_input_tokens": 10, "image_output_tokens": 40}
	case "gpt-image-1.5", "chatgpt-image-latest":
		return map[string]float64{"text_input_tokens": 5, "image_input_tokens": 8, "image_output_tokens": 32, "text_output_tokens": 10}
	case "gpt-4o-mini-tts":
		return map[string]float64{"text_input_tokens": 0.6, "audio_output_tokens": 12}
	case "gpt-audio-1.5", "gpt-audio":
		return map[string]float64{"text_input_tokens": 2.5, "text_output_tokens": 10, "audio_input_tokens": 32, "audio_output_tokens": 64}
	case "gpt-audio-mini":
		return map[string]float64{"text_input_tokens": 0.6, "text_output_tokens": 2.4, "audio_input_tokens": 10, "audio_output_tokens": 20}
	default:
		return map[string]float64{"text_input_tokens": 5, "image_input_tokens": 8, "image_output_tokens": 30}
	}
}

// Quote keeps vendor tariff rules inside the adapter. The manager attaches the
// owner-specific credential fingerprint using the common CostQuoter interface.
func (a *Adapter) Quote(ctx context.Context, _ core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.TaskCost{}, "", err
	}
	now := time.Now().UTC()
	p := core.PricingSnapshot{Currency: "USD", Unit: "multi_metric", Per: 1, Source: priceSource, Version: "openai-2026-10-12.1", CheckedAt: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), Region: "standard"}
	c := core.TaskCost{Status: "estimated", QuotedAt: now, Pricing: p}
	id := canonicalModel(r.Model)
	if strings.HasPrefix(r.Model, "tts-1") {
		rate := money.USD(15 * money.Scale)
		if id == "tts-1-hd" {
			rate = 30 * money.USD(money.Scale)
		}
		c.Pricing.Unit = "character"
		c.Pricing.Price = rate
		c.Pricing.Per = 1_000_000
		c.EstimatedUnits = float64(utf8.RuneCountInString(r.Prompt))
		c.ExpectedUSD, _ = rate.Multiply(c.EstimatedUnits, 1_000_000)
		return c, "", nil
	}
	// Byte count is a deliberately conservative local text token estimate, not
	// a tokenizer result. Reference dimensions are unknown before generation.
	quantities := map[string]float64{"text_input_tokens": float64(len(r.Prompt)+len(r.Options.Instructions)) + 32}
	switch {
	case strings.Contains(r.Model, "image"):
		quantities["image_input_tokens"] = 4096 * float64(len(r.Options.ImageURLs))
		quantities["image_output_tokens"] = outputTokens(r.Model, valueOr(r.Options.Resolution, "1024x1024"), valueOr(r.Options.Quality, "medium"))
		// Legacy Image 1.5 models can bill internal text/reasoning output even
		// though the public result is an image. This allowance is a planning
		// heuristic, not an API-enforced maximum or measured usage.
		if id == "gpt-image-1.5" || id == "chatgpt-image-latest" {
			quantities["text_output_tokens"] = 4096
		}
		if id == "gpt-image-1-mini" {
			// Express the separate published per-image planning amount in token
			// equivalents at its reviewed $8/M tariff. This is only an estimate:
			// settlement still uses actual response tokens. A tariff refresh can
			// now reprice the entire estimate without losing the Mini table.
			quantities["image_output_tokens"] = miniOutputEstimate(valueOr(r.Options.Resolution, "1024x1024"), valueOr(r.Options.Quality, "medium")) * 1_000_000 / 8
		}
	case id == "gpt-4o-mini-tts":
		// Planning heuristic: 900 characters/minute, 1,250 audio tokens/minute.
		// Binary Speech has no usage response; this remains an estimate.
		quantities["audio_output_tokens"] = math.Ceil(float64(utf8.RuneCountInString(r.Prompt)) * 1250 / 900)
	default:
		// Chat replies are not narration: their length cannot be inferred from the
		// input. Reserve the full configured completion limit at the higher rate.
		quantities["audio_output_tokens"] = 4096
		quantities["text_output_tokens"] = 0
		quantities["audio_input_tokens"] = 0 // This adapter sends text-only Chat messages.
	}
	tariff := rates(r.Model)
	explanation := []string{}
	names := make([]string, 0, len(tariff))
	for name := range tariff {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		rate, err := money.Parse(fmt.Sprintf("%.6f", tariff[name]))
		if err != nil {
			return core.TaskCost{}, "", err
		}
		c.Pricing.Components = append(c.Pricing.Components, core.PricingComponent{Metric: name, Price: rate, Per: 1_000_000, EstimatedUnits: quantities[name]})
		part, err := rate.Multiply(quantities[name], 1_000_000)
		if err != nil {
			return core.TaskCost{}, "", err
		}
		c.ExpectedUSD += part
		if id != "gpt-image-1-mini" || name != "image_output_tokens" {
			explanation = append(explanation, fmt.Sprintf("%g estimated %s × $%s / 1,000,000", quantities[name], strings.ReplaceAll(name, "_", " "), rate.String()))
		}
	}
	if id == "gpt-image-1-mini" {
		explanation = append(explanation, fmt.Sprintf("%g planning token equivalents from the published Mini size/quality table × $8 / 1,000,000 (not measured tokens)", quantities["image_output_tokens"]))
	}
	c.Pricing.EstimateDescription = strings.Join(explanation, " + ") + " = $" + c.ExpectedUSD.String() + ". Text input uses UTF-8 bytes plus 32 overhead tokens. These are planning estimates; actual usage can differ."
	if strings.Contains(r.Model, "image") {
		c.Pricing.EstimateDescription += " Each input image reserves 4,096 tokens."
		if tariff["text_output_tokens"] > 0 {
			c.Pricing.EstimateDescription += " Internal text/reasoning output assumes 4,096 tokens; this is a heuristic, not an enforced upper bound."
		}
	}
	if id == "gpt-4o-mini-tts" {
		c.Pricing.EstimateDescription += " Speech duration assumes 900 characters/minute and 1,250 output audio tokens/minute."
	}
	if strings.HasPrefix(r.Model, "gpt-audio") {
		c.Pricing.EstimateDescription += " Spoken replies reserve the full 4,096 output token allowance."
	}
	return c, "", nil
}

func miniOutputEstimate(size, quality string) float64 {
	table := map[string][2]float64{"low": {0.005, 0.006}, "medium": {0.011, 0.015}, "high": {0.036, 0.052}}
	i := 0
	if size != "1024x1024" {
		i = 1
	}
	return table[quality][i]
}

// Output estimates follow the official image generation guide's calculator
// (reviewed 2026-09-11). This is not a guarantee of final token consumption.
func outputTokens(id, size, quality string) float64 {
	var w, h int
	if _, err := fmt.Sscanf(size, "%dx%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return 0
	}
	if strings.HasPrefix(id, "gpt-image-2") {
		cells := map[string]float64{"low": 16, "medium": 48, "high": 96}
		if strings.HasPrefix(id, "gpt-image-2.5") {
			cells = map[string]float64{"low": 16, "medium": 24, "high": 48, "xhigh": 64, "max": 96}
		}
		long := cells[quality]
		ratio := math.Max(float64(w), float64(h)) / math.Min(float64(w), float64(h))
		short := math.RoundToEven(long / ratio)
		return math.Ceil(long * short * (2_000_000 + float64(w*h)) / 4_000_000)
	}
	// Earlier GPT Image models: output token counts documented per size/quality.
	table := map[string][3]float64{"low": {272, 400, 408}, "medium": {1056, 1568, 1584}, "high": {4160, 6208, 6240}}
	index := 0
	if w > h {
		index = 1
	}
	if h > w {
		index = 2
	}
	return table[quality][index]
}

func imageUsage(raw json.RawMessage, model string) *core.ProviderUsage {
	var value struct {
		Input   *float64 `json:"input_tokens"`
		Output  *float64 `json:"output_tokens"`
		Details struct {
			Text  *float64 `json:"text_tokens"`
			Image *float64 `json:"image_tokens"`
		} `json:"input_tokens_details"`
		OutputDetails *struct {
			Text  *float64 `json:"text_tokens"`
			Image *float64 `json:"image_tokens"`
		} `json:"output_tokens_details"`
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	usage := &core.ProviderUsage{Raw: append(json.RawMessage(nil), raw...), CostStatus: "metered"}
	if json.Unmarshal(raw, &value) != nil {
		return usage
	}
	addMetric(usage, "text_input_tokens", value.Details.Text)
	addMetric(usage, "image_input_tokens", value.Details.Image)
	if value.OutputDetails != nil {
		d := value.OutputDetails
		if d.Text != nil && d.Image != nil && value.Output != nil && *d.Text+*d.Image != *value.Output {
			return usage
		}
		addMetric(usage, "image_output_tokens", d.Image)
		addMetric(usage, "text_output_tokens", d.Text)
	} else if rates(model)["text_output_tokens"] == 0 {
		// Legacy responses without a breakdown are unambiguous only for a
		// model whose reviewed tariff has no text output. Otherwise leave cost
		// pending rather than pricing a mixed total as image tokens or zero text.
		addMetric(usage, "image_output_tokens", value.Output)
	}
	return usage
}

func audioUsage(raw json.RawMessage) *core.ProviderUsage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	usage := &core.ProviderUsage{Raw: append(json.RawMessage(nil), raw...), CostStatus: "metered"}
	var value struct {
		Input         *float64 `json:"prompt_tokens"`
		Output        *float64 `json:"completion_tokens"`
		OutputDetails struct {
			Audio *float64 `json:"audio_tokens"`
		} `json:"completion_tokens_details"`
	}
	if json.Unmarshal(raw, &value) != nil {
		return usage
	}
	// This adapter only sends text inputs; all prompt tokens are text tokens.
	addMetric(usage, "text_input_tokens", value.Input)
	zero := 0.0
	addMetric(usage, "audio_input_tokens", &zero) // No audio is sent by this adapter.
	addMetric(usage, "audio_output_tokens", value.OutputDetails.Audio)
	if value.Output != nil && value.OutputDetails.Audio != nil && *value.Output >= *value.OutputDetails.Audio {
		text := *value.Output - *value.OutputDetails.Audio
		addMetric(usage, "text_output_tokens", &text)
	}
	return usage
}

func addMetric(usage *core.ProviderUsage, name string, value *float64) {
	if value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 {
		usage.Metrics = append(usage.Metrics, core.UsageMetric{Name: name, Unit: "token", Quantity: *value})
	}
}

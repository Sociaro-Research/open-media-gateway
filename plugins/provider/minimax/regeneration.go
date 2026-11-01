package minimax

import (
	"context"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const Regeneration = "MiniMax-H3-Regeneration"

func regenerationModel() core.MediaModel {
	return core.MediaModel{ID: Regeneration, DisplayName: "MiniMax H3 · Regenerate an owned 768P task to 2K", MediaType: "video", NoPrompt: true, PromptOptional: true, NoSeed: true, NoLanguage: true, NoInstructions: true, Resolutions: []string{"2k"}, DurationFromInput: true, ReferenceModes: []core.ReferenceMode{{ID: "none", Label: "Owned source task"}}, Parameters: []core.ParameterField{{Key: "source_task", Label: "Owned source-task handle", Type: "text", Required: true, MaxLength: 2048}}, Metadata: &core.ModelMetadata{CheckedAt: "2026-10-31", Description: "Regenerates a succeeded, recent MiniMax-H3 768P generation. Source-task method requires provider whitelist access; this is not arbitrary video editing.", Sources: []core.ModelSource{{Title: "Video regeneration", URL: docsBase + "api-reference/video-generation-v2-regeneration"}, {Title: "Regeneration pricing", URL: priceSource}}}, Pricing: core.MediaModelPricing{ReferenceDescription: "Output USD 0.05/s, original reference-video input USD 0.05/s and original images beyond five USD 0.025/image, charged again. Total requires an owned source task with complete reported usage."}}
}
func (a *Adapter) validateRegeneration(ctx context.Context, r core.MediaRequest) error {
	m := regenerationModel()
	o := r.Options
	if r.Prompt != "" {
		return invalid("source-task regeneration inherits the original prompt")
	}
	if _, e := core.NormalizeModelOptions(m, o); e != nil {
		return e
	}
	if o.Resolution != "" && o.Resolution != "2k" || o.Ratio != "" || o.Duration != 0 || o.OutputCount != 0 || o.GenerateAudio != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.ResourceReferences) > 0 || len(o.ResourceSnapshots) > 0 || len(o.ImageURLs)+len(o.Videos())+len(o.Audios()) > 0 {
		return invalid("source-task regeneration accepts only its source handle, 2K resolution and optional AIGC watermark")
	}
	p, e := core.NormalizeParameters(m.Parameters, o.Parameters)
	if e != nil {
		return e
	}
	h, e := a.open(ctx, p["source_task"].(string))
	if e != nil {
		return e
	}
	if h.Kind != "h3" || h.Model != "MiniMax-H3" {
		return invalid("regeneration requires an owned original H3 generation")
	}
	return nil
}
func (a *Adapter) quoteRegeneration(ctx context.Context, r core.MediaRequest) (core.TaskCost, string, error) {
	if e := a.validateRegeneration(ctx, r); e != nil {
		return core.TaskCost{}, "", e
	}
	p, _ := core.NormalizeParameters(regenerationModel().Parameters, r.Options.Parameters)
	h, e := a.open(ctx, p["source_task"].(string))
	if e != nil {
		return core.TaskCost{}, "", e
	}
	t, e := a.queryNative(ctx, h)
	if e != nil {
		return core.TaskCost{}, "", e
	}
	if t.Status != "succeeded" || t.Resolution != "768P" {
		return core.TaskCost{}, "", invalid("regeneration source must be a succeeded H3 768P task")
	}
	usage, e := normalizeH3Usage(t.Usage, h.Model)
	if e != nil {
		return core.TaskCost{}, "", e
	}
	if usage == nil {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	values := map[string]float64{}
	for _, metric := range usage.Metrics {
		values[metric.Name] = metric.Quantity
	}
	snapshot := RegenerationTariff()
	cost := core.TaskCost{Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: snapshot}
	for i, c := range cost.Pricing.Components {
		q, ok := values[c.Metric]
		if !ok {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		cost.Pricing.Components[i].EstimatedUnits = q
		part, e := c.Price.Multiply(q, c.Per)
		if e != nil {
			return core.TaskCost{}, "", e
		}
		cost.ExpectedUSD += part
	}
	if values["output_video_duration"] <= 0 {
		return core.TaskCost{}, "", errors.New("MiniMax source output usage is invalid")
	}
	cost.EstimatedUnits = values["output_video_duration"]
	return cost, "", nil
}

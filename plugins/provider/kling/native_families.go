package kling

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const TextToVideo3Turbo = "kling-3.0-turbo-text-to-video"
const ImageToVideo3Turbo = "kling-3.0-turbo-image-to-video"
const TextToVideo25Turbo = "kling-2.5-turbo-text-to-video"
const ImageToVideo25Turbo = "kling-2.5-turbo-image-to-video"

// These operation IDs select reviewed serializers; bare upstream version strings
// are not aliases. Additional Omni/management contracts are tracked separately.
type nativeFamily struct {
	version, operation, label                                      string
	promptLimit, minDuration, maxDuration, durationStep, maxImages int
	rate720, rate1080                                              money.USD
}

func additionalNativeFamily(id string) (nativeFamily, bool) {
	f := nativeFamily{minDuration: 3, maxDuration: 15, durationStep: 1, promptLimit: 3072, rate720: 112000, rate1080: 140000}
	switch id {
	case TextToVideo3Turbo:
		f.version, f.operation, f.label = "kling-3.0-turbo", "text-to-video", "Kling 3.0 Turbo · Text to video"
	case ImageToVideo3Turbo:
		f.version, f.operation, f.label, f.maxImages, f.promptLimit = "kling-3.0-turbo", "image-to-video", "Kling 3.0 Turbo · Image to video", 1, 2500
	case TextToVideo25Turbo:
		f.version, f.operation, f.label = "kling-2.5-turbo", "text-to-video", "Kling 2.5 Turbo · Text to video"
	case ImageToVideo25Turbo:
		f.version, f.operation, f.label, f.maxImages = "kling-2.5-turbo", "image-to-video", "Kling 2.5 Turbo · Image to video", 2
	default:
		return nativeFamily{}, false
	}
	if f.version == "kling-2.5-turbo" {
		f.minDuration, f.maxDuration, f.durationStep, f.promptLimit, f.rate720, f.rate1080 = 5, 10, 5, 2500, 42000, 70000
	}
	return f, true
}

func additionalNativeCatalog() []core.MediaModel {
	models := []core.MediaModel{}
	for _, id := range []string{TextToVideo3Turbo, ImageToVideo3Turbo, TextToVideo25Turbo, ImageToVideo25Turbo} {
		f, _ := additionalNativeFamily(id)
		m := core.MediaModel{ID: id, DisplayName: f.label, MediaType: "video", MaxPromptCharacters: f.promptLimit, NoSeed: true, NoLanguage: true, NoInstructions: true,
			Resolutions: []string{"720p", "1080p"}, MinDuration: f.minDuration, MaxDuration: f.maxDuration, DefaultDuration: 5, DurationStep: f.durationStep,
			Pricing: core.MediaModelPricing{Known: true, ReferencePriceUSD: float64(f.rate720) * 5 / 1e6, ReferenceDescription: "5-second 720p output at published USD balance list price; funding and model access remain unverified."}}
		if f.maxImages == 0 {
			m.Ratios = []string{"16:9", "9:16", "1:1"}
			m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
		} else {
			m.SupportsImageInput, m.RequiresImageInput, m.MinImageInputs, m.MaxImageInputs = true, true, 1, f.maxImages
			m.ReferenceMediaLimits = frameLimits(f.maxImages, 50<<20)
			m.ReferenceModes = []core.ReferenceMode{{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}}}
			if f.maxImages == 2 {
				m.ReferenceModes = append(m.ReferenceModes, core.ReferenceMode{ID: "first_last_frame", Label: "First and last frames (1080p)", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}})
			}
		}
		models = append(models, m)
	}
	return models
}

func validateAdditionalNative(f nativeFamily, m core.MediaModel, r core.MediaRequest) error {
	invalid := func(s string) error { return errors.Join(core.ErrOptionsInvalid, errors.New(s)) }
	o := r.Options
	if strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > f.promptLimit {
		return invalid(fmt.Sprintf("Kling prompt must contain 1–%d characters", f.promptLimit))
	}
	if o.Resolution != "" && !slices.Contains(m.Resolutions, o.Resolution) {
		return invalid("unsupported Kling resolution")
	}
	if o.Ratio != "" && !slices.Contains(m.Ratios, o.Ratio) {
		return invalid("unsupported Kling aspect ratio; image-to-video derives it from the input")
	}
	if o.Duration != 0 && (o.Duration < f.minDuration || o.Duration > f.maxDuration || (o.Duration-f.minDuration)%f.durationStep != 0) {
		return invalid("unsupported Kling duration")
	}
	if o.GenerateAudio != nil {
		return invalid("this Kling operation does not document an audio switch; direct audio requirements through the prompt")
	}
	if o.Seed != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.Videos()) > 0 || len(o.Audios()) > 0 {
		return invalid("unsupported Kling option")
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return err
	}
	if f.version == "kling-2.5-turbo" && len(o.ImageURLs) == 2 && resolutionFor(m, o) != "1080p" {
		return invalid("Kling 2.5 Turbo first and last frames require 1080p")
	}
	for _, raw := range o.ImageURLs {
		if !publicURL(raw) {
			return invalid("Kling frame inputs require public HTTPS image URLs")
		}
	}
	_, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	return err
}

func (a *Adapter) generateAdditionalNative(ctx context.Context, f nativeFamily, m core.MediaModel, r core.MediaRequest) (core.ProviderTask, error) {
	settings := map[string]any{"duration": durationFor(m, r.Options), "resolution": resolutionFor(m, r.Options)}
	payload := map[string]any{"settings": settings}
	if f.maxImages == 0 {
		payload["prompt"] = r.Prompt
		settings["aspect_ratio"] = "16:9"
		if r.Options.Ratio != "" {
			settings["aspect_ratio"] = r.Options.Ratio
		}
	} else {
		contents := []map[string]string{{"type": "prompt", "text": r.Prompt}}
		for i, raw := range r.Options.ImageURLs {
			role := "first_frame"
			if i > 0 {
				role = "last_frame"
			}
			contents = append(contents, map[string]string{"type": role, "url": raw})
		}
		payload["contents"] = contents
	}
	options := map[string]any{}
	if r.Options.Watermark != nil {
		options["watermark_info"] = map[string]bool{"enabled": *r.Options.Watermark}
	}
	if r.TaskID != "" {
		options["external_task_id"] = r.TaskID
	}
	if len(options) > 0 {
		payload["options"] = options
	}
	return a.submit(ctx, http.MethodPost, "/"+f.operation+"/"+f.version, payload)
}

func quoteAdditionalNative(f nativeFamily, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	rate := f.rate720
	if resolutionFor(m, r.Options) == "1080p" {
		rate = f.rate1080
	}
	quantity := float64(durationFor(m, r.Options))
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{Currency: "USD", Unit: "second", Price: rate, Per: 1, Source: priceSource, Version: "2026-10-31", Region: "Singapore", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: "Published USD balance list price for requested output duration; account access, discounts, prepaid units and actual charges remain unverified."}}, "", nil
}

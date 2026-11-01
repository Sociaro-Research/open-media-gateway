package kling

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

const kling3PromptMaxCharacters = 3072

func promptCharacterLimit(id string) int {
	if id == OmniVideo4 {
		return 26000
	}
	if isKling4(id) {
		return core.MaxPromptCharacters
	}
	if f, ok := additionalNativeFamily(id); ok {
		return f.promptLimit
	}
	return kling3PromptMaxCharacters
}

func catalog() []core.MediaModel {
	models := []core.MediaModel{}
	for _, id := range []string{TextToVideo, ImageToVideo} {
		m := core.MediaModel{
			MaxPromptCharacters: promptCharacterLimit(id),
			ID:                  id, DisplayName: "Kling 3.0 · Text to video", MediaType: "video",
			NoSeed: true, NoLanguage: true, NoInstructions: true, NoWatermark: true,
			Resolutions: []string{"720p", "1080p", "4k"},
			MinDuration: 3, MaxDuration: 15, DefaultDuration: 5, DurationStep: 1, SupportsAudio: true,
			Parameters: []core.ParameterField{{Key: "multi_shot", Label: "Multi-shot video", Type: "boolean", Default: true}},
			Pricing:    core.MediaModelPricing{Known: true, ReferencePriceUSD: 0.42, ReferenceDescription: "5-second 720p video without audio at published USD balance list price; account discounts, trials and prepaid units are separate."},
		}
		if id == TextToVideo {
			m.Ratios = []string{"16:9", "9:16", "1:1"}
			m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
		} else {
			m.DisplayName = "Kling 3.0 · Image to video"
			m.SupportsImageInput, m.RequiresImageInput, m.MinImageInputs, m.MaxImageInputs = true, true, 1, 2
			m.ReferenceMediaLimits = frameLimits(2, 50<<20)
			m.ReferenceModes = []core.ReferenceMode{
				{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}},
				{ID: "first_last_frame", Label: "First and last frames", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}},
			}
		}
		models = append(models, m)
	}
	for _, id := range []string{TextToVideo4, ImageToVideo4} {
		m := core.MediaModel{
			MaxPromptCharacters: promptCharacterLimit(id),
			ID:                  id, DisplayName: "Kling 4.0 · Text to video", MediaType: "video",
			NoSeed: true, NoLanguage: true, NoInstructions: true, NoWatermark: true,
			Resolutions: []string{"1080p", "720p"}, MinDuration: 3, MaxDuration: 30, DefaultDuration: 10, DurationStep: 1,
			Pricing: core.MediaModelPricing{Known: true, ReferencePriceUSD: 2.35, ReferenceDescription: "10-second 1080p output at published USD balance list price; actual account charges and model access remain unverified."},
		}
		if id == TextToVideo4 {
			m.Ratios = []string{"16:9", "9:16", "1:1", "21:9"}
			m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
		} else {
			m.DisplayName = "Kling 4.0 · Image to video"
			m.ReferencePromptSyntax = "kling-id"
			m.ReferencePromptTypes = []string{"image"}
			m.SupportsImageInput, m.RequiresImageInput, m.MinImageInputs, m.MaxImageInputs = true, true, 1, 10
			m.ReferenceMediaLimits = frameLimits(10, 30<<20)
			m.ReferenceModes = []core.ReferenceMode{
				{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}},
				{ID: "first_last_frame", Label: "First and last frames", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}},
				{ID: "keyframes", Label: "First, middle and last keyframes (in order)", MinImages: 3, MaxImages: 10, ImageRoles: []string{"first_frame"}, DefaultImageRole: "middle_frame", LastImageRole: "last_frame"},
			}
		}
		models = append(models, m)
	}
	models = append(models, additionalNativeCatalog()...)
	models = append(models, omni4Model())
	models = append(models, imageModels()...)
	models = append(models, audioModels()...)
	models = append(models, generatedAudioModel())
	models = append(models, motionModels()...)
	models = append(models, solutionModels()...)
	models = append(models, legacyOmniModels()...)
	models = append(models, resourceGenerationModels()...)
	models = append(models, auxiliaryModels()...)
	return append(models, sessionModels()...)
}

func modelFor(id string) (core.MediaModel, bool) {
	for _, m := range catalog() {
		if m.ID == id {
			return m, true
		}
	}
	return core.MediaModel{}, false
}
func isKling4(id string) bool { return id == TextToVideo4 || id == ImageToVideo4 }
func (a *Adapter) models() []core.MediaModel {
	models := catalog()
	for i, m := range models {
		if !isKling4(m.ID) {
			continue
		}
		if rate, ok := a.kling4Prices["1080p"]; ok {
			models[i].Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: float64(rate) * 10 / 1e6, ReferenceDescription: "10-second 1080p output at operator-confirmed Kling 4.0 account tariff; account access is unverified."}
		}
	}
	return models
}
func (a *Adapter) ListModels(context.Context) ([]core.MediaModel, error) {
	return core.MediaGenerationModels(a.models()), nil
}
func (a *Adapter) ReviewedCatalogModels() []core.MediaModel {
	return core.MediaGenerationModels(a.models())
}

func (a *Adapter) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if r.Model == BindGeneratedAudio {
		_, e := a.prepareGeneratedAudio(ctx, r)
		return e
	}
	if isSessionOperation(r.Model) {
		if r.Prompt != "" {
			return auxiliaryInvalid("selection operations do not accept a prompt")
		}
		m, _ := modelFor(r.Model)
		return a.validateSessionOperation(ctx, m, r.Options)
	}
	if isLegacyOmniOperation(r.Model) {
		return a.validateLegacyOmniRequest(r)
	}
	if isResourceGenerationOperation(r.Model) {
		return a.validateResourceGenerationRequest(ctx, r)
	}
	if isAuxiliaryOperation(r.Model) {
		return a.validateAuxiliaryRequest(r, ctx)
	}
	if isImageOperation(r.Model) {
		return a.validateImageRequest(r)
	}
	if isAudioOperation(r.Model) {
		return a.validateAudioRequest(r, ctx)
	}
	if isMotionOperation(r.Model) {
		return a.validateMotionRequest(r)
	}
	if isSolutionOperation(r.Model) {
		return validateSolutionRequest(r)
	}
	invalid := func(s string) error { return errors.Join(core.ErrOptionsInvalid, errors.New(s)) }
	m, ok := modelFor(r.Model)
	if !ok {
		return invalid("unsupported Kling operation")
	}
	if r.Model == OmniVideo4 {
		return validateOmni4(m, r)
	}
	if f, ok := additionalNativeFamily(r.Model); ok {
		return validateAdditionalNative(f, m, r)
	}
	limit := promptCharacterLimit(r.Model)
	if strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > limit {
		return invalid(fmt.Sprintf("Kling prompt must contain 1–%d characters", limit))
	}
	o := r.Options
	if o.Resolution != "" && !slices.Contains(m.Resolutions, o.Resolution) {
		return invalid("unsupported Kling resolution")
	}
	if o.Ratio != "" && !slices.Contains(m.Ratios, o.Ratio) {
		return invalid("unsupported Kling aspect ratio; image-to-video derives it from the input")
	}
	if o.Duration != 0 && (o.Duration < m.MinDuration || o.Duration > m.MaxDuration) {
		return invalid(fmt.Sprintf("Kling duration must be %d–%d seconds", m.MinDuration, m.MaxDuration))
	}
	if isKling4(r.Model) && o.GenerateAudio != nil {
		return invalid("Kling 4.0 does not document an audio switch; direct audio requirements through the prompt")
	}
	if o.Seed != nil || o.Watermark != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.Videos()) > 0 || len(o.Audios()) > 0 {
		return invalid("unsupported Kling option")
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return err
	}
	for _, raw := range o.ImageURLs {
		if !publicURL(raw) {
			return invalid("Kling frame inputs require public HTTPS image URLs")
		}
	}
	_, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	return err
}

func (a *Adapter) Quote(ctx context.Context, _ core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if r.Model == BindGeneratedAudio {
		return a.quoteGeneratedAudio(ctx, r)
	}
	if err := a.ValidateRequest(ctx, r); err != nil {
		return core.TaskCost{}, "", err
	}
	m, _ := modelFor(r.Model)
	if isSessionOperation(r.Model) {
		return a.quoteSessionOperation(m, r.Options)
	}
	if isLegacyOmniOperation(r.Model) {
		return a.quoteLegacyOmni(r)
	}
	if isResourceGenerationOperation(r.Model) {
		return a.quoteResourceGeneration(ctx, r)
	}
	if isAuxiliaryOperation(r.Model) {
		return a.quoteAuxiliary(r, ctx)
	}
	if isImageOperation(r.Model) {
		return a.quoteImage(r)
	}
	if isAudioOperation(r.Model) {
		return a.quoteAudio(r, ctx)
	}
	if isMotionOperation(r.Model) {
		return a.quoteMotion(r)
	}
	if r.Model == KlingEffects {
		return a.quoteEffect(r)
	}
	if isSolutionOperation(r.Model) {
		return quoteSolution(r)
	}
	if r.Model == OmniVideo4 {
		return a.quoteOmni4(m, r)
	}
	if f, ok := additionalNativeFamily(r.Model); ok {
		return quoteAdditionalNative(f, m, r)
	}
	res := resolutionFor(m, r.Options)
	source := priceSource
	description := "Published USD balance list price for requested output duration. Prepaid units, discounts, trials and CNY charges are not converted; this is not a confirmed bill."
	rate := money.USD(84000)
	switch res {
	case "1080p":
		rate = 112000
	case "4k":
		rate = 420000
	}
	if r.Options.GenerateAudio != nil && *r.Options.GenerateAudio && res != "4k" {
		rate = rate * 3 / 2
	}
	if isKling4(r.Model) {
		rate = 235000
		if res == "720p" {
			rate = 157000
		}
		if override, configured := a.kling4Prices[res]; configured {
			rate = override
			source = "Operator-confirmed Kling 4.0 account tariff (USD/second)"
			description = "Operator-configured USD/second tariff for requested output duration. Account model access and actual charges remain unverified."
		}
	}
	quantity := float64(durationFor(m, r.Options))
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{
		Currency: "USD", Unit: "second", Price: rate, Per: 1, Source: source, Version: "2026-10-29", Region: "Singapore",
		AccountSpecific:     source != "" && source != priceSource,
		CheckedAt:           time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
		EstimateDescription: description,
	}}, "", nil
}

func durationFor(m core.MediaModel, o core.MediaOptions) int {
	if o.Duration != 0 {
		return o.Duration
	}
	return m.DefaultDuration
}
func resolutionFor(m core.MediaModel, o core.MediaOptions) string {
	if o.Resolution != "" {
		return o.Resolution
	}
	return m.Resolutions[0]
}

func (a *Adapter) setPrices(c providerkit.Credentials) error {
	for _, res := range []string{"720p", "1080p"} {
		field := "kling4_" + res + "_price"
		raw := strings.TrimSpace(c.Get(field))
		if raw == "" {
			continue
		}
		price, err := money.Parse(raw)
		if err != nil || price <= 0 {
			return fmt.Errorf("Kling %s must be a positive USD/second tariff", field)
		}
		a.kling4Prices[res] = price
	}
	return nil
}

func frameLimits(count int, size int64) *core.ReferenceMediaLimits {
	return &core.ReferenceMediaLimits{MaxFiles: count, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: size, MinDimension: 300, MinRatio: 0.4, MaxRatio: 2.5}}
}

// PreparationModels remains private to media-input preparation and old-task recovery.
func (a *Adapter) PreparationModels(context.Context) ([]core.MediaModel, error) {
	out := []core.MediaModel{}
	for _, m := range a.models() {
		if !core.MediaGenerationModel(m) && (len(m.ResourceOutputs) > 0 || m.ResourceOperation != nil || m.SessionOperation != nil) {
			out = append(out, m)
		}
	}
	return out, nil
}

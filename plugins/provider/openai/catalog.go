// Package openai adapts the OpenAI Images, Speech, and audio Chat APIs to media jobs.
package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelcatalog"
)

const (
	DefaultBaseURL = "https://api.openai.com/v1"
	DefaultModel   = "gpt-image-2.5-flare"
)

// Only reviewed aliases and exact snapshots are exposed. The account's /models response wins;
// unknown models and snapshots never inherit capabilities or tariffs by prefix.
var modelIDs = []string{
	"gpt-image-2.5-flare", "gpt-image-2.5-sunburst", "gpt-image-2",
	"gpt-image-1.5", "gpt-image-1-mini", "gpt-image-1", "chatgpt-image-latest",
	"gpt-4o-mini-tts", "tts-1", "tts-1-hd", "gpt-audio-1.5", "gpt-audio", "gpt-audio-mini",
	"gpt-image-2-2026-04-21", "gpt-image-2.5-flare-2026-09-08", "gpt-image-2.5-sunburst-2026-09-08",
	"gpt-image-1.5-2025-12-16", "gpt-4o-mini-tts-2025-03-20", "gpt-4o-mini-tts-2025-12-15",
	"gpt-audio-2025-08-28", "gpt-audio-mini-2025-10-06", "gpt-audio-mini-2025-12-15", "tts-1-1106", "tts-1-hd-1106",
}

// Never strip arbitrary date suffixes: future snapshots require a new review.
func tariffAliases() map[string]string {
	return map[string]string{
		"gpt-image-2-2026-04-21": "gpt-image-2", "gpt-image-2.5-flare-2026-09-08": "gpt-image-2.5-flare",
		"gpt-image-2.5-sunburst-2026-09-08": "gpt-image-2.5-sunburst", "gpt-image-1.5-2025-12-16": "gpt-image-1.5",
		"gpt-4o-mini-tts-2025-03-20": "gpt-4o-mini-tts", "gpt-4o-mini-tts-2025-12-15": "gpt-4o-mini-tts",
		"gpt-audio-2025-08-28": "gpt-audio", "gpt-audio-mini-2025-10-06": "gpt-audio-mini",
		"gpt-audio-mini-2025-12-15": "gpt-audio-mini", "tts-1-1106": "tts-1", "tts-1-hd-1106": "tts-1-hd",
	}
}
func canonicalModel(id string) string {
	if parent := tariffAliases()[id]; parent != "" {
		return parent
	}
	return id
}

func (a *Adapter) ReviewedCatalogModels() []core.MediaModel {
	models := make([]core.MediaModel, 0, len(modelIDs))
	for _, id := range modelIDs {
		models = append(models, describeModel(id))
	}
	return models
}

func describeModel(id string) core.MediaModel {
	m := core.MediaModel{ID: id, DisplayName: id, NoSeed: true, NoWatermark: true}
	id = canonicalModel(id)
	switch {
	case strings.Contains(id, "image"):
		m.MediaType = "image"
		m.Resolutions = []string{"1024x1024", "1536x1024", "1024x1536"}
		m.Qualities = []string{"medium", "low", "high"}
		if strings.HasPrefix(id, "gpt-image-2") {
			m.Resolutions = append(m.Resolutions, "2048x2048", "2048x1152", "1152x2048", "3840x2160", "2160x3840")
		}
		if strings.HasPrefix(id, "gpt-image-2.5") {
			m.Qualities = append(m.Qualities, "xhigh", "max")
		}
		m.Backgrounds = []string{"opaque", "transparent", "auto"}
		m.Formats = []string{"png", "jpeg", "webp"}
		m.SupportsImageInput = true
		m.MaxImageInputs = 16
		// Images edit API: PNG/JPEG/WebP, under 50 MB per input. Shared
		// library bounds remain an additional, more conservative upload limit.
		m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 16, Image: core.MediaFileLimits{ContentTypes: []string{"image/png", "image/jpeg", "image/webp"}, MaxBytes: 50_000_000}}
		m.ReferenceModes = core.ReferenceModesFor(m)
	case strings.HasPrefix(id, "gpt-audio"):
		// A bounded audio reply, not verbatim narration or an interactive session.
		m.MediaType = "speech"
		m.NoLanguage = true
		m.DisplayName = m.ID + " · spoken response"
		m.SupportsVoice = true
		m.RequiresVoice = true
		m.Voices = []string{"alloy", "ash", "ballad", "coral", "echo", "sage", "shimmer", "verse"}
		m.Formats = []string{"mp3", "wav", "flac", "opus"}
	default:
		m.MediaType = "speech"
		m.NoLanguage = true
		m.NoInstructions = strings.HasPrefix(id, "tts-1")
		m.SupportsVoice = true
		m.RequiresVoice = true
		m.Voices = []string{"alloy", "ash", "coral", "echo", "fable", "onyx", "nova", "sage", "shimmer"}
		if id == "gpt-4o-mini-tts" {
			m.Voices = append(m.Voices, "ballad", "verse", "marin", "cedar")
		}
		m.Formats = []string{"mp3", "wav", "flac", "opus", "aac"}
	}
	// Reference prices are indicative and exclude input tokens; quotes include
	// explicit local input estimates. No price is presented as an invoice.
	m.Pricing = core.MediaModelPricing{Known: true, ReferenceDescription: "Estimated usage at OpenAI standard list rates; final usage varies."}
	if m.MediaType == "image" {
		m.Pricing.ReferencePriceUSD = outputTokens(id, "1024x1024", "medium") * rates(id)["image_output_tokens"] / 1e6
		if id == "gpt-image-1-mini" {
			m.Pricing.ReferencePriceUSD = miniOutputEstimate("1024x1024", "medium")
		}
	} else {
		switch id {
		case "tts-1":
			m.Pricing.ReferencePriceUSD = 0.015
			m.Pricing.ReferenceDescription = "1,000 input characters at standard list rates."
		case "tts-1-hd":
			m.Pricing.ReferencePriceUSD = 0.03
			m.Pricing.ReferenceDescription = "1,000 input characters at standard list rates."
		case "gpt-4o-mini-tts":
			m.Pricing.ReferencePriceUSD = 0.015
			m.Pricing.ReferenceDescription = "Approximate output cost for one minute of speech; input tokens are additional."
		default:
			m.Pricing.ReferencePriceUSD = 4096 * rates(id)["audio_output_tokens"] / 1e6
			m.Pricing.ReferenceDescription = "Full 4,096-token audio output allowance; actual short replies can cost less."
		}
	}
	return m
}

func (a *Adapter) CheckCredentials(ctx context.Context) error {
	_, err := a.fetchModels(ctx)
	return err
}

func (a *Adapter) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	a.mu.Lock()
	if a.models != nil && time.Since(a.modelsAt) < 10*time.Minute {
		result := cloneModels(a.models)
		a.mu.Unlock()
		return result, nil
	}
	a.mu.Unlock()
	return a.fetchModels(ctx)
}

func (a *Adapter) fetchModels(ctx context.Context) ([]core.MediaModel, error) {
	data, _, err := a.do(ctx, http.MethodGet, "/models", nil, false)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			ID           string `json:"id"`
			Created      int64  `json:"created"`
			OwnedBy      string `json:"owned_by"`
			ShutdownDate string `json:"shutdown_date"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &response) != nil || response.Data == nil {
		return nil, fmt.Errorf("OpenAI models response is malformed")
	}
	available := map[string]*core.ModelMetadata{}
	for _, item := range response.Data {
		available[item.ID] = &core.ModelMetadata{APICreated: item.Created, OwnedBy: item.OwnedBy, ShutdownDate: item.ShutdownDate}
	}
	models := make([]core.MediaModel, 0)
	for _, id := range modelIDs {
		if modelcatalog.IsInactive("openai", id, time.Now()) {
			continue
		}
		if metadata := available[id]; metadata != nil {
			model := describeModel(id)
			model.Metadata = metadata
			models = append(models, model)
		}
	}
	a.mu.Lock()
	a.models = cloneModels(models)
	a.modelsAt = time.Now()
	a.mu.Unlock()
	return models, nil
}

func cloneModels(models []core.MediaModel) []core.MediaModel {
	// Deep copy: callers may sort catalog slices or adjust UI descriptors.
	data, _ := json.Marshal(models)
	var result []core.MediaModel
	_ = json.Unmarshal(data, &result)
	return result
}

func (a *Adapter) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if core.IsManualCall(ctx) {
		return nil
	}
	if !slices.Contains(modelIDs, r.Model) {
		return fmt.Errorf("unsupported OpenAI model")
	}
	if modelcatalog.IsInactive("openai", r.Model, time.Now()) {
		return fmt.Errorf("OpenAI model has been retired")
	}
	m := describeModel(r.Model)
	o := r.Options
	invalid := func(message string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, message) }
	if strings.TrimSpace(r.Prompt) == "" {
		return invalid("prompt is required")
	}
	if o.Seed != nil || o.Watermark != nil || o.GenerateAudio != nil || o.Draft != nil || o.CameraFixed != nil || o.ReturnLastFrame != nil || o.Duration != 0 || o.Ratio != "" || o.NegativePrompt != "" || len(o.Videos()) > 0 || len(o.Audios()) > 0 || o.Lyrics != "" || o.Gender != "" || len(o.ReferenceVoices) > 0 {
		return invalid("unsupported OpenAI media option")
	}
	if o.Format != "" && !slices.Contains(m.Formats, o.Format) {
		return invalid("unsupported output format")
	}
	if m.MediaType == "image" {
		if len([]rune(r.Prompt)) > 32000 {
			return invalid("image prompt exceeds 32000 characters")
		}
		if o.Resolution != "" && !slices.Contains(m.Resolutions, o.Resolution) {
			return invalid("unsupported image size")
		}
		if o.Quality != "" && !slices.Contains(m.Qualities, o.Quality) {
			return invalid("unsupported quality")
		}
		if o.Background != "" && !slices.Contains(m.Backgrounds, o.Background) {
			return invalid("unsupported background")
		}
		if o.Background == "transparent" && o.Format == "jpeg" {
			return invalid("transparent images require PNG or WebP")
		}
		if len(o.ImageURLs) > m.MaxImageInputs {
			return invalid("at most 16 image references are supported")
		}
		if o.ReferenceMode != "" && o.ReferenceMode != "references" {
			return invalid("unsupported image reference mode")
		}
		if o.Voice != "" || o.Language != "" || o.Instructions != "" {
			return invalid("speech options cannot be used for images")
		}
		for _, value := range o.ImageURLs {
			if err := validateImageURL(value); err != nil {
				return invalid(err.Error())
			}
		}
	} else {
		if len([]rune(r.Prompt)) > 4096 {
			return invalid("speech prompt exceeds 4096 characters")
		}
		if o.Resolution != "" || o.Quality != "" || o.Background != "" || len(o.ImageURLs) > 0 || o.ReferenceMode != "" {
			return invalid("image options cannot be used for speech")
		}
		if o.Voice != "" && !slices.Contains(m.Voices, o.Voice) {
			return invalid("unsupported voice")
		}
		if o.Language != "" && o.Language != "Auto" {
			return invalid("write the input in the desired language")
		}
		if strings.HasPrefix(r.Model, "tts-1") && o.Instructions != "" {
			return invalid("this TTS model does not support voice instructions")
		}
		if len([]rune(o.Instructions)) > 4096 {
			return invalid("voice instructions exceed 4096 characters")
		}
	}
	return nil
}

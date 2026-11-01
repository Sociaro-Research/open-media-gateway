package kling

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const (
	TextToVideo26   = "kling-2.6-text-to-video"
	ImageToVideo26  = "kling-2.6-image-to-video"
	MotionControl3  = "kling-3.0-motion-control"
	MotionControl26 = "kling-2.6-motion-control"
)

func isMotionOperation(id string) bool {
	switch id {
	case TextToVideo26, ImageToVideo26, MotionControl3, MotionControl26:
		return true
	}
	return false
}

func motionModels() []core.MediaModel {
	models := make([]core.MediaModel, 0, 4)
	for _, id := range []string{TextToVideo26, ImageToVideo26} {
		m := core.MediaModel{ID: id, DisplayName: "Kling 2.6 · Text to video", MediaType: "video", MaxPromptCharacters: 2500, NoSeed: true, NoLanguage: true, NoInstructions: true,
			Resolutions: []string{"720p", "1080p"}, MinDuration: 5, MaxDuration: 10, DefaultDuration: 5, DurationStep: 5, SupportsAudio: true,
			Pricing: core.MediaModelPricing{Known: true, ReferencePriceUSD: .21, ReferenceDescription: "5-second 720p output without native audio at published USD balance list price; native audio requires 1080p."},
		}
		if id == TextToVideo26 {
			m.Ratios = []string{"16:9", "9:16", "1:1"}
			m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
		} else {
			m.DisplayName = "Kling 2.6 · Image to video"
			m.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "voice", MaxCount: 2}}
			m.SupportsImageInput, m.RequiresImageInput, m.MinImageInputs, m.MaxImageInputs = true, true, 1, 2
			m.ReferenceMediaLimits = frameLimits(2, 50<<20)
			m.ReferenceModes = []core.ReferenceMode{{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}}, {ID: "first_last_frame", Label: "First and last frames (1080p)", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}}}
			m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"first_frame", "last_frame"}, Default: "first_frame"}}
		}
		models = append(models, m)
	}
	for _, id := range []string{MotionControl3, MotionControl26} {
		label := "Kling 3.0 · Motion Control"
		if id == MotionControl26 {
			label = "Kling 2.6 · Motion Control"
		}
		m := core.MediaModel{ID: id, DisplayName: label, MediaType: "video", MaxPromptCharacters: 2500, NoSeed: true, NoLanguage: true, NoInstructions: true,
			Resolutions: []string{"720p", "1080p"}, MinDuration: 3, MaxDuration: 30, DurationFromInput: true, SupportsImageInput: true, RequiresImageInput: true, MinImageInputs: 1, MaxImageInputs: 1, SupportsVideoInput: true, RequiresVideoInput: true,
			Parameters:           []core.ParameterField{{Key: "character_orientation", Label: "Character orientation follows", Type: "select", Required: true, Choices: []string{"image", "video"}}, {Key: "audio", Label: "Reference video sound", Type: "select", Default: "original", Choices: []string{"original", "off"}}},
			ReferenceModes:       []core.ReferenceMode{{ID: "motion", Label: "Appearance image and motion video", MinImages: 1, MaxImages: 1, MinVideos: 1, MaxVideos: 1, MaxVisuals: 2}},
			ReferenceRoleSchemas: []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"appearance"}, Default: "appearance"}, {Kind: "video", Roles: []string{"motion"}, Default: "motion"}},
			ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 2, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: 50 << 20, MinDimension: 300, MinRatio: .4, MaxRatio: 2.5}, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 100 << 20, MinDuration: 3, MaxDuration: 30, MinDimension: 340, MaxDimension: 3850}},
			Pricing:              core.MediaModelPricing{Known: false, ReferenceDescription: "Output-duration billing; quote requires inspected motion-video duration. Image orientation limits input to 10 seconds; video orientation allows 30 seconds. Provider motion extraction may shorten output."},
		}
		if id == MotionControl3 {
			m.RequiresImageInput, m.MinImageInputs = false, 0
			m.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "element", MaxCount: 1, Subtypes: []string{"multi_image", "video_character"}}}
			m.ReferenceModes = append(m.ReferenceModes, core.ReferenceMode{ID: "element_motion", Label: "Managed character element and motion video", MinVideos: 1, MaxVideos: 1})
		}
		models = append(models, m)
	}
	return models
}

func motionModel(id string) (core.MediaModel, bool) {
	for _, m := range motionModels() {
		if m.ID == id {
			return m, true
		}
	}
	return core.MediaModel{}, false
}

func (a *Adapter) validateMotionRequest(r core.MediaRequest) error {
	invalid := func(s string) error { return errors.Join(core.ErrOptionsInvalid, errors.New(s)) }
	m, ok := motionModel(r.Model)
	if !ok {
		return invalid("unsupported Kling operation")
	}
	if strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > 2500 {
		return invalid("Kling prompt must contain 1–2500 characters")
	}
	o := r.Options
	if err := validateMotionResources(r); err != nil {
		return err
	}
	if _, err := core.NormalizeModelOptions(m, o); err != nil {
		return err
	}
	if o.Seed != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.Audios()) > 0 {
		return invalid("unsupported Kling option; custom voices require managed voice resources")
	}
	for _, raw := range append(append([]string{}, o.ImageURLs...), o.Videos()...) {
		if !publicURL(raw) {
			return invalid("Kling references require public HTTPS URLs")
		}
	}
	if r.Model == TextToVideo26 || r.Model == ImageToVideo26 {
		resolution := resolutionFor(m, o)
		if o.GenerateAudio != nil && *o.GenerateAudio && resolution != "1080p" {
			return invalid("Kling 2.6 native audio requires 1080p")
		}
		if len(o.ImageURLs) == 2 && resolution != "1080p" {
			return invalid("Kling 2.6 first and last frames require 1080p")
		}
		for i := range o.ImageURLs {
			if role := o.ReferenceRoles[fmt.Sprintf("image_%d", i+1)]; role != "" && role != []string{"first_frame", "last_frame"}[i] {
				return invalid("Kling 2.6 image order must be first frame then last frame")
			}
		}
		if len(o.ResourceSnapshots) > 0 && (o.GenerateAudio == nil || !*o.GenerateAudio || resolution != "1080p") {
			return invalid("Kling 2.6 voice references require native audio and 1080p")
		}
		return nil
	}
	if len(o.ImageURLs)+len(o.ResourceSnapshots) == 0 {
		return invalid("Motion Control requires an appearance image or managed element")
	}
	if o.GenerateAudio != nil {
		return invalid("Motion Control uses the audio parameter to retain original sound or disable it")
	}
	parameters, _ := core.NormalizeParameters(m.Parameters, o.Parameters)
	if len(o.ResourceSnapshots) > 0 && parameters["character_orientation"] != "video" {
		return invalid("Kling element motion requires video character orientation")
	}
	maxDuration := 30.0
	if parameters["character_orientation"] == "image" {
		maxDuration = 10
	}
	for _, raw := range o.Videos() {
		if e, exists := o.ReferenceMediaEvidence[raw]; exists && (e.Type != "video" || e.Duration < 3 || e.Duration > maxDuration || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0)) {
			return invalid(fmt.Sprintf("Kling Motion Control input must be 3–%.0f seconds for this orientation", maxDuration))
		}
	}
	return nil
}

// These explicit wire structs keep native audio, source audio and omitted
// duration/aspect fields distinct across the reviewed unified API endpoints.
type motionSettings struct {
	Audio                string `json:"audio"`
	Resolution           string `json:"resolution"`
	AspectRatio          string `json:"aspect_ratio,omitempty"`
	Duration             int    `json:"duration,omitempty"`
	CharacterOrientation string `json:"character_orientation,omitempty"`
}
type motionContent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	URL       string `json:"url,omitempty"`
	VoiceID   string `json:"voice_id,omitempty"`
	ElementID string `json:"element_id,omitempty"`
	ID        string `json:"id,omitempty"`
}
type motionWatermark struct {
	Enabled bool `json:"enabled"`
}
type motionOptions struct {
	ExternalTaskID string           `json:"external_task_id,omitempty"`
	Watermark      *motionWatermark `json:"watermark_info,omitempty"`
}
type motionPayload struct {
	Prompt   string          `json:"prompt,omitempty"`
	Contents []motionContent `json:"contents,omitempty"`
	Settings motionSettings  `json:"settings"`
	Options  *motionOptions  `json:"options,omitempty"`
}

func (a *Adapter) generateMotion(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.validateMotionRequest(r); err != nil {
		return core.ProviderTask{}, err
	}
	m, _ := motionModel(r.Model)
	p := motionPayload{Settings: motionSettings{Resolution: resolutionFor(m, r.Options), Audio: "off"}}
	path := "/text-to-video/kling-2.6"
	if r.Model == TextToVideo26 || r.Model == ImageToVideo26 {
		p.Settings.Duration = durationFor(m, r.Options)
		if r.Options.GenerateAudio != nil && *r.Options.GenerateAudio {
			p.Settings.Audio = "native"
		}
		if r.Model == TextToVideo26 {
			p.Prompt = r.Prompt
			p.Settings.AspectRatio = r.Options.Ratio
			if p.Settings.AspectRatio == "" {
				p.Settings.AspectRatio = "16:9"
			}
		} else {
			path = "/image-to-video/kling-2.6"
			p.Contents = []motionContent{{Type: "prompt", Text: r.Prompt}}
			for i, raw := range r.Options.ImageURLs {
				p.Contents = append(p.Contents, motionContent{Type: []string{"first_frame", "last_frame"}[i], URL: raw})
			}
			for _, resource := range r.Options.ResourceSnapshots {
				p.Contents = append(p.Contents, motionContent{Type: "voice", VoiceID: resource.RemoteID, ID: resource.ReferenceID})
			}
		}
	} else {
		path = "/motion-control/kling-3.0"
		if r.Model == MotionControl26 {
			path = "/motion-control/kling-2.6"
		}
		parameters, _ := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
		p.Settings.Audio = parameters["audio"].(string)
		p.Settings.CharacterOrientation = parameters["character_orientation"].(string)
		p.Contents = []motionContent{{Type: "prompt", Text: r.Prompt}}
		for _, raw := range r.Options.ImageURLs {
			p.Contents = append(p.Contents, motionContent{Type: "image", URL: raw})
		}
		p.Contents = append(p.Contents, motionContent{Type: "video", URL: r.Options.Videos()[0]})
		for _, resource := range r.Options.ResourceSnapshots {
			p.Contents = append(p.Contents, motionContent{Type: "element", ElementID: resource.RemoteID, ID: resource.ReferenceID})
		}
	}
	if r.TaskID != "" || r.Options.Watermark != nil {
		p.Options = &motionOptions{ExternalTaskID: r.TaskID}
		if r.Options.Watermark != nil {
			p.Options.Watermark = &motionWatermark{Enabled: *r.Options.Watermark}
		}
	}
	return a.submit(ctx, http.MethodPost, path, p)
}

func (a *Adapter) quoteMotion(r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.validateMotionRequest(r); err != nil {
		return core.TaskCost{}, "", err
	}
	m, _ := motionModel(r.Model)
	resolution := resolutionFor(m, r.Options)
	rate := money.USD(42000)
	quantity := float64(durationFor(m, r.Options))
	description := "Published USD balance list price for output duration; discounts, units, trials and actual billed currency remain separate."
	if r.Model == MotionControl3 || r.Model == MotionControl26 {
		var known bool
		quantity, known = r.Options.InputVideoDuration()
		if !known {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		rate = 126000
		if resolution == "1080p" {
			rate = 168000
		}
		if r.Model == MotionControl26 {
			rate = 70000
			if resolution == "1080p" {
				rate = 112000
			}
		}
		description = "Published USD balance list price for estimated output duration from inspected motion input. The provider may shorten extracted motion; final billing uses actual output duration."
	} else {
		if resolution == "1080p" {
			rate = 70000
		}
		if r.Options.GenerateAudio != nil && *r.Options.GenerateAudio {
			rate = 140000
			if len(r.Options.ResourceSnapshots) > 0 {
				rate = 168000
			}
		}
	}
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{BillInputVideo: priceBool(false), Currency: "USD", Unit: "second", Price: rate, Per: 1, Source: priceSource, Version: "2026-10-31", Region: "Singapore", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: description}}, "", nil
}

// Core resolves bindings in the owner and concrete provider connection scope.
// Retain a second structural check here so a serializer cannot accept a raw ID
// parameter or silently discard an unresolved/unsupported managed reference.
func validateMotionResources(r core.MediaRequest) error {
	invalid := func(s string) error { return errors.Join(core.ErrOptionsInvalid, errors.New(s)) }
	refs, snapshots := r.Options.ResourceReferences, r.Options.ResourceSnapshots
	if len(refs) != len(snapshots) {
		return invalid("Kling managed references must be resolved before submission")
	}
	maximum, kind := 0, ""
	if r.Model == ImageToVideo26 {
		maximum, kind = 2, "voice"
	}
	if r.Model == MotionControl3 {
		maximum, kind = 1, "element"
	}
	if len(snapshots) > maximum {
		return invalid("unsupported Kling managed reference count")
	}
	aliases := map[string]bool{}
	for i, s := range snapshots {
		if s.Kind != kind || s.RemoteID == "" || !validTaskID(s.RemoteID) || s.ConnectionIdentity == "" || s.BindingID == "" || s.ReferenceID == "" || s.BindingID != refs[i].BindingID || s.ReferenceID != refs[i].ReferenceID || aliases[s.ReferenceID] {
			return invalid("invalid resolved Kling managed reference")
		}
		if kind == "element" && s.Subtype != "multi_image" && s.Subtype != "video_character" {
			return invalid("unsupported Kling element subtype")
		}
		aliases[s.ReferenceID] = true
	}
	return nil
}

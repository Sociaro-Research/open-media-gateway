package kling

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// Flash has separate native routes and reviewed limits. Do not classify it as
// standard Kling 4.0 or inherit that family's resolution, keyframes or voices.
const (
	TextToVideo4Flash  = "kling-4.0-flash-text-to-video"
	ImageToVideo4Flash = "kling-4.0-flash-image-to-video"
	OmniVideo4Flash    = "kling-4.0-flash-omni-video"
)

func isKling4Flash(id string) bool {
	return id == TextToVideo4Flash || id == ImageToVideo4Flash || id == OmniVideo4Flash
}

// Expected sources make omitted persisted schedules visible as not_checked for
// the exact operation. A source URL alone never verifies an operational field.
func flash4VerificationSources() map[string][]catalogsync.Source {
	source := func(id, path string) catalogsync.Source {
		return catalogsync.Source{ID: id, URL: docsBase + "api/video/4-0-flash/" + path + ".md", Parser: "document"}
	}
	return map[string][]catalogsync.Source{
		TextToVideo4Flash:  {source("kling4-flash-text-to-video", "text-to-video")},
		ImageToVideo4Flash: {source("kling4-flash-image-to-video", "image-to-video")},
		OmniVideo4Flash:    {source("kling4-flash-omni", "video-omni"), source("kling4-flash-elements", "elements")},
	}
}

func flash4Models() []core.MediaModel {
	models := make([]core.MediaModel, 0, 3)
	for _, id := range []string{TextToVideo4Flash, ImageToVideo4Flash, OmniVideo4Flash} {
		m := core.MediaModel{
			ID: id, DisplayName: "Kling 4.0 Flash · Text to video", MediaType: "video",
			NoSeed: true, NoLanguage: true, NoInstructions: true,
			MaxPromptCharacters: 26000,
			PromptPolicy:        &core.PromptPolicy{MaxCharacters: 26000, ASCIIBudget: 26000, NonASCIIBudget: 11000},
			Resolutions:         []string{"720p"}, MinDuration: 3, MaxDuration: 20, DefaultDuration: 10, DurationStep: 1,
			DurationMayBeInferred: true,
			Pricing:               core.MediaModelPricing{Known: false, ReferenceDescription: "Select an explicit output duration for a quote. Published Flash USD list prices do not establish account entitlement or actual charges."},
		}
		switch id {
		case TextToVideo4Flash:
			m.Ratios = []string{"16:9", "9:16", "1:1", "21:9"}
			m.ReferenceModes = []core.ReferenceMode{{ID: "none", Label: "Text only"}}
		case ImageToVideo4Flash:
			m.DisplayName = "Kling 4.0 Flash · Image to video"
			m.SupportsImageInput, m.RequiresImageInput, m.MinImageInputs, m.MaxImageInputs = true, true, 1, 1
			m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 1, Image: flash4ImageLimits()}
			m.ReferenceModes = []core.ReferenceMode{{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}}}
		case OmniVideo4Flash:
			m.DisplayName = "Kling 4.0 Flash · Omni Reference"
			m.Ratios = []string{"adaptive", "21:9", "16:9", "9:16", "1:1"}
			m.RatioMayBeInferred = true
			m.SupportsImageInput, m.MaxImageInputs, m.SupportsVideoInput, m.MaxReferenceInputs = true, 10, true, 15
			m.ReferencePromptSyntax, m.ReferencePromptTypes = "kling-id", []string{"image", "video"}
			m.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "element", SupportsPresets: true, MaxCount: 7, Subtypes: []string{"multi_image", "video_character"}}}
			m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "References", MaxImages: 10, MaxVideos: 5, MaxVisuals: 15}}
			m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference", "first_frame"}, Default: "reference"}, {Kind: "video", Roles: []string{"reference", "source"}, Default: "reference"}}
			m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 15, Image: flash4ImageLimits(), Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 300000000, MaxBytesInclusive: true, MinDuration: 3, MaxDuration: 20, MinTotalDuration: 3, MaxTotalDuration: 20, MinDimension: 700, MaxDimension: 4553, MaxPixels: 8294400, MinRatio: .4, MaxRatio: 2.5, MinFPS: 24, MaxFPS: 60}}
			m.Parameters = []core.ParameterField{{Key: "audio", Label: "Audio", Type: "select", Choices: []string{"native", "original"}, Default: "native"}}
		}
		models = append(models, m)
	}
	return models
}

func flash4ImageLimits() core.MediaFileLimits {
	return core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: 30000000, MaxBytesInclusive: true, MinDimension: 300, MinRatio: .4, MaxRatio: 2.5}
}

func validateFlash4(m core.MediaModel, r core.MediaRequest) error {
	invalid := func(message string) error { return errors.Join(core.ErrOptionsInvalid, errors.New(message)) }
	if strings.TrimSpace(r.Prompt) == "" {
		return invalid("Kling 4.0 Flash requires a prompt")
	}
	prompt := r.Prompt
	if r.Model == OmniVideo4Flash {
		prompt = omni4Prompt(r)
	}
	if err := core.ValidatePromptPolicy(m, prompt); err != nil {
		return err
	}
	o := r.Options
	if r.Model == TextToVideo4Flash && (len(o.ImageURLs) > 0 || len(o.Videos()) > 0 || len(o.ResourceReferences) > 0) {
		return invalid("Kling 4.0 Flash text-to-video does not accept references")
	}
	if r.Model == ImageToVideo4Flash && (len(o.ImageURLs) != 1 || len(o.Videos()) > 0 || len(o.ResourceReferences) > 0) {
		return invalid("Kling 4.0 Flash image-to-video requires exactly one first-frame image")
	}
	if _, err := core.NormalizeModelOptions(m, o); err != nil {
		return err
	}
	if err := core.ValidateResourceReferences(m, o); err != nil {
		return err
	}
	if o.GenerateAudio != nil || o.Seed != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.Audios()) > 0 {
		return invalid("unsupported Kling 4.0 Flash option")
	}
	first, base, videoElements := 0, 0, 0
	for i, raw := range o.ImageURLs {
		if !publicURL(raw) {
			return invalid("Kling 4.0 Flash images require public HTTPS URLs")
		}
		if core.ReferenceRole(m, o, "image", i) == "first_frame" {
			first++
		}
	}
	for i, raw := range o.Videos() {
		if !publicURL(raw) {
			return invalid("Kling 4.0 Flash videos require public HTTPS URLs")
		}
		if core.ReferenceRole(m, o, "video", i) == "source" {
			base++
		}
	}
	for _, resource := range o.ResourceSnapshots {
		if !validTaskID(resource.RemoteID) {
			return invalid("invalid resolved Kling resource ID")
		}
		if resource.Kind == "element" && resource.Subtype == "video_character" {
			videoElements++
		}
	}
	if first > 1 || base > 1 {
		return invalid("Kling 4.0 Flash accepts at most one first frame and one base video")
	}
	if videoElements > 3 || videoElements+len(o.Videos()) > 5 {
		return invalid("Kling 4.0 Flash permits at most three video-character elements and five videos/video-character elements combined")
	}
	parameters, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return err
	}
	if parameters["audio"] == "original" && len(o.Videos()) == 0 {
		return invalid("Kling 4.0 Flash original audio requires a reference or base video")
	}
	_, _, _, err = flash4InputDuration(o)
	return err
}

// Only server-inspected evidence can establish input duration, including video
// elements. Missing metadata remains unknown, never zero/free input.
func flash4InputDuration(o core.MediaOptions) (float64, bool, bool, error) {
	total, hasInput, known := 0.0, len(o.Videos()) > 0, true
	add := func(seconds float64) error {
		if seconds < 3 || seconds > 20 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return fmt.Errorf("%w: Kling 4.0 Flash input videos and video-character elements require 3–20 seconds each", core.ErrOptionsInvalid)
		}
		total += seconds
		if total > 20 {
			return fmt.Errorf("%w: Kling 4.0 Flash combined video and video-character duration exceeds 20 seconds", core.ErrOptionsInvalid)
		}
		return nil
	}
	for _, raw := range o.Videos() {
		evidence, ok := o.ReferenceMediaEvidence[raw]
		if !ok {
			known = false
			continue
		}
		if evidence.Type != "video" {
			return 0, hasInput, false, fmt.Errorf("%w: inspected Kling video type is invalid", core.ErrOptionsInvalid)
		}
		if err := add(evidence.Duration); err != nil {
			return 0, hasInput, false, err
		}
	}
	for _, resource := range o.ResourceSnapshots {
		if resource.Kind != "element" || resource.Subtype != "video_character" {
			continue
		}
		hasInput = true
		var source *core.ResourceSourceEvidence
		for i := range resource.Sources {
			if resource.Sources[i].MediaType == "video" {
				if source != nil {
					return 0, hasInput, false, fmt.Errorf("%w: a video-character element must have one video source", core.ErrOptionsInvalid)
				}
				source = &resource.Sources[i]
			}
		}
		if source == nil || source.Duration == nil {
			known = false
			continue
		}
		if err := add(*source.Duration); err != nil {
			return 0, hasInput, false, err
		}
		decoded, err := hex.DecodeString(source.SHA256)
		if source.ReferenceID == "" || err != nil || len(decoded) != 32 {
			known = false
		}
	}
	return total, hasInput, known, nil
}

func (a *Adapter) generateFlash4(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.ProviderTask, error) {
	if err := validateFlash4(m, r); err != nil {
		return core.ProviderTask{}, err
	}
	// Pin the operation to the existing owner/connection-bound opaque handle so
	// polling can require Flash billing counters without changing legacy fallback.
	if _, err := legacyScope(ctx); err != nil {
		return core.ProviderTask{}, err
	}
	settings := map[string]any{"resolution": "720p"}
	if r.Options.Duration != 0 {
		settings["duration"] = r.Options.Duration
	}
	payload := map[string]any{"settings": settings}
	path := "/text-to-video/kling-4.0-flash"
	if r.Model == TextToVideo4Flash {
		payload["prompt"] = r.Prompt
		ratio := r.Options.Ratio
		if ratio == "" {
			ratio = "16:9"
		}
		settings["aspect_ratio"] = ratio
	} else if r.Model == ImageToVideo4Flash {
		path = "/image-to-video/kling-4.0-flash"
		payload["contents"] = []omni4Content{{Type: "prompt", Text: r.Prompt}, {Type: "first_frame", URL: r.Options.ImageURLs[0]}}
	} else {
		path = "/omni-video/kling-4.0-flash"
		contents := []omni4Content{{Type: "prompt", Text: omni4Prompt(r)}}
		if r.Options.Ratio != "" {
			settings["aspect_ratio"] = r.Options.Ratio
		}
		parameters, _ := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
		settings["audio"] = parameters["audio"]
		for i, raw := range r.Options.ImageURLs {
			role := core.ReferenceRole(m, r.Options, "image", i)
			if role == "reference" {
				role = "refer_image"
			}
			contents = append(contents, omni4Content{Type: role, URL: raw, ID: "image_" + strconv.Itoa(i+1)})
		}
		for i, raw := range r.Options.Videos() {
			role := "feature_video"
			if core.ReferenceRole(m, r.Options, "video", i) == "source" {
				role = "base_video"
			}
			contents = append(contents, omni4Content{Type: role, URL: raw, ID: "video_" + strconv.Itoa(i+1)})
		}
		for _, resource := range r.Options.ResourceSnapshots {
			contents = append(contents, omni4Content{Type: "element", ID: resource.ReferenceID, ElementID: resource.RemoteID})
		}
		payload["contents"] = contents
	}
	if r.TaskID != "" || r.Options.Watermark != nil {
		options := &omni4Options{ExternalTaskID: r.TaskID}
		if r.Options.Watermark != nil {
			options.Watermark = &omni4Watermark{Enabled: *r.Options.Watermark}
		}
		payload["options"] = options
	}
	task, err := a.submit(ctx, http.MethodPost, path, payload)
	if err != nil {
		return core.ProviderTask{}, err
	}
	handle, err := a.signLegacy(ctx, r.Model, task.ID)
	return core.ProviderTask{ID: handle}, err
}

func quoteFlash4(m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if err := validateFlash4(m, r); err != nil {
		return core.TaskCost{}, "", err
	}
	if r.Options.Duration == 0 {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	input, hasVideo, known, err := flash4InputDuration(r.Options)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	rate, quantity, rule := money.USD(94000), float64(r.Options.Duration), ""
	if hasVideo {
		if !known {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		rate, quantity, rule = 63000, quantity+max(input, quantity), "output_plus_max_input_output"
	}
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	billInput := false
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{
		Currency: "USD", Unit: "second", Price: rate, Per: 1, Source: priceSource, Version: "2026-11-04", Region: "Singapore",
		BillInputVideo: &billInput, DurationBillingRule: rule, CheckedAt: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
		EstimateDescription: "Published Kling 4.0 Flash USD list tariff. With video input, billed seconds are output + max(total input, output), including inspected video-character elements. Unknown duration cannot be quoted. Account entitlement, discounts, package units and actual charges remain separate.",
	}}, "", nil
}

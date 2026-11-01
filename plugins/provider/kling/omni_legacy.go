package kling

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const (
	OmniVideo3  = "kling-3.0-omni-video"
	OmniVideoO1 = "kling-o1-omni-video"
)

func isLegacyOmniOperation(id string) bool { return id == OmniVideo3 || id == OmniVideoO1 }
func legacyOmniModels() []core.MediaModel {
	models := make([]core.MediaModel, 0, 2)
	for _, id := range []string{OmniVideo3, OmniVideoO1} {
		m := core.MediaModel{ID: id, DisplayName: "Kling 3.0 Omni · Reference video", MediaType: "video", NoSeed: true, NoLanguage: true, NoInstructions: true,
			MaxPromptCharacters: 3072, Resolutions: []string{"720p", "1080p", "4k"}, Ratios: []string{"16:9", "9:16", "1:1"}, MinDuration: 3, MaxDuration: 15, DefaultDuration: 5, DurationStep: 1, RatioMayBeInferred: true,
			SupportsImageInput: true, MaxImageInputs: 7, SupportsVideoInput: true, MaxReferenceInputs: 7, ReferencePromptSyntax: "kling-id", ReferencePromptTypes: []string{"image", "video"},
			ReferenceModes:       []core.ReferenceMode{{ID: "references", Label: "Images, video and managed elements", MaxImages: 7, MaxVideos: 1, MaxVisuals: 7}},
			ReferenceRoleSchemas: []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference", "first_frame", "last_frame"}, Default: "reference"}, {Kind: "video", Roles: []string{"reference", "source"}, Default: "reference"}},
			ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "element", MaxCount: 7, Subtypes: []string{"multi_image", "video_character"}}},
			Parameters:           []core.ParameterField{{Key: "audio", Label: "Video sound", Type: "select", Choices: []string{"off", "native", "original"}, Default: "off"}, {Key: "multi_shot", Label: "Multi-shot video", Type: "boolean"}},
			ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 7, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: 50 << 20, MinDimension: 300, MinRatio: .4, MaxRatio: 2.5}, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 200 << 20, MinDuration: 3, MaxDuration: 15.5, MinDimension: 700, MaxDimension: 4553, MaxPixels: 8294400, MinRatio: .4, MaxRatio: 2, MinFPS: 24, MaxFPS: 60}},
			Pricing:              core.MediaModelPricing{Known: true, ReferencePriceUSD: .42, ReferenceDescription: "Published USD balance list price for 5 seconds at 720p without reference video or native audio; reference-video and native-audio tariffs differ."},
		}
		if id == OmniVideoO1 {
			m.DisplayName, m.MaxPromptCharacters, m.MaxDuration = "Kling O1 · Reference video", 2500, 10
			m.Resolutions = []string{"720p", "1080p"}
			m.Parameters = []core.ParameterField{{Key: "audio", Label: "Reference video sound", Type: "select", Choices: []string{"off", "original"}, Default: "off"}}
			m.ResourceInputSchemas[0].Subtypes = []string{"multi_image"}
			m.ReferenceMediaLimits.Video.MaxDuration, m.ReferenceMediaLimits.Video.MaxDimension = 10, 2160
			m.ReferenceMediaLimits.Video.MaxPixels, m.ReferenceMediaLimits.Video.MinRatio, m.ReferenceMediaLimits.Video.MaxRatio = 0, 0, 0
		}
		models = append(models, m)
	}
	return models
}
func legacyOmniModel(id string) (core.MediaModel, bool) {
	for _, m := range legacyOmniModels() {
		if m.ID == id {
			return m, true
		}
	}
	return core.MediaModel{}, false
}

type legacyOmniIntent struct {
	first, last, references, multiElements, videoElements int
	feature, base                                         bool
	audio                                                 string
	multiShot                                             bool
}

func legacyOmniRequestIntent(m core.MediaModel, r core.MediaRequest) (legacyOmniIntent, error) {
	var intent legacyOmniIntent
	p, err := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
	if err != nil {
		return intent, err
	}
	intent.audio = p["audio"].(string)
	for i := range r.Options.ImageURLs {
		switch core.ReferenceRole(m, r.Options, "image", i) {
		case "first_frame":
			intent.first++
		case "last_frame":
			intent.last++
		default:
			intent.references++
		}
	}
	for i := range r.Options.Videos() {
		if core.ReferenceRole(m, r.Options, "video", i) == "source" {
			intent.base = true
		} else {
			intent.feature = true
		}
	}
	for _, s := range r.Options.ResourceSnapshots {
		switch s.Subtype {
		case "multi_image":
			intent.multiElements++
		case "video_character":
			intent.videoElements++
		}
	}
	intent.multiShot = !intent.base
	if value, ok := p["multi_shot"].(bool); ok {
		intent.multiShot = value
	}
	return intent, nil
}
func (a *Adapter) validateLegacyOmniRequest(r core.MediaRequest) error {
	invalid := func(s string) error { return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, s) }
	m, ok := legacyOmniModel(r.Model)
	if !ok {
		return invalid("unknown Kling Omni operation")
	}
	prompt := klingPrompt(r.Prompt)
	if strings.TrimSpace(prompt) == "" || utf8.RuneCountInString(prompt) > m.MaxPromptCharacters {
		return invalid(fmt.Sprintf("Kling Omni prompt must contain 1–%d characters", m.MaxPromptCharacters))
	}
	o := r.Options
	if _, err := core.NormalizeModelOptions(m, o); err != nil {
		return err
	}
	if err := core.ValidateResourceReferences(m, o); err != nil {
		return err
	}
	if o.GenerateAudio != nil || o.Seed != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.Audios()) > 0 {
		return invalid("unsupported Kling Omni control; use the documented audio parameter and managed element bindings")
	}
	for _, raw := range append(slices.Clone(o.ImageURLs), o.Videos()...) {
		if !publicURL(raw) {
			return invalid("Kling Omni references require public HTTPS URLs")
		}
	}
	for _, s := range o.ResourceSnapshots {
		if !validTaskID(s.RemoteID) {
			return invalid("invalid resolved Kling element ID")
		}
	}
	for _, raw := range o.Videos() {
		if e, ok := o.ReferenceMediaEvidence[raw]; ok && (e.Type != "video" || e.Duration < m.ReferenceMediaLimits.Video.MinDuration || e.Duration > m.ReferenceMediaLimits.Video.MaxDuration || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0)) {
			return invalid("inspected reference video duration is unsupported by this Kling Omni operation")
		}
	}
	intent, err := legacyOmniRequestIntent(m, r)
	if err != nil {
		return err
	}
	if intent.first > 1 || intent.last > 1 || intent.last > 0 && intent.first == 0 {
		return invalid("Kling Omni accepts one first frame, optionally followed by one last frame; last frame alone is unsupported")
	}
	if intent.base && (intent.first > 0 || intent.last > 0) {
		return invalid("base-video editing does not support first or last frame inputs")
	}
	if intent.audio == "original" && !intent.base && !intent.feature {
		return invalid("original sound requires a reference video")
	}
	images := len(o.ImageURLs)
	elements := intent.multiElements + intent.videoElements
	if intent.base || intent.feature {
		if images+intent.multiElements > 4 {
			return invalid("reference video permits at most four images and multi-image elements combined")
		}
	} else if images+intent.multiElements > 7 {
		return invalid("Kling Omni permits at most seven images and multi-image elements combined")
	}
	if r.Model == OmniVideoO1 {
		if intent.last > 0 && (intent.references > 0 || elements > 0) {
			return invalid("Kling O1 first and last frames do not allow extra reference images or elements")
		}
		if intent.feature && intent.last > 0 {
			return invalid("Kling O1 feature video supports a first frame but no last frame")
		}
		if intent.first > 0 && intent.references == 0 && len(o.Videos()) == 0 {
			duration := durationFor(m, o)
			if duration != 5 && duration != 10 {
				return invalid("Kling O1 frame generation without reference images or video supports only 5 or 10 seconds")
			}
		}
		return nil
	}
	if intent.videoElements > 3 {
		return invalid("Kling 3.0 Omni supports at most three video-character elements without a reference video")
	}
	if intent.first > 0 && elements > 3 {
		return invalid("Kling 3.0 Omni keyframes permit at most three managed elements")
	}
	if intent.videoElements > 0 {
		if len(o.Videos()) > 0 {
			if intent.videoElements > 1 || images > 0 || intent.multiElements > 0 {
				return invalid("reference video and video-character element permit one character and no image or multi-image references")
			}
		} else if images+intent.multiElements > 4 {
			return invalid("video-character elements permit at most four images and multi-image elements combined")
		}
	}
	if intent.feature && (!intent.multiShot || intent.audio != "off") {
		return invalid("Kling 3.0 Omni feature video requires multi_shot=true and audio=off")
	}
	if intent.base && (intent.multiShot || intent.audio == "native") {
		return invalid("Kling 3.0 Omni base-video editing requires multi_shot=false and does not support native audio")
	}
	return nil
}

type legacyOmniContent struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	URL       string `json:"url,omitempty"`
	ElementID string `json:"element_id,omitempty"`
	ID        string `json:"id,omitempty"`
}
type legacyOmniSettings struct {
	Resolution  string `json:"resolution"`
	Duration    int    `json:"duration"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Audio       string `json:"audio"`
	MultiShot   *bool  `json:"multi_shot,omitempty"`
}
type legacyOmniOptions struct {
	ExternalTaskID string               `json:"external_task_id,omitempty"`
	Watermark      *legacyOmniWatermark `json:"watermark_info,omitempty"`
}
type legacyOmniWatermark struct {
	Enabled bool `json:"enabled"`
}
type legacyOmniPayload struct {
	Contents []legacyOmniContent `json:"contents"`
	Settings legacyOmniSettings  `json:"settings"`
	Options  *legacyOmniOptions  `json:"options,omitempty"`
}

func (a *Adapter) generateLegacyOmni(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.validateLegacyOmniRequest(r); err != nil {
		return core.ProviderTask{}, err
	}
	m, _ := legacyOmniModel(r.Model)
	intent, _ := legacyOmniRequestIntent(m, r)
	p := legacyOmniPayload{Contents: []legacyOmniContent{{Type: "prompt", Text: klingPrompt(r.Prompt)}}, Settings: legacyOmniSettings{Resolution: resolutionFor(m, r.Options), Duration: durationFor(m, r.Options), AspectRatio: r.Options.Ratio, Audio: intent.audio}}
	if p.Settings.AspectRatio == "" && intent.first == 0 && len(r.Options.Videos()) == 0 {
		p.Settings.AspectRatio = "16:9"
	}
	route := "/omni-video/kling-o1"
	if r.Model == OmniVideo3 {
		route = "/omni-video/kling-3.0-omni"
		p.Settings.MultiShot = &intent.multiShot
	}
	for i, raw := range r.Options.ImageURLs {
		role := core.ReferenceRole(m, r.Options, "image", i)
		if role == "reference" {
			role = "refer_image"
		}
		p.Contents = append(p.Contents, legacyOmniContent{Type: role, URL: raw, ID: "image_" + strconv.Itoa(i+1)})
	}
	for i, raw := range r.Options.Videos() {
		role := "feature_video"
		if core.ReferenceRole(m, r.Options, "video", i) == "source" {
			role = "base_video"
		}
		p.Contents = append(p.Contents, legacyOmniContent{Type: role, URL: raw, ID: "video_" + strconv.Itoa(i+1)})
	}
	for _, s := range r.Options.ResourceSnapshots {
		p.Contents = append(p.Contents, legacyOmniContent{Type: "element", ElementID: s.RemoteID, ID: s.ReferenceID})
	}
	if r.TaskID != "" || r.Options.Watermark != nil {
		p.Options = &legacyOmniOptions{ExternalTaskID: r.TaskID}
		if r.Options.Watermark != nil {
			p.Options.Watermark = &legacyOmniWatermark{Enabled: *r.Options.Watermark}
		}
	}
	return a.submit(ctx, http.MethodPost, route, p)
}

func (a *Adapter) quoteLegacyOmni(r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.validateLegacyOmniRequest(r); err != nil {
		return core.TaskCost{}, "", err
	}
	m, _ := legacyOmniModel(r.Model)
	intent, _ := legacyOmniRequestIntent(m, r)
	// The published input-video tariff names feature/base videos. It does not
	// establish a tariff for a managed video-character element. Do not borrow the
	// Kling 4.0 billing formula or turn unknown resource duration into zero.
	if intent.videoElements > 0 {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	rate := money.USD(84000)
	resolution := resolutionFor(m, r.Options)
	if resolution == "1080p" {
		rate = 112000
	}
	if resolution == "4k" {
		rate = 420000
	}
	if len(r.Options.Videos()) > 0 {
		rate = 126000
		if resolution == "1080p" {
			rate = 168000
		}
		if resolution == "4k" {
			rate = 420000
		}
	} else if intent.audio == "native" {
		rate = 112000
		if resolution == "1080p" {
			rate = 140000
		}
		if resolution == "4k" {
			rate = 420000
		}
	}
	quantity := float64(durationFor(m, r.Options))
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	billInput := false
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{Currency: "USD", Unit: "second", Price: rate, Per: 1, Source: priceSource, Version: "2026-10-31", Region: "Singapore", BillInputVideo: &billInput, CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: "Published USD balance list price for requested/default output duration only; feature/base video selects the reference-video rate, not extra billable input seconds. Account access, discounts and actual charges remain unverified."}}, "", nil
}

package kling

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const OmniVideo4 = "kling-4.0-omni-video"

func omni4Model() core.MediaModel {
	return core.MediaModel{ID: OmniVideo4, DisplayName: "Kling 4.0 · Omni Reference", MediaType: "video", NoSeed: true, NoLanguage: true, NoInstructions: true,
		MaxPromptCharacters: 26000, PromptPolicy: &core.PromptPolicy{MaxCharacters: 26000, ASCIIBudget: 26000, NonASCIIBudget: 11000},
		Resolutions: []string{"1080p", "720p"}, Ratios: []string{"adaptive", "21:9", "16:9", "9:16", "1:1"}, MinDuration: 3, MaxDuration: 30, DefaultDuration: 10, DurationStep: 1, DurationMayBeInferred: true, RatioMayBeInferred: true,
		SupportsImageInput: true, MaxImageInputs: 10, SupportsVideoInput: true, MaxReferenceInputs: 15,
		ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "element", SupportsPresets: true, MaxCount: 7, Subtypes: []string{"multi_image", "video_character"}}, {Kind: "voice", SupportsPresets: true, MaxCount: 3}}, ReferencePromptSyntax: "kling-id", ReferencePromptTypes: []string{"image", "video"},
		ReferenceModes:       []core.ReferenceMode{{ID: "references", Label: "References", MaxImages: 10, MaxVideos: 5, MaxVisuals: 15}},
		ReferenceRoleSchemas: []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference", "first_frame", "middle_frame", "last_frame"}, Default: "reference"}, {Kind: "video", Roles: []string{"reference", "source"}, Default: "reference"}},
		ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 15, Image: core.MediaFileLimits{ContentTypes: []string{"image/jpeg", "image/png"}, MaxBytes: 30 << 20, MinDimension: 300, MinRatio: .4, MaxRatio: 2.5}, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 300000000, MaxBytesInclusive: true, MinDuration: 3, MaxDuration: 30, MinTotalDuration: 3, MaxTotalDuration: 30, MinDimension: 700, MaxDimension: 4553, MaxPixels: 8294400, MinRatio: .4, MaxRatio: 2.5, MinFPS: 24, MaxFPS: 60}},
		Pricing:              core.MediaModelPricing{Known: false, ReferenceDescription: "Select an explicit output duration for a quote. Video input requires inspected input duration; unknown duration is never treated as zero."},
	}
}

var uiKlingMention = regexp.MustCompile(`(?i)(^|[^\pL\pN_])@(image|video)([1-9][0-9]*)\b`)

// Persisted prompts retain user intent. Translate only shared UI addresses at the
// provider boundary; native @image_1/@video_1 IDs and arbitrary @names stay intact.
func klingPrompt(prompt string) string {
	return uiKlingMention.ReplaceAllStringFunc(prompt, func(m string) string {
		parts := uiKlingMention.FindStringSubmatch(m)
		return parts[1] + "@" + strings.ToLower(parts[2]) + "_" + parts[3]
	})
}

// Managed aliases take precedence over UI shorthand, so an element named
// image1 remains @image1 rather than being rewritten to an unrelated image_1.
func omni4Prompt(r core.MediaRequest) string {
	aliases := map[string]bool{}
	for _, resource := range r.Options.ResourceReferences {
		aliases[resource.ReferenceID] = true
	}
	return uiKlingMention.ReplaceAllStringFunc(r.Prompt, func(match string) string {
		p := uiKlingMention.FindStringSubmatch(match)
		if aliases[p[2]+p[3]] {
			return match
		}
		return p[1] + "@" + strings.ToLower(p[2]) + "_" + p[3]
	})
}

// Duration is inspected server evidence. Missing source identity/hash/duration
// remains unknown; IsPreset and the presence of a remote ID cannot fill it in.
func omni4InputDuration(o core.MediaOptions) (float64, bool, bool, error) {
	total, hasInput, known := 0.0, len(o.Videos()) > 0, true
	add := func(seconds float64) error {
		if seconds < 3 || seconds > 30 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			return fmt.Errorf("%w: Kling 4.0 input videos and video-character elements require 3–30 seconds each", core.ErrOptionsInvalid)
		}
		total += seconds
		if total > 30 {
			return fmt.Errorf("%w: Kling 4.0 combined video and video-character duration exceeds 30 seconds", core.ErrOptionsInvalid)
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

func validateOmni4(m core.MediaModel, r core.MediaRequest) error {
	invalid := func(s string) error { return errors.Join(core.ErrOptionsInvalid, errors.New(s)) }
	if strings.TrimSpace(r.Prompt) == "" {
		return invalid("Kling Omni requires a prompt")
	}
	if err := core.ValidatePromptPolicy(m, omni4Prompt(r)); err != nil {
		return err
	}
	o := r.Options
	if _, err := core.NormalizeModelOptions(m, o); err != nil {
		return err
	}
	if err := core.ValidateResourceReferences(m, o); err != nil {
		return err
	}
	if o.GenerateAudio != nil || o.Seed != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.Audios()) > 0 {
		return invalid("unsupported Kling 4.0 Omni option; voices require managed voice bindings")
	}
	first, last, base, videoElements := 0, 0, 0, 0
	for i, raw := range o.ImageURLs {
		if !publicURL(raw) {
			return invalid("Kling Omni images require public HTTPS URLs")
		}
		switch core.ReferenceRole(m, o, "image", i) {
		case "first_frame":
			first++
		case "last_frame":
			last++
		}
	}
	for i, raw := range o.Videos() {
		if !publicURL(raw) {
			return invalid("Kling Omni videos require public HTTPS URLs")
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
	if first > 1 || last > 1 || base > 1 {
		return invalid("Kling Omni accepts at most one first frame, last frame and base video")
	}
	if videoElements > 3 || videoElements+len(o.Videos()) > 5 {
		return invalid("Kling 4.0 permits at most three video-character elements and five videos/video-character elements combined")
	}
	_, _, _, err := omni4InputDuration(o)
	return err
}

type omni4Content struct {
	Type      string `json:"type"`
	Text      string `json:"text,omitempty"`
	URL       string `json:"url,omitempty"`
	ElementID string `json:"element_id,omitempty"`
	VoiceID   string `json:"voice_id,omitempty"`
	ID        string `json:"id,omitempty"`
}
type omni4Settings struct {
	Resolution  string `json:"resolution"`
	Duration    *int   `json:"duration,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
}
type omni4Watermark struct {
	Enabled bool `json:"enabled"`
}
type omni4Options struct {
	ExternalTaskID string          `json:"external_task_id,omitempty"`
	Watermark      *omni4Watermark `json:"watermark_info,omitempty"`
}
type omni4Payload struct {
	Contents []omni4Content `json:"contents"`
	Settings omni4Settings  `json:"settings"`
	Options  *omni4Options  `json:"options,omitempty"`
}

func (a *Adapter) generateOmni4(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.ProviderTask, error) {
	if err := validateOmni4(m, r); err != nil {
		return core.ProviderTask{}, err
	}
	p := omni4Payload{Contents: []omni4Content{{Type: "prompt", Text: omni4Prompt(r)}}, Settings: omni4Settings{Resolution: resolutionFor(m, r.Options), AspectRatio: r.Options.Ratio}}
	if r.Options.Duration != 0 {
		duration := r.Options.Duration
		p.Settings.Duration = &duration
	}
	for i, raw := range r.Options.ImageURLs {
		role := core.ReferenceRole(m, r.Options, "image", i)
		if role == "reference" {
			role = "refer_image"
		}
		p.Contents = append(p.Contents, omni4Content{Type: role, URL: raw, ID: "image_" + strconv.Itoa(i+1)})
	}
	for i, raw := range r.Options.Videos() {
		role := "feature_video"
		if core.ReferenceRole(m, r.Options, "video", i) == "source" {
			role = "base_video"
		}
		p.Contents = append(p.Contents, omni4Content{Type: role, URL: raw, ID: "video_" + strconv.Itoa(i+1)})
	}
	for _, resource := range r.Options.ResourceSnapshots {
		c := omni4Content{Type: resource.Kind, ID: resource.ReferenceID}
		if resource.Kind == "element" {
			c.ElementID = resource.RemoteID
		} else {
			c.VoiceID = resource.RemoteID
		}
		p.Contents = append(p.Contents, c)
	}
	if r.TaskID != "" || r.Options.Watermark != nil {
		p.Options = &omni4Options{ExternalTaskID: r.TaskID}
		if r.Options.Watermark != nil {
			p.Options.Watermark = &omni4Watermark{Enabled: *r.Options.Watermark}
		}
	}
	return a.submit(ctx, http.MethodPost, "/omni-video/kling-4.0", p)
}

func (a *Adapter) quoteOmni4(m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	if err := validateOmni4(m, r); err != nil {
		return core.TaskCost{}, "", err
	}
	if r.Options.Duration == 0 {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	resolution := resolutionFor(m, r.Options)
	rate := money.USD(235000)
	if resolution == "720p" {
		rate = 157000
	}
	quantity := float64(r.Options.Duration)
	source := priceSource
	rule := ""
	billInput := false
	accountSpecific := false
	input, hasVideo, known, err := omni4InputDuration(r.Options)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	if hasVideo {
		if !known {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		quantity += max(input, quantity)
		rate = 141000
		if resolution == "720p" {
			rate = 94000
		}
		rule = "output_plus_max_input_output"
	} else if override, configured := a.kling4Prices[resolution]; configured {
		rate = override
		source = "Operator-confirmed Kling 4.0 account tariff (USD/second)"
		accountSpecific = true
	}
	amount, err := rate.Multiply(quantity, 1)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	description := "Published USD balance tariff; input-video billing uses output + max(total input, output), including inspected video-character element duration. Unknown resource duration cannot be quoted. Account access, discounts, units and actual charges remain separate."
	return core.TaskCost{ExpectedUSD: amount, EstimatedUnits: quantity, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{Currency: "USD", Unit: "second", Price: rate, Per: 1, Source: source, Version: "2026-10-31", Region: "Singapore", AccountSpecific: accountSpecific, BillInputVideo: &billInput, DurationBillingRule: rule, CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: description}}, "", nil
}

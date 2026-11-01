package minimax

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

const RegenerationVideo = "MiniMax-H3-Regeneration-Video"

// Gateway parser/inspection ceiling; MiniMax does not publish a separate base-video byte cap.
const maxBaseVideoBytes = 200 << 20

func baseRegenerationModel() core.MediaModel {
	m := describe(operation{id: "MiniMax-H3", upstream: "MiniMax-H3", kind: "h3"})
	m.ID = RegenerationVideo
	m.DisplayName = "MiniMax H3 · Regenerate inspected 768P source to 2K"
	m.MaxPromptCharacters = 40000
	m.PromptPolicy = &core.PromptPolicy{MaxCharacters: 40000}
	m.Resolutions = []string{"2k"}
	m.Ratios = nil
	m.MinDuration = 0
	m.MaxDuration = 0
	m.DefaultDuration = 0
	m.DurationStep = 0
	m.DurationFromInput = true
	m.SupportsAudio = false
	m.AudioAlwaysOn = false
	m.NoWatermark = false
	m.RequiresVideoInput = true
	m.MaxReferenceInputs = 13
	m.ResourceInputSchemas[0].MaxCount = 13
	m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Original content and one inspected base video", MaxImages: 9, MinVideos: 1, MaxVideos: 4, MaxAudios: 3}}
	m.ReferenceRoleSchemas = []core.ReferenceRoleSchema{{Kind: "image", Roles: []string{"reference_image", "first_frame", "last_frame"}, Default: "reference_image"}, {Kind: "video", Roles: []string{"reference_video", "base_video"}, Default: "reference_video"}, {Kind: "audio", Roles: []string{"reference_audio"}, Default: "reference_audio"}}
	regular := h3Limits()
	m.ReferenceMediaLimits = regular.Clone()
	m.ReferenceMediaLimits.MaxFiles = 13
	m.ReferenceMediaLimits.Video = core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: maxBaseVideoBytes}
	yes := true
	m.ReferenceRoleLimits = []core.MediaRoleLimits{{Kind: "video", Role: "reference_video", Limits: regular.Video}, {Kind: "video", Role: "base_video", Limits: core.MediaFileLimits{ContentTypes: []string{"video/mp4"}, MinFrames: 107, MaxFrames: 362, MinFPS: 24, MaxFPS: 24, MinPixels: 589824, MaxPixels: 1032192, RequireAudio: &yes}}}
	m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Source video regeneration contract", URL: docsBase + "api-reference/video-generation-v2-regeneration"})
	m.Metadata.Description = "Exactly one inspected base_video matching MiniMax-H3 768P output: 24 fps, audio track, dimensions divisible by 32, area 589,824–1,032,192 pixels, 107–362 frames in steps of 17. Submit the exact final prompt/materials used for the original output; reference video limits apply separately."
	m.Pricing = core.MediaModelPricing{Known: true, ReferenceDescription: "USD 0.05 per output second plus original reference video USD 0.05/second and original images beyond 5 USD 0.025/image, charged again. Inspected source duration estimates output quantity; final vendor usage is authoritative."}
	return m
}
func (a *Adapter) validateBaseRegeneration(ctx context.Context, r core.MediaRequest) (int, error) {
	m := baseRegenerationModel()
	o := r.Options
	if strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(minimaxPrompt(r.Prompt)) > 40000 {
		return -1, invalid("regeneration requires the original final prompt, at most 40,000 characters")
	}
	if o.Resolution != "" && o.Resolution != "2k" || o.Duration != 0 || o.Ratio != "" || o.OutputCount != 0 || o.Seed != nil || o.GenerateAudio != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.Parameters)+len(o.ReferenceVoices)+len(o.ResourceReferences)+len(o.ResourceSnapshots) > 0 {
		return -1, invalid("unsupported standalone regeneration option")
	}
	if len(o.VideoURLs) > 0 && o.VideoURL != "" && o.VideoURL != o.VideoURLs[0] || len(o.AudioURLs) > 0 && o.AudioURL != "" && o.AudioURL != o.AudioURLs[0] {
		return -1, invalid("conflicting media references")
	}
	if _, err := effectiveMode(m, o); err != nil {
		return -1, err
	}
	if err := core.ValidateReferenceRoles(m, o); err != nil {
		return -1, err
	}
	base := -1
	inputSeconds, audioSeconds := 0., 0.
	frames := map[string]bool{}
	referenceImages := 0
	for i, u := range o.ImageURLs {
		role := core.ReferenceRole(m, o, "image", i)
		if role != "reference_image" {
			if frames[role] {
				return -1, invalid("duplicate original frame role")
			}
			frames[role] = true
		} else {
			referenceImages++
		}
		if !a.validRegenerationInput(ctx, u, "image", 30_000_000) {
			return -1, invalid("invalid original image input")
		}
	}
	for i, u := range o.Videos() {
		role := core.ReferenceRole(m, o, "video", i)
		e, known := o.ReferenceMediaEvidence[u]
		if role == "base_video" {
			if base >= 0 {
				return -1, invalid("exactly one base_video is required")
			}
			base = i
			digest, err := hex.DecodeString(e.SHA256)
			_, idErr := uuid.Parse(e.ReferenceID)
			if !known || err != nil || len(digest) != 32 || idErr != nil || e.Type != "video" || e.ContentType != "video/mp4" || e.SizeBytes <= 0 || e.SizeBytes >= maxBaseVideoBytes || e.Width <= 0 || e.Height <= 0 || e.Width%32 != 0 || e.Height%32 != 0 || int64(e.Width) > 1032192/int64(e.Height) || int64(e.Width)*int64(e.Height) < 589824 || int64(e.Width)*int64(e.Height) > 1032192 || math.IsNaN(e.FPS) || math.IsInf(e.FPS, 0) || math.Abs(e.FPS-24) > 0.000001 || e.FrameCount < 107 || e.FrameCount > 362 || (e.FrameCount-107)%17 != 0 || e.AudioStreams == nil || *e.AudioStreams <= 0 || e.VideoStreams == nil || *e.VideoStreams <= 0 || e.Duration <= 0 || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0) {
				return -1, invalid("base_video requires inspected 24 fps video with audio and the documented 768P dimensions and frame count")
			}

			timeline := float64(e.FrameCount) / 24
			if math.Abs(e.Duration-timeline) > 1.0/24+0.000001 {
				return -1, invalid("base video inspected duration and frame timeline are inconsistent")
			}
		} else {
			if !a.validRegenerationInput(ctx, u, "video", 50_000_000) {
				return -1, invalid("invalid original reference video")
			}
			if known {
				if e.Type != "video" || e.Duration < 2 || e.Duration > 15 || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0) {
					return -1, invalid("original reference videos require 2–15 seconds")
				}
				if !core.AudioCodecsAllowed(e.AudioStreams, e.AudioCodecs, h3Limits().Video.AllowedAudioCodecs) {
					return -1, invalid("original reference-video embedded audio requires inspected AAC/MP3 or verified silence")
				}
				inputSeconds += e.Duration
			}
		}
		if !a.validRegenerationInput(ctx, u, "video", maxBaseVideoBytes) {
			return -1, invalid("base and reference videos require authorized URLs or managed files")
		}
	}
	if frames["last_frame"] && !frames["first_frame"] {
		return -1, invalid("original last_frame must be paired with first_frame for regeneration")
	}
	if base < 0 {
		return -1, invalid("exactly one video input must have role base_video")
	}
	if len(o.Videos())-1 > 3 || inputSeconds > 15 {
		return -1, invalid("original reference videos exceed 3 files or 15 seconds")
	}
	for _, u := range o.Audios() {
		if !a.validRegenerationInput(ctx, u, "audio", 15_000_000) {
			return -1, invalid("invalid original audio reference")
		}
		if e, ok := o.ReferenceMediaEvidence[u]; ok {
			if e.Type != "audio" || e.Duration < 2 || e.Duration > 15 || math.IsNaN(e.Duration) || math.IsInf(e.Duration, 0) {
				return -1, invalid("original reference audio requires2–15seconds")
			}
			audioSeconds += e.Duration
		}
	}
	if audioSeconds > 15 || len(o.ImageURLs)+len(o.Videos())-1+len(o.Audios()) > 12 {
		return -1, invalid("original inputs exceed documented aggregate limits")
	}
	if len(frames) > 0 && (referenceImages > 0 || len(o.Videos()) > 1 || len(o.Audios()) > 0) {
		return -1, invalid("original frame and reference workflows are mutually exclusive")
	}
	return base, nil
}
func (a *Adapter) generateBaseRegeneration(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	base, err := a.validateBaseRegeneration(ctx, r)
	if err != nil {
		return core.ProviderTask{}, err
	}
	r, err = a.resolveRequestFiles(ctx, r)
	if err != nil {
		return core.ProviderTask{}, err
	}
	m := baseRegenerationModel()
	content := []any{map[string]any{"type": "text", "text": minimaxPrompt(r.Prompt)}}
	for i, u := range r.Options.ImageURLs {
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": u}, "role": core.ReferenceRole(m, r.Options, "image", i)})
	}
	for i, u := range r.Options.Videos() {
		if i == base {
			continue
		}
		content = append(content, map[string]any{"type": "video_url", "video_url": map[string]string{"url": u}, "role": "reference_video"})
	}
	for _, u := range r.Options.Audios() {
		content = append(content, map[string]any{"type": "audio_url", "audio_url": map[string]string{"url": u}, "role": "reference_audio"})
	}
	content = append(content, map[string]any{"type": "video_url", "video_url": map[string]string{"url": r.Options.Videos()[base]}, "role": "base_video"})
	body := map[string]any{"model": "MiniMax-H3", "content": content, "resolution": "2K"}
	if r.Options.Watermark != nil {
		body["aigc_watermark"] = *r.Options.Watermark
	}
	var out struct {
		ID string `json:"task_id"`
	}
	if err = a.json(ctx, "POST", "/v2/video_regeneration", body, &out); err != nil {
		return core.ProviderTask{}, err
	}
	if !numericID.MatchString(out.ID) {
		return core.ProviderTask{}, errors.New("invalid MiniMax regeneration task ID")
	}
	return core.ProviderTask{ID: a.seal(ctx, handle{ID: out.ID, Model: "MiniMax-H3", Kind: "regeneration"})}, nil
}
func (a *Adapter) quoteBaseRegeneration(ctx context.Context, r core.MediaRequest) (core.TaskCost, string, error) {
	base, err := a.validateBaseRegeneration(ctx, r)
	if err != nil {
		return core.TaskCost{}, "", err
	}
	duration := r.Options.ReferenceMediaEvidence[r.Options.Videos()[base]].Duration
	input := 0.
	for i, u := range r.Options.Videos() {
		if i == base {
			continue
		}
		e, ok := r.Options.ReferenceMediaEvidence[u]
		if !ok || e.Duration <= 0 {
			return core.TaskCost{}, "", core.ErrPricingUnavailable
		}
		input += e.Duration
	}
	q := core.TaskCost{Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: RegenerationTariff(), EstimatedUnits: duration}
	quantities := map[string]float64{"output_video_duration": duration, "input_video_duration": input, "billable_input_images": float64(max(0, len(r.Options.ImageURLs)-5))}
	for i, c := range q.Pricing.Components {
		v := quantities[c.Metric]
		q.Pricing.Components[i].EstimatedUnits = v
		part, e := c.Price.Multiply(v, c.Per)
		if e != nil {
			return core.TaskCost{}, "", e
		}
		q.ExpectedUSD += part
	}
	q.Pricing.EstimateDescription = "Uses inspected source duration as output estimate; original inputs are charged again. Final measured provider usage determines metered cost."
	return q, "", nil
}

func (a *Adapter) validRegenerationInput(ctx context.Context, value, kind string, limit int) bool {
	if strings.HasPrefix(value, "asset://") {
		_, err := a.resolveFileInput(ctx, value, kind)
		return err == nil
	}
	return validInput(value, kind, limit)
}

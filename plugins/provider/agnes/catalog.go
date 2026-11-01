package agnes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

var modelIDs = []string{"agnes-image-2.0-flash", "agnes-image-2.1-flash", DefaultModel, "agnes-video-2.5", "agnes-video-2.5-flash"}

func catalog() []core.MediaModel {
	result := make([]core.MediaModel, 0, len(modelIDs))
	for _, id := range modelIDs {
		result = append(result, describeModel(id))
	}
	return result
}

func describeModel(id string) core.MediaModel {
	m := core.MediaModel{ID: id, DisplayName: id, NoWatermark: true, NoLanguage: true, NoInstructions: true, SupportsImageInput: true}
	m.Pricing = core.MediaModelPricing{Known: true, ReferenceDescription: "Published temporary $0 offer; account eligibility and final charges are unverified."}
	if strings.HasPrefix(id, "agnes-image-") {
		m.MediaType, m.NoSeed, m.MaxImageInputs = "image", true, 14 // Gateway safety cap; no vendor maximum is published.
		m.Resolutions = []string{"1K", "2K", "3K", "4K"}
		m.Ratios = []string{"1:1", "3:4", "4:3", "16:9", "9:16", "2:3", "3:2", "21:9"}
		if id == "agnes-image-2.0-flash" {
			// 2.0 documents exact sizes, without a ratio control.
			m.Resolutions, m.Ratios = []string{"1024x1024", "1024x768", "768x1024"}, nil
		}
		m.ReferenceModes = []core.ReferenceMode{{ID: "references", Label: "Image references", MaxImages: 14}}
		return m
	}
	m.MediaType = "video"
	if id == "agnes-video-2.5" || id == "agnes-video-2.5-flash" {
		// Reviewed reference limits: https://wiki.agnes-ai.com/en/docs/agnes-video-25
		m.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: 12,
			Image: core.MediaFileLimits{ContentTypes: []string{"image/png", "image/jpeg", "image/gif", "image/webp"}, MaxBytes: 15_000_000, MaxTotalBytes: 50_000_000, MinDimension: 256, MaxDimension: 5760},
			Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 50_000_000, MinTotalDuration: 2, MaxTotalDuration: 12, MinFPS: 24, MaxFPS: 60},
			Audio: core.MediaFileLimits{ContentTypes: []string{"audio/mpeg", "audio/wave", "audio/wav", "audio/x-wav"}, MaxBytes: 15_000_000, MaxTotalBytes: 64_000_000, MinTotalDuration: 2, MaxTotalDuration: 12}}
		m.ReferencePromptSyntax = "picture"
		m.ReferencePromptTypes = []string{"image", "audio"}
		if id == "agnes-video-2.5" {
			m.ReferencePromptTypes = append(m.ReferencePromptTypes, "video")
		}
	}
	m.SupportsAudio, m.AudioAlwaysOn = true, true
	m.Resolutions = []string{"720P", "1080P", "1K", "2K"}
	m.Ratios = []string{"16:9", "9:16", "1:1", "4:3", "3:4", "21:9"}
	m.MinDuration, m.MaxDuration, m.DefaultDuration, m.DurationStep = 4, 12, 5, 1
	m.SupportsAudioInput, m.SupportsVideoInput, m.MaxImageInputs = true, true, 8
	maxVideos := 1
	if id == "agnes-video-2.5-flash" {
		m.Resolutions, m.MaxImageInputs, m.SupportsVideoInput, maxVideos = []string{"720P"}, 5, false, 0
	} else {
		m.Pricing.ReferencePriceUSD = 0.125
		m.Pricing.ReferenceDescription = "Estimated 5-second 720P output; video references and excess image references cost extra."
		zero := 0.0
		m.Parameters = []core.ParameterField{
			{Key: "video_start_seconds", Label: "Reference video start (seconds)", Type: "number", Min: &zero},
			{Key: "video_require_audio", Label: "Require an audio track in the reference video", Type: "boolean"},
		}
	}
	m.ReferenceModes = []core.ReferenceMode{
		{ID: "none", Label: "Text only"},
		{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"first_frame"}},
		{ID: "last_frame", Label: "Last frame", MinImages: 1, MaxImages: 1, ImageRoles: []string{"last_frame"}},
		{ID: "first_last_frame", Label: "First and last frames", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}},
		{ID: "references", Label: "Media references", MaxImages: m.MaxImageInputs, MaxAudios: 3, MaxVideos: maxVideos},
	}
	return m
}

func (a *Adapter) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.models != nil && time.Since(a.modelsAt) < 10*time.Minute {
		return cloneModels(a.models), nil
	}
	data, _, err := a.do(ctx, http.MethodGet, "/models", nil, false)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Reviewed public documentation is the conservative offline fallback.
		return catalog(), nil
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.Unmarshal(data, &response) != nil || response.Data == nil {
		return nil, errors.New("Agnes models response is malformed")
	}
	available := map[string]bool{}
	for _, item := range response.Data {
		available[item.ID] = true
	}
	models := []core.MediaModel{}
	for _, m := range catalog() {
		if available[m.ID] {
			models = append(models, m)
		}
	}
	a.models, a.modelsAt = cloneModels(models), time.Now()
	return models, nil
}

func cloneModels(models []core.MediaModel) []core.MediaModel {
	b, _ := json.Marshal(models)
	var result []core.MediaModel
	_ = json.Unmarshal(b, &result)
	return result
}

func (*Adapter) CheckCredentials(context.Context) error {
	// /models returns HTTP 200 without authentication. Never present that as
	// proof of access, and never submit a billable generation from a key check.
	return errors.New("Agnes model discovery is public and cannot verify an API key; verify access with an explicit generation")
}

func (a *Adapter) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	invalid := func(message string) error { return errors.Join(core.ErrOptionsInvalid, errors.New(message)) }
	if !slices.Contains(modelIDs, r.Model) {
		return invalid("unsupported Agnes media model")
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return invalid("prompt is required")
	}
	m, o := describeModel(r.Model), r.Options
	if o.Resolution != "" && !slices.Contains(m.Resolutions, o.Resolution) {
		return invalid("unsupported Agnes resolution")
	}
	if o.Ratio != "" && !slices.Contains(m.Ratios, o.Ratio) {
		return invalid("unsupported Agnes aspect ratio")
	}
	if (o.GenerateAudio != nil && (m.MediaType != "video" || !*o.GenerateAudio)) || o.Watermark != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 {
		return invalid("unsupported Agnes media option")
	}
	if m.MediaType == "image" && (o.Seed != nil || o.Duration != 0 || len(o.Videos()) > 0 || len(o.Audios()) > 0) {
		return invalid("video options cannot be used for images")
	}
	if m.MediaType == "video" && o.Duration != 0 && (o.Duration < 4 || o.Duration > 12) {
		return invalid("Agnes video duration must be 4–12 seconds")
	}
	if o.Seed != nil && (*o.Seed < 0 || *o.Seed > 4294967295) {
		return invalid("seed is outside the Gateway video range")
	}
	if err := core.ValidateReferenceMode(m, o); err != nil {
		return err
	}
	if o.ReferenceMode == "references" && m.MediaType == "video" && len(o.ImageURLs)+len(o.Audios())+len(o.Videos()) == 0 {
		return invalid("reference mode requires media")
	}
	if _, err := core.NormalizeParameters(m.Parameters, o.Parameters); err != nil {
		return err
	}
	if len(o.Videos()) == 0 {
		_, hasOffset := o.Parameters["video_start_seconds"]
		requireAudio, _ := o.Parameters["video_require_audio"].(bool)
		// The shared form always serializes an untouched boolean as false.
		// False imposes no reference-audio requirement and is valid without a video.
		if hasOffset || requireAudio {
			return invalid("reference video controls require a video reference")
		}
	}
	for _, raw := range append(append(append([]string{}, o.ImageURLs...), o.Videos()...), o.Audios()...) {
		inline := m.MediaType == "image"
		if !validReference(raw, inline) {
			return invalid("references require public HTTPS URLs; images also accept image data URIs")
		}
	}
	return nil
}

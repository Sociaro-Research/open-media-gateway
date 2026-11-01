package kling

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/catalogsync"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

// CatalogRateRequest constructs a local schema probe, not an admitted generation
// or evidence about real user media. It never fetches, registers, or submits.
// The host supplies real owner/connection context only when checking the quote.
func (a *Adapter) CatalogRateRequest(model core.MediaModel, rate catalogsync.Rate) (core.MediaRequest, error) {
	m, ok := pricingModel(model.ID)
	if !ok {
		return core.MediaRequest{}, core.ErrModelUnavailable
	}
	if rate.VideoInputIncludesResources && m.ID != OmniVideo4 {
		return core.MediaRequest{}, errors.New("unreviewed video-resource tariff condition")
	}
	if rate.Region != "Singapore" || rate.InputImages != nil || len(rate.Components) > 0 || rate.Quality != "" || rate.ValidateConditions() != nil {
		return core.MediaRequest{}, errors.New("unreviewed Kling tariff selector or region")
	}
	r := core.MediaRequest{Model: m.ID, TaskID: "d45de672-3245-49b7-85b5-410d89a284bf", Prompt: "Synthetic catalogue pricing schema probe", Options: core.MediaOptions{Parameters: map[string]any{}, ReferenceRoles: map[string]string{}}}
	if m.NoPrompt {
		r.Prompt = ""
	}
	if len(m.Resolutions) > 0 {
		r.Options.Resolution = m.Resolutions[0]
	}
	if rate.Resolution != "" {
		r.Options.Resolution = rate.Resolution
	}
	if len(m.Ratios) > 0 {
		r.Options.Ratio = m.Ratios[0]
	}
	if m.MediaType == "video" && !m.DurationFromInput {
		r.Options.Duration = m.DefaultDuration
	}
	if rate.Duration < 0 || rate.Duration > 0 && (m.MediaType != "video" || m.DurationFromInput || rate.Duration < m.MinDuration || rate.Duration > m.MaxDuration) {
		return core.MediaRequest{}, errors.New("unsupported Kling duration selector")
	}
	if rate.Duration > 0 {
		r.Options.Duration = rate.Duration
	}
	if m.RequiresImageInput {
		for i := 0; i < max(1, m.MinImageInputs); i++ {
			r.Options.ImageURLs = append(r.Options.ImageURLs, pricingURL("image", i))
		}
	}
	if m.RequiresVideoInput {
		pricingAddVideo(&r, m)
	}
	if m.RequiresAudioInput {
		r.Options.AudioURLs = []string{pricingURL("audio", 0)}
	}
	switch m.ID {
	case TextToAudio:
		r.Options.Parameters["duration"] = 3.0
	case TextToSpeech:
		r.Options.Voice = "oversea_male1"
		r.Options.Language = "en"
	case MotionControl3, MotionControl26:
		r.Options.Parameters["character_orientation"] = "image"
		if len(r.Options.ImageURLs) == 0 {
			r.Options.ImageURLs = []string{pricingURL("image", 0)}
		}
	case ImageOutpainting:
		for _, k := range []string{"up_expansion_ratio", "down_expansion_ratio", "left_expansion_ratio", "right_expansion_ratio"} {
			r.Options.Parameters[k] = 0.0
		}
		r.Options.Parameters["left_expansion_ratio"] = 0.5
	case GoodsStudio:
		r.Options.Parameters["duration"] = "15"
		r.Options.Ratio = "9:16"
	case VideoCommerce:
		r.Options.Parameters["avatar_id"] = commerceAvatars[0]
	case VirtualTryOn:
		r.Options.ReferenceRoles["image_1"] = "product_image"
		r.Options.ReferenceRoles["image_2"] = "person_image"
	case ResourceCreateVoice:
		sourceID := "b0bfffd5-89d9-4b1f-abdf-bf5b1c75c12c"
		seconds := 10.0
		creation := &core.ResourceCreateRequest{Kind: "voice", Subtype: "custom", Name: "Catalogue probe", VoiceSources: []core.ResourceSourceInput{{ReferenceID: sourceID, URL: pricingURL("audio", 0), MediaType: "audio", ContentType: "audio/mpeg", SizeBytes: 4096, SHA256: strings.Repeat("a", 64), Duration: &seconds}}}
		r.Options.ResourceCreation = creation
		r.Options.ResourceIntent = &core.ResourceIntent{Kind: "voice", Subtype: "custom", Name: creation.Name, ReferenceIDs: []string{sourceID}}
	}
	if rate.AudioInput != nil {
		if *rate.AudioInput {
			if !m.SupportsAudioInput {
				return core.MediaRequest{}, errors.New("unreviewed Kling audio-input selector")
			}
			if len(r.Options.Audios()) == 0 {
				r.Options.AudioURLs = []string{pricingURL("audio", 0)}
			}
		} else {
			r.Options.AudioURLs = nil
			r.Options.AudioURL = ""
		}
	}
	if rate.ImageInput != nil {
		if *rate.ImageInput {
			if len(r.Options.ImageURLs) == 0 {
				r.Options.ImageURLs = []string{pricingURL("image", 0)}
			}
		} else {
			r.Options.ImageURLs = nil
		}
	}
	if rate.VoiceInput != nil {
		if *rate.VoiceInput {
			if len(m.Voices) == 0 {
				return core.MediaRequest{}, errors.New("unreviewed Kling voice selector")
			}
			r.Options.Voice = m.Voices[0]
		} else {
			r.Options.Voice = ""
		}
	}
	if rate.VideoInput != nil {
		if *rate.VideoInput {
			if len(r.Options.Videos()) == 0 {
				pricingAddVideo(&r, m)
			}
		} else {
			r.Options.VideoURLs = nil
			r.Options.VideoURL = ""
		}
	}
	if rate.Audio != nil {
		if !m.SupportsAudio {
			return core.MediaRequest{}, errors.New("operation has no generated-audio switch")
		}
		r.Options.GenerateAudio = rate.Audio
	}
	for key, value := range rate.Parameters {
		r.Options.Parameters[key] = value
	}
	if m.ID == OmniVideo3 && r.Options.Parameters["audio"] == "original" {
		r.Options.ReferenceRoles["video_1"] = "source"
	}
	for _, selector := range rate.ResourceInputs {
		supported := false
		for _, schema := range m.ResourceInputSchemas {
			if schema.Kind == selector.Kind && selector.Max <= schema.MaxCount && (selector.Subtype == "" || pricingContains(schema.Subtypes, selector.Subtype)) {
				supported = true
			}
		}
		if !supported {
			return core.MediaRequest{}, errors.New("unreviewed Kling resource kind, subtype or count selector")
		}
		if selector.Kind != "voice" && selector.Kind != "element" {
			return core.MediaRequest{}, errors.New("unsupported Kling resource selector")
		}
		for i := 0; i < selector.Min; i++ {
			index := len(r.Options.ResourceReferences) + 1
			binding := fmt.Sprintf("d45de672-3245-49b7-85b5-%012d", index)
			alias := fmt.Sprintf("catalogue_resource_%d", index)
			snapshot := core.ResourceSnapshot{BindingID: binding, ReferenceID: alias, Kind: selector.Kind, Subtype: selector.Subtype, RemoteID: fmt.Sprintf("catalogue_native_%d", index), ConnectionIdentity: "catalogue-fixture-revision"}
			if selector.Subtype == "video_character" {
				seconds := 3.0
				snapshot.Sources = []core.ResourceSourceEvidence{{ReferenceID: "b0bfffd5-89d9-4b1f-abdf-bf5b1c75c12c", MediaType: "video", SHA256: strings.Repeat("a", 64), Duration: &seconds}}
			}
			r.Options.ResourceReferences = append(r.Options.ResourceReferences, core.ResourceReference{BindingID: binding, ReferenceID: alias})
			r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, snapshot)
		}
	}
	for _, selector := range rate.ReferenceRoles {
		supported := false
		for _, schema := range m.ReferenceRoleSchemas {
			if schema.Kind == selector.Kind && pricingContains(schema.Roles, selector.Role) {
				supported = true
			}
		}
		if !supported {
			return core.MediaRequest{}, errors.New("unreviewed Kling reference-role selector")
		}
		if selector.Kind != "image" {
			return core.MediaRequest{}, errors.New("unreviewed Kling role selector")
		}
		count := 0
		for i := range r.Options.ImageURLs {
			if core.ReferenceRole(m, r.Options, "image", i) == selector.Role {
				count++
			}
		}
		for count < selector.Min {
			index := len(r.Options.ImageURLs)
			r.Options.ImageURLs = append(r.Options.ImageURLs, pricingURL("image", index))
			r.Options.ReferenceRoles[fmt.Sprintf("image_%d", index+1)] = selector.Role
			count++
		}
		if m.ID == VideoCommerce && selector.Role == "ref_image" && selector.Min > 0 {
			r.Options.Parameters["goods_title"] = "Synthetic catalogue product"
		}
	}
	if !rate.MatchesConditions(m, r) {
		return core.MediaRequest{}, errors.New("Kling catalogue fixture does not satisfy typed conditions")
	}
	if rate.VideoInput != nil {
		hasVideo := len(r.Options.Videos()) > 0
		if rate.VideoInputIncludesResources {
			for _, resource := range r.Options.ResourceSnapshots {
				hasVideo = hasVideo || resource.Kind == "element" && resource.Subtype == "video_character"
			}
		}
		if hasVideo != *rate.VideoInput {
			return core.MediaRequest{}, errors.New("Kling catalogue fixture does not satisfy input-video condition")
		}
	}
	if err := a.validatePricingProbe(m, r); err != nil {
		return core.MediaRequest{}, err
	}
	return r, nil
}
func pricingURL(kind string, index int) string {
	suffix := "png"
	if kind == "video" {
		suffix = "mp4"
	}
	if kind == "audio" {
		suffix = "mp3"
	}
	return fmt.Sprintf("https://catalogue-fixture.invalid/%s_%d.%s", kind, index+1, suffix)
}
func pricingAddVideo(r *core.MediaRequest, m core.MediaModel) {
	raw := pricingURL("video", 0)
	r.Options.VideoURLs = []string{raw}
	seconds := 3.0
	if m.ReferenceMediaLimits != nil && m.ReferenceMediaLimits.Video.MinDuration > 0 {
		seconds = m.ReferenceMediaLimits.Video.MinDuration
	}
	r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{raw: {Type: "video", Duration: seconds}}
}
func pricingProbeContext() context.Context {
	return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "catalogue-fixture-owner"), core.ProviderConnectionScope{ID: "catalogue-fixture", OwnerID: "catalogue-fixture-owner", Revision: "catalogue-fixture-revision"})
}
func pricingModel(id string) (core.MediaModel, bool) {
	if m, ok := modelFor(id); ok {
		return m, true
	}
	groups := [][]core.MediaModel{imageModels(), motionModels(), audioModels(), solutionModels(), resourceGenerationModels(), legacyOmniModels(), auxiliaryModels()}
	for _, group := range groups {
		for _, m := range group {
			if m.ID == id {
				return m, true
			}
		}
	}
	return core.MediaModel{}, false
}
func (a *Adapter) validatePricingProbe(m core.MediaModel, r core.MediaRequest) error {
	switch {
	case isImageOperation(r.Model):
		return a.validateImageRequest(r)
	case isMotionOperation(r.Model):
		return a.validateMotionRequest(r)
	case isAudioOperation(r.Model):
		return a.validateAudioRequest(r)
	case isSolutionOperation(r.Model):
		return validateSolutionRequest(r)
	case isResourceGenerationOperation(r.Model):
		return a.validateResourceGenerationRequest(pricingProbeContext(), r)
	case isLegacyOmniOperation(r.Model):
		return a.validateLegacyOmniRequest(r)
	case isAuxiliaryOperation(r.Model):
		return a.validateAuxiliaryRequest(r)
	default:
		return a.ValidateRequest(pricingProbeContext(), r)
	}
}

func pricingContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

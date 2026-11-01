package kling

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/google/uuid"
)

const (
	ResourceCreateElement = "kling-create-element"
	ResourceCreateVoice   = "kling-create-voice"
)

func resourceGenerationModels() []core.MediaModel {
	base := func(id, label, kind string, subtypes []string) core.MediaModel {
		return core.MediaModel{ID: id, DisplayName: label, MediaType: "resource", NoPrompt: true, NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true, ReferenceModes: []core.ReferenceMode{{ID: "none", Label: "Managed source references"}}, ResourceOperation: &core.ResourceOperationSchema{Kind: kind, Subtypes: subtypes}}
	}
	element := base(ResourceCreateElement, "Kling · Create managed element", "element", []string{"multi_image", "video_character"})
	element.ResourceOperation.NameMaxCharacters = 20
	element.ResourceOperation.SupportsDescription = true
	element.ResourceOperation.DescriptionRequired = true
	element.ResourceOperation.MaxTags = 8
	element.ResourceOperation.DescriptionMaxCharacters = 100
	element.ResourceOperation.Tags = []core.ResourceTagChoice{{ID: "o_101", Label: "Hottest"}, {ID: "o_102", Label: "Character"}, {ID: "o_103", Label: "Animal"}, {ID: "o_104", Label: "Item"}, {ID: "o_105", Label: "Costume"}, {ID: "o_106", Label: "Scene"}, {ID: "o_107", Label: "Effect"}, {ID: "o_108", Label: "Others"}}
	element.ResourceOperation.Variants = []core.ResourceVariantSchema{
		{Subtype: "multi_image", MinSources: 2, MaxSources: 4, SourceKinds: []string{"image"}, SupportsBoundVoice: true, FirstSourceLabel: "Frontal image", AdditionalSourceLabel: "Reference images", Limits: frameLimits(4, 10<<20), Help: "One frontal image and 1–3 distinct reference images."},
		{Subtype: "video_character", MinSources: 1, MaxSources: 1, SourceKinds: []string{"video"}, SupportsBoundVoice: true, AllowedDimensions: []core.MediaDimensions{{Width: 1920, Height: 1080}, {Width: 1080, Height: 1920}}, Limits: &core.ReferenceMediaLimits{MaxFiles: 1, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 200 << 20, MinDuration: 3, MaxDuration: 8}}, Help: "A 3–8 second 1080p character video; creating it may also bind a voice."},
	}
	element.Pricing = core.MediaModelPricing{Known: false, ReferenceDescription: "No verified public element-creation tariff is available. A reviewed operator-confirmed rate is required for paid admission."}
	voice := base(ResourceCreateVoice, "Kling · Create custom voice", "voice", []string{"custom"})
	voice.ResourceOperation.NameMaxCharacters = 20
	voice.ResourceOperation.Variants = []core.ResourceVariantSchema{{Subtype: "custom", MinSources: 1, MaxSources: 1, SourceKinds: []string{"audio", "video"}, Limits: &core.ReferenceMediaLimits{MaxFiles: 1, Audio: core.MediaFileLimits{ContentTypes: []string{"audio/mpeg", "audio/wav"}, MinDuration: 5, MaxDuration: 30}, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MinDuration: 5, MaxDuration: 30}}, GeneratedSources: []core.GeneratedResourceRule{{Model: TextToVideo26, NativeAudioRequired: true, MinDuration: 5, MaxDuration: 30, MaxAgeSeconds: 2592000}, {Model: ImageToVideo26, NativeAudioRequired: true, MinDuration: 5, MaxDuration: 30, MaxAgeSeconds: 2592000}, {Model: AvatarImageToVideo, MinDuration: 5, MaxDuration: 30, MaxAgeSeconds: 2592000}, {Model: LipSync, MinDuration: 5, MaxDuration: 30, MaxAgeSeconds: 2592000}}, Help: "One clear human voice, 5–30 seconds. Use one local source or an eligible completed generation."}}
	voice.Pricing = core.MediaModelPricing{Known: true, ReferencePriceUSD: .007, ReferenceDescription: "One custom-voice creation request at the published USD balance list price; discounts, prepaid units and actual deductions remain unverified."}
	return []core.MediaModel{element, voice}
}
func resourceGenerationModel(id string) (core.MediaModel, bool) {
	for _, m := range resourceGenerationModels() {
		if m.ID == id {
			return m, true
		}
	}
	return core.MediaModel{}, false
}
func isResourceGenerationOperation(id string) bool { _, ok := resourceGenerationModel(id); return ok }
func (a *Adapter) prepareResourceGenerationRequest(ctx context.Context, r core.MediaRequest) (*core.ResourceCreateRequest, error) {
	m, ok := resourceGenerationModel(r.Model)
	if !ok {
		return nil, core.ErrModelUnavailable
	}
	o := r.Options
	if r.Prompt != "" || o.OutputCount != 0 || o.Resolution != "" || o.Ratio != "" || o.Duration != 0 || o.GenerateAudio != nil || o.Watermark != nil || o.CameraFixed != nil || o.Draft != nil || o.Seed != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Quality != "" || o.Background != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.OverrideSoundID != "" || o.OverrideSound != nil || len(o.ImageURLs) > 0 || len(o.Videos()) > 0 || len(o.Audios()) > 0 || len(o.ReferenceVoices) > 0 || len(o.ReferenceRoles) > 0 || len(o.ReferenceMediaEvidence) > 0 || len(o.ResourceReferences) > 0 || len(o.ResourceSnapshots) > 0 || len(o.Parameters) > 0 || (o.ReferenceMode != "" && o.ReferenceMode != "none") {
		return nil, resourceInvalid("resource generation accepts only immutable local resource intent and host-resolved creation evidence")
	}
	if err := core.ValidateResourceCreationIntent(m, o, false); err != nil {
		return nil, err
	}
	creation := core.CloneResourceCreation(o.ResourceCreation)
	// Input order is part of the admitted identity: the first image is frontal.
	// A set comparison alone would permit a different native element payload.
	ordered := []string{}
	for _, group := range [][]core.ResourceSourceInput{creation.Images, creation.Videos, creation.VoiceSources} {
		for _, source := range group {
			ordered = append(ordered, source.ReferenceID)
		}
	}
	if !slices.Equal(ordered, o.ResourceIntent.ReferenceIDs) {
		return nil, resourceInvalid("resolved resource sources do not preserve the admitted reference order")
	}
	if generated := creation.GeneratedVideo; generated != nil {
		if generated.TaskID != o.ResourceIntent.GeneratedTaskID || generated.TaskHandle == "" || generated.RemoteID != "" || !validTaskID(generated.OutputID) || !resourceSHA.MatchString(generated.SHA256) {
			return nil, resourceInvalid("generated voice source requires a resolved local task, scoped task handle and actual output video identity")
		}
		if strings.HasPrefix(generated.TaskHandle, legacyTaskPrefix) {
			handle, err := a.parseLegacy(ctx, generated.TaskHandle)
			if err != nil || handle.Operation != generated.Operation {
				return nil, resourceInvalid("generated voice source belongs to a different owner, operation or provider connection revision")
			}
		} else if !validTaskID(generated.TaskHandle) || (generated.Operation != TextToVideo26 && generated.Operation != ImageToVideo26) {
			return nil, resourceInvalid("unsupported generated voice source task handle")
		}
		// Upstream video_id identifies the generated output, not its task_id.
		// Only the host's archived output evidence may supply it.
		generated.RemoteID = generated.OutputID
	}
	if err := validateResourceCreate(ctx, *creation); err != nil {
		return nil, err
	}
	return creation, nil
}
func (a *Adapter) validateResourceGenerationRequest(ctx context.Context, r core.MediaRequest) error {
	_, err := a.prepareResourceGenerationRequest(ctx, r)
	return err
}
func resourceGenerationUsage(job core.ResourceJob) *core.ProviderUsage {
	if job.Usage != nil {
		return job.Usage
	}
	if job.Status == core.ProviderSucceeded && len(job.Resources) > 0 {
		// A completed operation is one request. This lets the accepted per-request
		// tariff be metered without claiming an invoice or inventing vendor counters.
		return &core.ProviderUsage{CostStatus: "metered"}
	}
	return nil
}
func (a *Adapter) generateResource(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	creation, err := a.prepareResourceGenerationRequest(ctx, r)
	if err != nil {
		return core.ProviderTask{}, err
	}
	id, err := uuid.Parse(r.TaskID)
	if err != nil || id.String() != r.TaskID {
		return core.ProviderTask{}, resourceInvalid("resource generation requires a canonical Gateway task UUID")
	}
	creation.ExternalTaskID = r.TaskID
	job, err := a.CreateResource(ctx, *creation)
	if err != nil {
		return core.ProviderTask{}, err
	}
	if job.Status == core.ProviderFailed {
		return core.ProviderTask{}, errors.New("Kling resource generation failed during submission; check provider task details")
	}
	return core.ProviderTask{ID: job.Handle, Resources: job.Resources, Usage: resourceGenerationUsage(job)}, nil
}
func (a *Adapter) quoteResourceGeneration(ctx context.Context, r core.MediaRequest) (core.TaskCost, string, error) {
	if err := a.validateResourceGenerationRequest(ctx, r); err != nil {
		return core.TaskCost{}, "", err
	}
	if r.Model != ResourceCreateVoice {
		return a.quoteFixedOperation("element_" + r.Options.ResourceIntent.Subtype)
	}
	rate := money.USD(7000)
	return core.TaskCost{ExpectedUSD: rate, EstimatedUnits: 1, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{Currency: "USD", Unit: "request", Price: rate, Per: 1, Source: "https://kling.ai/document-api/pricing/base/video", Version: "2026-10-31", Region: "Singapore", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: "Published USD balance list price for one custom-voice request; discounts, prepaid units and actual provider deductions remain unverified."}}, "", nil
}
func (a *Adapter) getResourceGenerationTask(ctx context.Context, kind, handle string) (core.ProviderResult, error) {
	if kind != "element" && kind != "voice" {
		return core.ProviderResult{}, resourceInvalid("unsupported generated resource kind")
	}
	decoded, err := a.parseLegacy(ctx, handle)
	if err != nil {
		return core.ProviderResult{}, err
	}
	if decoded.Operation != resourceOperation(kind, "create") {
		return core.ProviderResult{}, resourceInvalid("resource generation polling requires a matching creation-task handle")
	}
	job, err := a.GetResourceJob(ctx, kind, handle)
	if err != nil {
		return core.ProviderResult{}, err
	}
	return core.ProviderResult{Status: job.Status, Resources: job.Resources, Usage: resourceGenerationUsage(job), Error: job.Error}, nil
}

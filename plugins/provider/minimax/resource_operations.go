package minimax

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

const ResourceUpload = "MiniMax-Upload-File"
const ResourceOutputFile = "MiniMax-Bind-Output-File"
const ResourceSourceTask = "MiniMax-Bind-H3-Source"

func fileOperationModel(id string) core.MediaModel {
	m := core.MediaModel{ID: id, DisplayName: "MiniMax · Upload inspected input file", MediaType: "resource", NoPrompt: true, NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true, ReferenceModes: []core.ReferenceMode{{ID: "none", Label: "Managed local source"}}, ResourceOperation: &core.ResourceOperationSchema{Kind: "file_resource", Subtypes: []string{"image", "video", "audio"}, NameMaxCharacters: 64}, ResourceOutputs: []core.ResourceOutputSchema{{Kind: "file_resource", GatewayRetentionSeconds: 7 * 86400, RequiresInspectedInput: true, ProviderExpiryKnown: true}}, Pricing: core.MediaModelPricing{ReferenceDescription: "No separate public file-operation tariff is documented. An explicit operator-confirmed USD/request rate is required; blank is unknown, never free."}, Metadata: &core.ModelMetadata{CheckedAt: "2026-10-31", Sources: []core.ModelSource{{Title: "Upload input file", URL: docsBase + "api-reference/file-management-upload"}, {Title: "Retrieve file", URL: docsBase + "api-reference/file-management-retrieve"}}}}
	for _, kind := range []string{"image", "video", "audio"} {
		limits := h3Limits()
		limits.MaxFiles = 1
		m.ResourceOperation.Variants = append(m.ResourceOperation.Variants, core.ResourceVariantSchema{Subtype: kind, MinSources: 1, MaxSources: 1, SourceKinds: []string{kind}, Limits: limits, Help: "One inspected local reference. Uploaded input files expire after seven days and have no documented input deletion operation."})
	}
	if id == ResourceOutputFile {
		m.DisplayName = "MiniMax · Bind an owned generated output file"
		m.ResourceOperation.Subtypes = []string{"video"}
		m.ResourceOperation.Variants = nil
		rules := []core.GeneratedResourceRule{}
		for _, op := range operations() {
			if op.kind == "legacy" {
				rule := core.GeneratedResourceRule{Model: op.id, MinDuration: 0, MaxDuration: 10}
				if op.id == "S2V-01" {
					rule.MaxDuration = 0
					rule.AllowUnknownDuration = true
				} // The native endpoint documents no duration.
				rules = append(rules, rule)
			}
		}
		m.ResourceOperation.Variants = []core.ResourceVariantSchema{{Subtype: "video", MinSources: 0, MaxSources: 0, GeneratedSources: rules, Help: "An archived generation from this exact provider connection; raw provider IDs are never accepted."}}
		m.ResourceOutputs[0].GatewayRetentionSeconds = 0
		m.ResourceOutputs[0].ProviderExpiryKnown = false
	}

	if id == ResourceSourceTask {
		m.DisplayName = "MiniMax · Bind an owned H3 regeneration source"
		m.ResourceOperation.Subtypes = []string{"regeneration_source"}
		m.ResourceOperation.Variants = []core.ResourceVariantSchema{{Subtype: "regeneration_source", MinSources: 0, MaxSources: 0, GeneratedSources: []core.GeneratedResourceRule{{Model: "MiniMax-H3", NativeAudioRequired: true, MinDuration: 4, MaxDuration: 15, MaxAgeSeconds: 7 * 86400}}, Help: "An archived succeeded H3 768P generation from this exact connection. Provider whitelist access is required for subsequent regeneration."}}
		m.ResourceOutputs[0].GatewayRetentionSeconds = 7 * 86400
		m.Metadata.Sources = append(m.Metadata.Sources, core.ModelSource{Title: "Owned source-task regeneration", URL: docsBase + "api-reference/video-generation-v2-regeneration"})
	}
	return m
}
func (a *Adapter) validateFileCreation(ctx context.Context, r core.MediaRequest) (*core.ResourceCreateRequest, error) {
	m := fileOperationModel(r.Model)
	if r.Model != ResourceUpload && r.Model != ResourceOutputFile && r.Model != ResourceSourceTask {
		return nil, invalid("unsupported file operation")
	}
	o := r.Options
	if r.Prompt != "" || o.OutputCount != 0 || o.Duration != 0 || o.Resolution != "" || o.Ratio != "" || o.Seed != nil || o.Watermark != nil || o.GenerateAudio != nil || o.CameraFixed != nil || o.Draft != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || o.OverrideSoundID != "" || o.OverrideSound != nil || len(o.ImageURLs)+len(o.Videos())+len(o.Audios())+len(o.ReferenceVoices)+len(o.ReferenceRoles)+len(o.ReferenceMediaEvidence)+len(o.ResourceReferences)+len(o.ResourceSnapshots)+len(o.Parameters) > 0 || (o.ReferenceMode != "" && o.ReferenceMode != "none") {
		return nil, invalid("file operations accept only inspected local resource intent")
	}
	if err := core.ValidateResourceCreationIntent(m, o, false); err != nil {
		return nil, err
	}
	c := core.CloneResourceCreation(o.ResourceCreation)
	if len(c.Images)+len(c.Videos)+len(c.VoiceSources) > 0 || c.BoundVoice != nil || len(c.Tags) > 0 || c.Description != "" {
		return nil, invalid("unsupported file creation fields")
	}
	if r.Model == ResourceUpload {
		if c.GeneratedVideo != nil || len(c.FileSources) != 1 || len(o.ResourceIntent.ReferenceIDs) != 1 {
			return nil, invalid("upload requires one inspected local source")
		}
		s := c.FileSources[0]
		digest, e := hex.DecodeString(s.SHA256)
		if _, err := uuid.Parse(s.ReferenceID); err != nil || e != nil || len(digest) != 32 || s.ReferenceID != o.ResourceIntent.ReferenceIDs[0] || s.MediaType != c.Subtype || !slices.Contains([]string{"image", "video", "audio"}, c.Subtype) || s.SizeBytes <= 0 {
			return nil, invalid("upload requires trusted reference identity, type, hash and size")
		}
	} else {
		if len(c.FileSources) != 0 || c.GeneratedVideo == nil || (c.Subtype != "video" && c.Subtype != "regeneration_source") {
			return nil, invalid("output binding requires an authorized archived generation")
		}
		h, err := a.open(ctx, c.GeneratedVideo.TaskHandle)
		if err != nil || (c.Subtype == "video" && h.Kind != "legacy") || (c.Subtype == "regeneration_source" && (h.Kind != "h3" || h.Model != "MiniMax-H3")) || h.Model != c.GeneratedVideo.Model || c.GeneratedVideo.TaskID != o.ResourceIntent.GeneratedTaskID {
			return nil, invalid("output source belongs to a different owner, connection or operation")
		}
	}
	return c, nil
}
func (a *Adapter) CreateResource(ctx context.Context, c core.ResourceCreateRequest) (core.ResourceJob, error) {
	if strings.TrimSpace(c.Name) == "" || utf8.RuneCountInString(c.Name) > 64 || c.Description != "" || len(c.Tags)+len(c.Images)+len(c.Videos)+len(c.VoiceSources) > 0 || c.BoundVoice != nil {
		return core.ResourceJob{}, invalid("file resources require a name of at most 64 characters and only the documented local source")
	}
	if c.ExternalTaskID != "" {
		id, e := uuid.Parse(c.ExternalTaskID)
		if e != nil || id.String() != c.ExternalTaskID {
			return core.ResourceJob{}, invalid("canonical Gateway task ID required")
		}
	}
	if a.requestConnectionScope(ctx) == "" {
		return core.ResourceJob{}, invalid("provider connection scope missing")
	}
	var f File
	var err error
	if c.Kind != "file_resource" || !slices.Contains([]string{"image", "video", "audio", "regeneration_source"}, c.Subtype) {
		return core.ResourceJob{}, invalid("unsupported resource kind or subtype")
	}

	if c.Subtype == "regeneration_source" {
		if c.GeneratedVideo == nil || len(c.FileSources) > 0 {
			return core.ResourceJob{}, invalid("authorized H3 source required")
		}
		h, e := a.open(ctx, c.GeneratedVideo.TaskHandle)
		if e != nil || h.Kind != "h3" || h.Model != "MiniMax-H3" {
			return core.ResourceJob{}, invalid("owned H3 source required")
		}
		task, e := a.queryNative(ctx, h)
		if e != nil {
			return core.ResourceJob{}, e
		}
		if task.Status != "succeeded" || task.Resolution != "768P" {
			return core.ResourceJob{}, invalid("source must be succeeded H3 768P")
		}
		h.Kind = "source_binding"
		if h.ExpiresAt == 0 {
			h.ExpiresAt = time.Now().Add(7 * 24 * time.Hour).Unix()
		}
		token := a.seal(ctx, h)
		return core.ResourceJob{Handle: token, Status: core.ProviderSucceeded, Resources: []core.ProviderResource{{RemoteID: token, Kind: "file_resource", Subtype: "regeneration_source", Name: c.Name, Status: core.ProviderSucceeded}}}, nil
	}
	if c.GeneratedVideo != nil {
		if c.Subtype != "video" || len(c.FileSources) > 0 {
			return core.ResourceJob{}, invalid("owned generated source required")
		}
		f, err = a.GetTaskFile(ctx, c.GeneratedVideo.TaskHandle)
	} else {
		if len(c.FileSources) != 1 || c.GeneratedVideo != nil {
			return core.ResourceJob{}, invalid("one trusted file source required")
		}
		s := c.FileSources[0]
		opener, ok := core.ResourceSourceOpenerFromContext(ctx)
		if !ok {
			return core.ResourceJob{}, invalid("trusted upload source opener is unavailable")
		}
		source, e := opener.OpenResourceSource(ctx, s)
		if e != nil {
			return core.ResourceJob{}, errors.New("verified upload source is unavailable")
		}
		if source.Reader == nil {
			return core.ResourceJob{}, invalid("upload source reader missing")
		}
		defer source.Reader.Close()
		typ, e := uploadContentType(source.ContentType)
		admittedType, admittedErr := uploadContentType(s.ContentType)
		ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp", "image/heic": ".heic", "image/heif": ".heif", "video/mp4": ".mp4", "video/quicktime": ".mov", "audio/mpeg": ".mp3", "audio/wav": ".wav"}[typ]
		if e != nil || admittedErr != nil || ext == "" || typ != admittedType || source.SizeBytes != s.SizeBytes || !strings.HasPrefix(typ, c.Subtype+"/") {
			return core.ResourceJob{}, invalid("verified upload type/size does not match admitted source")
		}
		f, err = a.UploadFile(ctx, s.ReferenceID+ext, typ, source.Reader)
	}
	if err != nil {
		return core.ResourceJob{}, err
	}
	resource := core.ProviderResource{RemoteID: f.Handle, Kind: "file_resource", Subtype: c.Subtype, Name: c.Name, Status: core.ProviderSucceeded, Sources: c.FileSources}
	return core.ResourceJob{Handle: f.Handle, Status: core.ProviderSucceeded, Resources: []core.ProviderResource{resource}}, nil
}
func (a *Adapter) GetResourceJob(ctx context.Context, kind, token string) (core.ResourceJob, error) {
	if kind != "file_resource" {
		return core.ResourceJob{}, invalid("unsupported resource kind")
	}

	h, err := a.open(ctx, token)
	if err != nil {
		return core.ResourceJob{}, err
	}
	if h.Kind == "source_binding" {
		h.Kind = "h3"
		task, e := a.queryNative(ctx, h)
		if e != nil {
			return core.ResourceJob{}, e
		}
		if task.Status != "succeeded" || task.Resolution != "768P" {
			return core.ResourceJob{}, invalid("H3 source is no longer available")
		}
		return core.ResourceJob{Handle: token, Status: core.ProviderSucceeded, Resources: []core.ProviderResource{{RemoteID: token, Kind: kind, Subtype: "regeneration_source", Status: core.ProviderSucceeded}}}, nil
	}
	f, err := a.GetFile(ctx, token)
	if err != nil {
		return core.ResourceJob{}, err
	}
	subtype := f.MediaKind
	return core.ResourceJob{Handle: token, Status: core.ProviderSucceeded, Resources: []core.ProviderResource{{RemoteID: token, Kind: kind, Subtype: subtype, Status: core.ProviderSucceeded}}}, nil
}
func (*Adapter) ListResources(context.Context, string, bool, int, int) (core.ResourcePage, error) {
	return core.ResourcePage{}, invalid("list owner-bound Gateway resources; account-wide provider enumeration is not exposed")
}

// ValidateResourceDeletion is a pure capability check. The host calls it before
// reserving a deletion job so unsupported input-file deletion cannot disable a
// binding that is still usable upstream.
func (a *Adapter) ValidateResourceDeletion(ctx context.Context, r core.ResourceSnapshot) error {
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok || scope.Revision == "" || r.ConnectionIdentity != scope.Revision || r.Kind != "file_resource" || r.IsPreset {
		return invalid("owned file resource with unchanged connection revision required")
	}
	h, err := a.open(ctx, r.RemoteID)
	if err != nil {
		return err
	}
	if r.Subtype == "regeneration_source" && h.Kind == "source_binding" && h.Model == "MiniMax-H3" {
		return nil
	}
	if r.Subtype == "video" && h.Kind == "file" && h.Purpose == "video_generation" && h.MediaKind == "video" {
		return nil
	}
	return invalid("MiniMax does not document deleting uploaded input files; they expire after seven days")
}

func (a *Adapter) DeleteResource(ctx context.Context, r core.ResourceSnapshot) (core.ResourceJob, error) {
	if err := a.ValidateResourceDeletion(ctx, r); err != nil {
		return core.ResourceJob{}, err
	}

	if r.Kind == "file_resource" && r.Subtype == "regeneration_source" {
		h, e := a.open(ctx, r.RemoteID)
		if e != nil || h.Kind != "source_binding" {
			return core.ResourceJob{}, invalid("owned source binding required")
		}
		return core.ResourceJob{Handle: r.RemoteID, Status: core.ProviderSucceeded, Resources: []core.ProviderResource{{RemoteID: r.RemoteID, Kind: r.Kind, Subtype: r.Subtype, Status: core.ProviderSucceeded, IsDeleted: true}}}, nil
	}
	if r.Kind != "file_resource" || r.Subtype != "video" {
		return core.ResourceJob{}, invalid("MiniMax does not document deleting video_generation_input files; they expire after seven days")
	}
	if err := a.DeleteFile(ctx, r.RemoteID); err != nil {
		return core.ResourceJob{}, err
	}
	return core.ResourceJob{Handle: r.RemoteID, Status: core.ProviderSucceeded, Resources: []core.ProviderResource{{RemoteID: r.RemoteID, Kind: r.Kind, Subtype: r.Subtype, Status: core.ProviderSucceeded, IsDeleted: true}}}, nil
}
func (a *Adapter) generateFileResource(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	c, err := a.validateFileCreation(ctx, r)
	if err != nil {
		return core.ProviderTask{}, err
	}
	if _, err = uuid.Parse(r.TaskID); err != nil {
		return core.ProviderTask{}, invalid("canonical Gateway task ID required")
	}
	c.ExternalTaskID = r.TaskID
	j, err := a.CreateResource(ctx, *c)
	if err != nil {
		return core.ProviderTask{}, err
	}
	return core.ProviderTask{ID: j.Handle, Resources: j.Resources, Usage: &core.ProviderUsage{CostStatus: "metered"}}, nil
}
func (a *Adapter) quoteFileResource(ctx context.Context, r core.MediaRequest) (core.TaskCost, string, error) {
	if _, err := a.validateFileCreation(ctx, r); err != nil {
		return core.TaskCost{}, "", err
	}
	if a.fileOperationPrice == nil {
		return core.TaskCost{}, "", core.ErrPricingUnavailable
	}
	price := *a.fileOperationPrice
	return core.TaskCost{ExpectedUSD: price, EstimatedUnits: 1, Status: "estimated", QuotedAt: time.Now().UTC(), Pricing: core.PricingSnapshot{AccountSpecific: true, Currency: "USD", Unit: "request", Price: price, Per: 1, Source: "operator_confirmed", Version: "operator-file-operation", Region: "Global", EstimateDescription: "Explicit operator-confirmed file-operation price. This is not a published MiniMax tariff or a verified provider invoice."}}, "", nil
}
func (a *Adapter) expandManagedFiles(ctx context.Context, r core.MediaRequest) (core.MediaRequest, error) {
	if r.Model == Regeneration {
		return r, nil
	}
	if len(r.Options.ResourceReferences) == 0 && len(r.Options.ResourceSnapshots) == 0 {
		return r, nil
	}
	m, ok := modelFor(r.Model)
	if !ok || !(r.Model == ContextIR || strings.HasPrefix(r.Model, "MiniMax-H3")) {
		return r, invalid("managed file resources are unsupported for this operation")
	}
	if err := core.ValidateResourceReferences(m, r.Options); err != nil {
		return r, err
	}
	o := r.Options
	o.ImageURLs = slices.Clone(o.ImageURLs)
	o.VideoURLs = slices.Clone(o.Videos())
	o.AudioURLs = slices.Clone(o.Audios())
	o.ReferenceMediaEvidence = core.CloneInputMediaEvidence(o.ReferenceMediaEvidence)
	if o.ReferenceMediaEvidence == nil {
		o.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{}
	}
	for _, s := range o.ResourceSnapshots {
		h, err := a.open(ctx, s.RemoteID)
		if err != nil || s.Kind != "file_resource" || h.Kind != "file" || !referenceFilePurpose(h, s.Subtype) || h.MediaKind != s.Subtype {
			return r, invalid("managed file belongs to a different owner, connection or media kind")
		}
		scope, _ := core.ProviderConnectionScopeFromContext(ctx)
		if s.ConnectionIdentity != scope.Revision {
			return r, invalid("managed file revision changed")
		}
		uri := "asset://" + base64.RawURLEncoding.EncodeToString([]byte(s.RemoteID))
		n := 0
		switch s.Subtype {
		case "image":
			n = len(o.ImageURLs) + 1
			o.ImageURLs = append(o.ImageURLs, uri)
		case "video":
			n = len(o.VideoURLs) + 1
			o.VideoURLs = append(o.VideoURLs, uri)
		case "audio":
			n = len(o.AudioURLs) + 1
			o.AudioURLs = append(o.AudioURLs, uri)
		default:
			return r, invalid("unsupported managed file media kind")
		}
		if s.ReferenceID != fmt.Sprintf("%s_%d", s.Subtype, n) {
			return r, invalid("managed file aliases must preserve ordered image_N/video_N/audio_N positions")
		}
		e := core.InputMediaEvidence{Type: s.Subtype}
		if len(s.Sources) == 1 {
			source := s.Sources[0]
			if source.Inspection != nil {
				e = core.CloneInputMediaEvidence(map[string]core.InputMediaEvidence{"source": *source.Inspection})["source"]
				if e.ReferenceID != source.ReferenceID || e.Type != s.Subtype || e.SHA256 != source.SHA256 || source.Duration == nil && e.Duration != 0 || source.Duration != nil && e.Duration != *source.Duration {
					return r, invalid("managed file inspection does not match its trusted provenance")
				}
			}
			e.SHA256 = source.SHA256
			if d := source.Duration; d != nil {
				e.Duration = *d
			}
		}
		if s.Subtype == "video" && m.ReferenceMediaLimits != nil {
			limits := m.ReferenceMediaLimits.Video
			role := core.ReferenceRole(m, o, "video", n-1)
			for _, specific := range m.ReferenceRoleLimits {
				if specific.Kind == "video" && specific.Role == role {
					limits = specific.Limits
				}
			}
			if !core.AudioCodecsAllowed(e.AudioStreams, e.AudioCodecs, limits.AllowedAudioCodecs) {
				return r, invalid("managed reference-video audio codecs are unknown or unsupported; inspected AAC/MP3 or verified silence is required")
			}
		}
		o.ReferenceMediaEvidence[uri] = e
	}
	o.VideoURL = ""
	o.AudioURL = ""
	o.ResourceReferences = nil
	o.ResourceSnapshots = nil
	r.Options = o
	return r, nil
}

var _ core.ResourceProvider = (*Adapter)(nil)
var _ core.ResourceDeletionValidator = (*Adapter)(nil)

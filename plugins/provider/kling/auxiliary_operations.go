package kling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

const (
	ImageRecognition     = "kling-image-recognition"
	FaceDetection        = "kling-lip-sync-face-detection"
	LipSync              = "kling-lip-sync"
	MultiElementsEditing = "kling-multi-elements-editing"
)

func isAuxiliaryOperation(id string) bool {
	return slices.Contains([]string{ImageRecognition, FaceDetection, LipSync, MultiElementsEditing}, id)
}
func auxiliaryModels() []core.MediaModel {
	image := core.MediaModel{ID: ImageRecognition, DisplayName: "Kling · Image recognition", MediaType: "json", NoPrompt: true, NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true, SupportsImageInput: true, RequiresImageInput: true, MinImageInputs: 1, MaxImageInputs: 1,
		ReferenceModes: []core.ReferenceMode{{ID: "image", Label: "Image to inspect", MinImages: 1, MaxImages: 1}}, ReferenceMediaLimits: frameLimits(1, 10<<20),
		Pricing: core.MediaModelPricing{Known: true, ReferencePriceUSD: .014, ReferenceDescription: "Published USD balance list price per recognition request; all returned segmentation images are archived alongside the typed JSON summary."},
	}
	face := core.MediaModel{ResourceOutputs: []core.ResourceOutputSchema{{Kind: "face_session", GatewayRetentionSeconds: 3600, RequiresInspectedInput: true, ProviderExpiryKnown: false}}, ID: FaceDetection, DisplayName: "Kling · Lip-sync face detection", MediaType: "json", NoPrompt: true, NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true, SupportsVideoInput: true, RequiresVideoInput: true,
		ReferenceModes:       []core.ReferenceMode{{ID: "video", Label: "Video to inspect", MinVideos: 1, MaxVideos: 1}},
		ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 1, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MaxBytes: 100 << 20, MinDuration: 2, MaxDuration: 60, MinDimension: 512, MaxDimension: 2160}},
		Pricing:              core.MediaModelPricing{Known: true, ReferencePriceUSD: .007, ReferenceDescription: "Published USD balance list price per face-detection request; generated video IDs require managed provenance and are not accepted as free-form parameters."},
	}
	lip := core.MediaModel{ID: LipSync, DisplayName: "Kling · Lip-sync", MediaType: "video", NoPrompt: true, NoSeed: true, NoLanguage: true, NoInstructions: true, DurationFromInput: true, SupportsAudioInput: true, MaxReferenceInputs: 2,
		ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "face_session", MaxCount: 1}, {Kind: "file_resource", Subtypes: []string{"audio"}, MaxCount: 1}},
		Parameters:           []core.ParameterField{auxiliaryInteger("face_index", "Face number (1-based)", 1, 64, true), auxiliaryInteger("sound_start_time", "Audio crop start (ms)", 0, 60000, true), auxiliaryInteger("sound_end_time", "Audio crop end (ms)", 2000, 60000, true), auxiliaryInteger("sound_insert_time", "Insert audio at video time (ms)", 0, 60000, true), auxiliaryNumber("sound_volume", "Inserted audio volume", 0, 2, 1), auxiliaryNumber("original_audio_volume", "Original video volume", 0, 2, 1)},
		ReferenceModes:       []core.ReferenceMode{{ID: "audio", Label: "Managed face session and audio file", MinAudios: 1, MaxAudios: 1}, {ID: "managed_audio", Label: "Managed face session and generated audio", MinAudios: 1, MaxAudios: 1}},
		ReferenceMediaLimits: &core.ReferenceMediaLimits{MaxFiles: 1, Audio: core.MediaFileLimits{ContentTypes: []string{"audio/mpeg", "audio/wav", "audio/x-wav", "audio/mp4", "audio/aac"}, MaxBytes: 5 << 20, MinDuration: 2, MaxDuration: 60}},
		Pricing:              core.MediaModelPricing{Known: false, ReferenceDescription: "Published USD balance tariff is $0.07 per 5 seconds; partial-block rounding is unspecified, so a verified estimate is unavailable."},
	}
	edit := core.MediaModel{ID: MultiElementsEditing, DisplayName: "Kling · Multi-element editing", MediaType: "video", MaxPromptCharacters: 2500, NoSeed: true, NoLanguage: true, NoInstructions: true, SupportsNegativePrompt: true, MinDuration: 5, MaxDuration: 10, DefaultDuration: 5, DurationStep: 5, SupportsImageInput: true, MaxImageInputs: 2, MaxReferenceInputs: 3,
		ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "editing_session", MaxCount: 1}},
		Parameters:           []core.ParameterField{{Key: "edit_mode", Label: "Edit operation", Type: "select", Required: true, Choices: []string{"addition", "swap", "removal"}}, {Key: "mode", Label: "Rendering mode", Type: "select", Default: "std", Choices: []string{"std", "pro"}}},
		ReferenceModes:       []core.ReferenceMode{{ID: "addition", Label: "Add cropped subjects", MinImages: 1, MaxImages: 2}, {ID: "swap", Label: "Replace selected subject", MinImages: 1, MaxImages: 1}, {ID: "removal", Label: "Remove selected subject"}}, ReferenceMediaLimits: frameLimits(2, 10<<20),
		Pricing: core.MediaModelPricing{Known: false, ReferenceDescription: "Published tariffs are $0.42/5s at 720p and $0.70/5s at 1080p; the endpoint exposes std/pro without a documented resolution mapping."},
	}
	return []core.MediaModel{image, face, lip, edit}
}
func auxiliaryInteger(key, label string, min, max float64, required bool) core.ParameterField {
	return core.ParameterField{Key: key, Label: label, Type: "integer", Required: required, Min: &min, Max: &max}
}
func auxiliaryNumber(key, label string, min, max, defaultValue float64) core.ParameterField {
	return core.ParameterField{Key: key, Label: label, Type: "number", Default: defaultValue, Min: &min, Max: &max}
}
func auxiliaryModel(id string) (core.MediaModel, bool) {
	for _, m := range auxiliaryModels() {
		if m.ID == id {
			return m, true
		}
	}
	return core.MediaModel{}, false
}
func auxiliaryInvalid(message string) error {
	return errors.Join(core.ErrOptionsInvalid, errors.New(message))
}
func (a *Adapter) validateAuxiliaryRequest(r core.MediaRequest, scopes ...context.Context) error {
	if e := a.validateGeneratedAudioConsumers(generatedAudioScope(scopes), r); e != nil {
		return e
	}
	m, ok := auxiliaryModel(r.Model)
	if !ok {
		return auxiliaryInvalid("Kling operation requires a managed editing or face session")
	}
	if m.NoPrompt && r.Prompt != "" {
		return auxiliaryInvalid("this Kling inspection or lip-sync operation does not accept a prompt")
	}
	if !m.NoPrompt && (strings.TrimSpace(r.Prompt) == "" || utf8.RuneCountInString(r.Prompt) > 2500) {
		return auxiliaryInvalid("Kling editing prompt must contain 1–2500 characters")
	}
	if _, err := core.NormalizeModelOptions(m, r.Options); err != nil {
		return err
	}
	o := r.Options
	if o.OutputCount != 0 || o.GenerateAudio != nil || (m.NoWatermark && o.Watermark != nil) || o.CameraFixed != nil || o.Draft != nil || o.Seed != nil || o.ReturnLastFrame != nil || (!m.SupportsNegativePrompt && o.NegativePrompt != "") || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 {
		return auxiliaryInvalid("unsupported Kling inspection option")
	}
	if o.Resolution != "" || utf8.RuneCountInString(o.NegativePrompt) > 2500 {
		return auxiliaryInvalid("unsupported Kling auxiliary resolution or negative prompt")
	}
	if r.Model == LipSync || r.Model == MultiElementsEditing {
		return validateAuxiliarySessionRequest(m, r)
	}
	if len(o.ResourceReferences) > 0 || len(o.ResourceSnapshots) > 0 {
		return auxiliaryInvalid("inspection input requires a URL; generated IDs need managed provenance")
	}
	for _, raw := range append(append(append([]string{}, o.ImageURLs...), o.Videos()...), o.Audios()...) {
		if !publicURL(raw) {
			return auxiliaryInvalid("Kling inspection inputs require public HTTPS URLs")
		}
	}
	if r.Model == FaceDetection {
		for _, raw := range o.Videos() {
			if evidence, ok := o.ReferenceMediaEvidence[raw]; ok && (evidence.Type != "video" || evidence.Duration < 2 || evidence.Duration > 60 || math.IsNaN(evidence.Duration) || math.IsInf(evidence.Duration, 0)) {
				return auxiliaryInvalid("Kling face detection requires a 2–60 second video")
			}
		}
	}
	return nil
}

type auxiliaryBilling struct {
	Units   string `json:"final_unit_deduction"`
	Balance struct {
		Quota     string `json:"quota"`
		ListPrice string `json:"list_price"`
	} `json:"final_balance_deduction"`
}

func (b auxiliaryBilling) usage(extra map[string]any) *core.ProviderUsage {
	if extra == nil {
		extra = map[string]any{}
	}
	if decimalPattern.MatchString(b.Units) {
		extra["final_unit_deduction"] = b.Units
	}
	balance := map[string]string{}
	if decimalPattern.MatchString(b.Balance.Quota) {
		balance["quota"] = b.Balance.Quota
	}
	if decimalPattern.MatchString(b.Balance.ListPrice) {
		balance["list_price"] = b.Balance.ListPrice
	}
	if len(balance) > 0 {
		extra["final_balance_deduction"] = balance
	}
	return core.NormalizeProviderUsage(extra)
}

type auxiliaryRecognitionResponse struct {
	auxiliaryBilling
	Result struct {
		Images []struct {
			Type     string `json:"type"`
			Contains *bool  `json:"is_contain"`
			URL      string `json:"url"`
		} `json:"images"`
	} `json:"task_result"`
}
type auxiliaryRecognitionItem struct {
	Type       string `json:"type"`
	Contains   bool   `json:"is_contain"`
	ArtifactID string `json:"artifact_id,omitempty"`
}
type auxiliaryRecognitionSummary struct {
	Images []auxiliaryRecognitionItem `json:"images"`
}

func normalizeImageRecognition(raw json.RawMessage) (core.ProviderTask, error) {
	var response auxiliaryRecognitionResponse
	if json.Unmarshal(raw, &response) != nil || len(response.Result.Images) == 0 || len(response.Result.Images) > 4 {
		return core.ProviderTask{}, errors.New("Kling image recognition returned malformed results")
	}
	summary := auxiliaryRecognitionSummary{Images: []auxiliaryRecognitionItem{}}
	artifacts := []core.ProviderArtifact{{ID: "recognition", MediaType: "json", Format: "json"}}
	seen := map[string]bool{}
	for _, image := range response.Result.Images {
		if !slices.Contains([]string{"object_seg", "head_seg", "face_seg", "cloth_seg"}, image.Type) || seen[image.Type] || image.Contains == nil || *image.Contains && image.URL == "" {
			return core.ProviderTask{}, errors.New("Kling image recognition returned invalid segmentation metadata")
		}
		seen[image.Type] = true
		item := auxiliaryRecognitionItem{Type: image.Type, Contains: *image.Contains}
		if image.URL != "" {
			if !publicURL(image.URL) {
				return core.ProviderTask{}, errors.New("Kling image recognition returned an invalid segmentation image")
			}
			item.ArtifactID = strconv.Itoa(len(artifacts) + 1)
			artifacts = append(artifacts, core.ProviderArtifact{ID: image.Type, MediaType: "image", URL: image.URL})
		}
		summary.Images = append(summary.Images, item)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return core.ProviderTask{}, err
	}
	artifacts[0].Text = string(encoded)
	return core.ProviderTask{Artifacts: artifacts, Usage: response.usage(nil)}, nil
}

type auxiliaryFace struct {
	ID    legacyOpaqueID `json:"face_id"`
	Image string         `json:"face_image"`
	Start *int64         `json:"start_time"`
	End   *int64         `json:"end_time"`
}
type auxiliaryFaceSession struct {
	SessionID legacyOpaqueID  `json:"session_id"`
	Faces     []auxiliaryFace `json:"face_data"`
	auxiliaryBilling
}
type auxiliaryFaceItem struct {
	Index      int    `json:"index"`
	Start      int64  `json:"start_time"`
	End        int64  `json:"end_time"`
	ArtifactID string `json:"artifact_id,omitempty"`
}
type auxiliaryFaceSummary struct {
	Faces []auxiliaryFaceItem `json:"faces"`
}

// Native session/face IDs remain private to the managed session integration.
// The durable result exposes indexes and owned preview artifact addresses only.
func normalizeFaceDetection(raw json.RawMessage) (core.ProviderTask, auxiliaryFaceSession, error) {
	var session auxiliaryFaceSession
	if json.Unmarshal(raw, &session) != nil || session.SessionID == "" || len(session.Faces) > 64 {
		return core.ProviderTask{}, session, errors.New("Kling face detection returned malformed session data")
	}
	summary := auxiliaryFaceSummary{Faces: []auxiliaryFaceItem{}}
	artifacts := []core.ProviderArtifact{{ID: "faces", MediaType: "json", Format: "json"}}
	seen := map[string]bool{}
	for i, face := range session.Faces {
		if face.ID == "" || seen[string(face.ID)] || face.Start == nil || face.End == nil || *face.Start < 0 || *face.End <= *face.Start || *face.End > 60000 {
			return core.ProviderTask{}, session, errors.New("Kling face detection returned invalid face intervals")
		}
		seen[string(face.ID)] = true
		item := auxiliaryFaceItem{Index: i + 1, Start: *face.Start, End: *face.End}
		if face.Image != "" {
			if !publicURL(face.Image) {
				return core.ProviderTask{}, session, errors.New("Kling face detection returned an invalid face preview")
			}
			item.ArtifactID = strconv.Itoa(len(artifacts) + 1)
			artifacts = append(artifacts, core.ProviderArtifact{ID: "face:" + strconv.Itoa(i), MediaType: "image", URL: face.Image})
		}
		summary.Faces = append(summary.Faces, item)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return core.ProviderTask{}, session, err
	}
	artifacts[0].Text = string(encoded)
	return core.ProviderTask{Artifacts: artifacts, Usage: session.usage(nil)}, session, nil
}
func (a *Adapter) generateAuxiliary(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	if err := a.validateAuxiliaryRequest(r, ctx); err != nil {
		return core.ProviderTask{}, err
	}
	if r.Model == LipSync || r.Model == MultiElementsEditing {
		for _, binding := range r.Options.ResourceSnapshots {
			if err := auxiliaryBindingScope(ctx, binding); err != nil {
				return core.ProviderTask{}, err
			}
		}
		nativeAudioID := ""
		if r.Model == LipSync {
			for _, binding := range r.Options.ResourceSnapshots {
				if binding.Kind == "file_resource" {
					id, _, e := a.generatedAudioID(ctx, binding, 60, false)
					if e != nil {
						return core.ProviderTask{}, e
					}
					nativeAudioID = id
				}
			}
		}
		payload, err := auxiliaryVideoPayload(r, nativeAudioID)
		if err != nil {
			return core.ProviderTask{}, err
		}
		return a.submitLegacy(ctx, r.Model, auxiliaryVideoPath(r.Model), payload)
	}
	if r.Model == FaceDetection {
		raw := r.Options.Videos()[0]
		if evidence, ok := r.Options.ReferenceMediaEvidence[raw]; ok && evidence.ReferenceID != "" && evidence.SHA256 != "" && evidence.Duration > 0 {
			duration := evidence.Duration
			source := core.ResourceSourceInput{ReferenceID: evidence.ReferenceID, URL: raw, MediaType: "video", ContentType: evidence.ContentType, SizeBytes: evidence.SizeBytes, Width: evidence.Width, Height: evidence.Height, SHA256: evidence.SHA256, Duration: &duration, FrameCount: evidence.FrameCount, AudioStreams: evidence.AudioStreams, VideoStreams: evidence.VideoStreams}
			initialized, err := a.initializeAuxiliarySession(ctx, "face_session", source, time.Now().Add(time.Hour))
			if err != nil {
				return core.ProviderTask{}, err
			}
			return core.ProviderTask{Artifacts: initialized.Artifacts, Resources: []core.ProviderResource{initialized.Resource}, Usage: initialized.Usage}, nil
		}
	}
	var payload any
	route := "/v1/videos/image-recognize"
	if r.Model == ImageRecognition {
		payload = struct {
			Image string `json:"image"`
		}{r.Options.ImageURLs[0]}
	} else {
		route = "/v1/videos/identify-face"
		payload = struct {
			VideoURL string `json:"video_url"`
		}{r.Options.Videos()[0]}
	}
	raw, err := a.do(ctx, http.MethodPost, route, payload)
	if err != nil {
		return core.ProviderTask{}, err
	}
	if r.Model == ImageRecognition {
		return normalizeImageRecognition(raw)
	}
	task, _, err := normalizeFaceDetection(raw)
	return task, err
}
func (a *Adapter) quoteAuxiliary(r core.MediaRequest, scopes ...context.Context) (core.TaskCost, string, error) {
	if err := a.validateAuxiliaryRequest(r, scopes...); err != nil {
		return core.TaskCost{}, "", err
	}
	if r.Model == LipSync {
		return a.quoteFixedOperation("lip_sync")
	}
	if r.Model == MultiElementsEditing {
		m, _ := auxiliaryModel(r.Model)
		parameters, err := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
		if err != nil {
			return core.TaskCost{}, "", err
		}
		return a.quoteFixedOperation("multi_edit_" + parameters["mode"].(string))
	}
	rate := money.USD(14000)
	if r.Model == FaceDetection {
		rate = 7000
	}
	now := time.Now().UTC()
	return core.TaskCost{ExpectedUSD: rate, EstimatedUnits: 1, Status: "estimated", QuotedAt: now, Pricing: core.PricingSnapshot{Currency: "USD", Unit: "request", Price: rate, Per: 1, Source: priceSource, Version: "2026-10-31", Region: "Singapore", CheckedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC), EstimateDescription: "Published USD balance list price per inspection call; discounts, package units and unlabelled balance deductions are not confirmed USD."}}, "", nil
}

func auxiliarySessionSnapshot(o core.MediaOptions, kind string) (core.ResourceSnapshot, error) {
	var found core.ResourceSnapshot
	count := 0
	for _, r := range o.ResourceSnapshots {
		if r.Kind == kind {
			found = r
			count++
		}
	}
	if count != 1 || found.RemoteID == "" || !validTaskID(found.RemoteID) || found.ConnectionIdentity == "" || found.Session == nil {
		return found, auxiliaryInvalid("Kling operation requires one resolved managed session")
	}
	e := found.Session
	if !e.ExpiresAt.After(time.Now()) || e.SourceSHA256 == "" || len(e.SourceSHA256) != 64 || e.SourceVideoDuration < 2 || e.SourceVideoDuration > 60 || math.IsNaN(e.SourceVideoDuration) || math.IsInf(e.SourceVideoDuration, 0) {
		return found, auxiliaryInvalid("Kling session evidence is missing, expired or invalid")
	}
	for _, ch := range e.SourceSHA256 {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return found, auxiliaryInvalid("Kling session source checksum is invalid")
		}
	}
	return found, nil
}
func validateAuxiliarySessionRequest(m core.MediaModel, r core.MediaRequest) error {
	o := r.Options
	for _, raw := range append(append([]string{}, o.ImageURLs...), o.Audios()...) {
		if !publicURL(raw) {
			return auxiliaryInvalid("Kling auxiliary inputs require public HTTPS URLs")
		}
	}
	p, err := core.NormalizeParameters(m.Parameters, o.Parameters)
	if err != nil {
		return err
	}
	if r.Model == MultiElementsEditing {
		session, err := auxiliarySessionSnapshot(o, "editing_session")
		if err != nil {
			return err
		}
		if len(o.ResourceSnapshots) != 1 {
			return auxiliaryInvalid("Kling editing accepts only one managed editing session")
		}
		mode := p["edit_mode"].(string)
		if o.ReferenceMode != "" && o.ReferenceMode != mode {
			return auxiliaryInvalid("Kling edit mode and reference workflow must match")
		}
		images := len(o.ImageURLs)
		if mode == "addition" && (images < 1 || images > 2) || mode == "swap" && images != 1 || mode == "removal" && images != 0 {
			return auxiliaryInvalid("Kling editing images do not match the edit mode")
		}
		if mode != "addition" && (!session.Session.HasSelection || session.Session.SelectionRevision <= 0) {
			return auxiliaryInvalid("Kling replacement and removal require a managed marked selection")
		}
		duration := durationFor(m, o)
		input := session.Session.SourceVideoDuration
		if duration == 5 && (input < 2 || input > 5) || duration == 10 && (input < 7 || input > 10) {
			return auxiliaryInvalid("Kling edit output duration does not match the initialized input duration")
		}
		if !strings.Contains(r.Prompt, "<<<video_1>>>") {
			return auxiliaryInvalid("Kling editing prompt must reference <<<video_1>>>")
		}
		for i := range o.ImageURLs {
			if !strings.Contains(r.Prompt, "<<<image_"+strconv.Itoa(i+1)+">>>") {
				return auxiliaryInvalid("Kling editing prompt must reference every supplied image")
			}
		}
		return nil
	}
	session, err := auxiliarySessionSnapshot(o, "face_session")
	if err != nil {
		return err
	}
	index := int(p["face_index"].(float64)) - 1
	if index < 0 || index >= len(session.Session.Faces) {
		return auxiliaryInvalid("Kling face number does not exist in this managed session")
	}
	face := session.Session.Faces[index]
	if !validTaskID(face.ID) || face.StartMS < 0 || face.EndMS <= face.StartMS || float64(face.EndMS) > session.Session.SourceVideoDuration*1000 {
		return auxiliaryInvalid("Kling managed face interval is invalid")
	}
	start, end, insert := int64(p["sound_start_time"].(float64)), int64(p["sound_end_time"].(float64)), int64(p["sound_insert_time"].(float64))
	length := end - start
	if length < 2000 || float64(insert+length) > session.Session.SourceVideoDuration*1000 || min(insert+length, face.EndMS)-max(insert, face.StartMS) < 2000 {
		return auxiliaryInvalid("Kling lip-sync audio must fit the video and overlap the selected face by at least two seconds")
	}
	audioResources := 0
	audioDuration := 0.0
	for _, resource := range o.ResourceSnapshots {
		if resource.Kind == "file_resource" {
			audioResources++
			d, e := generatedAudioEvidenceDuration(resource, 60, false)
			if e != nil {
				return e
			}
			audioDuration = d
		}
	}
	if len(o.Audios())+audioResources != 1 || len(o.ResourceSnapshots) != 1+audioResources {
		return auxiliaryInvalid("Kling lip-sync requires exactly one audio file or managed generated audio")
	}
	if len(o.Audios()) == 1 {
		if e, known := o.ReferenceMediaEvidence[o.Audios()[0]]; known {
			if e.Type != "audio" {
				return auxiliaryInvalid("Kling lip-sync requires audio input")
			}
			audioDuration = e.Duration
		}
	}
	if audioResources > 0 && audioDuration <= 0 {
		return auxiliaryInvalid("Kling generated audio duration must be independently verified")
	}
	if audioDuration != 0 && (audioDuration < 2 || audioDuration > 60 || math.IsNaN(audioDuration) || math.IsInf(audioDuration, 0) || float64(end) > audioDuration*1000) {
		return auxiliaryInvalid("Kling audio crop exceeds the inspected 2–60 second audio source")
	}
	return nil
}

type auxiliaryLipFace struct {
	FaceID         string  `json:"face_id"`
	AudioID        string  `json:"audio_id,omitempty"`
	SoundFile      string  `json:"sound_file,omitempty"`
	Start          int64   `json:"sound_start_time"`
	End            int64   `json:"sound_end_time"`
	Insert         int64   `json:"sound_insert_time"`
	SoundVolume    float64 `json:"sound_volume"`
	OriginalVolume float64 `json:"original_audio_volume"`
}
type auxiliaryLipPayload struct {
	SessionID      string             `json:"session_id"`
	Faces          []auxiliaryLipFace `json:"face_choose"`
	Watermark      *motionWatermark   `json:"watermark_info,omitempty"`
	ExternalTaskID string             `json:"external_task_id,omitempty"`
}
type auxiliaryEditImage struct {
	Image string `json:"image"`
}
type auxiliaryEditPayload struct {
	SessionID      string               `json:"session_id"`
	EditMode       string               `json:"edit_mode"`
	Images         []auxiliaryEditImage `json:"image_list"`
	Prompt         string               `json:"prompt"`
	NegativePrompt string               `json:"negative_prompt,omitempty"`
	Mode           string               `json:"mode"`
	Duration       string               `json:"duration"`
	Watermark      *motionWatermark     `json:"watermark_info,omitempty"`
	ExternalTaskID string               `json:"external_task_id,omitempty"`
}

func auxiliaryVideoPath(id string) string {
	switch id {
	case LipSync:
		return "/v1/videos/advanced-lip-sync"
	case MultiElementsEditing:
		return "/v1/videos/multi-elements"
	}
	return ""
}
func auxiliaryVideoPayload(r core.MediaRequest, audioIDs ...string) (any, error) {
	m, ok := auxiliaryModel(r.Model)
	if !ok {
		return nil, auxiliaryInvalid("unsupported Kling auxiliary operation")
	}
	p, err := core.NormalizeParameters(m.Parameters, r.Options.Parameters)
	if err != nil {
		return nil, err
	}
	var watermark *motionWatermark
	if r.Options.Watermark != nil {
		watermark = &motionWatermark{Enabled: *r.Options.Watermark}
	}
	if r.Model == MultiElementsEditing {
		session, err := auxiliarySessionSnapshot(r.Options, "editing_session")
		if err != nil {
			return nil, err
		}
		images := make([]auxiliaryEditImage, 0, len(r.Options.ImageURLs))
		for _, raw := range r.Options.ImageURLs {
			images = append(images, auxiliaryEditImage{raw})
		}
		return auxiliaryEditPayload{SessionID: session.RemoteID, EditMode: p["edit_mode"].(string), Images: images, Prompt: r.Prompt, NegativePrompt: r.Options.NegativePrompt, Mode: p["mode"].(string), Duration: strconv.Itoa(durationFor(m, r.Options)), Watermark: watermark, ExternalTaskID: r.TaskID}, nil
	}
	if r.Model != LipSync {
		return nil, auxiliaryInvalid("unsupported Kling video auxiliary operation")
	}
	session, err := auxiliarySessionSnapshot(r.Options, "face_session")
	if err != nil {
		return nil, err
	}
	index := int(p["face_index"].(float64)) - 1
	if index < 0 || index >= len(session.Session.Faces) {
		return nil, auxiliaryInvalid("Kling face number is invalid")
	}
	face := auxiliaryLipFace{FaceID: session.Session.Faces[index].ID, Start: int64(p["sound_start_time"].(float64)), End: int64(p["sound_end_time"].(float64)), Insert: int64(p["sound_insert_time"].(float64)), SoundVolume: p["sound_volume"].(float64), OriginalVolume: p["original_audio_volume"].(float64)}
	if len(r.Options.Audios()) > 0 {
		face.SoundFile = r.Options.Audios()[0]
	}
	for _, resource := range r.Options.ResourceSnapshots {
		if resource.Kind == "file_resource" {
			if len(audioIDs) != 1 || !validTaskID(audioIDs[0]) {
				return nil, auxiliaryInvalid("generated audio must resolve to an authenticated native identity")
			}
			face.AudioID = audioIDs[0]
		}
	}
	return auxiliaryLipPayload{SessionID: session.RemoteID, Faces: []auxiliaryLipFace{face}, Watermark: watermark, ExternalTaskID: r.TaskID}, nil
}

type auxiliaryVideoTask struct {
	ID     legacyOpaqueID `json:"task_id"`
	Status string         `json:"task_status"`
	Result struct {
		Videos []struct {
			ID        legacyOpaqueID `json:"id"`
			URL       string         `json:"url"`
			Watermark string         `json:"watermark_url"`
			Duration  string         `json:"duration"`
		} `json:"videos"`
	} `json:"task_result"`
	auxiliaryBilling
}

func (a *Adapter) getAuxiliaryTask(ctx context.Context, operation, id string) (core.ProviderResult, error) {
	route := auxiliaryVideoPath(operation)
	if route == "" || !validTaskID(id) {
		return core.ProviderResult{}, errors.New("invalid Kling auxiliary polling operation or ID")
	}
	raw, err := a.do(ctx, http.MethodGet, route+"/"+url.PathEscape(id), nil)
	if err != nil {
		return core.ProviderResult{}, err
	}
	return normalizeAuxiliaryVideo(operation, id, raw)
}
func normalizeAuxiliaryVideo(operation, id string, raw json.RawMessage) (core.ProviderResult, error) {
	var task auxiliaryVideoTask
	if auxiliaryVideoPath(operation) == "" || json.Unmarshal(raw, &task) != nil || task.ID == "" || string(task.ID) != id {
		return core.ProviderResult{}, errors.New("Kling auxiliary task response is malformed or mismatches the requested task")
	}
	switch task.Status {
	case "submitted":
		return core.ProviderResult{Status: core.ProviderQueued}, nil
	case "processing":
		return core.ProviderResult{Status: core.ProviderRunning}, nil
	case "failed":
		return core.ProviderResult{Status: core.ProviderFailed, Error: "Kling video auxiliary operation failed; check provider task details"}, nil
	case "succeed":
	default:
		return core.ProviderResult{}, errors.New("Kling auxiliary task returned an unknown status")
	}
	if len(task.Result.Videos) == 0 || len(task.Result.Videos) > 32 {
		return core.ProviderResult{}, errors.New("Kling auxiliary task returned an invalid video count")
	}
	result := core.ProviderResult{Status: core.ProviderSucceeded}
	seen := map[string]bool{}
	duration := 0.0
	for _, video := range task.Result.Videos {
		seconds, err := audioOutputDuration(video.Duration)
		if err != nil || video.ID == "" || seen[string(video.ID)] || !publicURL(video.URL) {
			return core.ProviderResult{}, errors.New("Kling auxiliary task returned malformed video output")
		}
		seen[string(video.ID)] = true
		duration += seconds
		result.Artifacts = append(result.Artifacts, core.ProviderArtifact{ID: string(video.ID), MediaType: "video", Format: "mp4", URL: video.URL, Duration: seconds, GroupID: string(video.ID)})
		if video.Watermark != "" {
			if !publicURL(video.Watermark) {
				return core.ProviderResult{}, errors.New("Kling auxiliary task returned an invalid watermarked video")
			}
			result.Artifacts = append(result.Artifacts, core.ProviderArtifact{ID: string(video.ID) + ":watermark", MediaType: "video", Format: "mp4", URL: video.Watermark, Duration: seconds, GroupID: string(video.ID)})
		}
	}
	result.ResultURL = result.Artifacts[0].URL
	result.Usage = task.usage(map[string]any{"output_video_duration": duration})
	return result, nil
}

// auxiliarySessionResult retains native session/selection evidence privately.
// TrackingURL is a documented opaque export whose format is not specified; the
// host must inspect/archive it before exposing it as a durable result.
type auxiliarySessionResult struct {
	Resource    core.ProviderResource
	Artifacts   []core.ProviderArtifact
	Usage       *core.ProviderUsage
	TrackingURL string
}

func cloneAuxiliarySession(e *core.MediaSessionEvidence) *core.MediaSessionEvidence {
	copy := *e
	copy.Faces = append([]core.SessionFace(nil), e.Faces...)
	copy.Selections = make([]core.FrameSelection, len(e.Selections))
	for i, f := range e.Selections {
		copy.Selections[i] = core.FrameSelection{FrameIndex: f.FrameIndex, Points: append([]core.SelectionPoint(nil), f.Points...)}
	}
	return &copy
}
func validAuxiliarySource(source core.ResourceSourceInput, kind string) error {
	if source.MediaType != "video" || !slices.Contains([]string{"video/mp4", "video/quicktime"}, source.ContentType) || !publicURL(source.URL) || source.ReferenceID == "" || len(source.SHA256) != 64 || source.SizeBytes <= 0 || source.Duration == nil || math.IsNaN(*source.Duration) || math.IsInf(*source.Duration, 0) {
		return auxiliaryInvalid("Kling sessions require an authorized inspected MP4/MOV source")
	}
	for _, ch := range source.SHA256 {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return auxiliaryInvalid("Kling session source checksum is invalid")
		}
	}
	minimum := 512
	if kind == "editing_session" {
		minimum = 720
		if !(*source.Duration >= 2 && *source.Duration <= 5 || *source.Duration >= 7 && *source.Duration <= 10) {
			return auxiliaryInvalid("Kling editing sessions require 2–5 or 7–10 seconds")
		}
	} else if kind != "face_session" {
		return auxiliaryInvalid("unsupported Kling auxiliary session kind")
	} else if *source.Duration < 2 || *source.Duration > 60 || source.SizeBytes > 100<<20 {
		return auxiliaryInvalid("Kling face sessions require 2–60 seconds and at most 100 MB")
	}
	if source.Width < minimum || source.Height < minimum || source.Width > 2160 || source.Height > 2160 {
		return auxiliaryInvalid("Kling session video dimensions are unsupported")
	}
	return nil
}

// faceExpiry is a host retention policy: the face-detection documentation does
// not publish a TTL. Editing sessions have the documented 24-hour lifetime.
func (a *Adapter) initializeAuxiliarySession(ctx context.Context, kind string, source core.ResourceSourceInput, faceExpiry time.Time) (auxiliarySessionResult, error) {
	if _, err := legacyScope(ctx); err != nil {
		return auxiliarySessionResult{}, err
	}
	if err := validAuxiliarySource(source, kind); err != nil {
		return auxiliarySessionResult{}, err
	}
	duration := *source.Duration
	source.Duration = &duration
	if kind == "face_session" && !faceExpiry.After(time.Now()) {
		return auxiliarySessionResult{}, auxiliaryInvalid("face session activation requires a host expiry policy")
	}
	route := "/v1/videos/identify-face"
	if kind == "editing_session" {
		route = "/v1/videos/multi-elements/init-selection"
	}
	raw, err := a.do(ctx, http.MethodPost, route, struct {
		VideoURL string `json:"video_url"`
	}{source.URL})
	if err != nil {
		return auxiliarySessionResult{}, err
	}
	evidence := &core.MediaSessionEvidence{SourceVideoDuration: *source.Duration, SourceSHA256: source.SHA256}
	result := auxiliarySessionResult{Resource: core.ProviderResource{Kind: kind, Status: core.ProviderSucceeded, Sources: []core.ResourceSourceInput{source}, Session: evidence}}
	if kind == "face_session" {
		task, native, err := normalizeFaceDetection(raw)
		if err != nil {
			return auxiliarySessionResult{}, err
		}
		evidence.ExpiresAt = faceExpiry
		for i, f := range native.Faces {
			if float64(*f.End) > *source.Duration*1000 {
				return auxiliarySessionResult{}, errors.New("Kling face interval exceeds inspected video duration")
			}
			preview := ""
			if f.Image != "" {
				for index, artifact := range task.Artifacts {
					if artifact.ID == "face:"+strconv.Itoa(i) {
						preview = strconv.Itoa(index + 1)
					}
				}
			}
			evidence.Faces = append(evidence.Faces, core.SessionFace{ID: string(f.ID), StartMS: *f.Start, EndMS: *f.End, PreviewArtifactID: preview})
		}
		result.Resource.RemoteID = string(native.SessionID)
		result.Artifacts = task.Artifacts
		result.Usage = task.Usage
		return result, nil
	}
	var native struct {
		Status           *int           `json:"status"`
		SessionID        legacyOpaqueID `json:"session_id"`
		FPS              float64        `json:"fps"`
		Frames           int            `json:"total_frame"`
		Width            int            `json:"width"`
		Height           int            `json:"height"`
		OriginalDuration *float64       `json:"original_duration"`
		Video            string         `json:"normalized_video"`
		auxiliaryBilling
	}
	if json.Unmarshal(raw, &native) != nil || native.Status == nil || *native.Status != 0 || native.SessionID == "" || !slices.Contains([]float64{24, 30, 60}, native.FPS) || native.Frames <= 0 || native.Frames > 600 || native.Width < 720 || native.Height < 720 || native.Width > 2160 || native.Height > 2160 || native.OriginalDuration == nil || *native.OriginalDuration <= 0 || math.IsNaN(*native.OriginalDuration) || math.IsInf(*native.OriginalDuration, 0) || !publicURL(native.Video) {
		return auxiliarySessionResult{}, errors.New("Kling editing initialization returned malformed session metadata")
	}
	evidence.ExpiresAt = time.Now().UTC().Add(24 * time.Hour)
	evidence.FrameCount = native.Frames
	evidence.FPS = native.FPS
	result.Resource.RemoteID = string(native.SessionID)
	result.Artifacts = []core.ProviderArtifact{{ID: "normalized_video", MediaType: "video", Format: "mp4", URL: native.Video, Duration: *source.Duration}}
	result.Usage = native.usage(nil)
	return result, nil
}
func auxiliarySelections(e *core.MediaSessionEvidence, action string, frame *core.FrameSelection) ([]core.FrameSelection, error) {
	copy := cloneAuxiliarySession(e)
	if action == "clear" {
		if frame != nil {
			return nil, auxiliaryInvalid("clear selection does not take frame points")
		}
		return nil, nil
	}
	if action != "add" && action != "delete" {
		return nil, auxiliaryInvalid("unknown Kling selection mutation")
	}
	if frame == nil || frame.FrameIndex < 0 || frame.FrameIndex >= e.FrameCount || len(frame.Points) == 0 || len(frame.Points) > 10 || e.FrameCount <= 0 || len(e.Selections) > 10 {
		return nil, auxiliaryInvalid("Kling selection requires one valid frame and 1–10 points")
	}
	pointSet := map[core.SelectionPoint]bool{}
	for _, point := range frame.Points {
		if point.X < 0 || point.X > 1 || point.Y < 0 || point.Y > 1 || math.IsNaN(point.X) || math.IsNaN(point.Y) || math.IsInf(point.X, 0) || math.IsInf(point.Y, 0) || pointSet[point] {
			return nil, auxiliaryInvalid("Kling selection coordinates must be unique finite values in [0,1]")
		}
		pointSet[point] = true
	}
	index := -1
	for i, f := range copy.Selections {
		if f.FrameIndex == frame.FrameIndex {
			index = i
		}
	}
	if action == "add" {
		if index < 0 {
			if len(copy.Selections) >= 10 {
				return nil, auxiliaryInvalid("Kling permits at most ten selected frames")
			}
			copy.Selections = append(copy.Selections, core.FrameSelection{FrameIndex: frame.FrameIndex})
			index = len(copy.Selections) - 1
		}
		for _, existing := range copy.Selections[index].Points {
			if pointSet[existing] {
				return nil, auxiliaryInvalid("Kling selection point was already added")
			}
		}
		if len(copy.Selections[index].Points)+len(frame.Points) > 10 {
			return nil, auxiliaryInvalid("Kling permits at most ten points on a selected frame")
		}
		copy.Selections[index].Points = append(copy.Selections[index].Points, frame.Points...)
	} else {
		if index < 0 {
			return nil, auxiliaryInvalid("Kling deletion must match a previously added frame")
		}
		existingSet := map[core.SelectionPoint]bool{}
		for _, point := range copy.Selections[index].Points {
			existingSet[point] = true
		}
		for _, point := range frame.Points {
			if !existingSet[point] {
				return nil, auxiliaryInvalid("Kling deletion must exactly match previously added coordinates")
			}
		}
		remaining := []core.SelectionPoint{}
		for _, point := range copy.Selections[index].Points {
			if !pointSet[point] {
				remaining = append(remaining, point)
			}
		}
		copy.Selections[index].Points = remaining
		if len(remaining) == 0 {
			copy.Selections = append(copy.Selections[:index], copy.Selections[index+1:]...)
		}
	}
	return copy.Selections, nil
}

type auxiliarySelectionReply struct {
	Status    *int           `json:"status"`
	SessionID legacyOpaqueID `json:"session_id"`
	Result    struct {
		Frame int `json:"frame_index"`
		Masks []struct {
			ID  int `json:"object_id"`
			RLE struct {
				Size   []int  `json:"size"`
				Counts string `json:"counts"`
			} `json:"rle_mask"`
			PNG struct {
				Size   []int  `json:"size"`
				Base64 string `json:"base64"`
			} `json:"png_mask"`
		} `json:"rle_mask_list"`
		Video    string `json:"video"`
		Cover    string `json:"video_cover"`
		Tracking string `json:"tracking_output"`
	} `json:"res"`
	auxiliaryBilling
}
type auxiliaryMaskSummary struct {
	Frame int                 `json:"frame_index"`
	Masks []auxiliaryMaskItem `json:"masks"`
}
type auxiliaryMaskItem struct {
	ObjectID   int    `json:"object_id"`
	Size       []int  `json:"size"`
	Counts     string `json:"counts"`
	ArtifactID string `json:"artifact_id,omitempty"`
}

// The caller must hold the binding's mutation lock and compare SelectionRevision
// before calling this helper; queued/running generation references freeze edits.
func (a *Adapter) mutateAuxiliarySelection(ctx context.Context, binding core.ResourceSnapshot, action string, frame *core.FrameSelection) (auxiliarySessionResult, error) {
	if err := auxiliaryBindingScope(ctx, binding); err != nil {
		return auxiliarySessionResult{}, err
	}
	session, err := auxiliarySessionSnapshot(core.MediaOptions{ResourceSnapshots: []core.ResourceSnapshot{binding}}, "editing_session")
	if err != nil {
		return auxiliarySessionResult{}, err
	}
	selected, err := auxiliarySelections(session.Session, action, frame)
	if err != nil {
		return auxiliarySessionResult{}, err
	}
	payload := struct {
		SessionID string                `json:"session_id"`
		Frame     *int                  `json:"frame_index,omitempty"`
		Points    []core.SelectionPoint `json:"points,omitempty"`
	}{SessionID: binding.RemoteID}
	if frame != nil {
		index := frame.FrameIndex
		payload.Frame = &index
		payload.Points = append([]core.SelectionPoint(nil), frame.Points...)
	}
	suffix := map[string]string{"add": "add-selection", "delete": "delete-selection", "clear": "clear-selection"}[action]
	raw, err := a.do(ctx, http.MethodPost, "/v1/videos/multi-elements/"+suffix, payload)
	if err != nil {
		return auxiliarySessionResult{}, errors.Join(ErrAuxiliarySessionStateUncertain, err)
	}
	var reply auxiliarySelectionReply
	if json.Unmarshal(raw, &reply) != nil || reply.Status == nil || *reply.Status != 0 || string(reply.SessionID) != binding.RemoteID {
		return auxiliarySessionResult{}, errors.Join(ErrAuxiliarySessionStateUncertain, errors.New("Kling selection update returned a malformed or mismatched session"))
	}
	result := auxiliarySessionResult{Resource: core.ProviderResource{Kind: "editing_session", RemoteID: binding.RemoteID, Status: core.ProviderSucceeded, Session: cloneAuxiliarySession(binding.Session)}, Usage: reply.usage(nil)}
	result.Resource.Session.Selections = selected
	result.Resource.Session.SelectionRevision++
	result.Resource.Session.HasSelection = len(selected) > 0
	if action == "clear" {
		return result, nil
	}
	pointsRemain := false
	for _, f := range selected {
		if f.FrameIndex == frame.FrameIndex && len(f.Points) > 0 {
			pointsRemain = true
		}
	}
	if reply.Result.Frame != frame.FrameIndex || len(reply.Result.Masks) > 10 || pointsRemain && len(reply.Result.Masks) == 0 {
		return auxiliarySessionResult{}, errors.Join(ErrAuxiliarySessionStateUncertain, errors.New("Kling selection returned an invalid mask frame or count"))
	}
	summary := auxiliaryMaskSummary{Frame: reply.Result.Frame, Masks: []auxiliaryMaskItem{}}
	result.Artifacts = []core.ProviderArtifact{{ID: "selection", MediaType: "json", Format: "json"}}
	seen := map[int]bool{}
	for _, mask := range reply.Result.Masks {
		if mask.ID < 0 || seen[mask.ID] || len(mask.RLE.Size) != 2 || mask.RLE.Size[0] <= 0 || mask.RLE.Size[1] <= 0 || mask.RLE.Size[0] > 2160 || mask.RLE.Size[1] > 2160 || mask.RLE.Counts == "" {
			return auxiliarySessionResult{}, errors.Join(ErrAuxiliarySessionStateUncertain, errors.New("Kling selection returned malformed RLE mask metadata"))
		}
		seen[mask.ID] = true
		item := auxiliaryMaskItem{ObjectID: mask.ID, Size: append([]int(nil), mask.RLE.Size...), Counts: mask.RLE.Counts}
		if mask.PNG.Base64 != "" {
			data, err := base64.StdEncoding.DecodeString(mask.PNG.Base64)
			if err != nil {
				return auxiliarySessionResult{}, errors.Join(ErrAuxiliarySessionStateUncertain, errors.New("Kling selection returned invalid PNG mask encoding"))
			}
			info, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil || len(mask.PNG.Size) != 2 || info.Height != mask.PNG.Size[0] || info.Width != mask.PNG.Size[1] || info.Height != mask.RLE.Size[0] || info.Width != mask.RLE.Size[1] {
				return auxiliarySessionResult{}, errors.Join(ErrAuxiliarySessionStateUncertain, errors.New("Kling selection PNG mask does not match its declared dimensions"))
			}
			item.ArtifactID = strconv.Itoa(len(result.Artifacts) + 1)
			result.Artifacts = append(result.Artifacts, core.ProviderArtifact{ID: "mask:" + strconv.Itoa(mask.ID), MediaType: "image", Format: "png", URL: "data:image/png;base64," + mask.PNG.Base64})
		}
		summary.Masks = append(summary.Masks, item)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return auxiliarySessionResult{}, errors.Join(ErrAuxiliarySessionStateUncertain, err)
	}
	result.Artifacts[0].Text = string(encoded)
	return result, nil
}
func (a *Adapter) previewAuxiliarySelection(ctx context.Context, binding core.ResourceSnapshot) (auxiliarySessionResult, error) {
	if err := auxiliaryBindingScope(ctx, binding); err != nil {
		return auxiliarySessionResult{}, err
	}
	if _, err := auxiliarySessionSnapshot(core.MediaOptions{ResourceSnapshots: []core.ResourceSnapshot{binding}}, "editing_session"); err != nil {
		return auxiliarySessionResult{}, err
	}
	raw, err := a.do(ctx, http.MethodPost, "/v1/videos/multi-elements/preview-selection", struct {
		SessionID string `json:"session_id"`
	}{binding.RemoteID})
	if err != nil {
		return auxiliarySessionResult{}, err
	}
	var reply auxiliarySelectionReply
	if json.Unmarshal(raw, &reply) != nil || reply.Status == nil || *reply.Status != 0 || string(reply.SessionID) != binding.RemoteID || !publicURL(reply.Result.Video) || !publicURL(reply.Result.Cover) || !publicURL(reply.Result.Tracking) {
		return auxiliarySessionResult{}, errors.New("Kling selection preview returned malformed or mismatched sources")
	}
	return auxiliarySessionResult{Resource: core.ProviderResource{Kind: "editing_session", RemoteID: binding.RemoteID, Status: core.ProviderSucceeded, Session: cloneAuxiliarySession(binding.Session)}, Artifacts: []core.ProviderArtifact{{ID: "selection_video", MediaType: "video", Format: "mp4", URL: reply.Result.Video, Duration: binding.Session.SourceVideoDuration}, {ID: "selection_cover", MediaType: "image", URL: reply.Result.Cover}}, TrackingURL: reply.Result.Tracking, Usage: reply.usage(nil)}, nil
}

func auxiliaryBindingScope(ctx context.Context, binding core.ResourceSnapshot) error {
	scope, ok := core.ProviderConnectionScopeFromContext(ctx)
	if !ok {
		return auxiliaryInvalid("Kling session operation requires a concrete provider connection scope")
	}
	if binding.ConnectionIdentity != scope.Revision {
		return errors.Join(core.ErrProviderChanged, errors.New("Kling managed session does not match this connection revision"))
	}
	return nil
}

// ErrAuxiliarySessionStateUncertain marks failures after a selection mutation
// was dispatched. The host must reconcile/clear the mutable provider session
// instead of retrying against a snapshot that may no longer describe it.
var ErrAuxiliarySessionStateUncertain = errors.New("Kling selection state is uncertain; reconcile it before another mutation")

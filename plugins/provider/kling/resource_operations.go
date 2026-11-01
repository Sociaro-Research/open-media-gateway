package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
)

var _ core.ResourceProvider = (*Adapter)(nil)
var resourceSHA = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var resourceTagIDs = []string{"o_101", "o_102", "o_103", "o_104", "o_105", "o_106", "o_107", "o_108"}

const resourceEvidenceWarning = "Provider resource results do not establish inspected source duration or Gateway ownership; retain host-resolved source evidence and owner-scoped bindings."
const resourceDeletionWarning = "Deletion is pending. The reviewed Kling documentation does not specify a deletion-task polling route; do not treat submission as terminal deletion."
const resourceCreationWarning = "Resource creation may consume provider units; no verified numeric creation tariff is available in the reviewed contract."

func resourcePath(kind string, preset bool) string {
	switch kind {
	case "element":
		if preset {
			return "/v1/general/advanced-presets-elements"
		}
		return "/v1/general/advanced-custom-elements"
	case "voice":
		if preset {
			return "/v1/general/presets-voices"
		}
		return "/v1/general/custom-voices"
	}
	return ""
}
func resourceOperation(kind, action string) string { return "resource:" + kind + ":" + action }
func resourceInvalid(message string) error {
	return fmt.Errorf("%w: %s", core.ErrOptionsInvalid, message)
}
func resourceContentType(source core.ResourceSourceInput) string {
	kind, _, err := mime.ParseMediaType(source.ContentType)
	if err != nil {
		return ""
	}
	return strings.ToLower(kind)
}
func resourceSourceIdentity(source core.ResourceSourceInput) error {
	if source.ReferenceID == "" || !resourceSHA.MatchString(source.SHA256) || !publicURL(source.URL) || source.SizeBytes <= 0 {
		return resourceInvalid("resource source requires host-resolved reference identity, inspected hash/size and a public HTTPS URL")
	}
	return nil
}
func resourceDuration(source *float64, min, max float64) bool {
	return source != nil && !math.IsNaN(*source) && !math.IsInf(*source, 0) && *source >= min && *source <= max
}
func validateResourceCreate(ctx context.Context, r core.ResourceCreateRequest) error {
	if _, err := legacyScope(ctx); err != nil {
		return err
	}
	if r.ExternalTaskID != "" {
		id, err := uuid.Parse(r.ExternalTaskID)
		if err != nil || id.String() != r.ExternalTaskID {
			return resourceInvalid("resource external task ID must be a canonical server-assigned UUID")
		}
	}
	if resourcePath(r.Kind, false) == "" {
		return resourceInvalid("unsupported resource kind")
	}
	if strings.TrimSpace(r.Name) == "" || utf8.RuneCountInString(r.Name) > 20 {
		return resourceInvalid("resource name must contain 1–20 characters")
	}
	if len(r.Tags) > 8 {
		return resourceInvalid("at most eight reviewed element tags are supported")
	}
	seenTags := map[string]bool{}
	for _, tag := range r.Tags {
		if !slices.Contains(resourceTagIDs, tag) || seenTags[tag] {
			return resourceInvalid("unknown or duplicate element tag")
		}
		seenTags[tag] = true
	}
	switch r.Kind {
	case "element":
		if strings.TrimSpace(r.Description) == "" || utf8.RuneCountInString(r.Description) > 100 {
			return resourceInvalid("element description must contain 1–100 characters")
		}
		if len(r.VoiceSources) > 0 || r.GeneratedVideo != nil {
			return resourceInvalid("element creation does not accept voice-customization sources")
		}
		if r.Subtype == "multi_image" {
			if len(r.Images) < 2 || len(r.Images) > 4 || len(r.Videos) != 0 {
				return resourceInvalid("multi-image elements require one frontal image plus 1–3 different reference images")
			}
			seen := map[string]bool{}
			for _, image := range r.Images {
				if err := resourceSourceIdentity(image); err != nil {
					return err
				}
				kind := resourceContentType(image)
				if image.MediaType != "image" || (kind != "image/jpeg" && kind != "image/png") || image.SizeBytes > 10<<20 || image.Width < 300 || image.Height < 300 {
					return resourceInvalid("element images must be JPEG/PNG, at most 10MB, and at least 300px on both sides")
				}
				ratio := float64(image.Width) / float64(image.Height)
				if ratio < .4 || ratio > 2.5 {
					return resourceInvalid("element image aspect ratio must be between 0.4 and 2.5")
				}
				if seen[strings.ToLower(image.SHA256)] {
					return resourceInvalid("element reference images must differ from the frontal image and each other")
				}
				seen[strings.ToLower(image.SHA256)] = true
			}
		} else if r.Subtype == "video_character" {
			if len(r.Images) != 0 || len(r.Videos) != 1 {
				return resourceInvalid("video-character elements require exactly one source video")
			}
			video := r.Videos[0]
			if err := resourceSourceIdentity(video); err != nil {
				return err
			}
			kind := resourceContentType(video)
			if video.MediaType != "video" || (kind != "video/mp4" && kind != "video/quicktime") || video.SizeBytes > 200<<20 || !resourceDuration(video.Duration, 3, 8) || !((video.Width == 1920 && video.Height == 1080) || (video.Width == 1080 && video.Height == 1920)) {
				return resourceInvalid("video-character source must be MP4/MOV, 3–8 seconds, at most 200MB, and 1080p at 16:9 or 9:16")
			}
		} else {
			return resourceInvalid("unsupported element subtype")
		}
		if r.BoundVoice != nil {
			scope, _ := core.ProviderConnectionScopeFromContext(ctx)
			voice := r.BoundVoice
			if _, err := uuid.Parse(voice.BindingID); err != nil || voice.Kind != "voice" || !validTaskID(voice.RemoteID) || voice.ConnectionIdentity != scope.Revision {
				return resourceInvalid("element voice must be resolved from an authorized binding on the same provider connection revision")
			}
		}
	case "voice":
		if r.Subtype != "" && r.Subtype != "custom" {
			return resourceInvalid("unsupported custom voice subtype")
		}
		if r.Description != "" || len(r.Images) > 0 || len(r.Videos) > 0 || r.BoundVoice != nil || len(r.Tags) > 0 {
			return resourceInvalid("unsupported voice customization control")
		}
		if (len(r.VoiceSources) == 1) == (r.GeneratedVideo != nil) || len(r.VoiceSources) > 1 {
			return resourceInvalid("voice customization requires exactly one inspected source or eligible generated video")
		}
		if len(r.VoiceSources) == 1 {
			source := r.VoiceSources[0]
			if err := resourceSourceIdentity(source); err != nil {
				return err
			}
			kind := resourceContentType(source)
			if !((source.MediaType == "audio" && (kind == "audio/mpeg" || kind == "audio/wav" || kind == "audio/x-wav" || kind == "audio/wave")) || (source.MediaType == "video" && (kind == "video/mp4" || kind == "video/quicktime"))) || !resourceDuration(source.Duration, 5, 30) {
				return resourceInvalid("voice source must be MP3/WAV/MP4/MOV with an inspected duration of 5–30 seconds")
			}
		}
		if generated := r.GeneratedVideo; generated != nil {
			eligible := (generated.Operation == TextToVideo26 || generated.Operation == ImageToVideo26) && generated.NativeAudio || generated.Operation == AvatarImageToVideo || generated.Operation == "kling-lip-sync"
			if !eligible || !validTaskID(generated.RemoteID) || !resourceDuration(generated.Duration, 5, 30) {
				return resourceInvalid("generated voice source must be a host-authorized 5–30 second V2.6 native-audio, Avatar, or Lip-Sync output")
			}
		}
	}
	return nil
}

type resourceURL struct {
	ImageURL string `json:"image_url,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
}
type resourceTag struct {
	ID string `json:"tag_id"`
}
type resourceElementPayload struct {
	ExternalTaskID string `json:"external_task_id,omitempty"`
	Name           string `json:"element_name"`
	Description    string `json:"element_description"`
	ReferenceType  string `json:"reference_type"`
	Images         *struct {
		Frontal    string        `json:"frontal_image"`
		References []resourceURL `json:"refer_images"`
	} `json:"element_image_list,omitempty"`
	Videos *struct {
		References []resourceURL `json:"refer_videos"`
	} `json:"element_video_list,omitempty"`
	VoiceID string        `json:"element_voice_id,omitempty"`
	Tags    []resourceTag `json:"tag_list,omitempty"`
}
type resourceVoicePayload struct {
	ExternalTaskID string `json:"external_task_id,omitempty"`
	Name           string `json:"voice_name"`
	URL            string `json:"voice_url,omitempty"`
	VideoID        string `json:"video_id,omitempty"`
}

func resourcePayload(r core.ResourceCreateRequest) any {
	if r.Kind == "voice" {
		p := resourceVoicePayload{Name: r.Name, ExternalTaskID: r.ExternalTaskID}
		if len(r.VoiceSources) > 0 {
			p.URL = r.VoiceSources[0].URL
		} else {
			p.VideoID = r.GeneratedVideo.RemoteID
		}
		return p
	}
	p := resourceElementPayload{Name: r.Name, Description: r.Description, ExternalTaskID: r.ExternalTaskID}
	if r.Subtype == "multi_image" {
		p.ReferenceType = "image_refer"
		p.Images = &struct {
			Frontal    string        `json:"frontal_image"`
			References []resourceURL `json:"refer_images"`
		}{Frontal: r.Images[0].URL}
		for _, image := range r.Images[1:] {
			p.Images.References = append(p.Images.References, resourceURL{ImageURL: image.URL})
		}
	} else {
		p.ReferenceType = "video_refer"
		p.Videos = &struct {
			References []resourceURL `json:"refer_videos"`
		}{References: []resourceURL{{VideoURL: r.Videos[0].URL}}}
	}
	if r.BoundVoice != nil {
		p.VoiceID = r.BoundVoice.RemoteID
	}
	for _, tag := range r.Tags {
		p.Tags = append(p.Tags, resourceTag{ID: tag})
	}
	return p
}

type resourceNativeVoice struct {
	ID      legacyOpaqueID `json:"voice_id"`
	Name    string         `json:"voice_name"`
	Preview string         `json:"trial_url"`
	OwnedBy legacyOpaqueID `json:"owned_by"`
}
type resourceNativeElement struct {
	ID            legacyOpaqueID `json:"element_id"`
	Name          string         `json:"element_name"`
	Description   string         `json:"element_description"`
	ReferenceType string         `json:"reference_type"`
	Status        string         `json:"status"`
	OwnedBy       legacyOpaqueID `json:"owned_by"`
	Images        struct {
		Frontal    string        `json:"frontal_image"`
		References []resourceURL `json:"refer_images"`
	} `json:"element_image_list"`
	Videos struct {
		References []resourceURL `json:"refer_videos"`
	} `json:"element_video_list"`
	Voice *resourceNativeVoice `json:"element_voice_info"`
	Tags  []resourceTag        `json:"tag_list"`
}
type resourceNativeTask struct {
	ID     legacyOpaqueID `json:"task_id"`
	Status string         `json:"task_status"`
	Result struct {
		Elements []resourceNativeElement `json:"elements"`
		Voices   []resourceNativeVoice   `json:"voices"`
	} `json:"task_result"`
	Units   json.RawMessage `json:"final_unit_deduction"`
	Balance json.RawMessage `json:"final_balance_deduction"`
}

func resourceStatus(status string) (core.ProviderStatus, error) {
	switch status {
	case "submitted":
		return core.ProviderQueued, nil
	case "processing":
		return core.ProviderRunning, nil
	case "succeed":
		return core.ProviderSucceeded, nil
	case "failed":
		return core.ProviderFailed, nil
	}
	return "", errors.New("Kling resource task has an unknown status")
}
func normalizeResourceVoice(voice resourceNativeVoice) (core.ProviderResource, error) {
	if voice.ID == "" || voice.OwnedBy == "" || strings.TrimSpace(voice.Name) == "" || (voice.Preview != "" && !publicURL(voice.Preview)) {
		return core.ProviderResource{}, errors.New("malformed Kling resource voice")
	}
	subtype := "custom"
	if voice.OwnedBy == "kling" {
		subtype = "preset"
	}
	return core.ProviderResource{RemoteID: string(voice.ID), Kind: "voice", Subtype: subtype, Name: voice.Name, Status: core.ProviderSucceeded, IsPreset: voice.OwnedBy == "kling", PreviewURL: voice.Preview}, nil
}
func normalizeResourceElement(element resourceNativeElement) (core.ProviderResource, error) {
	if element.ID == "" || element.OwnedBy == "" || strings.TrimSpace(element.Name) == "" || (element.Status != "succeed" && element.Status != "deleted") {
		return core.ProviderResource{}, errors.New("malformed Kling resource element")
	}
	subtype := ""
	switch element.ReferenceType {
	case "image_refer":
		subtype = "multi_image"
	case "video_refer":
		subtype = "video_character"
	default:
		return core.ProviderResource{}, errors.New("unknown Kling element reference type")
	}
	r := core.ProviderResource{RemoteID: string(element.ID), Kind: "element", Subtype: subtype, Name: element.Name, Description: element.Description, Status: core.ProviderSucceeded, IsPreset: element.OwnedBy == "kling", IsDeleted: element.Status == "deleted"}
	addSource := func(raw, kind string) error {
		if !publicURL(raw) {
			return errors.New("Kling resource contains an invalid source URL")
		}
		r.Sources = append(r.Sources, core.ResourceSourceInput{URL: raw, MediaType: kind})
		return nil
	}
	if element.Images.Frontal != "" {
		if err := addSource(element.Images.Frontal, "image"); err != nil {
			return core.ProviderResource{}, err
		}
	}
	for _, source := range element.Images.References {
		if err := addSource(source.ImageURL, "image"); err != nil {
			return core.ProviderResource{}, err
		}
	}
	for _, source := range element.Videos.References {
		if err := addSource(source.VideoURL, "video"); err != nil {
			return core.ProviderResource{}, err
		}
	}
	if element.Voice != nil && element.Voice.ID != "" {
		voice, err := normalizeResourceVoice(*element.Voice)
		if err != nil {
			return core.ProviderResource{}, err
		}
		r.BoundVoice = &voice
	}
	for _, tag := range element.Tags {
		if !slices.Contains(resourceTagIDs, tag.ID) {
			return core.ProviderResource{}, errors.New("unknown Kling resource element tag")
		}
		r.Tags = append(r.Tags, tag.ID)
	}
	return r, nil
}
func resourceUsage(task resourceNativeTask) *core.ProviderUsage {
	if len(task.Units) == 0 && len(task.Balance) == 0 {
		return nil
	}
	raw, _ := json.Marshal(map[string]json.RawMessage{"final_unit_deduction": task.Units, "final_balance_deduction": task.Balance})
	return &core.ProviderUsage{Raw: raw}
}
func (a *Adapter) normalizeResourceJob(ctx context.Context, kind string, task resourceNativeTask, action string) (core.ResourceJob, error) {
	status, err := resourceStatus(task.Status)
	if err != nil || task.ID == "" {
		return core.ResourceJob{}, errors.New("malformed Kling resource task")
	}
	handle, err := a.signLegacy(ctx, resourceOperation(kind, action), string(task.ID))
	if err != nil {
		return core.ResourceJob{}, err
	}
	job := core.ResourceJob{Handle: handle, Status: status, Warning: resourceEvidenceWarning, Usage: resourceUsage(task)}
	if status == core.ProviderFailed {
		job.Error = "Kling resource operation failed; check provider task details"
		return job, nil
	}
	if status == core.ProviderSucceeded {
		if kind == "element" {
			if len(task.Result.Voices) > 0 {
				return core.ResourceJob{}, errors.New("Kling element task returned voice-only results")
			}
			for _, element := range task.Result.Elements {
				r, err := normalizeResourceElement(element)
				if err != nil {
					return core.ResourceJob{}, err
				}
				job.Resources = append(job.Resources, r)
			}
		} else {
			if len(task.Result.Elements) > 0 {
				return core.ResourceJob{}, errors.New("Kling voice task returned element-only results")
			}
			for _, voice := range task.Result.Voices {
				r, err := normalizeResourceVoice(voice)
				if err != nil {
					return core.ResourceJob{}, err
				}
				job.Resources = append(job.Resources, r)
			}
		}
		if len(job.Resources) == 0 {
			return core.ResourceJob{}, errors.New("Kling resource task succeeded without resources")
		}
	}
	return job, nil
}
func (a *Adapter) CreateResource(ctx context.Context, r core.ResourceCreateRequest) (core.ResourceJob, error) {
	if err := validateResourceCreate(ctx, r); err != nil {
		return core.ResourceJob{}, err
	}
	raw, err := a.do(ctx, http.MethodPost, resourcePath(r.Kind, false), resourcePayload(r))
	if err != nil {
		return core.ResourceJob{}, err
	}
	var task resourceNativeTask
	if json.Unmarshal(raw, &task) != nil {
		return core.ResourceJob{}, errors.New("malformed Kling resource creation response; check provider history before resubmitting")
	}
	if task.Status == "succeed" && len(task.Result.Elements) == 0 && len(task.Result.Voices) == 0 && task.ID != "" {
		handle, err := a.signLegacy(ctx, resourceOperation(r.Kind, "create"), string(task.ID))
		if err != nil {
			return core.ResourceJob{}, err
		}
		return core.ResourceJob{Handle: handle, Status: core.ProviderRunning, Usage: resourceUsage(task), Warning: resourceCreationWarning + " " + resourceEvidenceWarning + " Provider reports completion; poll the task to resolve generated resource identity."}, nil
	}
	job, err := a.normalizeResourceJob(ctx, r.Kind, task, "create")
	if err != nil {
		return core.ResourceJob{}, err
	}
	job.Warning = resourceCreationWarning + " " + job.Warning
	if r.Subtype == "video_character" {
		job.Warning += " A source video containing human speech may also trigger voice customization and binding; only realistic humanoid video characters are supported."
	}
	if r.BoundVoice != nil && r.Subtype == "multi_image" {
		job.Warning += " Voice binding requires a character or humanoid image element."
	}
	return job, nil
}
func (a *Adapter) GetResourceJob(ctx context.Context, kind, handle string) (core.ResourceJob, error) {
	if resourcePath(kind, false) == "" {
		return core.ResourceJob{}, resourceInvalid("unsupported resource kind")
	}
	h, err := a.parseLegacy(ctx, handle)
	if err != nil {
		return core.ResourceJob{}, err
	}
	prefix := resourceOperation(kind, "delete:")
	if strings.HasPrefix(h.Operation, prefix) {
		native := strings.TrimPrefix(h.Operation, prefix)
		status, err := resourceStatus(native)
		if err != nil {
			return core.ResourceJob{}, err
		}
		warning := ""
		if status == core.ProviderQueued || status == core.ProviderRunning {
			warning = resourceDeletionWarning
		}
		job := core.ResourceJob{Handle: handle, Status: status, Warning: warning}
		if status == core.ProviderFailed {
			job.Error = "Kling resource deletion failed; check provider task details"
		}
		return job, nil
	}
	if h.Operation != resourceOperation(kind, "create") {
		return core.ResourceJob{}, errors.New("resource handle kind or operation does not match")
	}
	raw, err := a.do(ctx, http.MethodGet, resourcePath(kind, false)+"/"+h.ID, nil)
	if err != nil {
		return core.ResourceJob{}, err
	}
	var task resourceNativeTask
	if json.Unmarshal(raw, &task) != nil || string(task.ID) != h.ID {
		return core.ResourceJob{}, errors.New("Kling resource response mismatches requested task")
	}
	return a.normalizeResourceJob(ctx, kind, task, "create")
}
func (a *Adapter) ListResources(ctx context.Context, kind string, preset bool, page, size int) (core.ResourcePage, error) {
	if _, err := legacyScope(ctx); err != nil {
		return core.ResourcePage{}, err
	}
	path := resourcePath(kind, preset)
	if path == "" {
		return core.ResourcePage{}, resourceInvalid("unsupported resource kind")
	}
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 30
	}
	limit := 500
	if kind == "voice" {
		limit = 1000
	}
	if page < 1 || page > 1000 || size < 1 || size > limit {
		return core.ResourcePage{}, resourceInvalid("unsupported resource pagination")
	}
	raw, err := a.do(ctx, http.MethodGet, path+"?"+url.Values{"pageNum": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(size)}}.Encode(), nil)
	if err != nil {
		return core.ResourcePage{}, err
	}
	var tasks []resourceNativeTask
	if json.Unmarshal(raw, &tasks) != nil {
		return core.ResourcePage{}, errors.New("malformed Kling resource list")
	}
	result := core.ResourcePage{Page: page, PageSize: size, HasMore: len(tasks) == size}
	for _, task := range tasks {
		job, err := a.normalizeResourceJob(ctx, kind, task, "create")
		if err != nil {
			return core.ResourcePage{}, err
		}
		for _, resource := range job.Resources {
			if preset && !resource.IsPreset {
				return core.ResourcePage{}, errors.New("Kling preset catalogue returned a custom resource")
			}
			result.Resources = append(result.Resources, resource)
		}
	}
	return result, nil
}
func (a *Adapter) DeleteResource(ctx context.Context, binding core.ResourceSnapshot) (core.ResourceJob, error) {
	if _, err := legacyScope(ctx); err != nil {
		return core.ResourceJob{}, err
	}
	scope, _ := core.ProviderConnectionScopeFromContext(ctx)
	if _, err := uuid.Parse(binding.BindingID); err != nil || resourcePath(binding.Kind, false) == "" || !validTaskID(binding.RemoteID) || binding.ConnectionIdentity != scope.Revision || binding.IsPreset {
		return core.ResourceJob{}, resourceInvalid("deletion requires an authorized custom-resource binding on this concrete provider connection revision")
	}
	path := "/v1/general/delete-advanced-elements"
	var payload any = struct {
		ID string `json:"element_id"`
	}{ID: binding.RemoteID}
	if binding.Kind == "voice" {
		path = "/v1/general/delete-voices"
		payload = struct {
			ID string `json:"voice_id"`
		}{ID: binding.RemoteID}
	}
	raw, err := a.do(ctx, http.MethodPost, path, payload)
	if err != nil {
		return core.ResourceJob{}, err
	}
	var task resourceNativeTask
	if json.Unmarshal(raw, &task) != nil || task.ID == "" {
		return core.ResourceJob{}, errors.New("malformed Kling resource deletion response; check provider history before resubmitting")
	}
	status, err := resourceStatus(task.Status)
	if err != nil {
		return core.ResourceJob{}, err
	}
	handle, err := a.signLegacy(ctx, resourceOperation(binding.Kind, "delete:"+task.Status), string(task.ID))
	if err != nil {
		return core.ResourceJob{}, err
	}
	job := core.ResourceJob{Handle: handle, Status: status}
	if status == core.ProviderQueued || status == core.ProviderRunning {
		job.Warning = resourceDeletionWarning
	}
	if status == core.ProviderFailed {
		job.Error = "Kling resource deletion failed; check provider task details"
	}
	return job, nil
}

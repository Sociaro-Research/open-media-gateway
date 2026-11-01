package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func resourceFixtureContext() context.Context {
	return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "fixture-owner"), core.ProviderConnectionScope{ID: "kling--fixture", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
}
func resourceFixtureSource(i int, kind string) core.ResourceSourceInput {
	source := core.ResourceSourceInput{ReferenceID: uuid.NewString(), URL: fmt.Sprintf("https://cdn.example/source%d.png", i), MediaType: kind, SHA256: strings.Repeat(fmt.Sprintf("%x", i), 64), ContentType: "image/png", SizeBytes: 4096, Width: 600, Height: 600}
	switch kind {
	case "video":
		source.URL = fmt.Sprintf("https://cdn.example/source%d.mp4", i)
		source.ContentType = "video/mp4"
		source.Width = 1920
		source.Height = 1080
		source.Duration = ptr(5.0)
	case "audio":
		source.URL = fmt.Sprintf("https://cdn.example/source%d.mp3", i)
		source.ContentType = "audio/mpeg"
		source.Width = 0
		source.Height = 0
		source.Duration = ptr(10.0)
	}
	return source
}
func resourceFixtureRequest(kind, subtype string) core.ResourceCreateRequest {
	r := core.ResourceCreateRequest{Kind: kind, Subtype: subtype, Name: "Synthetic resource", ExternalTaskID: uuid.NewString()}
	if kind == "element" {
		r.Description = "Synthetic inspected resource"
		if subtype == "multi_image" {
			r.Images = []core.ResourceSourceInput{resourceFixtureSource(1, "image"), resourceFixtureSource(2, "image")}
		} else {
			r.Videos = []core.ResourceSourceInput{resourceFixtureSource(1, "video")}
		}
	} else {
		r.VoiceSources = []core.ResourceSourceInput{resourceFixtureSource(1, "audio")}
	}
	return r
}
func resourceFixtureBinding(kind string) core.ResourceSnapshot {
	return core.ResourceSnapshot{BindingID: uuid.NewString(), ReferenceID: "managed_resource", Kind: kind, Subtype: "custom", RemoteID: "9007199254740993", ConnectionIdentity: "fixture-revision"}
}

const resourceElementResult = `{"element_id":9007199254740993,"element_name":"Synthetic element","element_description":"Synthetic description","reference_type":"video_refer","status":"succeed","owned_by":9007199254740994,"element_video_list":{"refer_videos":[{"video_url":"https://cdn.example/source.mp4"}]},"element_voice_info":{"voice_id":9007199254740995,"voice_name":"Bound voice","trial_url":"https://cdn.example/trial.mp3","owned_by":"fixture_creator"},"tag_list":[{"tag_id":"o_102"}]}`
const resourceVoiceResult = `{"voice_id":9007199254740993,"voice_name":"Synthetic voice","trial_url":"https://cdn.example/trial.mp3","owned_by":9007199254740994}`

func TestResourceCreationTypedNativePayloadsAndSourceLimits(t *testing.T) {
	for _, tc := range []struct{ kind, subtype string }{{"element", "multi_image"}, {"element", "video_character"}, {"voice", "custom"}} {
		t.Run(tc.kind+"/"+tc.subtype, func(t *testing.T) {
			r := resourceFixtureRequest(tc.kind, tc.subtype)
			if tc.subtype == "multi_image" {
				voice := resourceFixtureBinding("voice")
				r.BoundVoice = &voice
				r.Tags = []string{"o_102", "o_108"}
			}
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.URL.Path != resourcePath(tc.kind, false) {
					t.Fatal(req.Method, req.URL)
				}
				raw, _ := io.ReadAll(req.Body)
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				if body["external_task_id"] != r.ExternalTaskID {
					t.Fatal(body)
				}
				if body["callback_url"] != nil || body["sources"] != nil || body["duration"] != nil || body["parameters"] != nil || body["remote_id"] != nil {
					t.Fatal(body)
				}
				switch tc.subtype {
				case "multi_image":
					images := body["element_image_list"].(map[string]any)
					refs := images["refer_images"].([]any)
					if body["reference_type"] != "image_refer" || images["frontal_image"] != r.Images[0].URL || len(refs) != 1 || refs[0].(map[string]any)["image_url"] != r.Images[1].URL || body["element_voice_id"] != "9007199254740993" {
						t.Fatal(body)
					}
				case "video_character":
					if body["reference_type"] != "video_refer" || body["element_image_list"] != nil || body["element_video_list"] == nil {
						t.Fatal(body)
					}
				case "custom":
					if body["voice_name"] != r.Name || body["voice_url"] != r.VoiceSources[0].URL || body["video_id"] != nil {
						t.Fatal(body)
					}
				}
				return response(200, `{"code":0,"data":{"task_id":9007199254740993,"task_status":"submitted"}}`), nil
			}))
			job, err := a.CreateResource(resourceFixtureContext(), r)
			if err != nil || job.Status != core.ProviderQueued || job.Handle == "" || len(job.Resources) != 0 || !strings.Contains(job.Warning, "tariff") {
				t.Fatalf("%+v %v", job, err)
			}
			handle, err := a.parseLegacy(resourceFixtureContext(), job.Handle)
			if err != nil || handle.Operation != resourceOperation(tc.kind, "create") || handle.ID != "9007199254740993" {
				t.Fatal(handle, err)
			}
		})
	}
}
func TestResourceGeneratedVoiceEligibilityAndOpaqueID(t *testing.T) {
	for _, source := range []core.GeneratedResourceSource{{RemoteID: "9007199254740993", Operation: TextToVideo26, NativeAudio: true, Duration: ptr(5.0)}, {RemoteID: "9007199254740993", Operation: ImageToVideo26, NativeAudio: true, Duration: ptr(30.0)}, {RemoteID: "9007199254740993", Operation: AvatarImageToVideo, Duration: ptr(12.5)}, {RemoteID: "9007199254740993", Operation: "kling-lip-sync", Duration: ptr(10.0)}} {
		r := resourceFixtureRequest("voice", "custom")
		r.VoiceSources = nil
		r.GeneratedVideo = &source
		if err := validateResourceCreate(resourceFixtureContext(), r); err != nil {
			t.Fatal(source, err)
		}
		payload := resourcePayload(r).(resourceVoicePayload)
		if payload.VideoID != source.RemoteID || payload.URL != "" {
			t.Fatal(payload)
		}
	}
}
func TestResourceCreationRejectsUninspectedAndUnsupportedInputs(t *testing.T) {
	cases := []struct {
		name, kind, subtype string
		change              func(*core.ResourceCreateRequest)
	}{
		{"invalid correlation UUID", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.ExternalTaskID = "arbitrary-remote-id" }},
		{"name length", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Name = strings.Repeat("界", 21) }},
		{"description length", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Description = strings.Repeat("界", 101) }},
		{"single image insufficient", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images = r.Images[:1] }},
		{"five images", "element", "multi_image", func(r *core.ResourceCreateRequest) {
			r.Images = append(r.Images, resourceFixtureSource(3, "image"), resourceFixtureSource(4, "image"), resourceFixtureSource(5, "image"))
		}},
		{"duplicate images", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[1].SHA256 = r.Images[0].SHA256 }},
		{"no source hash", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[0].SHA256 = "" }},
		{"no source identity", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[0].ReferenceID = "" }},
		{"image size", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[0].SizeBytes = (10 << 20) + 1 }},
		{"image format", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[0].ContentType = "image/webp" }},
		{"image dimensions", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[0].Width = 299 }},
		{"image ratio", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[0].Width = 1800 }},
		{"private URL", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Images[0].URL = "https://127.0.0.1/a.png" }},
		{"unknown tag", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Tags = []string{"o_999"} }},
		{"duplicate tag", "element", "multi_image", func(r *core.ResourceCreateRequest) { r.Tags = []string{"o_102", "o_102"} }},
		{"unbound voice", "element", "multi_image", func(r *core.ResourceCreateRequest) {
			r.BoundVoice = &core.ResourceSnapshot{Kind: "voice", RemoteID: "arbitrary"}
		}},
		{"foreign voice revision", "element", "multi_image", func(r *core.ResourceCreateRequest) {
			v := resourceFixtureBinding("voice")
			v.ConnectionIdentity = "other-revision"
			r.BoundVoice = &v
		}},
		{"video too long", "element", "video_character", func(r *core.ResourceCreateRequest) { r.Videos[0].Duration = ptr(8.1) }},
		{"video duration unknown", "element", "video_character", func(r *core.ResourceCreateRequest) { r.Videos[0].Duration = nil }},
		{"video size", "element", "video_character", func(r *core.ResourceCreateRequest) { r.Videos[0].SizeBytes = (200 << 20) + 1 }},
		{"video resolution", "element", "video_character", func(r *core.ResourceCreateRequest) { r.Videos[0].Width = 1280; r.Videos[0].Height = 720 }},
		{"voice no sources", "voice", "custom", func(r *core.ResourceCreateRequest) { r.VoiceSources = nil }},
		{"voice both sources", "voice", "custom", func(r *core.ResourceCreateRequest) {
			r.GeneratedVideo = &core.GeneratedResourceSource{RemoteID: "fixture", Operation: AvatarImageToVideo, Duration: ptr(10.0)}
		}},
		{"voice duration unknown", "voice", "custom", func(r *core.ResourceCreateRequest) { r.VoiceSources[0].Duration = nil }},
		{"voice duration low", "voice", "custom", func(r *core.ResourceCreateRequest) { r.VoiceSources[0].Duration = ptr(4.9) }},
		{"voice duration NaN", "voice", "custom", func(r *core.ResourceCreateRequest) { r.VoiceSources[0].Duration = ptr(math.NaN()) }},
		{"voice m4a unsupported", "voice", "custom", func(r *core.ResourceCreateRequest) { r.VoiceSources[0].ContentType = "audio/mp4" }},
		{"unverified generated origin", "voice", "custom", func(r *core.ResourceCreateRequest) {
			r.VoiceSources = nil
			r.GeneratedVideo = &core.GeneratedResourceSource{RemoteID: "fixture", Operation: TextToVideo4, NativeAudio: true, Duration: ptr(10.0)}
		}},
		{"v26 audio off", "voice", "custom", func(r *core.ResourceCreateRequest) {
			r.VoiceSources = nil
			r.GeneratedVideo = &core.GeneratedResourceSource{RemoteID: "fixture", Operation: TextToVideo26, Duration: ptr(10.0)}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := resourceFixtureRequest(tc.kind, tc.subtype)
			tc.change(&r)
			calls := 0
			a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") }))
			if _, err := a.CreateResource(resourceFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
				t.Fatal(err, calls)
			}
		})
	}
}
func TestResourceJobsPollLosslessResultsAndUnknownDuration(t *testing.T) {
	for _, kind := range []string{"element", "voice"} {
		a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != resourcePath(kind, false)+"/9007199254740993" {
				t.Fatal(req.URL)
			}
			field, result := "elements", resourceElementResult
			if kind == "voice" {
				field, result = "voices", resourceVoiceResult
			}
			return response(200, `{"code":0,"data":{"task_id":9007199254740993,"task_status":"succeed","task_result":{"`+field+`":[`+result+`]},"final_unit_deduction":"8.0000"}}`), nil
		}))
		handle, err := a.signLegacy(resourceFixtureContext(), resourceOperation(kind, "create"), "9007199254740993")
		if err != nil {
			t.Fatal(err)
		}
		job, err := a.GetResourceJob(resourceFixtureContext(), kind, handle)
		if err != nil || job.Status != core.ProviderSucceeded || len(job.Resources) != 1 || job.Resources[0].RemoteID != "9007199254740993" || job.Resources[0].IsPreset || !strings.Contains(string(job.Usage.Raw), "8.0000") {
			t.Fatalf("%+v %v", job, err)
		}
		if kind == "element" {
			resource := job.Resources[0]
			if resource.Subtype != "video_character" || len(resource.Sources) != 1 || resource.Sources[0].Duration != nil || resource.Sources[0].SHA256 != "" || resource.BoundVoice == nil || resource.BoundVoice.RemoteID != "9007199254740995" || !reflect.DeepEqual(resource.Tags, []string{"o_102"}) {
				t.Fatal(resource)
			}
		}
	}
}
func TestResourceListExactRoutesPaginationAndDeletedElements(t *testing.T) {
	for _, kind := range []string{"element", "voice"} {
		for _, preset := range []bool{false, true} {
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Path != resourcePath(kind, preset) || req.URL.Query().Get("pageNum") != "2" || req.URL.Query().Get("pageSize") != "1" {
					t.Fatal(req.URL)
				}
				field, result := "elements", resourceElementResult
				if kind == "voice" {
					field, result = "voices", resourceVoiceResult
				}
				if preset {
					result = strings.ReplaceAll(result, `"owned_by":9007199254740994`, `"owned_by":"kling"`)
				}
				if kind == "element" && !preset {
					result = strings.Replace(result, `"status":"succeed"`, `"status":"deleted"`, 1)
				}
				return response(200, `{"code":0,"data":[{"task_id":"fixture-task","task_status":"succeed","task_result":{"`+field+`":[`+result+`]}}]}`), nil
			}))
			page, err := a.ListResources(resourceFixtureContext(), kind, preset, 2, 1)
			if err != nil || page.Page != 2 || page.PageSize != 1 || !page.HasMore || len(page.Resources) != 1 || page.Resources[0].IsPreset != preset {
				t.Fatalf("%+v %v", page, err)
			}
			if kind == "element" && !preset && !page.Resources[0].IsDeleted {
				t.Fatal("deleted element could be admitted as live")
			}
		}
	}
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") }))
	for _, tc := range []struct {
		kind       string
		page, size int
	}{{"element", 1, 501}, {"voice", 1, 1001}, {"voice", 1001, 30}, {"unknown", 1, 30}, {"voice", -1, 30}} {
		if _, err := a.ListResources(resourceFixtureContext(), tc.kind, false, tc.page, tc.size); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal(tc, err)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}
func TestResourceAsyncDeletionDoesNotInventPollOrDeletePresets(t *testing.T) {
	for _, kind := range []string{"element", "voice"} {
		for _, native := range []string{"submitted", "processing", "succeed", "failed"} {
			calls := 0
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				calls++
				path, key := "/v1/general/delete-advanced-elements", "element_id"
				if kind == "voice" {
					path, key = "/v1/general/delete-voices", "voice_id"
				}
				if req.Method != "POST" || req.URL.Path != path {
					t.Fatal(req.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body[key] != "9007199254740993" || len(body) != 1 {
					t.Fatal(body)
				}
				return response(200, `{"code":0,"data":{"task_id":"delete-task","task_status":"`+native+`"}}`), nil
			}))
			job, err := a.DeleteResource(resourceFixtureContext(), resourceFixtureBinding(kind))
			expected, _ := resourceStatus(native)
			if err != nil || job.Status != expected || calls != 1 {
				t.Fatalf("%+v %v", job, err)
			}
			polled, err := a.GetResourceJob(resourceFixtureContext(), kind, job.Handle)
			if err != nil || polled.Status != job.Status || calls != 1 {
				t.Fatalf("invented delete poll: %+v %v calls=%d", polled, err, calls)
			}
			if native == "submitted" || native == "processing" {
				if !strings.Contains(polled.Warning, "does not specify") {
					t.Fatal(polled)
				}
			}
		}
	}
	for _, change := range []func(*core.ResourceSnapshot){func(s *core.ResourceSnapshot) { s.IsPreset = true }, func(s *core.ResourceSnapshot) { s.ConnectionIdentity = "foreign-revision" }, func(s *core.ResourceSnapshot) { s.BindingID = "raw-id" }, func(s *core.ResourceSnapshot) { s.RemoteID = "../escape" }} {
		binding := resourceFixtureBinding("voice")
		change(&binding)
		calls := 0
		a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") }))
		if _, err := a.DeleteResource(resourceFixtureContext(), binding); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
			t.Fatal(err, calls)
		}
	}
}
func TestResourceHandlesRejectForeignScopeKindAndMalformedResponses(t *testing.T) {
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) {
		calls++
		return response(200, `{"code":0,"data":{"task_id":"other","task_status":"succeed"}}`), nil
	}))
	handle, err := a.signLegacy(resourceFixtureContext(), resourceOperation("voice", "create"), "fixture-task")
	if err != nil {
		t.Fatal(err)
	}
	foreign := core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "foreign-owner"), core.ProviderConnectionScope{ID: "kling--fixture", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
	if _, err := a.GetResourceJob(foreign, "voice", handle); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := a.GetResourceJob(resourceFixtureContext(), "element", handle); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	if _, err := a.GetResourceJob(resourceFixtureContext(), "voice", handle); err == nil || calls != 1 {
		t.Fatal(err, calls)
	}
	for _, status := range []string{"submitted", "processing", "failed"} {
		a = adapter(t, transport(func(*http.Request) (*http.Response, error) {
			return response(200, `{"code":0,"data":{"task_id":"fixture-task","task_status":"`+status+`","task_status_msg":"private_remote_detail"}}`), nil
		}))
		job, err := a.GetResourceJob(resourceFixtureContext(), "voice", handle)
		expected, _ := resourceStatus(status)
		if err != nil || job.Status != expected || strings.Contains(job.Error, "private_remote_detail") {
			t.Fatal(job, err)
		}
	}
}
func TestResourceSubmissionImmediateSuccessWithoutIdentityRemainsPollable(t *testing.T) {
	calls := 0
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method == "POST" {
			return response(200, `{"code":0,"data":{"task_id":"ready-task","task_status":"succeed"}}`), nil
		}
		return response(200, `{"code":0,"data":{"task_id":"ready-task","task_status":"succeed","task_result":{"voices":[`+resourceVoiceResult+`]}}}`), nil
	}))
	job, err := a.CreateResource(resourceFixtureContext(), resourceFixtureRequest("voice", "custom"))
	if err != nil || job.Handle == "" || job.Status != core.ProviderRunning || calls != 1 {
		t.Fatal(job, err, calls)
	}
	done, err := a.GetResourceJob(resourceFixtureContext(), "voice", job.Handle)
	if err != nil || done.Status != core.ProviderSucceeded || len(done.Resources) != 1 || calls != 2 {
		t.Fatal(done, err, calls)
	}
}

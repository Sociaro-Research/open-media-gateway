package alibaba

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/modelcatalog"
	"time"
)

func TestAuditedImageEndpoints(t *testing.T) {
	for _, tc := range []struct {
		model, path string
		async       bool
	}{
		{"qwen-image-2.0-pro", ttsPath, false}, {"qwen-image-2.0", ttsPath, false},
		{"qwen-image-max", ttsPath, false}, {"qwen-image-plus-2026-01-09", ttsPath, false},
		{"qwen-image-edit", ttsPath, false}, {"qwen-image-edit-plus", ttsPath, false}, {"qwen-image-edit-max", ttsPath, false},
		{"qwen-image-3.0", imagePath, true}, {"qwen-image-plus", ttsPath, false}, {"qwen-image", ttsPath, false},
		{"z-image-turbo", ttsPath, false}, {"wan2.7-image-pro", imagePath, true}, {"wan2.6-image", imagePath, true},
		{"wan2.6-t2i", imagePath, true}, {"wan2.5-t2i-preview", legacyTextImagePath, true}, {"wan2.2-t2i-flash", legacyTextImagePath, true},
		{"wan2.5-i2i-preview", imageEditPath, true}, {"wanx2.1-imageedit", imageEditPath, true},
	} {
		t.Run(tc.model, func(t *testing.T) {
			adapter := &Adapter{apiKey: "test", baseURL: "https://dash.test", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tc.path || (r.Header.Get("X-DashScope-Async") == "enable") != tc.async {
					t.Fatalf("incorrect protocol: %s async=%s", r.URL.Path, r.Header.Get("X-DashScope-Async"))
				}
				var body struct {
					Input map[string]json.RawMessage `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if tc.path == imagePath || tc.path == ttsPath {
					if len(body.Input["messages"]) == 0 || len(body.Input["prompt"]) != 0 {
						t.Fatal("multimodal image protocol requires messages, not a plain prompt")
					}
				} else if tc.path == legacyTextImagePath && string(body.Input["prompt"]) != `"test"` {
					t.Fatal("legacy text2image protocol requires a plain prompt")
				}
				if tc.async {
					return dashJSONResponse(200, `{"output":{"task_id":"queued"}}`), nil
				}
				return dashJSONResponse(200, `{"request_id":"done","output":{"choices":[{"message":{"content":[{"image":"https://cdn.example/result.png"}]}}]},"usage":{"image_count":1}}`), nil
			})}}
			if modelcatalog.IsInactive("alibaba", tc.model, time.Now()) {
				if _, err := adapter.Generate(context.Background(), core.MediaRequest{Model: tc.model, Prompt: "test"}); err == nil {
					t.Fatal("retired model accepted a new generation")
				}
				return
			}
			task, err := adapter.Generate(context.Background(), core.MediaRequest{Model: tc.model, Prompt: "test"})
			if err != nil {
				t.Fatal(err)
			}
			if tc.async && task.ID != "queued" || !tc.async && (task.ResultURL == "" || task.Usage == nil) {
				t.Fatal("response protocol mismatch")
			}
		})
	}
}

func TestAuditedVideoWireParameters(t *testing.T) {
	for _, tc := range []struct {
		model, path, key, value string
		count                   int
		video                   bool
	}{
		{"wan2.6-t2v", videoPath, "size", "1280*720", 0, false},
		{"wan2.6-i2v", videoPath, "resolution", "720P", 1, false},
		{"wan2.6-r2v", videoPath, "size", "1280*720", 1, false},
		{"wan2.2-kf2v-flash", "/services/aigc/image2video/video-synthesis", "resolution", "720P", 2, false},
		{"wan2.7-i2v", videoPath, "resolution", "720P", 0, true},
		{"wan2.7-videoedit", videoPath, "resolution", "720P", 1, true},
		{"happyhorse-1.1-i2v", videoPath, "resolution", "720P", 1, false},
		{"happyhorse-1.0-video-edit", videoPath, "resolution", "720P", 1, true},
	} {
		t.Run(tc.model, func(t *testing.T) {
			o := core.MediaOptions{Resolution: "720p", Ratio: "16:9", Duration: 5}
			for i := 0; i < tc.count; i++ {
				o.ImageURLs = append(o.ImageURLs, "https://example.com/image.png")
			}
			if tc.video {
				o.VideoURL = "https://example.com/video.mp4"
			}
			adapter := &Adapter{baseURL: "https://dash.test", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != tc.path || r.Header.Get("X-DashScope-Async") != "enable" {
					t.Fatalf("wrong video endpoint: %s", r.URL.Path)
				}
				var p struct {
					Input      map[string]any `json:"input"`
					Parameters map[string]any `json:"parameters"`
				}
				if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if p.Parameters[tc.key] != tc.value {
					t.Fatalf("wrong dimensions: %#v", p.Parameters)
				}
				if tc.key == "resolution" {
					if _, ok := p.Parameters["size"]; ok {
						t.Fatal("size is invalid for frame-based generation")
					}
					if _, ok := p.Parameters["ratio"]; ok {
						t.Fatal("aspect ratio must follow input")
					}
				}
				if strings.Contains(tc.model, "videoedit") || strings.Contains(tc.model, "video-edit") {
					if p.Parameters["duration"] != nil {
						t.Fatal("duration must follow input")
					}
					media := p.Input["media"].([]any)
					if media[0].(map[string]any)["type"] != "video" {
						t.Fatal("editing requires video role")
					}
				}
				return dashJSONResponse(200, `{"output":{"task_id":"ok"}}`), nil
			})}}
			if _, err := adapter.Generate(context.Background(), core.MediaRequest{Model: tc.model, Prompt: "test", Options: o}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAnimationNativeRequestAndNestedResult(t *testing.T) {
	for _, model := range []string{"wan2.2-animate-move", "wan2.2-animate-mix"} {
		t.Run(model, func(t *testing.T) {
			a := &Adapter{baseURL: "https://dash.test", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return dashJSONResponse(200, `{"output":{"task_status":"SUCCEEDED","results":{"video_url":"https://cdn.example/animation.mp4"}},"usage":{"video_duration":5.2}}`), nil
				}
				if r.URL.Path != "/services/aigc/image2video/video-synthesis" {
					t.Fatal("wrong animation endpoint")
				}
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				params := body["parameters"].(map[string]any)
				if len(params) != 1 || params["mode"] != "wan-std" {
					t.Fatalf("unsupported animation controls: %#v", params)
				}
				return dashJSONResponse(200, `{"output":{"task_id":"animation"}}`), nil
			})}}
			_, err := a.Generate(context.Background(), core.MediaRequest{Model: model, Options: core.MediaOptions{ImageURLs: []string{"https://example.com/a.png"}, VideoURL: "https://example.com/v.mp4"}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.GetTask(context.Background(), "animation")
			if err != nil || result.ResultURL != "https://cdn.example/animation.mp4" || result.Usage == nil {
				t.Fatalf("nested result not decoded: %+v %v", result, err)
			}
		})
	}
}

func TestAuditedCatalogDoesNotClaimUnsupportedControls(t *testing.T) {
	for _, m := range mediaModels(false) {
		if m.MediaType == "video" && m.DefaultDuration < m.MinDuration {
			t.Fatalf("invalid default for %s", m.ID)
		}
		if strings.HasPrefix(m.ID, "wan2.6-i2v") && (m.MaxImageInputs != 1 || m.SupportsVideoInput) {
			t.Fatalf("legacy i2v silently discards inputs: %+v", m)
		}
		if strings.HasPrefix(m.ID, "happyhorse-") && strings.Contains(m.ID, "-r2v") && (m.MaxImageInputs != 9 || !m.RequiresImageInput || m.SupportsVideoInput || m.SupportsAudioInput) {
			t.Fatalf("incorrect reference contract: %+v", m)
		}
		if m.ID == "qwen-image-max" && m.Resolutions[0] == "1024*1024" {
			t.Fatal("invalid Qwen default resolution")
		}
		if m.ID == "wan2.5-i2v-preview" && m.DurationStep != 5 {
			t.Fatal("invalid duration increments")
		}
	}
	if _, ok := describeRemoteModel("qwen-vl-image-understanding", "", []string{"IG"}, []string{"Image"}, []string{"Image"}); ok {
		t.Fatal("unknown model admitted")
	}
}

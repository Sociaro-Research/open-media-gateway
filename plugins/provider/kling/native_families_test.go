package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Synthetic contracts follow the current direct API exports, not creator specifications.
func TestAdditionalNativeVideoFamilies(t *testing.T) {
	for _, version := range []string{"kling-3.0-turbo", "kling-2.5-turbo"} {
		for _, operation := range []string{"text-to-video", "image-to-video"} {
			t.Run(version+"/"+operation, func(t *testing.T) {
				req := core.MediaRequest{Model: version + "-" + operation, Prompt: "Synthetic fixture", Options: core.MediaOptions{Watermark: ptr(true)}}
				if operation == "image-to-video" {
					req.Options.ImageURLs = []string{"https://cdn.example/first.png"}
				}
				a := adapter(t, func(r *http.Request) (*http.Response, error) {
					if r.Method != "POST" || r.URL.Path != "/"+operation+"/"+version {
						t.Fatal(r.URL)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					settings := body["settings"].(map[string]any)
					if settings["duration"] != float64(5) || settings["resolution"] != "720p" || settings["audio"] != nil || settings["multi_shot"] != nil || body["model_name"] != nil {
						t.Fatal(body)
					}
					if body["options"].(map[string]any)["watermark_info"].(map[string]any)["enabled"] != true {
						t.Fatal(body)
					}
					if operation == "text-to-video" {
						if body["prompt"] != req.Prompt || settings["aspect_ratio"] != "16:9" {
							t.Fatal(body)
						}
					} else {
						contents := body["contents"].([]any)
						if len(contents) != 2 || contents[0].(map[string]any)["text"] != req.Prompt || contents[1].(map[string]any)["type"] != "first_frame" || contents[1].(map[string]any)["url"] != req.Options.ImageURLs[0] || settings["aspect_ratio"] != nil {
							t.Fatal(body)
						}
					}
					return response(200, `{"code":0,"data":{"id":"additional_native_task","status":"submitted"}}`), nil
				})
				if task, err := a.Generate(context.Background(), req); err != nil || task.ID != "additional_native_task" {
					t.Fatal(task, err)
				}
				cost, warning, err := a.Quote(context.Background(), core.MediaModel{}, req)
				expected := int64(560000)
				if version == "kling-2.5-turbo" {
					expected = 210000
				}
				if err != nil || warning != "" || int64(cost.ExpectedUSD) != expected || cost.Pricing.AccountSpecific {
					t.Fatal(cost, warning, err)
				}
				for name, mutate := range map[string]func(*core.MediaRequest){
					"audio switch":         func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(false) },
					"video input":          func(r *core.MediaRequest) { r.Options.VideoURLs = []string{"https://cdn.example/input.mp4"} },
					"4k":                   func(r *core.MediaRequest) { r.Options.Resolution = "4k" },
					"unsupported duration": func(r *core.MediaRequest) { r.Options.Duration = 2 },
					"multi shot switch":    func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"multi_shot": true} },
				} {
					t.Run(name, func(t *testing.T) {
						bad := req
						mutate(&bad)
						if err := a.ValidateRequest(context.Background(), bad); !errors.Is(err, core.ErrOptionsInvalid) {
							t.Fatal(err)
						}
					})
				}
				if operation == "image-to-video" {
					req.Options.ImageURLs = append(req.Options.ImageURLs, "https://cdn.example/last.png")
					err := a.ValidateRequest(context.Background(), req)
					if version == "kling-3.0-turbo" && !errors.Is(err, core.ErrOptionsInvalid) {
						t.Fatal("Turbo only supports the first frame", err)
					}
					if version == "kling-2.5-turbo" {
						if err == nil {
							t.Fatal("last-frame workflow requires 1080p")
						}
						req.Options.Resolution = "1080p"
						if err := a.ValidateRequest(context.Background(), req); err != nil {
							t.Fatal(err)
						}
					}
				}
			})
		}
	}
}

func TestAdditionalNativeDurationAndPromptBounds(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("validation must be local"); return nil, nil })
	for _, d := range []int{3, 4, 5, 6, 10, 15} {
		r := core.MediaRequest{Model: "kling-2.5-turbo-text-to-video", Prompt: "fixture", Options: core.MediaOptions{Duration: d}}
		err := a.ValidateRequest(context.Background(), r)
		if (d == 5 || d == 10) != (err == nil) {
			t.Fatal(fmt.Sprint(d), err)
		}
		r.Model = "kling-3.0-turbo-text-to-video"
		if err := a.ValidateRequest(context.Background(), r); err != nil {
			t.Fatal(d, err)
		}
	}
}

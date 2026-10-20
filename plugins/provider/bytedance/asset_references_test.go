package bytedance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func TestRegisteredAssetsReachBytePlusUnchanged(t *testing.T) {
	for _, id := range []string{"dreamina-seedance-2-0-mini-260615", "dreamina-seedance-2-0-fast-260128", "dreamina-seedance-2-0-260128", "dreamina-seedance-2-5-260628"} {
		for _, mode := range []string{"references", "first_frame", "first_last_frames"} {
			t.Run(id+"/"+mode, func(t *testing.T) {
				options := core.MediaOptions{ReferenceMode: mode, ImageURLs: []string{"asset://asset-first"}}
				want := []map[string]any{{"type": "text", "text": "Use @Image1 and @Image2"}}
				role := "reference_image"
				if mode != "references" {
					role = "first_frame"
				}
				want = append(want, map[string]any{"type": "image_url", "image_url": map[string]any{"url": options.ImageURLs[0]}, "role": role})
				if mode != "first_frame" {
					options.ImageURLs = append(options.ImageURLs, "https://example.test/scene.png")
					role = "reference_image"
					if mode == "first_last_frames" {
						role = "last_frame"
					}
					want = append(want, map[string]any{"type": "image_url", "image_url": map[string]any{"url": options.ImageURLs[1]}, "role": role})
				}
				if mode == "references" {
					options.VideoURLs = []string{"https://example.test/clip.mp4", "asset://asset-video"}
					options.AudioURLs = []string{"asset://asset-audio", "https://example.test/voice.mp3"}
					for _, kind := range []string{"video", "audio"} {
						refs := options.VideoURLs
						if kind == "audio" {
							refs = options.AudioURLs
						}
						for _, ref := range refs {
							want = append(want, map[string]any{"type": kind + "_url", kind + "_url": map[string]any{"url": ref}, "role": "reference_" + kind})
						}
					}
				}
				calls := 0
				adapter := &Adapter{apiKey: "test-key", baseURL: "https://byte.test", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if r.URL.Path != tasksPath {
						t.Fatalf("unexpected fetch: %s", r.URL)
					}
					var body struct {
						Content []map[string]any `json:"content"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(body.Content, want) {
						t.Fatalf("content=%#v, want %#v", body.Content, want)
					}
					return jsonResponse(http.StatusOK, `{"id":"asset-test"}`), nil
				})}}
				if !describeModel(id).SupportsAssetReferences {
					t.Fatal("capability missing")
				}
				if _, err := adapter.Generate(context.Background(), core.MediaRequest{Model: id, Prompt: "Use @Image1 and @Image2", Options: options}); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("requests=%d", calls)
				}
			})
		}
	}
}

func TestRegisteredAssetsRejectUnsupportedModelsAndModes(t *testing.T) {
	adapter := &Adapter{}
	for _, tc := range []struct {
		id      string
		options core.MediaOptions
	}{
		{"seedance-1-5-pro-251215", core.MediaOptions{ImageURLs: []string{"asset://asset-character"}}},
		{"seedream-5-0-260128", core.MediaOptions{ImageURLs: []string{"asset://asset-character"}}},
		{"dreamina-seedance-2-5-260628", core.MediaOptions{ImageURLs: []string{"asset://asset-character/path"}}},
		{"dreamina-seedance-2-0-260128", core.MediaOptions{ReferenceMode: "references", AudioURLs: []string{"asset://asset-voice"}}},
		{"dreamina-seedance-2-5-260628", core.MediaOptions{ReferenceMode: "first_frame", ImageURLs: []string{"asset://asset-character"}, VideoURLs: []string{"asset://asset-video"}}},
	} {
		if _, err := adapter.Generate(context.Background(), core.MediaRequest{Model: tc.id, Options: tc.options}); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatalf("%s: %v", tc.id, err)
		}
	}
	if err := adapter.ValidateRequest(context.Background(), core.MediaRequest{Model: "dreamina-seedance-2-5-260628", Options: core.MediaOptions{ReferenceMode: "references", AudioURLs: []string{"asset://asset-voice"}}}); err != nil {
		t.Fatal(err)
	}
}

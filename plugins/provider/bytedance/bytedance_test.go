package bytedance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func TestCreateAndPollVideo(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing authorization header")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == tasksPath:
			var body struct {
				Model   string `json:"model"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Model != "seedance-model" || len(body.Content) != 1 || body.Content[0].Text != "sunrise" {
				t.Fatalf("unexpected request: %#v", body)
			}
			return jsonResponse(http.StatusOK, `{"id":"provider-1"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == tasksPath+"/provider-1":
			return jsonResponse(http.StatusOK, `{"status":"succeeded","content":{"video_url":"https://cdn.example/video.mp4","last_frame_url":"https://cdn.example/frame.jpg"},"usage":{"completion_tokens":648900,"total_tokens":648900}}`), nil
		default:
			return jsonResponse(http.StatusNotFound, `{}`), nil
		}
	})}

	adapter := &Adapter{apiKey: "test-key", baseURL: "http://byte.test", client: client}
	task, err := adapter.Generate(context.Background(), core.MediaRequest{Model: "seedance-model", Prompt: "sunrise"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != core.ProviderSucceeded || result.ResultURL != "https://cdn.example/video.mp4" || result.LastFrameURL != "https://cdn.example/frame.jpg" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Usage == nil || len(result.Usage.Metrics) != 2 {
		t.Fatalf("usage was not captured: %#v", result.Usage)
	}
}

func TestCreateVideoTranslatesLegacyOptionsIntoTextCommands(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Seed       *int64 `json:"seed"`
			Resolution string `json:"resolution"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		want := "sunrise --resolution 1080p --ratio 9:16 --duration 8 --camerafixed true --watermark false"
		if body.Content[0].Text != want || body.Seed == nil || *body.Seed != 42 || body.Resolution != "" {
			t.Fatalf("unexpected legacy payload: %#v", body)
		}
		return jsonResponse(http.StatusOK, `{"id":"provider-legacy"}`), nil
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "http://byte.test", client: client}
	fixed, watermark, seed := true, false, int64(42)
	_, err := adapter.Generate(context.Background(), core.MediaRequest{
		Model: "seedance-1-0-pro-fast-251015", Prompt: "sunrise",
		Options: core.MediaOptions{Resolution: "1080p", Ratio: "9:16", Duration: 8, CameraFixed: &fixed, Watermark: &watermark, Seed: &seed},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateVideoUsesStructuredOptionsFor2x(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			Resolution      string `json:"resolution"`
			Ratio           string `json:"ratio"`
			Duration        int    `json:"duration"`
			GenerateAudio   *bool  `json:"generate_audio"`
			Watermark       *bool  `json:"watermark"`
			ReturnLastFrame *bool  `json:"return_last_frame"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Content[0].Text != "sunrise" || body.Resolution != "720p" || body.Ratio != "16:9" || body.Duration != 6 ||
			body.GenerateAudio == nil || !*body.GenerateAudio || body.Watermark == nil || *body.Watermark ||
			body.ReturnLastFrame == nil || !*body.ReturnLastFrame {
			t.Fatalf("unexpected 2.x payload: %#v", body)
		}
		return jsonResponse(http.StatusOK, `{"id":"provider-2x"}`), nil
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "http://byte.test", client: client}
	audio, watermark, lastFrame := true, false, true
	_, err := adapter.Generate(context.Background(), core.MediaRequest{
		Model: "dreamina-seedance-2-0-260128", Prompt: "sunrise",
		Options: core.MediaOptions{Resolution: "720p", Ratio: "16:9", Duration: 6, GenerateAudio: &audio, Watermark: &watermark, ReturnLastFrame: &lastFrame},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestListModelsIncludesEveryMediaGenerationModel(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"data":[{"id":"seedance-1-0-pro-fast-251015"},{"id":"seedance-1-0-lite-i2v-250428"},{"id":"dreamina-seedance-2-0-mini-260615"},{"id":"seedream-5-0-260128"}]}`), nil
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "http://byte.test", client: client}
	models, err := adapter.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"dreamina-seedance-2-0-mini-260615", "seedance-1-0-lite-i2v-250428", "seedance-1-0-pro-fast-251015", "seedream-5-0-260128"}
	if len(models) != len(want) {
		t.Fatalf("got %#v, want %#v", models, want)
	}
	for index := range want {
		if models[index].ID != want[index] {
			t.Fatalf("got %#v, want %#v", models, want)
		}
	}
}

func TestImageToVideoPassesImageContent(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body struct {
			Content []struct {
				Type     string `json:"type"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Content) != 2 || body.Content[1].Type != "image_url" || body.Content[1].ImageURL.URL != "https://example.com/frame.png" {
			t.Fatalf("unexpected image-to-video content: %#v", body.Content)
		}
		return jsonResponse(http.StatusOK, `{"id":"provider-i2v"}`), nil
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "http://byte.test", client: client}
	_, err := adapter.Generate(context.Background(), core.MediaRequest{
		Model: "seedance-1-0-lite-i2v-250428", Prompt: "animate",
		Options: core.MediaOptions{ImageURLs: []string{"https://example.com/frame.png"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestSeedreamImageRequestFormats(t *testing.T) {
	for _, tc := range []struct {
		model, format, output string
		references            []string
	}{
		{"seedream-4-0-250828", "", "", nil},
		{"seedream-4-0-250828", "jpeg", "", []string{"https://example.com/one.jpg"}},
		{"seedream-4-5-251128", "", "", []string{"https://example.com/one.jpg", "https://example.com/two.jpg", "https://example.com/three.jpg"}},
		{"seedream-4-5-251128", "jpeg", "", nil},
		{"seedream-5-0-260128", "", "png", nil},
		{"seedream-5-0-lite-260128", "jpeg", "jpeg", nil},
		{"dola-seedream-5-0-pro-260628", "png", "png", nil},
		{"dola-seedream-5-0-flash-260915", "", "png", []string{"https://example.com/one.jpg"}},
		{"seedream-future-model", "", "", nil},
	} {
		t.Run(tc.model+"/"+tc.format, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPost || r.URL.Path != "/images/generations" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatalf("unexpected image request: %s %s", r.Method, r.URL.Path)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload["model"] != tc.model || payload["prompt"] != "Draw an avatar" || payload["size"] != "2K" || payload["response_format"] != "url" {
					t.Fatalf("unexpected payload: %#v", payload)
				}
				output, sent := payload["output_format"]
				if strings.Contains(tc.model, "5-0-pro") || strings.Contains(tc.model, "5-0-flash") {
					if _, sent := payload["sequential_image_generation"]; sent {
						t.Fatal("unsupported sequential generation sent")
					}
				}
				if tc.output == "" && sent {
					t.Fatalf("unsupported output_format sent: %#v", output)
				}
				if tc.output != "" && output != tc.output {
					t.Fatalf("wrong output_format: %#v", output)
				}
				switch len(tc.references) {
				case 0:
					if _, exists := payload["image"]; exists {
						t.Fatal("unexpected image input")
					}
				case 1:
					if payload["image"] != tc.references[0] {
						t.Fatal("single image must be a string")
					}
				default:
					want := make([]any, len(tc.references))
					for i, url := range tc.references {
						want[i] = url
					}
					if !reflect.DeepEqual(payload["image"], want) {
						t.Fatalf("reference order changed: %#v", payload["image"])
					}
				}
				return jsonResponse(http.StatusOK, `{"data":[{"url":"https://cdn.example/avatar.jpeg"}],"usage":{"generated_images":1}}`), nil
			})}
			adapter := &Adapter{apiKey: "test-key", baseURL: "http://byte.test", client: client}
			task, err := adapter.Generate(context.Background(), core.MediaRequest{Model: tc.model, Prompt: "Draw an avatar", Options: core.MediaOptions{Resolution: "2K", Format: tc.format, ImageURLs: tc.references}})
			if err != nil {
				t.Fatal(err)
			}
			if task.ResultURL != "https://cdn.example/avatar.jpeg" || task.Usage == nil {
				t.Fatalf("missing image result or usage: %#v", task)
			}
		})
	}
}

func TestSeedreamRejectsUnsupportedFormatBeforeProviderCall(t *testing.T) {
	adapter := &Adapter{client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported format reached provider")
		return nil, nil
	})}}
	for _, model := range []string{"seedream-4-0-250828", "seedream-4-5-251128"} {
		descriptor := describeModel(model)
		if !reflect.DeepEqual(descriptor.Formats, []string{"jpeg"}) {
			t.Fatalf("incorrect supported formats: %#v", descriptor.Formats)
		}
		_, err := adapter.Generate(context.Background(), core.MediaRequest{Model: model, Options: core.MediaOptions{Format: "png"}})
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("expected unsupported format error: %v", err)
		}
	}
	if !reflect.DeepEqual(describeModel("seedream-5-0-lite-260128").Formats, []string{"png", "jpeg"}) {
		t.Fatal("Seedream 5.0 format selection was lost")
	}
}

func TestVideoReferenceRolesAndStructuredCamera(t *testing.T) {
	for _, tc := range []struct {
		model string
		roles []string
	}{
		{"seedance-1-0-pro-250528", []string{"first_frame", "last_frame"}},
		{"seedance-1-5-pro-251215", []string{"first_frame", "last_frame"}},
		{"dreamina-seedance-2-0-260128", []string{"reference_image", "reference_image", "reference_video", "reference_audio"}},
	} {
		t.Run(tc.model, func(t *testing.T) {
			fixed := true
			o := core.MediaOptions{Resolution: "720p", Duration: 5, ImageURLs: []string{"https://example.com/a.jpg", "https://example.com/b.jpg"}}
			if tc.model == "seedance-1-5-pro-251215" {
				o.CameraFixed = &fixed
			}
			if len(tc.roles) == 4 {
				o.VideoURL = "https://example.com/v.mp4"
				o.AudioURL = "https://example.com/a.mp3"
			}
			a := &Adapter{baseURL: "https://byte.test", client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body struct {
					Content []struct {
						Role string `json:"role"`
						Text string `json:"text"`
					} `json:"content"`
					CameraFixed *bool  `json:"camera_fixed"`
					Resolution  string `json:"resolution"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if len(body.Content) != len(tc.roles)+1 {
					t.Fatal("lost input media")
				}
				for i, role := range tc.roles {
					if body.Content[i+1].Role != role {
						t.Fatalf("wrong role: %+v", body.Content)
					}
				}
				if tc.model == "seedance-1-5-pro-251215" && (body.CameraFixed == nil || !*body.CameraFixed || body.Resolution != "720p" || body.Content[0].Text != "test") {
					t.Fatal("1.5 camera/settings not passed as structured fields")
				}
				return jsonResponse(200, `{"id":"ok"}`), nil
			})}}
			if _, err := a.Generate(context.Background(), core.MediaRequest{Model: tc.model, Prompt: "test", Options: o}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVideoRejectsAudioOnlyBeforeCallingProvider(t *testing.T) {
	a := &Adapter{}
	_, err := a.Generate(context.Background(), core.MediaRequest{Model: "dreamina-seedance-2-0-260128", Options: core.MediaOptions{AudioURL: "https://example.com/a.mp3"}})
	if err == nil {
		t.Fatal("audio-only reference was accepted")
	}
}

func TestSeedreamModelSpecificLimits(t *testing.T) {
	for _, tc := range []struct {
		id          string
		resolutions []string
		images      int
	}{
		{"seedream-4-0-250828", []string{"1K", "2K", "4K"}, 14},
		{"seedream-4-0-20260415", []string{"1K", "2K", "4K"}, 14},
		{"seedream-4-5-251128", []string{"2K", "4K"}, 14},
		{"seedream-5-0-lite-260128", []string{"2K", "3K", "4K"}, 14},
		{"dola-seedream-5-0-pro-260628", []string{"1K", "1.5K", "2K"}, 10},
		{"dola-seedream-5-0-flash-260915", []string{"1K", "1.5K", "2K"}, 10},
	} {
		m := describeModel(tc.id)
		if m.MaxImageInputs != tc.images || !reflect.DeepEqual(m.Resolutions, tc.resolutions) {
			t.Fatalf("wrong contract: %+v", m)
		}
	}
	if describeModel("seedance-1-0-pro-fast-251015").MaxImageInputs != 1 {
		t.Fatal("Pro Fast only supports a first frame")
	}
	if knownModel("seedance-unknown-model") {
		t.Fatal("unknown protocol admitted")
	}
	for _, id := range []string{"seedream-4-0-20270101", "seedream-5-0-999999", "dola-seedream-5-0-pro-999999", "dola-seedream-5-0-flash-999999", "seedream-3-0-t2i-250415"} {
		if knownModel(id) {
			t.Fatal("unreviewed or deactivated model admitted", id)
		}
	}
}

func TestReviewedSeedreamAprilVersionDoesNotInheritAnotherVersionsPrice(t *testing.T) {
	const id = "seedream-4-0-20260415"
	if !knownModel(id) {
		t.Fatal("reviewed API version disappeared")
	}
	if _, err := quotePublic("https://ark.ap-southeast.bytepluses.com/api/v3", describeModel(id), core.MediaRequest{Model: id, Options: core.MediaOptions{Resolution: "2K"}}, time.Now()); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("unpublished exact-version price inherited an older tariff", err)
	}
}

func TestSynchronousImageHasIndependentTimeoutAndDoesNotRetry(t *testing.T) {
	calls := 0
	client := &http.Client{Timeout: 10 * time.Millisecond, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(40 * time.Millisecond):
			return jsonResponse(200, `{"data":[{"url":"https://cdn.example/result.jpeg"}]}`), nil
		}
	})}
	a, err := New("test", "https://provider.example", client, WithImageTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Generate(context.Background(), core.MediaRequest{Model: "seedream-4-5-251128"}); err != nil {
		t.Fatal(err)
	}
	if client.Timeout != 10*time.Millisecond {
		t.Fatal("shared API timeout was changed")
	}
	if _, err = a.ListModels(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("catalog should retain short timeout: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = a.Generate(ctx, core.MediaRequest{Model: "seedream-4-5-251128"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation ignored: %v", err)
	}
	a.imageTimeout = 5 * time.Millisecond
	before := calls
	if _, err = a.Generate(context.Background(), core.MediaRequest{Model: "seedream-4-5-251128"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("image timeout not enforced: %v", err)
	}
	if calls-before != 1 {
		t.Fatal("timed-out paid request was retried")
	}
}

func TestReferenceModesBecomeNativeRoles(t *testing.T) {
	for _, tc := range []struct {
		model, mode            string
		images, videos, audios []string
		roles                  []string
	}{
		{"seedance-1-5-pro-251215", "first_last_frames", []string{"https://test/first.png", "https://test/last.png"}, nil, nil, []string{"first_frame", "last_frame"}},
		{"seedance-1-0-lite-i2v-250428", "references", []string{"https://test/one.png", "https://test/two.png", "https://test/three.png"}, nil, nil, []string{"reference_image", "reference_image", "reference_image"}},
		{"dreamina-seedance-2-0-260128", "first_last_frames", []string{"https://test/first.png", "https://test/last.png"}, nil, nil, []string{"first_frame", "last_frame"}},
		{"dreamina-seedance-2-0-260128", "references", []string{"https://test/one.png"}, []string{"https://test/one.mp4", "https://test/two.mp4"}, []string{"https://test/one.mp3", "https://test/two.mp3"}, []string{"reference_image", "reference_video", "reference_video", "reference_audio", "reference_audio"}},
	} {
		t.Run(tc.model+tc.mode, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var payload struct {
					Content []struct {
						Role string `json:"role"`
					} `json:"content"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				roles := []string{}
				for _, item := range payload.Content {
					if item.Role != "" {
						roles = append(roles, item.Role)
					}
				}
				if !reflect.DeepEqual(roles, tc.roles) {
					t.Fatalf("roles=%v want=%v", roles, tc.roles)
				}
				return jsonResponse(200, `{"id":"task"}`), nil
			})}
			a := &Adapter{apiKey: "test", baseURL: "https://provider.test", client: client}
			_, err := a.Generate(context.Background(), core.MediaRequest{Model: tc.model, Prompt: "test", Options: core.MediaOptions{ReferenceMode: tc.mode, ImageURLs: tc.images, VideoURLs: tc.videos, AudioURLs: tc.audios}})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

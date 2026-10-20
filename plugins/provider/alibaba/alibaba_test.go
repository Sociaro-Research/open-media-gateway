package alibaba

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestListModelsUsesNativeCatalogAndFiltersNonMediaModels(t *testing.T) {
	client := &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/models" || r.URL.Query().Get("page_size") != "200" {
			t.Fatalf("unexpected catalog request: %s %s", r.Method, r.URL.String())
		}
		return dashJSONResponse(http.StatusOK, `{"output":{"models":[{"model":"qwen-image-3.0","name":"Qwen Image 3","capabilities":["IG"],"inference_metadata":{"request_modality":["Text","Image"],"response_modality":["Image"]}},{"model":"qwen-plus","name":"Qwen Plus","capabilities":["TG"],"inference_metadata":{"request_modality":["Text"],"response_modality":["Text"]}}]}}`), nil
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "https://dash.test", musicBaseURL: "https://dash.test", client: client}
	models, err := adapter.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "qwen-image-3.0" || models[0].MediaType != "image" || !models[0].SupportsImageInput {
		t.Fatalf("unexpected media catalog: %#v", models)
	}
}

func TestCreateAndPollImageTask(t *testing.T) {
	client := &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing authorization")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == imagePath:
			if r.Header.Get("X-DashScope-Async") != "enable" {
				t.Fatalf("missing asynchronous header")
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["model"] != "qwen-image-3.0" {
				t.Fatalf("unexpected payload: %#v", body)
			}
			return dashJSONResponse(http.StatusOK, `{"output":{"task_id":"image-task"},"request_id":"request-1"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/tasks/image-task":
			return dashJSONResponse(http.StatusOK, `{"output":{"task_status":"SUCCEEDED","choices":[{"message":{"content":[{"image":"https://cdn.example/result.png"}]}}]},"usage":{"image_count":1}}`), nil
		default:
			return dashJSONResponse(http.StatusNotFound, `{}`), nil
		}
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "https://dash.test", client: client}
	task, err := adapter.Generate(context.Background(), core.MediaRequest{Model: "qwen-image-3.0", Prompt: "a lighthouse", Options: core.MediaOptions{Resolution: "1024*1024"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != core.ProviderSucceeded || result.ResultURL != "https://cdn.example/result.png" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Usage == nil || len(result.Usage.Metrics) != 1 || result.Usage.Metrics[0].Unit != "image" {
		t.Fatalf("usage was not captured: %#v", result.Usage)
	}
}

func TestTextToSpeechReturnsImmediateAudio(t *testing.T) {
	client := &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != ttsPath || r.Header.Get("X-DashScope-Async") != "" {
			t.Fatalf("unexpected TTS request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Input struct {
				Text     string `json:"text"`
				Language string `json:"language_type"`
			} `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Input.Text != "Allô !" || payload.Input.Language != "French" {
			t.Fatalf("Unicode TTS text or language changed: %+v", payload.Input)
		}
		return dashJSONResponse(http.StatusOK, `{"request_id":"speech-request","output":{"audio":{"id":"speech-1","url":"https://cdn.example/speech.wav"}}}`), nil
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "https://dash.test", client: client}
	task, err := adapter.Generate(context.Background(), core.MediaRequest{Model: "qwen3-tts-flash", Prompt: "Allô !", Options: core.MediaOptions{Voice: "Cherry", Language: "French"}})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "speech-1" || task.ResultURL != "https://cdn.example/speech.wav" {
		t.Fatalf("unexpected immediate task: %#v", task)
	}
}

func TestLegacyImageFamiliesUseTheirNativeEndpointsAndPayloads(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		path       string
		imageURLs  []string
		inputField string
		inputValue any
	}{
		{name: "text to image", model: "wan2.5-t2i-preview", path: legacyTextImagePath, inputField: "prompt", inputValue: "a lighthouse"},
		{name: "image editing", model: "wan2.5-i2i-preview", path: imageEditPath, imageURLs: []string{"https://example.com/source.png"}, inputField: "images", inputValue: []any{"https://example.com/source.png"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet && r.URL.Path == "/models" {
					return dashJSONResponse(http.StatusServiceUnavailable, `{}`), nil
				}
				if r.Method != http.MethodPost || r.URL.Path != test.path {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				input := body["input"].(map[string]any)
				if got, ok := input[test.inputField]; !ok || fmt.Sprint(got) != fmt.Sprint(test.inputValue) {
					t.Fatalf("unexpected native input: %#v", input)
				}
				return dashJSONResponse(http.StatusOK, `{"output":{"task_id":"image-task"}}`), nil
			})}
			adapter := &Adapter{apiKey: "test-key", baseURL: "https://dash.test", client: client}
			_, err := adapter.Generate(context.Background(), core.MediaRequest{Model: test.model, Prompt: "a lighthouse", Options: core.MediaOptions{Resolution: "1280*1280", ImageURLs: test.imageURLs}})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnknownImageProtocolIsNotGuessed(t *testing.T) {
	adapter := &Adapter{apiKey: "test-key", baseURL: "https://dash.test", client: &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			t.Fatal("unknown model was submitted to a guessed endpoint")
		}
		return dashJSONResponse(200, `{"output":{"models":[{"model":"partner-image-generator","capabilities":["IG"],"inference_metadata":{"response_modality":["Image"]}}]}}`), nil
	})}}
	if _, err := adapter.Generate(context.Background(), core.MediaRequest{Model: "partner-image-generator", Prompt: "test"}); err == nil {
		t.Fatal("unknown protocol accepted")
	}
}

func TestLegacyWanImageToVideoUsesImgURL(t *testing.T) {
	client := &http.Client{Transport: dashRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != videoPath {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		input := body["input"].(map[string]any)
		if input["img_url"] != "https://example.com/first.png" || input["media"] != nil {
			t.Fatalf("unexpected legacy I2V input: %#v", input)
		}
		return dashJSONResponse(http.StatusOK, `{"output":{"task_id":"video-task"}}`), nil
	})}
	adapter := &Adapter{apiKey: "test-key", baseURL: "https://dash.test", client: client}
	_, err := adapter.Generate(context.Background(), core.MediaRequest{
		Model: "wan2.6-i2v", Prompt: "move", Options: core.MediaOptions{
			Resolution: "720p", Ratio: "16:9", Duration: 5,
			ImageURLs: []string{"https://example.com/first.png"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

type dashRoundTripFunc func(*http.Request) (*http.Response, error)

func (f dashRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func dashJSONResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestExplicitFramesAndReferenceVoices(t *testing.T) {
	frames := videoMedia(core.MediaRequest{Model: "wan3.0-video-prime", Options: core.MediaOptions{ReferenceMode: "first_last_frames", ImageURLs: []string{"first", "last"}}})
	if len(frames) != 2 || frames[0]["type"] != "first_frame" || frames[1]["type"] != "last_frame" {
		t.Fatalf("frames=%v", frames)
	}
	media := videoMedia(core.MediaRequest{Model: "wan2.7-r2v", Options: core.MediaOptions{ReferenceMode: "first_frame_references", ImageURLs: []string{"first", "subject"}, VideoURLs: []string{"clip1", "clip2"}, ReferenceVoices: map[string]string{"subject": "voice1", "clip2": "voice2"}}})
	if len(media) != 4 || media[0]["type"] != "first_frame" || media[1]["type"] != "reference_image" || media[1]["reference_voice"] != "voice1" || media[3]["reference_voice"] != "voice2" {
		t.Fatalf("media=%v", media)
	}
}

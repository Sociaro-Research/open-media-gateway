package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

func TestOptionalGenerationNullsPreserveOmissionAndIdempotency(t *testing.T) {
	for _, tc := range []struct{ path, model, fields string }{
		{"/v1/videos", "mock-video-v1", `"prompt":"test"`},
		{"/v1/generations", "mock-video-v1", `"prompt":"test"`},
		{"/v1/generations/estimate", "mock-video-v1", `"prompt":"test"`},
		{"/v1/images/generations", "mock-image-v1", `"prompt":"test"`},
		{"/v1/images/edits", "mock-image-v1", `"prompt":"test","image":"https://fixture.invalid/a.png"`},
		{"/v1/audio/speech", "mock-speech-v1", `"input":"test","voice":"Mock","response_format":"wav"`},
		{"/v1/audio/generations", "mock-music-v1", `"prompt":"test"`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			repo := &admissionRepository{}
			svc := core.NewTaskServiceWithProviders(repo, &admissionQueue{}, map[string]core.MediaProvider{"mock": mock.New(time.Millisecond, "")}, map[string]string{"mock": "mock-video-v1"}, "mock")
			svc.EnableAccounting()
			h := New(svc, healthStub{}, "secret")
			base := `{"model":"mock/` + tc.model + `",` + tc.fields + `}`
			for _, extra := range []string{"", `,"webhook_url":null`, `,"max_expected_cost_usd":null`, `,"options":{"parameters":null,"seed":null,"ratio":null,"generate_audio":null,"watermark":null,"camera_fixed":null,"draft":null,"return_last_frame":null,"negative_prompt":null,"reference_mode":null,"reference_voices":null,"video_urls":null,"audio_urls":null,"language":null,"lyrics":null,"gender":null}`} {
				w := openAIRequest(h, tc.path, strings.TrimSuffix(base, "}")+extra+`}`)
				want := 202
				if tc.path == "/v1/generations/estimate" {
					want = 200
				}
				if w.Code != want {
					t.Fatalf("null no longer matches omission: %d %s", w.Code, w.Body)
				}
				if want == 202 && repo.creates != 1 {
					t.Fatal("null changed idempotent identity", repo.creates)
				}
			}
			if repo.task.MaxExpectedUSD != nil || repo.task.WebhookURL != nil {
				t.Fatal("null became a configured budget or webhook")
			}
		})
	}
	// Required model, explicitly supplied options objects and standard controls keep
	// their existing validation. Null prompt also does not bypass model requirements.
	for _, body := range []string{`{"model":null,"prompt":"test"}`, `{"model":"mock/mock-video-v1","prompt":null}`, `{"model":"mock/mock-video-v1","prompt":"test","options":null}`, `{"model":"mock/mock-video-v1","prompt":"test","seconds":null}`, `{"model":"mock/mock-speech-v1","input":null}`} {
		svc := core.NewTaskServiceWithProviders(&admissionRepository{}, &admissionQueue{}, map[string]core.MediaProvider{"mock": mock.New(time.Millisecond, "")}, map[string]string{"mock": "mock-video-v1"}, "mock")
		w := openAIRequest(New(svc, healthStub{}, "secret"), "/v1/generations", body)
		if w.Code != 400 {
			t.Fatalf("required/type validation relaxed: %d %s", w.Code, w.Body)
		}
	}
}

func TestOptionalGenerationNullsMatchOpenAPI(t *testing.T) {
	data, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					Type any
					Enum []any
				}
			}
		}
	}
	if err = json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}

	gender := spec.Components.Schemas["MediaOptions"].Properties["gender"]
	acceptsNull := false
	for _, value := range gender.Enum {
		if value == nil {
			acceptsNull = true
		}
	}
	if !acceptsNull {
		t.Error("MediaOptions.gender enum must retain null as omission")
	}
	for schema, fields := range map[string][]string{"GenerationRequest": {"prompt", "webhook_url", "max_expected_cost_usd"}, "MediaOptions": {"parameters", "ratio", "seed", "duration", "generate_audio", "reference_voices", "video_urls", "audio_urls", "format"}} {
		for _, field := range fields {
			b, _ := json.Marshal(spec.Components.Schemas[schema].Properties[field].Type)
			if !strings.Contains(string(b), `"null"`) {
				t.Errorf("%s.%s must document null as omission", schema, field)
			}
		}
	}
}

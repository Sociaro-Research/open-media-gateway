package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

type diagnosticProvider struct{ *costLimitProvider }

func (p diagnosticProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	models, err := p.Adapter.ListModels(ctx)
	for i := range models {
		switch models[i].MediaType {
		case "image":
			models[i].Qualities = []string{"low", "high"}
			models[i].Formats = []string{"png", "jpeg"}
		case "video", "music":
			models[i].MinDuration, models[i].MaxDuration, models[i].DefaultDuration, models[i].DurationStep = 5, 15, 5, 5
		}
	}
	return models, err
}

func TestSemanticOptionsIdentifyPublicFields(t *testing.T) {
	for _, tc := range []struct{ path, model, text, field, invalid, valid, hint string }{
		{"/v1/audio/speech", "mock-speech-v1", `"input":"test"`, "voice", `"voice":"private-rejected-value"`, `"voice":"Mock"`, "Mock"},
		{"/v1/audio/speech", "mock-speech-v1", `"input":"test"`, "response_format", `"response_format":"private-rejected-value"`, `"response_format":"mp3"`, "mp3"},
		{"/v1/images/generations", "mock-image-v1", `"prompt":"test"`, "output_format", `"output_format":"private-rejected-value"`, `"output_format":"png"`, "png, jpeg"},
		{"/v1/images/generations", "mock-image-v1", `"prompt":"test"`, "quality", `"quality":"private-rejected-value"`, `"quality":"high"`, "low, high"},
		{"/v1/videos", "mock-video-v1", `"prompt":"test"`, "seconds", `"seconds":"7"`, `"seconds":"10"`, "increments of 5 starting at 5"},
		{"/v1/videos", "mock-video-v1", `"prompt":"test"`, "seconds", `"seconds":"20"`, `"seconds":"15"`, "between 5 and 15 seconds"},
		{"/v1/audio/generations", "mock-music-v1", `"prompt":"test"`, "options.duration", `"options":{"duration":7}`, `"options":{"duration":10}`, "increments of 5 starting at 5"},
		{"/v1/audio/generations", "mock-music-v1", `"prompt":"test"`, "options.format", `"options":{"format":"private-rejected-value"}`, `"options":{"format":"mp3"}`, "mp3"},
		{"/v1/images/generations", "mock-image-v1", `"prompt":"test"`, "size", `"size":"private-rejected-value"`, `"size":"1024x1024"`, "1024x1024"},
	} {
		for _, path := range []string{tc.path, "/v1/generations", "/v1/generations/estimate"} {
			t.Run(tc.field+"/"+path+"/"+tc.invalid, func(t *testing.T) {
				repo, queue := &admissionRepository{}, &admissionQueue{}
				provider := diagnosticProvider{&costLimitProvider{Adapter: mock.New(0, "embedded")}}
				service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": "mock-video-v1"}, "mock")
				service.EnableAccounting()
				server := New(service, healthStub{}, "secret")
				body := func(options string) string {
					return fmt.Sprintf(`{"model":"mock/%s",%s,%s}`, tc.model, tc.text, options)
				}
				response := openAIRequest(server, path, body(tc.invalid))
				var got struct {
					Error struct{ Type, Code, Param, Message string }
				}
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if response.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Type != "invalid_request_error" || got.Error.Param != tc.field || !strings.Contains(got.Error.Message, tc.field) || !strings.Contains(got.Error.Message, tc.hint) {
					t.Fatalf("diagnostic: %d %s", response.Code, response.Body)
				}
				if strings.Contains(response.Body.String(), "private-rejected-value") || repo.creates != 0 || len(queue.ids) != 0 || provider.generateCalls != 0 {
					t.Fatal("invalid request leaked data or reached admission/generation")
				}
				response = openAIRequest(server, path, body(tc.valid))
				status, creates := 202, 1
				if strings.HasSuffix(path, "/estimate") {
					status, creates = 200, 0
				}
				if response.Code != status || repo.creates != creates || len(queue.ids) != creates || provider.generateCalls != 0 {
					t.Fatalf("corrected request: %d %s", response.Code, response.Body)
				}
			})
		}
	}
}

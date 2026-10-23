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

type costLimitProvider struct {
	*mock.Adapter
	generateCalls int
}

func (p *costLimitProvider) Generate(ctx context.Context, request core.MediaRequest) (core.ProviderTask, error) {
	p.generateCalls++
	return p.Adapter.Generate(ctx, request)
}

func TestGenerationCostLimitDiagnostics(t *testing.T) {
	for _, endpoint := range []struct{ path, model, text string }{
		{"/v1/videos", "mock-video-v1", `"prompt":"test"`},
		{"/v1/images/generations", "mock-image-v1", `"prompt":"test"`},
		{"/v1/audio/speech", "mock-speech-v1", `"input":"test"`},
		{"/v1/audio/generations", "mock-music-v1", `"prompt":"test"`},
		{"/v1/generations", "mock-video-v1", `"prompt":"test"`},
		{"/v1/generations/estimate", "mock-video-v1", `"prompt":"test"`},
	} {
		for _, limit := range []string{"-1", "0", "1", "-0.000001"} {
			t.Run(endpoint.path+"/"+limit, func(t *testing.T) {
				repo, queue := &admissionRepository{}, &admissionQueue{}
				provider := &costLimitProvider{Adapter: mock.New(0, "embedded")}
				service := core.NewTaskServiceWithProviders(repo, queue,
					map[string]core.MediaProvider{"mock": provider},
					map[string]string{"mock": "mock-video-v1"}, "mock")
				service.EnableAccounting()
				body := fmt.Sprintf(`{"model":"mock/%s",%s,"max_expected_cost_usd":%s}`, endpoint.model, endpoint.text, limit)
				response := openAIRequest(New(service, healthStub{}, "secret"), endpoint.path, body)
				if strings.HasPrefix(limit, "-") {
					var got struct {
						Error struct{ Type, Code, Message, Param string }
					}
					if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
						t.Fatal(err)
					}
					if response.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Type != "invalid_request_error" ||
						got.Error.Message != "max_expected_cost_usd must not be negative" || got.Error.Param != "max_expected_cost_usd" {
						t.Fatalf("negative limit response: %d %s", response.Code, response.Body)
					}
					if repo.creates != 0 || len(queue.ids) != 0 {
						t.Fatal("negative limit reached task admission")
					}
				} else {
					status, creates := 202, 1
					if strings.HasSuffix(endpoint.path, "/estimate") {
						status, creates = 200, 0
					}
					if response.Code != status || repo.creates != creates || len(queue.ids) != creates {
						t.Fatalf("valid limit changed behavior: %d %s; creates=%d queued=%d", response.Code, response.Body, repo.creates, len(queue.ids))
					}
				}
				if provider.generateCalls != 0 {
					t.Fatal("admission unexpectedly called provider generation")
				}
			})
		}
	}
}

func TestUnsupportedOptionsKeepExistingDiagnostic(t *testing.T) {
	for _, path := range []string{"/v1/videos", "/v1/generations/estimate"} {
		t.Run(path, func(t *testing.T) {
			repo, queue := &admissionRepository{}, &admissionQueue{}
			service := core.NewTaskServiceWithProviders(repo, queue,
				map[string]core.MediaProvider{"mock": mock.New(0, "embedded")},
				map[string]string{"mock": "mock-video-v1"}, "mock")
			body := `{"model":"mock/mock-video-v1","prompt":"test","options":{"negative_prompt":"unsupported"},"max_expected_cost_usd":0}`
			response := openAIRequest(New(service, healthStub{}, "secret"), path, body)
			if response.Code != 400 || !strings.Contains(response.Body.String(), core.ErrOptionsInvalid.Error()) || !strings.Contains(response.Body.String(), `"param":null`) {
				t.Fatalf("unsupported options diagnostic changed: %d %s", response.Code, response.Body)
			}
			if repo.creates != 0 || len(queue.ids) != 0 {
				t.Fatal("unsupported options reached admission")
			}
		})
	}
}

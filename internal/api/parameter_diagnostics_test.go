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

type parameterDiagnosticProvider struct{ *costLimitProvider }

func (p parameterDiagnosticProvider) ListModels(ctx context.Context) ([]core.MediaModel, error) {
	models, err := p.Adapter.ListModels(ctx)
	min, max := 0.0, 1.0
	for i := range models {
		models[i].MaxPromptCharacters = 10
		models[i].Parameters = []core.ParameterField{{Key: "strength", Type: "number", Min: &min, Max: &max}, {Key: "tags", Type: "text_list", MaxItems: 2, MaxLength: 3}}
	}
	return models, err
}

func TestParameterAndPromptDiagnosticsReachAdmissionAPI(t *testing.T) {
	for _, tc := range []struct{ model, path, field, options, prompt, hint string }{
		{"mock-image-v1", "/v1/images/generations", "options.parameters.strength", `{"parameters":{"strength":"private-input"}}`, "safe", "JSON number"},
		{"mock-image-v1", "/v1/images/generations", "options.parameters.strength", `{"parameters":{"strength":2}}`, "safe", "between 0 and 1"},
		{"mock-image-v1", "/v1/images/generations", "options.parameters.tags.1", `{"parameters":{"tags":["a",7]}}`, "safe", "string"},
		{"mock-image-v1", "/v1/images/generations", "options.parameters", `{"parameters":{"sk_live_private_value":true}}`, "safe", "omit undocumented fields"},
		{"mock-image-v1", "/v1/images/generations", "options.parameters", `{"parameters":{"unrecognized_name":"private-input"}}`, "safe", "omit undocumented fields"},
		{"mock-image-v1", "/v1/images/generations", "prompt", `{}`, strings.Repeat("秘", 11), "11 characters; maximum 10"},
		{"mock-speech-v1", "/v1/audio/speech", "input", `{}`, strings.Repeat("秘", 50), "50 characters; maximum 10"},
	} {
		for _, path := range []string{tc.path, "/v1/generations", "/v1/generations/estimate"} {
			t.Run(tc.field+tc.hint+path, func(t *testing.T) {
				repo, queue := &admissionRepository{}, &admissionQueue{}
				provider := parameterDiagnosticProvider{&costLimitProvider{Adapter: mock.New(0, "embedded")}}
				service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"mock": provider}, nil, "mock")
				service.EnableAccounting()
				server := New(service, healthStub{}, "secret")
				textField := "prompt"
				if tc.model == "mock-speech-v1" {
					textField = "input"
				}
				body := func(prompt, options string) string {
					raw, _ := json.Marshal(prompt)
					return fmt.Sprintf(`{"model":"mock/%s","%s":%s,"options":%s}`, tc.model, textField, raw, options)
				}
				w := openAIRequest(server, path, body(tc.prompt, tc.options))
				var got struct {
					Error struct{ Code, Param, Message string }
				}
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if w.Code != 400 || got.Error.Code != "invalid_request" || got.Error.Param != tc.field || !strings.Contains(got.Error.Message, tc.hint) {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
				if strings.Contains(w.Body.String(), "private-input") || strings.Contains(w.Body.String(), "sk_live_private_value") || strings.Contains(w.Body.String(), "unrecognized_name") || strings.Contains(w.Body.String(), "秘") || repo.creates != 0 || len(queue.ids) != 0 || provider.generateCalls != 0 {
					t.Fatal("reject leaked input or reached admission", w.Body)
				}
				w = openAIRequest(server, path, body("safe", `{"parameters":{"strength":1,"tags":["界界界"]}}`))
				status, creates := 202, 1
				if strings.HasSuffix(path, "/estimate") {
					status, creates = 200, 0
				}
				if w.Code != status || repo.creates != creates || len(queue.ids) != creates || provider.generateCalls != 0 {
					t.Fatalf("corrected: %d %s", w.Code, w.Body)
				}
			})
		}
	}
}

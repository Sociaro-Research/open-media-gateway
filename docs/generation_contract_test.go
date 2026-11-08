package docs_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func generationSpecification(t *testing.T) map[string]any {
	t.Helper()
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func object(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected schema object, got %T", value)
	}
	return result
}

func TestSingleGenerationGuideSeparatesCreationEstimatesAndTemplates(t *testing.T) {
	guide, err := os.ReadFile("openai-api.md")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(guide), "## Single generation contract\n", 2)
	if len(parts) != 2 {
		t.Fatal("single generation contract section missing")
	}
	section := strings.SplitN(parts[1], "\n## Reference contracts checked", 2)[0]
	for _, distinction := range []string{"ordinary media creation", "route-specific", "cost estimate", "Template"} {
		if !strings.Contains(section, distinction) {
			t.Errorf("contract summary must distinguish %q", distinction)
		}
	}
	paths := object(t, generationSpecification(t)["paths"])
	responseSchema := func(path, code string) map[string]any {
		op := object(t, object(t, paths[path])["post"])
		response := object(t, object(t, op["responses"])[code])
		return object(t, object(t, object(t, response["content"])["application/json"])["schema"])
	}
	for _, path := range []string{"/v1/generations", "/v1/videos", "/v1/images/generations", "/v1/images/edits", "/v1/audio/speech", "/v1/audio/generations"} {
		if responseSchema(path, "202")["$ref"] != "#/components/schemas/Generation" {
			t.Error(path, "must return the shared generation job representation")
		}
	}
	estimate := responseSchema("/v1/generations/estimate", "200")
	properties := object(t, estimate["properties"])
	if estimate["$ref"] == "#/components/schemas/Generation" || properties["cost"] == nil || properties["can_generate"] == nil {
		t.Error("estimate must return cost/budget information rather than a generation job")
	}
	template := object(t, object(t, paths["/v1/templates/{template}/generations"])["post"])
	body := object(t, template["requestBody"])
	schema := object(t, object(t, object(t, body["content"])["application/json"])["schema"])
	separate := schema["$ref"] == "#/components/schemas/TemplateGenerationRequest"
	branches, _ := schema["allOf"].([]any)
	for _, branch := range branches {
		separate = separate || object(t, branch)["$ref"] == "#/components/schemas/TemplateGenerationRequest"
	}
	if !separate {
		t.Error("template generation has its own request contract before common admission")
	}
}

type documentedCountService struct {
	maximum int
	created int
}

func (s *documentedCountService) ListProviders(context.Context) []core.ProviderDescriptor {
	return []core.ProviderDescriptor{{ID: "fixture", Models: []core.MediaModel{{
		ID: "image", MediaType: "image", MaxOutputs: s.maximum,
	}, {ID: "video", MediaType: "video"}}}}
}
func (s *documentedCountService) Info() core.ServiceInfo { return core.ServiceInfo{} }
func (s *documentedCountService) ValidateMediaRequest(context.Context, core.CreateMediaInput) error {
	return nil
}
func (s *documentedCountService) CreateMediaRequest(ctx context.Context, input core.CreateMediaInput, _ string) (core.Task, error) {
	options, err := core.NormalizeModelOptions(s.ListProviders(ctx)[0].Models[0], input.Options)
	if err != nil {
		return core.Task{}, err
	}
	s.created++
	return core.Task{ID: "fixture-generation", Provider: input.Provider, Model: input.Model,
		PublicModel: input.PublicModel, MediaType: input.MediaType, Prompt: input.Prompt,
		Options: options, Status: core.TaskQueued, CreatedAt: time.Unix(123, 0), UpdatedAt: time.Unix(123, 0)}, nil
}
func (s *documentedCountService) GetTask(context.Context, string) (core.Task, error) {
	return core.Task{}, core.ErrTaskNotFound
}
func (s *documentedCountService) ListTasks(context.Context, int) ([]core.Task, error) {
	return nil, nil
}

type documentedCountHealth struct{}

func (documentedCountHealth) Check(context.Context) error { return nil }

// Exercise the real public parser and core normalization without a provider,
// worker, database or network call. The catalog controls the per-model ceiling.
func TestGenerationCountGuideMatchesPublicHTTP(t *testing.T) {
	guide, err := os.ReadFile("openai-api.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(guide), "`n` can only be 1") || !strings.Contains(string(guide), "`max_outputs`") {
		t.Error("guide must describe the selected model's image-output ceiling")
	}
	for _, maximum := range []int{0, 3} {
		service := &documentedCountService{maximum: maximum}
		handler := api.New(service, documentedCountHealth{}, "synthetic-docs-key")
		catalogRequest := httptest.NewRequest("GET", "/v1/models", nil)
		catalogRequest.Header.Set("Authorization", "Bearer synthetic-docs-key")
		catalogResponse := httptest.NewRecorder()
		handler.ServeHTTP(catalogResponse, catalogRequest)
		var catalog struct {
			Data []struct {
				Capabilities map[string]any `json:"capabilities"`
			}
		}
		if err := json.Unmarshal(catalogResponse.Body.Bytes(), &catalog); err != nil || catalogResponse.Code != 200 || len(catalog.Data) != 2 {
			t.Fatal("fixture catalog unavailable", catalogResponse.Code, err)
		}
		limit, present := catalog.Data[0].Capabilities["max_outputs"]
		if present != (maximum > 0) || present && limit != float64(maximum) {
			t.Fatal("public max_outputs differs from the selected model", limit, present)
		}
		for _, tc := range []struct {
			fields string
			count  int
			valid  bool
		}{
			{"", 0, true}, {`,"n":1`, 0, true}, {`,"n":3`, 3, maximum == 3},
			{`,"n":4`, 0, false}, {`,"n":0`, 0, false}, {`,"n":1.5`, 0, false},
			{`,"options":{"output_count":3}`, 0, false},
		} {
			request := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{"model":"fixture/image","prompt":"Synthetic documentation fixture"`+tc.fields+`}`))
			request.Header.Set("Authorization", "Bearer synthetic-docs-key")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			before := service.created
			handler.ServeHTTP(response, request)
			want := http.StatusBadRequest
			if tc.valid {
				want = http.StatusAccepted
			}
			if response.Code != want {
				t.Fatalf("max_outputs=%d fields=%s: %d %s", maximum, tc.fields, response.Code, response.Body.String())
			}
			if !tc.valid {
				if service.created != before {
					t.Fatal("invalid count or internal option reached admission")
				}
				continue
			}
			var result struct {
				Options struct {
					OutputCount int `json:"output_count"`
				}
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Options.OutputCount != tc.count {
				t.Fatalf("normalized response count=%d, want %d: %v", result.Options.OutputCount, tc.count, err)
			}
		}
		video := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"fixture/video","prompt":"Synthetic fixture","n":1}`))
		video.Header.Set("Authorization", "Bearer synthetic-docs-key")
		video.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		before := service.created
		handler.ServeHTTP(response, video)
		if response.Code != http.StatusBadRequest || service.created != before {
			t.Fatal("image output count became valid for video", response.Code)
		}
	}
}

func TestGenerationCountSchemasSeparateRequestsAndResults(t *testing.T) {
	schemas := object(t, object(t, generationSpecification(t)["components"])["schemas"])
	request := object(t, object(t, schemas["GenerationRequest"])["properties"])
	count := object(t, request["n"])
	if count["type"] != "integer" || count["minimum"] != float64(1) || count["enum"] != nil || count["maximum"] != nil {
		t.Error("request n needs a positive integer with a model-dependent ceiling, not a fixed enum/maximum")
	}
	model := object(t, object(t, schemas["MediaModel"])["properties"])
	limit := object(t, model["max_outputs"])
	if limit["type"] != "integer" || limit["minimum"] != float64(1) {
		t.Error("catalog must document its optional output ceiling")
	}
	common := object(t, object(t, schemas["MediaOptions"])["properties"])
	if common["output_count"] != nil {
		t.Fatal("ordinary request options must not acquire output_count")
	}
	resultSchema := object(t, schemas["GenerationOptions"])
	result := object(t, resultSchema["properties"])
	retainedCount := object(t, result["output_count"])
	if retainedCount["type"] != "integer" || retainedCount["readOnly"] != true {
		t.Error("saved output_count is response metadata")
	}
	delete(result, "output_count")
	if !reflect.DeepEqual(common, result) || resultSchema["additionalProperties"] != false {
		t.Error("response options must preserve shared option definitions without opening request fields")
	}
	generation := object(t, object(t, schemas["Generation"])["properties"])
	if object(t, generation["options"])["$ref"] != "#/components/schemas/GenerationOptions" || object(t, request["options"])["$ref"] != "#/components/schemas/MediaOptions" {
		t.Error("request and response option schemas must remain separate")
	}
	for _, name := range []string{"ReferenceValidationTarget", "TemplateDefinition"} {
		properties := object(t, object(t, schemas[name])["properties"])
		if object(t, properties["options"])["$ref"] != "#/components/schemas/MediaOptions" {
			t.Errorf("%s must retain its existing options contract", name)
		}
	}
	paths := object(t, generationSpecification(t)["paths"])
	edit := object(t, object(t, paths["/v1/images/edits"])["post"])
	content := object(t, object(t, edit["requestBody"])["content"])
	form := object(t, object(t, content["multipart/form-data"])["schema"])
	if object(t, object(t, form["properties"])["n"])["$ref"] != "#/components/schemas/GenerationRequest/properties/n" {
		t.Error("multipart image edits must share the documented image count contract")
	}
}

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

func TestSpeechSchemaRejectsWrongFieldAliases(t *testing.T) {
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schema := imageContractValue(spec, "paths", "/v1/audio/speech", "post", "requestBody", "content", "application/json", "schema")
	branches, _ := imageContractValue(schema, "allOf").([]any)
	base, prompt, output, format := false, false, false, false
	for _, branch := range branches {
		base = base || imageContractValue(branch, "$ref") == "#/components/schemas/GenerationRequest"
		for _, field := range []string{"input", "voice", "instructions", "response_format"} {
			if imageContractRequires(branch, field) || imageContractRequires(imageContractValue(spec, "components", "schemas", "GenerationRequest"), field) {
				t.Errorf("%s cannot be unconditionally required across speech models", field)
			}
		}
		forbidden, _ := imageContractValue(branch, "not", "anyOf").([]any)
		for _, rule := range forbidden {
			prompt = prompt || imageContractRequires(rule, "prompt")
			output = output || imageContractRequires(rule, "output_format")
		}
		format = format || imageContractRequires(imageContractValue(branch, "properties", "options", "not"), "format")
	}
	if !base || !prompt || !output || !format {
		t.Fatalf("speech must retain the base schema and reject wrong aliases regardless of value: base=%v prompt=%v output=%v format=%v", base, prompt, output, format)
	}
}

func TestSpeechJSONFieldsAndModelDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, extra, prompt, voice, format, instructions string
		want                                             int
		optional, noPrompt                               bool
	}{
		{name: "defaults", extra: `"input":"  Hello \n"`, prompt: "Hello", voice: "First", format: "wav", want: 202},
		{name: "explicit", extra: `"input":"Hello","voice":"Second","response_format":"mp3","instructions":"  Warm  "`, prompt: "Hello", voice: "Second", format: "mp3", instructions: "  Warm  ", want: 202},
		{name: "empty_controls", extra: `"input":"Hello","voice":"","response_format":"","instructions":""`, prompt: "Hello", voice: "First", format: "wav", want: 202},
		{name: "blank_voice", extra: `"input":"Hello","voice":"  "`, prompt: "Hello", voice: "First", format: "wav", want: 202},
		{name: "missing_input", want: 400},
		{name: "empty_input", extra: `"input":""`, want: 400},
		{name: "blank_input", extra: `"input":" \t "`, want: 400},
		{name: "exact_voice_id", extra: `"input":"Hello","voice":" Second "`, want: 400},
		{name: "unknown_voice", extra: `"input":"Hello","voice":"alloy"`, want: 400},
		{name: "blank_format", extra: `"input":"Hello","response_format":" "`, want: 400},
		{name: "unsupported_format", extra: `"input":"Hello","response_format":"aac"`, want: 400},
		{name: "optional_input", optional: true, voice: "First", format: "wav", want: 202},
		{name: "no_prompt_input", noPrompt: true, extra: `"input":"  "`, voice: "First", format: "wav", want: 202},
		{name: "no_prompt_text", noPrompt: true, extra: `"input":"Hello"`, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := core.MediaModel{ID: "speech", MediaType: "speech", SupportsVoice: true, RequiresVoice: true, Voices: []string{"First", "Second"}, Formats: []string{"wav", "mp3"}, PromptOptional: tc.optional, NoPrompt: tc.noPrompt}
			repo, queue := &admissionRepository{}, &admissionQueue{}
			provider := promptContractProvider{Adapter: mock.New(time.Millisecond, ""), model: model}
			service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"fixture": provider}, nil, "fixture")
			body := `{"model":"fixture/speech"`
			if tc.extra != "" {
				body += "," + tc.extra
			}
			out := openAIRequest(New(service, healthStub{}, "secret"), "/v1/audio/speech", body+"}")
			if out.Code != tc.want {
				t.Fatalf("%d want %d: %s", out.Code, tc.want, out.Body)
			}
			if tc.want != 202 {
				if repo.creates != 0 || len(queue.ids) != 0 {
					t.Fatal("invalid request queued")
				}
				return
			}
			if repo.creates != 1 || len(queue.ids) != 1 || repo.task.Status != core.TaskQueued || repo.task.Prompt != tc.prompt || repo.task.Options.Voice != tc.voice || repo.task.Options.Format != tc.format || repo.task.Options.Instructions != tc.instructions {
				t.Fatal("speech normalization or queue admission changed", repo.task)
			}
			var job map[string]any
			if json.Unmarshal(out.Body.Bytes(), &job) != nil || job["status"] != "queued" || job["status_url"] == nil || !strings.Contains(out.Header().Get("Content-Type"), "application/json") {
				t.Fatal("speech must return a queued JSON job", out.Header(), out.Body)
			}
		})
	}
}

func TestSpeechRejectsNonStringsAndMisplacedControls(t *testing.T) {
	for _, field := range []string{"input", "voice", "instructions", "response_format"} {
		for _, value := range []string{"null", "3", "false", "[]", "{}"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				body := `{"model":"mock/speech","input":"Hello","` + field + `":` + value + `}`
				if field == "input" {
					body = `{"model":"mock/speech","input":` + value + `}`
				}
				service := openAIFixture()
				out := openAIRequest(New(service, healthStub{}, "secret"), "/v1/audio/speech", body)
				var failure struct{ Error struct{ Param string } }
				_ = json.Unmarshal(out.Body.Bytes(), &failure)
				if out.Code != 400 || service.calls != 0 || failure.Error.Param != field {
					t.Fatal(out.Code, out.Body)
				}
			})
		}
	}
	for _, extra := range []string{`"prompt":null`, `"prompt":""`, `"prompt":"Hello"`, `"output_format":null`, `"output_format":""`, `"output_format":"mp3"`, `"options":{"format":null}`, `"options":{"format":""}`, `"options":{"format":"mp3"}`, `"options":{"voice":"Mock"}`, `"options":{"instructions":"Warm"}`, `"stream":true`, `"speed":1`} {
		t.Run(extra, func(t *testing.T) {
			out := openAIRequest(New(openAIFixture(), healthStub{}, "secret"), "/v1/audio/speech", `{"model":"mock/speech","input":"Hello",`+extra+`}`)
			if out.Code != 400 {
				t.Fatal(out.Code, out.Body)
			}
		})
	}
}

func TestSpeechRouteSelectionAndGenericAliases(t *testing.T) {
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/v1/audio/speech", 202},
		{"/v1/generations", 202},
		{"/v1/generations/estimate", 200},
		{"/v1/audio/generations", 400},
	} {
		repo, queue := &admissionRepository{}, &admissionQueue{}
		provider := promptContractProvider{Adapter: mock.New(time.Millisecond, ""), model: core.MediaModel{ID: "speech", MediaType: "speech", SupportsVoice: true, Voices: []string{"fixture"}, Formats: []string{"wav"}}}
		service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"fixture": provider}, nil, "fixture")
		service.EnableAccounting()
		out := openAIRequest(New(service, healthStub{}, "secret"), tc.path, `{"model":"fixture/speech","input":"Hello","voice":"fixture","instructions":"Warm","response_format":"wav","stream":false}`)
		if out.Code != tc.want {
			t.Fatal(tc.path, out.Code, out.Body)
		}
		if tc.want == 202 && (repo.creates != 1 || len(queue.ids) != 1 || repo.task.MediaType != "speech" || repo.task.Prompt != "Hello" || repo.task.Options.Voice != "fixture" || repo.task.Options.Instructions != "Warm" || repo.task.Options.Format != "wav") {
			t.Fatal("generic speech mapping changed", repo.task)
		}
		if tc.want != 202 && (repo.creates != 0 || len(queue.ids) != 0) {
			t.Fatal("estimate or mismatched route created a generation")
		}
	}
}

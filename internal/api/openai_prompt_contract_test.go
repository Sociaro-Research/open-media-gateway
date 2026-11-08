package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

type promptContractProvider struct {
	*mock.Adapter
	model core.MediaModel
}

func (p promptContractProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{p.model}, nil
}

func TestOpenAPIPromptRequirementsMatchJSONAndMultipartAdmission(t *testing.T) {
	var spec struct {
		Paths map[string]struct {
			Post struct {
				RequestBody struct {
					Content map[string]struct {
						Schema struct{ Required []string }
					}
				}
			}
		}
	}
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/videos", "/v1/images/edits"} {
		t.Run(path, func(t *testing.T) {
			required := spec.Paths[path].Post.RequestBody.Content["multipart/form-data"].Schema.Required
			// File alternatives are covered by TestImageInputSchemaMatchesEditRepresentations.
			// image[] and mixed spellings are valid; image is not globally required.
			if slices.Contains(required, "prompt") || !slices.Contains(required, "model") {
				t.Errorf("multipart schema must preserve model without globally requiring prompt: %v", required)
			}
			for _, format := range []string{"json", "multipart"} {
				for _, tc := range []struct {
					name, prompt                      string
					optional, noPrompt, omitted, null bool
					want                              int
				}{
					{name: "required_omitted", omitted: true, want: 400},
					{name: "required_whitespace", prompt: " \t\n", want: 400},
					{name: "required_text", prompt: " direction ", want: 202},
					{name: "optional_omitted", optional: true, omitted: true, want: 202},
					{name: "optional_whitespace", optional: true, prompt: " \t\n", want: 202},
					{name: "no_prompt_omitted", noPrompt: true, omitted: true, want: 202},
					{name: "no_prompt_whitespace", noPrompt: true, prompt: " \t\n", want: 202},
					{name: "no_prompt_text", noPrompt: true, prompt: "direction", want: 400},
					{name: "no_prompt_null", noPrompt: true, prompt: "null", null: true, want: 202},
				} {
					t.Run(format+"/"+tc.name, func(t *testing.T) {
						model := core.MediaModel{ID: "fixture", MediaType: "video", PromptOptional: tc.optional, NoPrompt: tc.noPrompt}
						if path == "/v1/images/edits" {
							model.MediaType, model.MaxImageInputs, model.SupportsImageInput = "image", 1, true
						}
						repo, queue := &admissionRepository{}, &admissionQueue{}
						provider := promptContractProvider{Adapter: mock.New(time.Millisecond, ""), model: model}
						service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"fixture": provider}, nil, "fixture")
						local, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 32<<20)
						if err != nil {
							t.Fatal(err)
						}
						library := references.New(&referenceMemory{items: map[string]references.Image{}}, local)
						h := NewGateway(Dependencies{Service: service, Health: healthStub{}, APIKey: "secret", Storage: local, SigningSecret: "fixture-secret", MediaURLTTL: time.Hour, References: library, AssetPublicURL: "https://fixture.invalid"})
						fields := map[string]any{"model": "fixture/fixture"}
						if !tc.omitted {
							fields["prompt"] = tc.prompt
						}
						var request *http.Request
						if format == "json" {
							if tc.null {
								fields["prompt"] = nil
							}
							if model.MediaType == "image" {
								fields["image"] = "https://fixture.invalid/image.png"
							}
							body, err := json.Marshal(fields)
							if err != nil {
								t.Fatal(err)
							}
							request = httptest.NewRequest("POST", path, bytes.NewReader(body))
							request.Header.Set("Content-Type", "application/json")
						} else {
							var body bytes.Buffer
							form := multipart.NewWriter(&body)
							for name, value := range fields {
								if err := form.WriteField(name, value.(string)); err != nil {
									t.Fatal(err)
								}
							}
							if model.MediaType == "image" {
								part, err := form.CreateFormFile("image", "fixture.png")
								if err != nil {
									t.Fatal(err)
								}
								if _, err = part.Write(referencePNG(t)); err != nil {
									t.Fatal(err)
								}
							}
							if err := form.Close(); err != nil {
								t.Fatal(err)
							}
							request = httptest.NewRequest("POST", "https://fixture.invalid"+path, &body)
							request.Header.Set("Content-Type", form.FormDataContentType())
						}
						request.Header.Set("Authorization", "Bearer secret")
						out := httptest.NewRecorder()
						h.ServeHTTP(out, request)
						want := tc.want
						if tc.null && format == "multipart" {
							want = 400
						} // Literal text, not JSON null.
						if out.Code != want {
							t.Fatalf("%d want %d: %s", out.Code, want, out.Body)
						}
						if want == 202 {
							prompt := strings.TrimSpace(tc.prompt)
							if tc.null {
								prompt = ""
							}
							if repo.creates != 1 || len(queue.ids) != 1 || repo.task.Prompt != prompt {
								t.Fatal("trimmed admission changed", repo.creates, repo.task.Prompt)
							}
						} else if repo.creates != 0 || len(queue.ids) != 0 {
							t.Fatal("invalid prompt admitted")
						}
					})
				}
			}
		})
	}
}

func TestOpenAPIPromptLengthMatchesTrimmedReviewedAdmission(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]struct {
					MaxLength, Maximum int
					Required           []string
				}
				Required []string
			}
		}
	}
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	request := spec.Components.Schemas["GenerationRequest"]
	if slices.Contains(request.Required, "prompt") {
		t.Error("JSON prompt cannot be globally required")
	}
	maximum := spec.Components.Schemas["MediaModel"].Properties["max_prompt_characters"].Maximum
	if maximum != core.MaxReviewedPromptCharacters {
		t.Errorf("catalog schema excludes reviewed limits: maximum=%d", maximum)
	}
	for _, tc := range []struct {
		name, prompt   string
		manual, speech bool
		want           int
	}{
		{name: "reviewed_ascii_boundary", prompt: strings.Repeat("a", 40000), want: 202},
		{name: "reviewed_padded_boundary", prompt: " \t" + strings.Repeat("a", 40000) + "\n ", want: 202},
		{name: "reviewed_unicode", prompt: strings.Repeat("界", 10001), want: 202},
		{name: "reviewed_over_limit", prompt: strings.Repeat("a", 40001), want: 400},
		{name: "manual_not_extended", prompt: strings.Repeat("a", 10001), manual: true, want: 400},
		{name: "speech_padded_4096", prompt: strings.Repeat(" ", 6000) + strings.Repeat("a", 4096), speech: true, want: 202},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := core.MediaModel{ID: "reviewed", MediaType: "video", Manual: tc.manual, PromptPolicy: &core.PromptPolicy{MaxCharacters: 40000}}
			field, path := "prompt", "/v1/videos"
			if tc.speech {
				model.MediaType, model.MaxPromptCharacters, model.PromptPolicy = "speech", 4096, nil
				field, path = "input", "/v1/audio/speech"
			}
			provider := promptContractProvider{Adapter: mock.New(time.Millisecond, ""), model: model}
			repo := &admissionRepository{}
			service := core.NewTaskServiceWithProviders(repo, &admissionQueue{}, map[string]core.MediaProvider{"fixture": provider}, nil, "fixture")
			body, err := json.Marshal(map[string]string{"model": "fixture/reviewed", field: tc.prompt})
			if err != nil {
				t.Fatal(err)
			}
			out := openAIRequest(New(service, healthStub{}, "secret"), path, string(body))
			if out.Code != tc.want {
				t.Fatalf("%d want %d: %s", out.Code, tc.want, out.Body)
			}
			if tc.want == 202 {
				if repo.task.Prompt != strings.TrimSpace(tc.prompt) {
					t.Fatal("admission must trim before counting")
				}
				if limit := request.Properties[field].MaxLength; limit > 0 && utf8.RuneCountInString(tc.prompt) > limit {
					t.Errorf("schema maxLength=%d rejects accepted raw prompt of %d code points", limit, utf8.RuneCountInString(tc.prompt))
				}
			}
		})
	}
}

package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/docs"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

func TestMusicSchemaMatchesFieldPlacementAndEmptyGender(t *testing.T) {
	raw, err := docs.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err = json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schema := imageContractValue(spec, "paths", "/v1/audio/generations", "post", "requestBody", "content", "application/json", "schema")
	branches, _ := imageContractValue(schema, "allOf").([]any)
	base, forbidden := false, map[string]bool{}
	for _, branch := range branches {
		base = base || imageContractValue(branch, "$ref") == "#/components/schemas/GenerationRequest"
		rules, _ := imageContractValue(branch, "not", "anyOf").([]any)
		for _, rule := range rules {
			for _, field := range []string{"input", "voice", "instructions", "response_format", "output_format"} {
				forbidden[field] = forbidden[field] || imageContractRequires(rule, field)
			}
		}
		if imageContractRequires(branch, "prompt") {
			t.Error("music prompt cannot be unconditionally required")
		}
	}
	if !base || imageContractRequires(imageContractValue(spec, "components", "schemas", "GenerationRequest"), "prompt") {
		t.Error("music must retain the base schema and model-dependent prompt requirements")
	}
	for _, field := range []string{"input", "voice", "instructions", "response_format", "output_format"} {
		if !forbidden[field] {
			t.Errorf("music must reject %s regardless of its value", field)
		}
	}
	for _, name := range []string{"MediaOptions", "GenerationOptions"} {
		values, _ := imageContractValue(spec, "components", "schemas", name, "properties", "gender", "enum").([]any)
		empty := false
		for _, value := range values {
			empty = empty || value == ""
		}
		if !empty {
			t.Errorf("%s.gender must allow empty string as omission/default", name)
		}
	}
}

func TestMusicAndAudioRouteSelection(t *testing.T) {
	for _, media := range []string{"music", "audio"} {
		for _, path := range []string{"/v1/audio/generations", "/v1/generations", "/v1/generations/estimate"} {
			t.Run(media+path, func(t *testing.T) {
				model := core.MediaModel{ID: "native", PublicID: "public/renamed", MediaType: media, Formats: []string{"wav"}}
				repo, queue := &admissionRepository{}, &admissionQueue{}
				provider := promptContractProvider{Adapter: mock.New(time.Millisecond, ""), model: model}
				service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"fixture": provider}, nil, "fixture")
				service.EnableAccounting()
				out := openAIRequest(New(service, healthStub{}, "secret"), path, `{"model":"public/renamed","prompt":"  Music \n","options":{"format":"wav"}}`)
				want := 202
				if path == "/v1/generations/estimate" {
					want = 200
				}
				if media == "audio" && path == "/v1/audio/generations" {
					want = 400
				}
				if out.Code != want {
					t.Fatalf("%d want %d: %s", out.Code, want, out.Body)
				}
				if want == 202 && (repo.creates != 1 || len(queue.ids) != 1 || repo.task.MediaType != media || repo.task.Model != "native" || repo.task.PublicModel != "public/renamed" || repo.task.Prompt != "Music") {
					t.Fatal("exact public ID or media selection changed", repo.task)
				}
				if want != 202 && (repo.creates != 0 || len(queue.ids) != 0) {
					t.Fatal("estimate or mismatched route created work")
				}
				for _, wrong := range []string{"fixture/native", "public/Renamed", " public/renamed "} {
					out = openAIRequest(New(service, healthStub{}, "secret"), path, `{"model":"`+wrong+`","prompt":"Music"}`)
					if out.Code != 400 {
						t.Fatal("non-public model alias accepted", wrong, out.Code, out.Body)
					}
				}
			})
		}
	}
}

func TestMusicPromptLyricsAndOptionDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, fields, prompt, lyrics, language, gender, format        string
		duration                                                      int
		want                                                          int
		noLyrics, noGender, optional, noPrompt, sourceDuration, audio bool
	}{
		{name: "lyrics_only", fields: `"options":{"lyrics":"  Verse  "}`, lyrics: "  Verse  ", gender: "female", format: "wav", duration: 10, want: 202},
		{name: "null_prompt", fields: `"prompt":null,"options":{"lyrics":"Verse"}`, lyrics: "Verse", gender: "female", format: "wav", duration: 10, want: 202},
		{name: "null_controls", fields: `"prompt":"  Music ","options":{"language":null,"lyrics":null,"gender":null,"format":null,"duration":null}`, prompt: "Music", gender: "female", format: "wav", duration: 10, want: 202},
		{name: "empty_controls", fields: `"prompt":"Music","options":{"language":"","lyrics":"","gender":"","format":"","duration":0}`, prompt: "Music", gender: "female", format: "wav", duration: 10, want: 202},
		{name: "explicit", fields: `"prompt":"Music","options":{"language":"en","gender":"male","format":"mp3","duration":15}`, prompt: "Music", language: "en", gender: "male", format: "mp3", duration: 15, want: 202},
		{name: "missing_text", want: 400},
		{name: "null_text", fields: `"prompt":null`, want: 400},
		{name: "blank_text", fields: `"prompt":" \t ","options":{"lyrics":"  "}`, want: 400},
		{name: "no_lyrics_capability", fields: `"options":{"lyrics":"Verse"}`, noLyrics: true, want: 400},
		{name: "audio_not_lyrics_exception", fields: `"options":{"lyrics":"Verse"}`, audio: true, want: 400},
		{name: "optional_prompt", optional: true, gender: "female", format: "wav", duration: 10, want: 202},
		{name: "no_prompt_text", noPrompt: true, fields: `"prompt":"Music"`, want: 400},
		{name: "no_gender_empty", noGender: true, fields: `"prompt":"Music","options":{"gender":""}`, prompt: "Music", format: "wav", duration: 10, want: 202},
		{name: "no_gender_nonempty", noGender: true, fields: `"prompt":"Music","options":{"gender":"female"}`, want: 400},
		{name: "gender_whitespace", fields: `"prompt":"Music","options":{"gender":" female "}`, want: 400},
		{name: "format_whitespace", fields: `"prompt":"Music","options":{"format":" wav "}`, want: 400},
		{name: "duration_step", fields: `"prompt":"Music","options":{"duration":7}`, want: 400},
		{name: "duration_range", fields: `"prompt":"Music","options":{"duration":35}`, want: 400},
		{name: "source_duration_unset", sourceDuration: true, fields: `"prompt":"Music","options":{"duration":null}`, prompt: "Music", gender: "female", format: "wav", want: 202},
		{name: "source_duration_override", sourceDuration: true, fields: `"prompt":"Music","options":{"duration":10}`, want: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := core.MediaModel{ID: "music", MediaType: "music", SupportsLyrics: !tc.noLyrics, SupportsGender: !tc.noGender, Formats: []string{"wav", "mp3"}, MinDuration: 5, MaxDuration: 30, DurationStep: 5, DefaultDuration: 10, PromptOptional: tc.optional, NoPrompt: tc.noPrompt, DurationFromInput: tc.sourceDuration}
			if tc.audio {
				model.MediaType = "audio"
			}
			repo, queue := &admissionRepository{}, &admissionQueue{}
			provider := promptContractProvider{Adapter: mock.New(time.Millisecond, ""), model: model}
			service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"fixture": provider}, nil, "fixture")
			body := `{"model":"fixture/music"`
			if tc.fields != "" {
				body += "," + tc.fields
			}
			out := openAIRequest(New(service, healthStub{}, "secret"), "/v1/generations", body+"}")
			if out.Code != tc.want {
				t.Fatalf("%d want %d: %s", out.Code, tc.want, out.Body)
			}
			if tc.want == 202 && (repo.creates != 1 || len(queue.ids) != 1 || repo.task.Prompt != tc.prompt || repo.task.Options.Lyrics != tc.lyrics || repo.task.Options.Language != tc.language || repo.task.Options.Gender != tc.gender || repo.task.Options.Format != tc.format || repo.task.Options.Duration != tc.duration) {
				t.Fatal("music normalization changed", repo.task)
			}
			if tc.want != 202 && (repo.creates != 0 || len(queue.ids) != 0) {
				t.Fatal("invalid music input queued")
			}
		})
	}
}

func TestMusicJSONTypesAndMisplacedControls(t *testing.T) {
	for _, field := range []string{"language", "lyrics", "gender", "format", "duration"} {
		values := []string{"false", "[]", "{}", "3"}
		if field == "duration" {
			values = []string{"false", "[]", "{}", "3.5", `"3"`, `""`}
		}
		for _, value := range values {
			t.Run(field+value, func(t *testing.T) {
				service := openAIFixture()
				out := openAIRequest(New(service, healthStub{}, "secret"), "/v1/audio/generations", `{"model":"mock/music","prompt":"Music","options":{"`+field+`":`+value+`}}`)
				if out.Code != 400 || service.calls != 0 {
					t.Fatal(out.Code, out.Body)
				}
			})
		}
	}
	for _, field := range []string{"input", "voice", "instructions", "response_format", "output_format", "language", "lyrics", "gender", "format", "duration"} {
		for _, value := range []string{`null`, `""`, `"wav"`} {
			t.Run(field+value, func(t *testing.T) {
				service := openAIFixture()
				out := openAIRequest(New(service, healthStub{}, "secret"), "/v1/audio/generations", `{"model":"mock/music","prompt":"Music","`+field+`":`+value+`}`)
				if out.Code != 400 || service.calls != 0 {
					t.Fatal(out.Code, out.Body)
				}
			})
		}
	}
}

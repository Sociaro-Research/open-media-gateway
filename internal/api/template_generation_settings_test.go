package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplateGenerationSettingsSavePublishAndAdmission(t *testing.T) {
	for _, audio := range []bool{false, true} {
		t.Run(map[bool]string{false: "silent", true: "audio"}[audio], func(t *testing.T) {
			s, repo, svc, _ := templateFixture(t)
			s.service = &videoDurationService{openAIServiceFixture: svc}
			model := templateTestModel()
			model.Resolutions = []string{"720p", "1080p"}
			model.Ratios = []string{"16:9", "9:16"}
			model.SupportsAudio = true
			svc.catalog[0].Models = []core.MediaModel{model}
			id := uuid.NewString()
			d := templates.Definition{Title: "Output controls", Provider: "mock", Model: model.ID, MediaType: "video", Prompt: "Image 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "9:16", Resolution: "1080p", Duration: 9, GenerateAudio: &audio}}
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			asset := templateCopy(t, s, id, referencePNG(t))
			d.CoverID = asset.ID
			d.Slots = []templates.Slot{{ID: "image:1", Type: "image", Index: 1, Role: "reference", Label: "Subject", AssetID: asset.ID}}
			for _, action := range []string{"", "publish"} {
				body, _ := json.Marshal(map[string]any{"version": repo.items[id].Version, "action": action, "draft": d})
				w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			w := sendTest(s.handler(), "GET", "/v1/system/templates/"+id, "", "alice", "")
			var loaded templates.Record
			if err := json.Unmarshal(w.Body.Bytes(), &loaded); err != nil {
				t.Fatal(err)
			}
			assertOptions := func(o core.MediaOptions) {
				t.Helper()
				if o.Duration != 9 || o.Resolution != "1080p" || o.Ratio != "9:16" || o.GenerateAudio == nil || *o.GenerateAudio != audio {
					t.Fatalf("settings changed: %+v", o)
				}
			}
			assertOptions(loaded.Draft.Options)
			assertOptions(loaded.Published.Options)
			_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Output settings"}`)
			body, _ := json.Marshal(map[string]any{"revision": loaded.Revision, "provider": "mock", "model": model.ID, "inputs": map[string]string{}, "max_expected_cost_usd": 0})
			for _, endpoint := range []string{"estimate", "generations"} {
				r := httptest.NewRequest("POST", "/v1/templates/"+id+"/"+endpoint, bytes.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+key)
				r.Header.Set("Idempotency-Key", uuid.NewString())
				w := httptest.NewRecorder()
				s.handler().ServeHTTP(w, r)
				if w.Code != 200 && w.Code != 202 {
					t.Fatal(endpoint, w.Code, w.Body.String())
				}
			}
			assertOptions(svc.input.Options)
			var snapshot struct {
				Options core.MediaOptions `json:"options"`
			}
			if err := json.Unmarshal(svc.input.TemplateSnapshot, &snapshot); err != nil {
				t.Fatal(err)
			}
			assertOptions(snapshot.Options)
		})
	}
}

func TestTemplateDraftValidatesSettingsWithOptionalAudio(t *testing.T) {
	for name, change := range map[string]func(*core.MediaOptions){
		"ratio":      func(o *core.MediaOptions) { o.Ratio = "7:1" },
		"resolution": func(o *core.MediaOptions) { o.Resolution = "8K" },
		"duration":   func(o *core.MediaOptions) { o.Duration = 999 },
		"audio":      func(o *core.MediaOptions) { enabled := true; o.GenerateAudio = &enabled },
	} {
		t.Run(name, func(t *testing.T) {
			s, repo, _, _ := templateFixture(t)
			id := uuid.NewString()
			d := templates.Definition{Provider: "mock", Model: "dance", MediaType: "video", Options: core.MediaOptions{ReferenceMode: "references"}}
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			change(&d.Options)
			body, _ := json.Marshal(map[string]any{"version": 1, "draft": d})
			w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
			if name == "audio" {
				if w.Code != 200 || repo.saves != 1 || repo.items[id].Draft.Options.GenerateAudio == nil || !*repo.items[id].Draft.Options.GenerateAudio {
					t.Fatal("optional preference not retained", w.Code, w.Body.String())
				}
				return
			}
			if w.Code != 400 || repo.saves != 0 {
				t.Fatal(w.Code, w.Body.String(), repo.saves)
			}
		})
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestPromptCharacterLimitsRuntimeCatalogs(t *testing.T) {
	s, _, svc, _ := templateFixture(t)
	base := templateTestModel()
	svc.catalog = []core.ProviderDescriptor{{ID: "arbitrary-connection", Models: []core.MediaModel{base}}}
	_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Prompt metadata fixture"}`)
	for _, endpoint := range []string{"/v1/providers", "/v1/models", "/v1/models/arbitrary-connection/" + base.ID, "/v1/system/templates"} {
		t.Run(endpoint, func(t *testing.T) {
			actor, bearer := "", key
			if endpoint == "/v1/system/templates" {
				actor, bearer = "alice", ""
			}
			w := sendTest(s.handler(), "GET", endpoint, "", actor, bearer)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var root map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &root); err != nil {
				t.Fatal(err)
			}
			var model map[string]any
			switch endpoint {
			case "/v1/providers":
				model = root["data"].([]any)[0].(map[string]any)["models"].([]any)[0].(map[string]any)
			case "/v1/models":
				model = root["data"].([]any)[0].(map[string]any)["capabilities"].(map[string]any)
			case "/v1/system/templates":
				model = root["catalog"].([]any)[0].(map[string]any)["models"].([]any)[0].(map[string]any)
			default:
				model = root["capabilities"].(map[string]any)
			}
			if model["max_prompt_characters"] != float64(10000) {
				t.Fatal("missing effective gateway prompt limit", model["max_prompt_characters"])
			}
		})
	}
}

func TestPromptCharacterLimitsProjectionModesAndCacheIsolation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model core.MediaModel
		want  int
	}{
		{"native lower", core.MediaModel{MaxPromptCharacters: 3072}, 3072},
		{"native higher", core.MediaModel{MaxPromptCharacters: 32000}, 10000},
		{"unspecified upstream", core.MediaModel{}, 10000},
		{"manual profile", core.MediaModel{Manual: true, MaxPromptCharacters: 4096}, 10000},
		{"no prompt", core.MediaModel{NoPrompt: true, MaxPromptCharacters: 2000}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := openAIFixture()
			tc.model.ID = "arbitrary-upstream-name"
			tc.model.MediaType = "video"
			svc.catalog = []core.ProviderDescriptor{{ID: "arbitrary-connection", Models: []core.MediaModel{tc.model}}}
			before, _ := json.Marshal(svc.catalog)
			s := &Server{service: svc}
			for _, cached := range []bool{false, true} {
				r := httptest.NewRequest("GET", "/v1/models", nil)
				if cached {
					r = r.WithContext(context.WithValue(r.Context(), modelCatalogKey{}, svc.catalog))
				}
				models := catalogModels(s.visibleCatalog(r))
				if len(models) != 1 || models[0].Capabilities.MaxPromptCharacters != tc.want {
					t.Fatal("wrong effective limit", models)
				}
				raw, _ := json.Marshal(models[0])
				var wire map[string]any
				json.Unmarshal(raw, &wire)
				cap := wire["capabilities"].(map[string]any)
				if tc.want == 0 {
					if _, ok := cap["max_prompt_characters"]; ok {
						t.Fatal("non-applicable limit serialized")
					}
				} else if cap["max_prompt_characters"] != float64(tc.want) {
					t.Fatal("limit lost in JSON", cap)
				}
			}
			after, _ := json.Marshal(svc.catalog)
			if !bytes.Equal(before, after) {
				t.Fatal("runtime metadata mutated cached adapter/catalogue descriptors")
			}
		})
	}
}

func TestPromptCharacterLimitsPublishedTemplateRunner(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	model := templateTestModel()
	model.MaxPromptCharacters = 3072
	svc.catalog = []core.ProviderDescriptor{{ID: "arbitrary-connection", Models: []core.MediaModel{model}}}
	id := uuid.NewString()
	d := templates.Definition{Title: "Runtime limit", Provider: "arbitrary-connection", Model: model.ID, MediaType: "video", Prompt: "Image 1 moves", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Resolution: "720p", Ratio: "16:9"}}
	d.Slots, _ = d.InferSlots(model)
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
	repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
	_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Template prompt limit fixture"}`)
	w := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", key)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var view struct {
		Catalog []core.ProviderDescriptor `json:"catalog"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Catalog) != 1 || len(view.Catalog[0].Models) != 1 || view.Catalog[0].Models[0].MaxPromptCharacters != 3072 {
		t.Fatal("runner lost effective limit", w.Body.String())
	}
}

func TestPromptCharacterLimitsPreserveEmptyCatalogSerialization(t *testing.T) {
	svc := openAIFixture()
	s := &Server{service: svc}
	for _, catalog := range [][]core.ProviderDescriptor{nil, {}} {
		svc.catalog = catalog
		original, _ := json.Marshal(catalog)
		projected, _ := json.Marshal(s.visibleCatalog(httptest.NewRequest("GET", "/v1/providers", nil)))
		if !bytes.Equal(original, projected) {
			t.Fatal("projection changed empty catalogue serialization", string(original), string(projected))
		}
	}
}

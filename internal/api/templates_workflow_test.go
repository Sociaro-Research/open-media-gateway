package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/google/uuid"
)

func TestTemplateReferenceWorkflowBoundaries(t *testing.T) {
	var model core.MediaModel
	for _, m := range alibaba.Definition().Profiles() {
		if m.ID == "wan3.0-video" {
			model = m
		}
	}
	if model.ID == "" {
		t.Fatal("native model absent")
	}
	for _, mode := range []string{"", "none", "first_frame", "first_last_frames", "first_last_frame", "first_frame_references", "keyframes", "continue_video", "references"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, svc, _ := templateFixture(t)
			svc.catalog = []core.ProviderDescriptor{{ID: "native-alias", Models: []core.MediaModel{model}}}
			id := uuid.NewString()
			d := templates.Definition{Title: "Workflow", MediaType: "video", Provider: "native-alias", Model: model.ID, Prompt: "Image 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: mode, Duration: 5}}
			body, _ := json.Marshal(map[string]any{"id": id, "version": 0, "draft": d})
			w := sendTest(s.handler(), "POST", "/v1/system/templates", string(body), "alice", "")
			if mode != "references" {
				if w.Code != 400 || repo.saves != 0 {
					t.Fatal("unsupported workflow created", w.Code, w.Body.String())
				}
			} else if w.Code != 201 {
				t.Fatal(w.Code, w.Body.String())
			}
			// Existing/imported drafts must pass the same publication and inference gate.
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			if mode != "references" {
				body, _ = json.Marshal(map[string]any{"version": 1, "draft": d})
				if saved := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); saved.Code != 400 || repo.saves != 0 {
					t.Fatal("unsupported workflow saved", saved.Code, saved.Body.String())
				}
			}
			d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
			body, _ = json.Marshal(d)
			inferred := sendTest(s.handler(), "POST", "/v1/system/templates/"+id+"/infer", string(body), "alice", "")
			if mode == "references" {
				if inferred.Code != 200 {
					t.Fatal(inferred.Code, inferred.Body.String())
				}
				var result struct {
					Slots []templates.Slot `json:"slots"`
				}
				if err := json.Unmarshal(inferred.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				d.Slots = result.Slots
			} else if inferred.Code != 400 {
				t.Fatal("unsupported inference", inferred.Code)
			}
			body, _ = json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
			published := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
			want := 400
			if mode == "references" {
				want = 200
			}
			if published.Code != want {
				t.Fatal("publication", published.Code, published.Body.String())
			}
			if svc.calls != 0 {
				t.Fatal("generation invoked")
			}
		})
	}
}

func TestTemplateAuthoringCatalogUsesModeContracts(t *testing.T) {
	s, _, svc, _ := templateFixture(t)
	eligible := templateTestModel()
	eligible.ID = "operator-alias"
	frames := eligible
	frames.ID = "frames-only"
	frames.ReferenceModes = []core.ReferenceMode{{ID: "first_frame", MinImages: 1, MaxImages: 1}, {ID: "first_last_frame", MinImages: 2, MaxImages: 2}, {ID: "keyframes", MaxImages: 10}}
	text := eligible
	text.ID = "text-only"
	text.ReferenceModes = []core.ReferenceMode{{ID: "none"}}
	noPrompt := eligible
	noPrompt.ID, noPrompt.NoPrompt = "no-prompt", true
	empty := eligible
	empty.ID, empty.ReferenceModes = "empty-contract", []core.ReferenceMode{{ID: "references"}}
	fallback := eligible
	fallback.ID, fallback.ReferenceModes = "inherited-input-contract", nil
	svc.catalog = []core.ProviderDescriptor{{ID: "arbitrary-connection", Models: []core.MediaModel{eligible, frames, text, noPrompt, empty, fallback}}}
	w := sendTest(s.handler(), "GET", "/v1/system/templates", "", "alice", "")
	var result struct {
		Catalog []core.ProviderDescriptor `json:"catalog"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Catalog) != 1 || len(result.Catalog[0].Models) != 2 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, model := range result.Catalog[0].Models {
		if len(model.ReferenceModes) != 1 || model.ReferenceModes[0].ID != "references" {
			t.Fatal(model)
		}
	}
	if len(svc.catalog[0].Models) != 6 || svc.catalog[0].Models[1].ReferenceModes[0].ID != "first_frame" {
		t.Fatal("ordinary catalog changed")
	}
	for _, model := range []core.MediaModel{frames, text, noPrompt, empty} {
		d := templates.Definition{MediaType: model.MediaType, Provider: "arbitrary-connection", Model: model.ID, Options: core.MediaOptions{ReferenceMode: "references"}}
		body, _ := json.Marshal(map[string]any{"version": 0, "draft": d})
		if created := sendTest(s.handler(), "POST", "/v1/system/templates", string(body), "alice", ""); created.Code != 400 {
			t.Fatal("ineligible model created", created.Code)
		}
	}
}

func TestLegacyFrameRecipeRequiresExplicitRevision(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	model := templateTestModel()
	model.ReferenceModes = append(model.ReferenceModes, core.ReferenceMode{ID: "first_last_frames", MinImages: 2, MaxImages: 2, ImageRoles: []string{"first_frame", "last_frame"}})
	svc.catalog = []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{model}}}
	id := uuid.NewString()
	d := templates.Definition{Title: "Retained legacy frames", MediaType: "video", Provider: "mock", Model: model.ID, Prompt: "Image 1 follows Image 2", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "first_last_frames"}}
	d.Slots, _ = templates.Infer(d.Prompt, d.Syntax, model.ReferencePromptTypes, model.ReferenceModes[1])
	repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
	d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
	repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
	gallery := sendTest(s.handler(), "GET", "/v1/templates", "", "", "service-secret")
	if gallery.Code != 200 || strings.Contains(gallery.Body.String(), id) {
		t.Fatal("legacy workflow offered", gallery.Body.String())
	}
	admin := sendTest(s.handler(), "GET", "/v1/system/templates/"+id, "", "alice", "")
	if admin.Code != 200 || !strings.Contains(admin.Body.String(), "first_last_frames") {
		t.Fatal("legacy authoring record lost")
	}
	view := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", "service-secret")
	if view.Code != 200 || !strings.Contains(view.Body.String(), `"default_available":false`) || !strings.Contains(view.Body.String(), `"workflow_error":"invalid template:`) {
		t.Fatal(view.Code, view.Body.String())
	}
	for _, endpoint := range []string{"estimate", "generations"} {
		w := sendTest(s.handler(), "POST", "/v1/templates/"+id+"/"+endpoint, `{"revision":2,"provider":"mock","model":"dance","inputs":{}}`, "", "service-secret")
		if w.Code != 400 || svc.calls != 0 {
			t.Fatal("legacy admitted", w.Code, w.Body.String())
		}
	}
	if repo.saves != 0 || repo.items[id].Published.Options.ReferenceMode != "first_last_frames" {
		t.Fatal("legacy meaning changed")
	}
	// Unpublishing is always possible, even when a saved recipe is ineligible.
	body, _ := json.Marshal(map[string]any{"version": 2, "action": "unpublish", "draft": d})
	if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if repo.items[id].Draft.Options.ReferenceMode != "first_last_frames" || repo.items[id].Published != nil {
		t.Fatal("unpublish rewrote draft")
	}
	// Conversion requires an explicit mode change and newly reviewed slot roles.
	d.Options.ReferenceMode = "references"
	d.Slots, _ = d.InferSlots(model)
	body, _ = json.Marshal(map[string]any{"version": 3, "action": "publish", "draft": d})
	if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if repo.items[id].Published.Options.ReferenceMode != "references" {
		t.Fatal("explicit conversion failed")
	}
}

func TestTemplatesRequireReferencesAtInferenceAndPublication(t *testing.T) {
	for _, marker := range []bool{true, false} {
		s, repo, svc, _ := templateFixture(t)
		model := templateTestModel()
		if !marker {
			model.ReferencePromptSyntax = ""
		}
		svc.catalog = []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{model}}}
		id := uuid.NewString()
		d := templates.Definition{Title: "Missing references", MediaType: "video", Provider: "mock", Model: model.ID, Prompt: "A calm landscape", Syntax: model.ReferencePromptSyntax, Options: core.MediaOptions{ReferenceMode: "references"}}
		repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
		d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
		body, _ := json.Marshal(d)
		if w := sendTest(s.handler(), "POST", "/v1/system/templates/"+id+"/infer", string(body), "alice", ""); w.Code != 400 {
			t.Fatal("empty inference", w.Code)
		}
		body, _ = json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
		if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 400 || repo.saves != 0 {
			t.Fatal("empty publication", w.Code)
		}
	}
}

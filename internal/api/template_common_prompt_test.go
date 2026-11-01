package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestCommonTemplatePromptTransmission(t *testing.T) {
	for _, prompt := range []string{"Use <PiCtUrE_1>. Literal <img src=x> and @Image 1 stay text.", "Use [Image1], {Picture 1} and @IMAGE_1; repeated mentions share one slot."} {
		t.Run(prompt, func(t *testing.T) {
			s, repo, svc, _ := templateFixture(t)
			id := uuid.NewString()
			d := templates.Definition{Title: "Common references", Provider: "mock", Model: "dance", MediaType: "video", Syntax: templates.UIReferenceSyntax, Prompt: prompt, Options: core.MediaOptions{ReferenceMode: "references"}}
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
			b, _ := json.Marshal(d)
			w := sendTest(s.handler(), "POST", "/v1/system/templates/"+id+"/infer", string(b), "alice", "")
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var inferred struct {
				Syntax string           `json:"syntax"`
				Slots  []templates.Slot `json:"slots"`
			}
			if json.Unmarshal(w.Body.Bytes(), &inferred) != nil || inferred.Syntax != templates.UIReferenceSyntax || len(inferred.Slots) != 1 {
				t.Fatal(w.Body.String())
			}
			d.Slots = inferred.Slots
			b, _ = json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
			w = sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(b), "alice", "")
			if w.Code != 200 || repo.items[id].Published.Prompt != prompt {
				t.Fatal(w.Code, w.Body.String())
			}
			ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
			ref, e := s.references.Upload(ctx, "face.png", bytes.NewReader(referencePNG(t)))
			if e != nil {
				t.Fatal(e)
			}
			_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Synthetic"}`)
			b, _ = json.Marshal(map[string]any{"revision": 2, "provider": "mock", "model": "dance", "inputs": map[string]string{"image:1": ref.ID}, "max_expected_cost_usd": 0})
			request := httptest.NewRequest("POST", "/v1/templates/"+id+"/generations", bytes.NewReader(b))
			request.Header.Set("Authorization", "Bearer "+key)
			request.Header.Set("Idempotency-Key", "synthetic-common-confirmation")
			w = httptest.NewRecorder()
			s.handler().ServeHTTP(w, request)
			if w.Code != 202 || svc.input.Prompt != prompt || svc.calls != 1 {
				t.Fatal(w.Code, w.Body.String(), svc.input)
			}
		})
	}
}
func TestPublishedTemplateSaveAppliesValidatedPromptSyntax(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	id := uuid.NewString()
	m := templateTestModel()
	old := templates.Definition{Title: "Existing", Provider: "mock", Model: "dance", MediaType: "video", Syntax: "numbered", Prompt: "Image 1 moves", Options: core.MediaOptions{ReferenceMode: "references"}}
	old.Slots, _ = old.InferSlots(m)
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: old}
	old.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
	repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: old, Published: &old}
	next := old
	next.Syntax = templates.UIReferenceSyntax
	next.Prompt = "New @Picture_1 moves"
	next.Slots, _ = next.InferSlots(m)
	b, _ := json.Marshal(map[string]any{"version": 2, "draft": next})
	w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(b), "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	record := repo.items[id]
	if record.Published.Prompt != next.Prompt || record.Published.Syntax != templates.UIReferenceSyntax || record.Draft.Syntax != templates.UIReferenceSyntax || !strings.Contains(record.Draft.Prompt, "@Picture_1") {
		t.Fatal(record)
	}
	if err := record.Published.Compatible(m); err != nil {
		t.Fatal("saved current content unavailable", err)
	}
}

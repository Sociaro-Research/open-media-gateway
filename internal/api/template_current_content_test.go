package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplateCurrentContentSaveVisibilityAndAdmission(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	s.service = &videoDurationService{openAIServiceFixture: svc}
	model := templateTestModel()
	model.Ratios = []string{"16:9", "9:16"}
	model.SupportsAudio = true
	svc.catalog[0].Models = []core.MediaModel{model}
	id := uuid.NewString()
	d := templates.Definition{Title: "Current template", Provider: "mock", Model: model.ID, MediaType: "video", Prompt: "Image 1 moves", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Ratio: "16:9", Resolution: "720p"}}
	d.Slots, _ = d.InferSlots(model)
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	asset := templateCopy(t, s, id, referencePNG(t))
	audio := templateCopy(t, s, id, templateAudioWAV(1))
	d.CoverID = asset.ID
	d.Slots[0].AssetID = asset.ID
	d.OverrideSoundID = audio.ID
	write := func(body any) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		return sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(b), "alice", "")
	}
	assertWire := func(w *httptest.ResponseRecorder, published bool) {
		t.Helper()
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var v map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v["is_published"] != published || !reflect.DeepEqual(v["content"], v["draft"]) {
			t.Fatal("canonical aliases diverged", w.Body.String())
		}
		if published && !reflect.DeepEqual(v["content"], v["published"]) {
			t.Fatal("published alias diverged")
		}
		if !published && v["published"] != nil {
			t.Fatal("unpublished alias exposed")
		}
	}
	assertWire(write(map[string]any{"version": 1, "action": "publish", "draft": d}), true)
	oldToken := repo.items[id].Revision
	_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Current template fixture"}`)
	call := func(token int64, endpoint string) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"revision": token, "provider": "mock", "model": model.ID, "inputs": map[string]string{}, "max_expected_cost_usd": 0})
		r := httptest.NewRequest("POST", "/v1/templates/"+id+"/"+endpoint, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Idempotency-Key", uuid.NewString())
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	generate := func(token int64) *httptest.ResponseRecorder { return call(token, "generations") }
	if w := generate(oldToken); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	acceptedPrompt := svc.input.Prompt
	acceptedSnapshot := append([]byte(nil), svc.input.TemplateSnapshot...)
	acceptedOptions, _ := json.Marshal(svc.input.Options)
	// A published Save is immediately current, including settings and sound clearing.
	d.Prompt = "Image 1 now jumps"
	d.Options.Duration = 9
	d.Options.Ratio = "9:16"
	d.OverrideSoundID = ""
	assertWire(write(map[string]any{"version": 2, "content": d}), true)
	saved := repo.items[id]
	if saved.Revision <= oldToken {
		t.Fatal("published change token did not advance")
	}
	for _, endpoint := range []string{"generations", "estimate", "validate-media", "editing"} {
		if w := call(oldToken, endpoint); w.Code != 409 {
			t.Fatal("stale template request admitted", endpoint, w.Code, w.Body.String())
		}
	}
	if w := generate(saved.Revision); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if svc.input.Prompt != d.Prompt || svc.input.Options.Duration != 9 || svc.input.Options.Ratio != "9:16" || svc.input.Options.OverrideSoundID != "" || len(svc.input.Options.ImageURLs) != 1 {
		t.Fatal("new generation did not use current content", svc.input)
	}
	if acceptedPrompt != "Image 1 moves" || !bytes.Contains(acceptedSnapshot, []byte(`"revision":2`)) || !bytes.Contains(acceptedOptions, []byte(audio.ID)) {
		t.Fatal("old admitted snapshot not independently captured")
	}
	before, _ := json.Marshal(repo.items[id])
	beforeSaves := repo.saves
	invalid := []templates.Definition{}
	bad := d
	bad.CoverID = ""
	invalid = append(invalid, bad)
	bad = d
	bad.CoverID = uuid.NewString()
	invalid = append(invalid, bad)
	bad = d
	bad.CoverID = audio.ID
	invalid = append(invalid, bad)
	bad = d
	bad.Options.Duration = 999
	invalid = append(invalid, bad)
	bad = d
	bad.OverrideSoundID = uuid.NewString()
	invalid = append(invalid, bad)
	bad = d
	bad.Prompt = "Image 2 undeclared"
	invalid = append(invalid, bad)
	for i, candidate := range invalid {
		w := write(map[string]any{"version": saved.Version, "content": candidate})
		if w.Code < 400 {
			t.Fatal("invalid published Save accepted", i)
		}
		after, _ := json.Marshal(repo.items[id])
		if !bytes.Equal(before, after) || repo.saves != beforeSaves {
			t.Fatal("invalid Save mutated current content", i)
		}
	}
	if w := write(map[string]any{"version": 2, "content": d}); w.Code != 409 {
		t.Fatal("stale editor accepted", w.Code)
	}
	conflicting := d
	conflicting.Title = "Different alias"
	if w := write(map[string]any{"version": saved.Version, "content": d, "draft": conflicting}); w.Code != 400 {
		t.Fatal("conflicting aliases accepted", w.Code)
	}
	// Toggle only changes visibility; an unpublished Save never switches it on.
	assertWire(write(map[string]any{"version": saved.Version, "action": "unpublish"}), false)
	d.Prompt = "Image 1 offline edit"
	d.CoverID = ""
	assertWire(write(map[string]any{"version": repo.items[id].Version, "draft": d}), false)
	if w := generate(repo.items[id].Revision); w.Code != 404 {
		t.Fatal("unpublished generation admitted", w.Code)
	}
	if w := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", key); w.Code != 404 {
		t.Fatal("unpublished template visible")
	}
	if w := write(map[string]any{"version": repo.items[id].Version, "action": "publish"}); w.Code < 400 || repo.items[id].Published != nil {
		t.Fatal("publish without explicit cover accepted", w.Code)
	}
	d.CoverID = asset.ID
	assertWire(write(map[string]any{"version": repo.items[id].Version, "content": d, "draft": d}), false)
	assertWire(write(map[string]any{"version": repo.items[id].Version, "action": "publish"}), true)
	if w := generate(repo.items[id].Revision); w.Code != 202 || svc.input.Prompt != d.Prompt {
		t.Fatal("toggle did not expose current saved content", w.Code)
	}
}

func TestTemplateCurrentContentCanonicalCreateCompatibility(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	model := templateTestModel()
	d := templates.Definition{Title: "One current definition", Provider: "mock", Model: model.ID, MediaType: "video", Prompt: "Image 1 moves", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Ratio: "16:9", Resolution: "720p"}}
	d.Slots, _ = d.InferSlots(model)
	id := uuid.NewString()
	b, _ := json.Marshal(map[string]any{"id": id, "content": d})
	w := sendTest(s.handler(), "POST", "/v1/system/templates", string(b), "alice", "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if repo.items[id].Published != nil || repo.items[id].Draft.Title != d.Title {
		t.Fatal("create did not save unpublished current content")
	}
	// Existing legacy clients still receive the same content through draft.
	var response struct {
		Content     templates.Definition `json:"content"`
		Draft       templates.Definition `json:"draft"`
		IsPublished bool                 `json:"is_published"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.IsPublished || !reflect.DeepEqual(response.Content, response.Draft) {
		t.Fatal("create aliases diverged", err)
	}
}

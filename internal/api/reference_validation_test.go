package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	providers "github.com/Sociaro-Research/open-media-gateway/plugins/provider"
	"github.com/google/uuid"
)

func validationUpload(t *testing.T, h http.Handler, path, key, actor, name string, data []byte, target *referencevalidation.Target) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	part, _ := m.CreateFormFile("file", name)
	part.Write(data)
	if target != nil {
		raw, _ := json.Marshal(target)
		m.WriteField("validation", string(raw))
	}
	m.Close()
	r := httptest.NewRequest("POST", path, &body)
	r.Header.Set("Content-Type", m.FormDataContentType())
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if actor != "" {
		r.AddCookie(&http.Cookie{Name: "test_session", Value: actor})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestReferenceValidationBeforeRetentionAndOwnedSelection(t *testing.T) {
	s, _, svc, _ := templateFixture(t)
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Validation"}`)
	target := &referencevalidation.Target{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references"}}
	// Actual timing shape from the report, independently synthesized without content.
	bad := testmedia.MP4(125956, 12288, 244)
	out := validationUpload(t, h, "/v1/references", key, "", "timing.mp4", bad, target)
	if out.Code != 400 || !strings.Contains(out.Body.String(), "23.8041220744") || !strings.Contains(out.Body.String(), "24–60 FPS") {
		t.Fatal(out.Code, out.Body.String())
	}
	refs, _ := s.references.List(identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"})), 10, 0)
	if len(refs) != 0 {
		t.Fatal("rejected upload retained", refs)
	}
	out = validationUpload(t, h, "/v1/references", key, "", "timing.mp4", bad, nil)
	if out.Code != 201 {
		t.Fatal("generic library imposed a model FPS minimum", out.Code, out.Body.String())
	}
	var saved referenceResponse
	json.Unmarshal(out.Body.Bytes(), &saved)
	req := func(id string) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"validation": target, "reference_ids": []string{id}})
		return sendTest(h, "POST", "/v1/references/validate", string(raw), "", key)
	}
	out = req(saved.ID)
	if out.Code != 400 {
		t.Fatal("stored file bound without validation", out.Code, out.Body.String())
	}
	if svc.calls != 0 {
		t.Fatal("validation admitted a task")
	}
	bob := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "bob"}))
	foreign, err := s.references.Upload(bob, "private.png", bytes.NewReader(referencePNG(t)))
	if err != nil {
		t.Fatal(err)
	}
	out = req(foreign.ID)
	if out.Code != 404 || strings.Contains(out.Body.String(), "private.png") {
		t.Fatal("foreign reference disclosed", out.Code, out.Body.String())
	}
	out = sendTest(h, "POST", "/v1/references/validate", `{}`, "alice", "")
	if out.Code != 401 {
		t.Fatal("session authorized generation reference validation", out.Code)
	}
}

func TestReferenceValidationMandatoryWorkflowAndVoiceRole(t *testing.T) {
	s, _, svc, _ := templateFixture(t)
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Validation"}`)
	m := templateTestModel()
	m.ReferenceModes = append(m.ReferenceModes, core.ReferenceMode{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1})
	svc.catalog[0].Models[0] = m
	target := &referencevalidation.Target{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "first_frame"}}
	out := validationUpload(t, h, "/v1/references", key, "", "frame.png", referencePNG(t), target)
	if out.Code != 201 {
		t.Fatal("valid required-frame upload failed", out.Code, out.Body.String())
	}
	def, _ := providers.Find("alibaba")
	for _, p := range def.Profiles() {
		if p.ID == "wan2.7-r2v" {
			m = p
		}
	}
	svc.catalog = []core.ProviderDescriptor{{ID: "custom", Models: []core.MediaModel{m}}}
	target = &referencevalidation.Target{Provider: "custom", Model: m.ID, Options: core.MediaOptions{ReferenceMode: "references"}, Role: "reference_voice"}
	out = validationUpload(t, h, "/v1/references", key, "", "voice.wav", testmedia.WAV(), target)
	if out.Code != 201 {
		t.Fatal("per-visual voice consumed a free audio slot", out.Code, out.Body.String())
	}
	target.Role = ""
	out = validationUpload(t, h, "/v1/references", key, "", "voice.wav", testmedia.WAV(), target)
	if out.Code != 400 {
		t.Fatal("ordinary Audio N accepted", out.Code, out.Body.String())
	}
	target.Role = "reference_voice"
	target.Options.ReferenceMode = "first_frame"
	out = validationUpload(t, h, "/v1/references", key, "", "voice.wav", testmedia.WAV(), target)
	if out.Code != 400 {
		t.Fatal("unsupported workflow voice accepted", out.Code, out.Body.String())
	}
}

func TestTemplateMediaCheckedAtUploadBindingAndDraftSave(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	h := s.handler()
	id := uuid.NewString()
	d := templates.Definition{Title: "Incomplete", Provider: "mock", Model: "dance", MediaType: "video", Options: core.MediaOptions{ReferenceMode: "references"}, Slots: []templates.Slot{{ID: "video:1", Type: "video", Index: 1, Role: "reference", Label: "Motion"}}}
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	target := &referencevalidation.Target{Provider: d.Provider, Model: d.Model, Options: d.Options}
	bad := testmedia.MP4(125956, 12288, 244)
	out := validationUpload(t, h, "/v1/system/templates/"+id+"/assets", "", "alice", "bad.mp4", bad, target)
	if out.Code != 400 || len(repo.assets) != 0 {
		t.Fatal("invalid fixed upload persisted", out.Code, len(repo.assets), out.Body.String())
	}
	// Existing, model-neutral copies are checked before binding/save as well.
	out = validationUpload(t, h, "/v1/system/templates/"+id+"/assets", "", "alice", "old.mp4", bad, nil)
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body.String())
	}
	var asset templates.Asset
	json.Unmarshal(out.Body.Bytes(), &asset)
	d.Slots[0].AssetID = asset.ID
	raw, _ := json.Marshal(map[string]any{"draft": d})
	out = sendTest(h, "POST", "/v1/system/templates/"+id+"/validate-media", string(raw), "alice", "")
	if out.Code != 400 || repo.saves != 0 {
		t.Fatal("binding check mutated or accepted bad media", out.Code, out.Body.String())
	}
	raw, _ = json.Marshal(map[string]any{"version": 1, "draft": d})
	out = sendTest(h, "PUT", "/v1/system/templates/"+id, string(raw), "alice", "")
	if out.Code != 400 || repo.saves != 0 {
		t.Fatal("draft save bypassed media check", out.Code, out.Body.String())
	}
	out = sendTest(h, "POST", "/v1/system/templates/"+id+"/validate-media", `{}`, "bob", "")
	if out.Code != 403 {
		t.Fatal("non-admin checked private template", out.Code)
	}
}

func TestGenerationReferenceValidationPreservesExternalURLsAndInputTypes(t *testing.T) {
	s, _, _, _ := templateFixture(t)
	ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
	input := core.CreateMediaInput{Provider: "mock", Model: "dance", Options: core.MediaOptions{ReferenceMode: "references", ImageURLs: []string{"https://vendor.example/references/character/video.mp4"}}}
	r := httptest.NewRequest("POST", "https://gateway.example/v1/generations", nil).WithContext(ctx)
	if err := s.validateGenerationReferences(r, input); err != nil {
		t.Fatal("external path was claimed as a local reference", err)
	}
	saved, err := s.references.Upload(ctx, "video.mp4", bytes.NewReader(testmedia.MP4(5000, 1000, 150)))
	if err != nil {
		t.Fatal(err)
	}
	input.Options.ImageURLs = []string{s.referenceAbsoluteURL(r, saved)}
	if err = s.validateGenerationReferences(r, input); err == nil || !strings.Contains(err.Error(), "video media cannot be used in a image input") {
		t.Fatal("wrong typed binding accepted", err)
	}
}

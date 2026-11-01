package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplateEditingSoundAndSettingsAdmission(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	s.service = &videoDurationService{openAIServiceFixture: svc}
	model := templateTestModel()
	model.Resolutions = []string{"720p", "1080p"}
	model.Ratios = []string{"16:9", "9:16"}
	model.SupportsAudio = true
	svc.catalog[0].Models = []core.MediaModel{model}
	h := s.handler()
	_, key := issueTestKey(t, h, "alice", `{"name":"Combined handoff"}`)
	id := uuid.NewString()
	audioEnabled := true
	d := templates.Definition{Title: "Combined handoff", Provider: "mock", Model: model.ID, MediaType: "video", Prompt: "Image 1 follows Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "9:16", Resolution: "1080p", Duration: 9, GenerateAudio: &audioEnabled}}
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	image := templateCopy(t, s, id, referencePNG(t))
	video := templateCopy(t, s, id, templateVideo())
	audio := templateCopy(t, s, id, templateAudioWAV(1))
	d.CoverID = image.ID
	d.Slots = []templates.Slot{{ID: "image:1", Type: "image", Index: 1, Role: "reference", Label: "Subject", AssetID: image.ID}, {ID: "video:1", Type: "video", Index: 1, Role: "reference", Label: "Motion", AssetID: video.ID}}
	for _, soundID := range []string{audio.ID, ""} {
		d.OverrideSoundID = soundID
		body, _ := json.Marshal(map[string]any{"version": repo.items[id].Version, "action": "publish", "draft": d})
		if w := sendTest(h, "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		body, _ = json.Marshal(map[string]any{"revision": repo.items[id].Revision, "provider": "mock", "model": model.ID, "inputs": map[string]string{}})
		w := sendTest(h, "POST", "/v1/templates/"+id+"/editing", string(body), "", key)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var snapshot struct {
			Prompt    string            `json:"prompt"`
			Provider  string            `json:"provider"`
			Model     string            `json:"model"`
			MediaType string            `json:"media_type"`
			Options   core.MediaOptions `json:"options"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Options.ImageURLs) != 1 || !strings.Contains(snapshot.Options.ImageURLs[0], image.ID) || len(snapshot.Options.VideoURLs) != 1 || !strings.Contains(snapshot.Options.VideoURLs[0], video.ID) {
			t.Fatal("handoff lost signed fixed media", w.Body.String())
		}
		if snapshot.Options.Duration != 9 || snapshot.Options.Resolution != "1080p" || snapshot.Options.Ratio != "9:16" || snapshot.Options.GenerateAudio == nil || !*snapshot.Options.GenerateAudio {
			t.Fatalf("editing response lost settings: %+v", snapshot.Options)
		}
		// Match the same-revision published-ID mapping used by the browser handoff.
		snapshot.Options.OverrideSoundID = repo.items[id].Published.OverrideSoundID
		options := map[string]any{"reference_mode": snapshot.Options.ReferenceMode, "ratio": snapshot.Options.Ratio, "generate_audio": snapshot.Options.GenerateAudio, "video_urls": snapshot.Options.VideoURLs}
		if soundID != "" {
			options["override_sound_id"] = soundID
		}
		body, _ = json.Marshal(map[string]any{"model": "mock/" + snapshot.Model, "prompt": snapshot.Prompt, "seconds": "9", "size": "1080p", "input_reference": snapshot.Options.ImageURLs, "options": options})
		r := httptest.NewRequest("POST", "/v1/generations", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Idempotency-Key", uuid.NewString())
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 && w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		o := svc.input.Options
		if o.Duration != 9 || o.Resolution != "1080p" || o.Ratio != "9:16" || o.GenerateAudio == nil || !*o.GenerateAudio {
			t.Fatalf("settings lost at admission: %+v", o)
		}
		if soundID == "" {
			if o.OverrideSoundID != "" || o.OverrideSound != nil {
				t.Fatal("previous audio leaked into sound-free handoff")
			}
		} else if o.OverrideSound == nil || o.OverrideSound.ReferenceID != audio.ID || o.OverrideSound.SHA256 != audio.SHA256 || o.OverrideSound.OwnerID != templates.LibraryOwnerID {
			t.Fatal("shared audio was not resolved at admission")
		}
	}
}

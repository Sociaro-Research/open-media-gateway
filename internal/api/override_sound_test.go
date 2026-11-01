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

func TestOverrideSoundAuthorizesPrivateAndSharedSources(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
	audio, err := s.references.Upload(ctx, "sound.wav", bytes.NewReader(templateAudioWAV(1)))
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"alice", "bob"} {
		r := httptest.NewRequest("POST", "/v1/videos", nil).WithContext(identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: actor})))
		in := core.CreateMediaInput{MediaType: "video", Options: core.MediaOptions{OverrideSoundID: audio.ID, OverrideSound: &core.SoundOverride{StorageKey: "spoofed"}}}
		err := s.resolveOverrideSound(r, &in)
		if actor == "bob" {
			if err == nil || in.Options.OverrideSound != nil {
				t.Fatal("cross-owner source admitted")
			}
			continue
		}
		if err != nil || in.Options.OverrideSound.StorageKey == "spoofed" || in.Options.OverrideSound.SHA256 != audio.SHA256 {
			t.Fatal("source not safely resolved", err)
		}
		in.MediaType = "image"
		if s.resolveOverrideSound(r, &in) == nil {
			t.Fatal("image override accepted")
		}
	}
	id := uuid.NewString()
	repo.items[id] = templates.Record{ID: id, Version: 1}
	shared := templateCopy(t, s, id, templateAudioWAV(1))
	r := httptest.NewRequest("POST", "/v1/videos", nil).WithContext(identity.WithOwnerID(context.Background(), "bob"))
	in := core.CreateMediaInput{MediaType: "video", Options: core.MediaOptions{OverrideSoundID: shared.ID}}
	if err := s.resolveOverrideSound(r, &in); err != nil {
		t.Fatal(err)
	}
	if in.Options.OverrideSound.OwnerID != templates.LibraryOwnerID {
		t.Fatal("wrong shared namespace")
	}
	raw, _ := json.Marshal(core.GenerationView(core.Task{Options: in.Options}))
	if strings.Contains(string(raw), "_override_sound") || strings.Contains(string(raw), "storage_key") {
		t.Fatal("internal source disclosed", string(raw))
	}
}

func TestTemplateOverrideSoundPersistsAndAppliesToAPI(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	id := uuid.NewString()
	d := templates.Definition{Title: "Sound", MediaType: "video", Provider: "mock", Model: "dance", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references"}}
	d.Slots, _ = templates.Infer(d.Prompt, d.Syntax, []string{"image", "video"}, templateTestModel().ReferenceModes[0])
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	image := templateCopy(t, s, id, referencePNG(t))
	video := templateCopy(t, s, id, templateVideo())
	audio := templateCopy(t, s, id, templateAudioWAV(1))
	for i := range d.Slots {
		d.Slots[i].AssetID = image.ID
		if d.Slots[i].Type == "video" {
			d.Slots[i].AssetID = video.ID
		}
	}
	d.CoverID = image.ID
	d.OverrideSoundID = audio.ID
	body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
	w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved := repo.items[id]
	if saved.Published.OverrideSoundID != audio.ID {
		t.Fatal("source was not persisted")
	}
	r := httptest.NewRequest("POST", "/v1/templates/"+id+"/generations", nil).WithContext(identity.WithOwnerID(context.Background(), "alice"))
	in, err := s.resolveTemplate(r, saved, templateGenerationInput{Revision: saved.Revision, Provider: "mock", Model: "dance"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Options.OverrideSoundID != audio.ID {
		t.Fatal("template API omitted override")
	}
	if err = s.resolveOverrideSound(r, &in); err != nil {
		t.Fatal(err)
	}
	saved.Draft.OverrideSoundID = "" // Editing the recipe cannot change the admitted snapshot.
	if in.Options.OverrideSound.ReferenceID != audio.ID {
		t.Fatal("snapshot changed")
	}
}

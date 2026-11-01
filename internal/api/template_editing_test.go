package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/google/uuid"
)

func TestTemplateEditingPreservesPartialBindingsAndIsolation(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	h := s.handler()
	id := uuid.NewString()
	d := templates.Definition{Title: "Editing", MediaType: "video", Provider: "mock", Model: "dance", Prompt: "Image 1 and Image 2 follow Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Ratio: "16:9", Resolution: "720p"}}
	d.Slots, _ = templates.Infer(d.Prompt, "numbered", []string{"image", "video"}, templateTestModel().ReferenceModes[0])
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	video := templateCopy(t, s, id, templateVideo())
	cover := templateCopy(t, s, id, referencePNG(t))
	d.Slots[2].AssetID = video.ID
	d.CoverID = cover.ID
	body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
	if out := sendTest(h, "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
	ref, e := s.references.Upload(ctx, "face.png", bytes.NewReader(referencePNG(t)))
	if e != nil {
		t.Fatal(e)
	}
	_, aliceKey := issueTestKey(t, h, "alice", `{"name":"Editing"}`)
	_, bobKey := issueTestKey(t, h, "bob", `{"name":"Editing"}`)
	keys := map[string]string{"alice": aliceKey, "bob": bobKey}
	saves := repo.saves
	request := func(actor string, revision int, inputs map[string]string) (int, string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"revision": revision, "provider": "mock", "model": "dance", "inputs": inputs})
		out := sendTest(h, "POST", "/v1/templates/"+id+"/editing", string(b), "", keys[actor])
		return out.Code, out.Body.String()
	}
	code, text := request("alice", 2, map[string]string{"image:2": ref.ID})
	if code != 200 {
		t.Fatal(code, text)
	}
	var result struct {
		Prompt  string            `json:"prompt"`
		Options core.MediaOptions `json:"options"`
	}
	if e = json.Unmarshal([]byte(text), &result); e != nil {
		t.Fatal(e)
	}
	if result.Prompt != d.Prompt || len(result.Options.ImageURLs) != 2 || result.Options.ImageURLs[0] != "" || !strings.Contains(result.Options.ImageURLs[1], ref.ID) || len(result.Options.VideoURLs) != 1 || !strings.Contains(result.Options.VideoURLs[0], "/references/"+templates.LibraryOwnerID+"/"+video.ID) || result.Options.Duration != 5 || result.Options.Ratio != "16:9" || result.Options.Resolution != "720p" {
		t.Fatal(text)
	}
	for _, raw := range []string{result.Options.ImageURLs[1], result.Options.VideoURLs[0]} {
		parsed, e := url.Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		out := sendTest(h, "GET", parsed.RequestURI(), "", "", "")
		if out.Code != 200 {
			t.Fatal("transferred capability unreadable", out.Code, out.Body.String())
		}
	}
	if code, text = request("alice", 2, map[string]string{"image:1": ref.ID, "image:2": ref.ID}); code != 200 {
		t.Fatal(code, text)
	}
	json.Unmarshal([]byte(text), &result)
	if result.Options.ImageURLs[0] != result.Options.ImageURLs[1] {
		t.Fatal("duplicate binding lost", text)
	}
	for _, tc := range []struct {
		actor    string
		revision int
		inputs   map[string]string
		want     int
	}{{"bob", 2, map[string]string{"image:1": ref.ID}, 404}, {"alice", 1, nil, 409}, {"alice", 2, map[string]string{"video:1": ref.ID}, 400}, {"alice", 2, map[string]string{"extra": ref.ID}, 400}} {
		if code, text = request(tc.actor, tc.revision, tc.inputs); code != tc.want {
			t.Fatal(tc, code, text)
		}
	}
	if repo.saves != saves || svc.input.Prompt != "" {
		t.Fatal("editing mutated template or admitted generation")
	}
}

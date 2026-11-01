package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func TestTemplateThreeVideoIdentityAndPublication(t *testing.T) {
	s, repo, _, _ := templateFixture(t)
	h := s.handler()
	id := uuid.NewString()
	d := templates.Definition{Title: "Identity control", Provider: "mock", Model: "dance", MediaType: "video", Prompt: "Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5}}
	d.Slots, _ = d.InferSlots(templateTestModel())
	repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
	clips := os.Getenv("GATEWAY_TEMPLATE_CLIP_DIR")
	load := func(name string, fallback []byte) []byte {
		if clips == "" {
			return fallback
		}
		b, e := os.ReadFile(filepath.Join(clips, name+".mp4"))
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	aBytes := load("cfr24", testmedia.MP4(5000, 1000, 150))
	cBytes := load("third", testmedia.MP4(5000, 1000, 180))
	a := templateCopy(t, s, id, aBytes)
	d.CoverID = a.ID
	d.Slots[0].AssetID = a.ID
	write := func(version int, action string) {
		body, _ := json.Marshal(map[string]any{"version": version, "action": action, "draft": d})
		w := sendTest(h, "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
		if w.Code != 200 {
			t.Fatal(action, w.Code, w.Body.String())
		}
	}
	write(1, "publish")
	target := &referencevalidation.Target{Provider: "mock", Model: "dance", Options: d.Options}
	for _, clip := range []struct {
		name string
		data []byte
	}{{"first", load("first", testmedia.MP4(5000, 1000, 100))}, {"second", load("second", testmedia.MP4(13000, 1000, 390))}} {
		before := len(repo.assets)
		for _, path := range []string{"/v1/system/template-references", "/v1/system/templates/" + id + "/assets"} {
			w := validationUpload(t, h, path, "", "alice", clip.name+".mp4", clip.data, target)
			if w.Code != 400 || len(repo.assets) != before {
				t.Fatal("rejection persisted an asset", w.Code)
			}
			t.Log(clip.name, path, w.Body.String())
		}
	}
	w := validationUpload(t, h, "/v1/system/templates/"+id+"/assets", "", "alice", "third.mp4", cBytes, target)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var c templates.Asset
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if c.SHA256 != fmt.Sprintf("%x", sha256.Sum256(cBytes)) || a.ID == c.ID || a.SHA256 == c.SHA256 {
		t.Fatal("identity reused")
	}
	d.Slots[0].AssetID = c.ID
	write(2, "")
	if repo.items[id].Published.Slots[0].AssetID != a.ID || repo.items[id].Draft.Slots[0].AssetID != c.ID || repo.items[id].Revision != 2 {
		t.Fatal("draft save changed published bytes")
	}
	before := sendTest(h, "GET", "/v1/templates/"+id, "", "", "service-secret")
	if strings.Contains(before.Body.String(), c.ID) {
		t.Fatal("draft replacement exposed before publication", before.Body.String())
	}
	write(3, "publish")
	if repo.items[id].Published.Slots[0].AssetID != c.ID || repo.items[id].Published.CoverID != a.ID || repo.items[id].Revision != 4 {
		t.Fatal("slot and cover mixed")
	}
	gallery := sendTest(h, "GET", "/v1/templates", "", "", "service-secret")
	if !strings.Contains(gallery.Body.String(), a.ID) || strings.Contains(gallery.Body.String(), c.ID) {
		t.Fatal("gallery stopped using independent cover", gallery.Body.String())
	}
	for _, tc := range []struct {
		asset templates.Asset
		data  []byte
	}{{a, aBytes}, {c, cBytes}} {
		out := httptest.NewRecorder()
		h.ServeHTTP(out, httptest.NewRequest(http.MethodGet, s.templateAssetURL(tc.asset.ID), nil))
		if out.Code != 200 || !bytes.Equal(out.Body.Bytes(), tc.data) {
			t.Fatal("consumer bytes changed", out.Code, tc.asset.ID)
		}
	}
	detail := sendTest(h, "GET", "/v1/templates/"+id, "", "", "service-secret")
	var view struct {
		Fixed []struct {
			SlotID  string `json:"slot_id"`
			AssetID string `json:"asset_id"`
			Name    string `json:"name"`
			URL     string `json:"url"`
		} `json:"fixed_references"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Fixed) != 1 || view.Fixed[0].AssetID != c.ID || view.Fixed[0].Name != "third.mp4" || !strings.Contains(view.Fixed[0].URL, c.ID) {
		t.Fatal("consumer preview identity", detail.Body.String())
	}
	for _, name := range []string{"cfr24", "cfr23976"} {
		if clips == "" {
			continue
		}
		data := load(name, nil)
		info, err := mediafile.Inspect(data, "video/mp4")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s average FPS %.12g", name, info.FPS)
		if name == "cfr24" && info.FPS != 24 || name == "cfr23976" && info.FPS >= 24 {
			t.Fatal(info)
		}
	}
	t.Log("PASS rejected uploads did not persist; accepted third asset kept distinct hash/ID/bytes; draft retains old publication, explicit publish changes slot while cover remains first video")
}

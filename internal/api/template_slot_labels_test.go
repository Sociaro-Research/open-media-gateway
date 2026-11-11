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

func TestTemplateSlotLabelPublicationAndDrafts(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "publish"
		if published {
			name = "publishedSave"
		}
		t.Run(name, func(t *testing.T) {
			s, repo, svc, _ := templateFixture(t)
			s.service = &videoDurationService{openAIServiceFixture: svc}
			model := templateTestModel()
			svc.catalog[0].Models = []core.MediaModel{model}
			id := uuid.NewString()
			d := templates.Definition{Title: "Labels", Provider: "mock", Model: model.ID, MediaType: "video", Prompt: "Image 1 moves", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Ratio: "16:9", Resolution: "720p"}}
			d.Slots, _ = d.InferSlots(model)
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			cover := templateCopy(t, s, id, referencePNG(t))
			d.CoverID, d.Slots[0].AssetID = cover.ID, cover.ID
			record := templates.Record{ID: id, Version: 1, Draft: d}
			if published {
				record.Published = &d
				record.Revision = 1
			}
			repo.items[id] = record
			write := func(content templates.Definition, action string) *httptest.ResponseRecorder {
				t.Helper()
				data, _ := json.Marshal(map[string]any{"version": repo.items[id].Version, "content": content, "action": action})
				return sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(data), "alice", "")
			}
			for _, label := range []string{" \t", "\u0085", "\u00a0", strings.Repeat("я", 121), strings.Repeat("a", 121), strings.Repeat("😀", 121), strings.Repeat("aя😀", 40) + "z"} {
				candidate := d
				candidate.Slots = append([]templates.Slot(nil), d.Slots...)
				candidate.Slots[0].Label = label
				if !published {
					// Incomplete unpublished saves retain the exact label and content.
					candidate.Title, candidate.Prompt = "", ""
					if w := write(candidate, ""); w.Code != 200 {
						t.Fatal("incomplete draft rejected", w.Code, w.Body.String())
					}
					if repo.items[id].Draft.Slots[0].Label != label || repo.items[id].Published != nil {
						t.Fatal("draft was changed or published")
					}
					candidate.Title, candidate.Prompt = d.Title, d.Prompt
				}
				before, _ := json.Marshal(repo.items[id])
				saves := repo.saves
				action := ""
				if !published {
					action = "publish"
				}
				w := write(candidate, action)
				after, _ := json.Marshal(repo.items[id])
				if !bytes.Equal(before, after) || repo.saves != saves {
					t.Fatal("rejected publication/live save changed prior content")
				}
				var body struct {
					Error struct{ Code, Message, Param string }
				}
				_ = json.Unmarshal(w.Body.Bytes(), &body)
				if w.Code != 400 || body.Error.Code != "invalid_template" || body.Error.Param != "slots[0].label" || !strings.Contains(body.Error.Message, "label") || strings.Contains(body.Error.Message, "incorrectly bound") {
					t.Fatalf("wrong label diagnostic: HTTP%d %s", w.Code, w.Body.String())
				}
				if strings.TrimSpace(label) != "" && strings.Contains(body.Error.Message, label) {
					t.Fatal("label value reflected in error")
				}
			}
			// Valid multi-byte labels work for both publication and live saving.
			for _, label := range []string{"\ufeff", strings.Repeat("я", 70), strings.Repeat("a", 120), strings.Repeat("я", 120), strings.Repeat("aя😀", 40), strings.Repeat("😀", 120)} {
				candidate := d
				candidate.Slots = append([]templates.Slot(nil), d.Slots...)
				candidate.Slots[0].Label = label
				action := ""
				if repo.items[id].Published == nil {
					action = "publish"
				}
				if w := write(candidate, action); w.Code != 200 {
					t.Fatal("valid Unicode label rejected", w.Code, w.Body.String())
				}
				if repo.items[id].Published.Slots[0].Label != label {
					t.Fatal("saved label was normalized or truncated")
				}
			}
		})
	}
}

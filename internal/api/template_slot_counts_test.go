package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

func TestTemplateSlotCountWarningKeepsDraftAndChecksPublication(t *testing.T) {
	models, err := mock.New(0, "embedded").ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var model core.MediaModel
	for _, m := range models {
		if m.ID == "mock-image-v1" {
			model = m
		}
	}
	for _, count := range []int{15, 14} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			s, repo, svc, _ := templateFixture(t)
			svc.catalog = []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{model}}}
			id := uuid.NewString()
			parts := []string{}
			for i := 1; i <= count; i++ {
				parts = append(parts, fmt.Sprintf("@Image%d", i))
			}
			d := templates.Definition{Title: "Count review", MediaType: "image", Provider: "mock", Model: model.ID, Prompt: strings.Join(parts, " "), Syntax: templates.UIReferenceSyntax, Options: core.MediaOptions{ReferenceMode: "references"}}
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
			body, _ := json.Marshal(d)
			inferred := sendTest(s.handler(), "POST", "/v1/system/templates/"+id+"/infer", string(body), "alice", "")
			var result struct {
				Slots   []templates.Slot `json:"slots"`
				Warning string           `json:"warning"`
			}
			if inferred.Code != 200 || json.Unmarshal(inferred.Body.Bytes(), &result) != nil || len(result.Slots) != count {
				t.Fatal("inference lost slots", inferred.Code, inferred.Body.String())
			}
			if count == 15 && (!strings.Contains(result.Warning, "15 image slots") || !strings.Contains(result.Warning, "maximum 14")) {
				t.Error("missing early actual/maximum warning", result.Warning)
			}
			if count == 14 && result.Warning != "" {
				t.Error("valid boundary warned", result.Warning)
			}
			d.Slots = result.Slots
			body, _ = json.Marshal(map[string]any{"version": 1, "content": d})
			saved := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
			if saved.Code != 200 || len(repo.items[id].Draft.Slots) != count || repo.items[id].Published != nil {
				t.Fatal("unpublished draft could not be saved intact", saved.Code, saved.Body.String())
			}
			saves := repo.saves
			body, _ = json.Marshal(map[string]any{"version": repo.items[id].Version, "action": "publish"})
			published := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
			if count == 15 {
				if published.Code != 400 || !strings.Contains(published.Body.String(), "15 image slots") || !strings.Contains(published.Body.String(), "maximum 14") {
					t.Error("publication lacks actual/maximum rejection", published.Code, published.Body.String())
				}
				if repo.saves != saves || repo.items[id].Published != nil {
					t.Fatal("invalid publication changed stored state")
				}
			} else if published.Code != 200 {
				t.Fatal("valid boundary publication rejected", published.Code, published.Body.String())
			}
			if svc.calls != 0 {
				t.Fatal("generation was invoked")
			}
		})
	}
}

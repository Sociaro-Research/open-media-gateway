package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/google/uuid"
)

type videoDurationService struct {
	*openAIServiceFixture
	estimates int
}

func (s *videoDurationService) Estimate(_ context.Context, input core.CreateMediaInput) (core.CostEstimate, error) {
	s.estimates++
	s.input = input
	return core.CostEstimate{}, nil
}

// Actual MP4 bytes supply duration. Caller-provided duration metadata is never used.
func TestTemplatesWan3CombinedDurationBeforeAdmission(t *testing.T) {
	count := 0
	for _, model := range alibaba.Definition().Profiles() {
		if model.ID != "wan3.0-video" && model.ID != "wan3.0-video-prime" {
			continue
		}
		count++
		for _, fixed := range []bool{true, false} {
			for _, output := range []int{20, 21} {
				t.Run(fmt.Sprintf("%s/fixed=%v/output=%d", model.ID, fixed, output), func(t *testing.T) {
					s, repo, svc, _ := templateFixture(t)
					counter := &videoDurationService{openAIServiceFixture: svc}
					s.service = counter
					svc.catalog = []core.ProviderDescriptor{{ID: "alibaba", Models: []core.MediaModel{model}}}
					id := uuid.NewString()
					d := templates.Definition{Title: "Duration", MediaType: "video", Provider: "alibaba", Model: model.ID, Prompt: "Video 1 follows Video 2", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: output}}
					var err error
					d.Slots, err = d.InferSlots(model)
					if err != nil {
						t.Fatal(err)
					}
					repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
					d.CoverID = templateCopy(t, s, id, referencePNG(t)).ID
					inputs := map[string]string{}
					if fixed {
						video := templateCopy(t, s, id, templateVideo())
						for i := range d.Slots {
							d.Slots[i].AssetID = video.ID
						}
					} else {
						ctx := identity.WithPersonalData(identity.WithUser(context.Background(), identity.User{ID: "alice"}))
						ref, err := s.references.Upload(ctx, "clip.mp4", bytes.NewReader(templateVideo()))
						if err != nil {
							t.Fatal(err)
						}
						for _, slot := range d.Slots {
							inputs[slot.ID] = ref.ID
						}
					}
					body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
					w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", "")
					if fixed && output == 21 {
						if w.Code != 400 || repo.saves != 0 || svc.calls != 0 {
							t.Fatal("invalid fixed media published", w.Code, w.Body.String(), repo.saves, svc.calls)
						}
						// Existing published revisions must also become unavailable and fail admission.
						repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
						for k, a := range repo.assets {
							a.Published = true
							repo.assets[k] = a
						}
					} else if w.Code != 200 {
						t.Fatal("publication", w.Code, w.Body.String())
					}
					view := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", "service-secret")
					wantAvailable := !fixed || output == 20
					if view.Code != 200 || !strings.Contains(view.Body.String(), fmt.Sprintf(`"default_available":%v`, wantAvailable)) {
						t.Fatal("availability", view.Code, view.Body.String())
					}
					_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Duration"}`)
					request, _ := json.Marshal(map[string]any{"revision": 2, "provider": "alibaba", "model": model.ID, "inputs": inputs, "max_expected_cost_usd": 0})
					for _, endpoint := range []string{"estimate", "generations"} {
						before := svc.calls + counter.estimates
						r := httptest.NewRequest("POST", "/v1/templates/"+id+"/"+endpoint, bytes.NewReader(request))
						r.Header.Set("Authorization", "Bearer "+key)
						r.Header.Set("Idempotency-Key", "duration-boundary")
						r.Header.Set("Content-Type", "application/json")
						w = httptest.NewRecorder()
						s.handler().ServeHTTP(w, r)
						if output == 21 {
							if w.Code != 400 || svc.calls+counter.estimates != before {
								t.Fatal("invalid inspected duration reached admission", endpoint, w.Code, w.Body.String(), svc.calls)
							}
						} else if endpoint == "generations" && w.Code != 202 {
							t.Fatal("boundary rejected", w.Code, w.Body.String())
						} else if endpoint == "estimate" && w.Code != 200 {
							t.Fatal("boundary estimate", w.Code, w.Body.String())
						}
					}
				})
			}
		}
	}
	if count != 2 {
		t.Fatalf("expected both Wan 3 variants, got %d", count)
	}
}

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type templateNativeProvider struct {
	*mock.Adapter
	model       core.MediaModel
	native      core.RequestValidator
	validations int
}

func (p *templateNativeProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{p.model}, nil
}
func (p *templateNativeProvider) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	p.validations++
	return p.native.ValidateRequest(ctx, r)
}

func TestTemplateNativeValidationPublicationAndAvailability(t *testing.T) {
	var model core.MediaModel
	for _, m := range alibaba.Definition().Profiles() {
		if m.ID == "wan2.7-image-pro" {
			model = m
		}
	}
	if model.ID == "" {
		t.Fatal("native descriptor absent")
	}
	native, err := alibaba.New("synthetic-unused", "https://provider.invalid/api/v1", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, resolution string
		refs             int
		valid            bool
	}{
		{"4K with references", "4096*4096", 1, false},
		{"2K with references", "2048*2048", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, repo, _, _ := templateFixture(t)
			admission, queue := &admissionRepository{}, &admissionQueue{}
			provider := &templateNativeProvider{Adapter: mock.New(time.Second, ""), model: model, native: native}
			service := core.NewTaskServiceWithProviders(admission, queue, map[string]core.MediaProvider{"arbitrary-connection": provider}, map[string]string{"arbitrary-connection": model.ID}, "arbitrary-connection")
			service.EnableAccounting()
			s.service = service
			id := uuid.NewString()
			d := templates.Definition{Title: "Native rules", Provider: "arbitrary-connection", Model: model.ID, MediaType: "image", Prompt: "Preserve the supplied portrait", ReferenceCounts: map[string]int{"image": tc.refs}, Options: core.MediaOptions{ReferenceMode: "references", Resolution: tc.resolution}}
			d.Slots, err = d.InferSlots(model)
			if err != nil {
				t.Fatal(err)
			}
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			var imageData bytes.Buffer
			if err = png.Encode(&imageData, image.NewGray(image.Rect(0, 0, 512, 512))); err != nil {
				t.Fatal(err)
			}
			asset := templateCopy(t, s, id, imageData.Bytes())
			d.CoverID = asset.ID
			if tc.refs > 0 {
				d.Slots[0].AssetID = asset.ID
			}
			body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "draft": d})
			published := sendTest(s.handler(), http.MethodPut, "/v1/system/templates/"+id, string(body), "alice", "")
			if tc.valid {
				if published.Code != 200 {
					t.Fatal(published.Code, published.Body.String())
				}
			} else {
				if published.Code != 400 || !strings.Contains(published.Body.String(), "4K is supported for text-to-image only") || repo.saves != 0 {
					t.Fatal("native-invalid recipe published", published.Code, published.Body.String())
				}
				// A legacy/imported revision must also disappear from availability.
				repo.items[id] = templates.Record{ID: id, Version: 2, Revision: 2, Draft: d, Published: &d}
			}
			view := sendTest(s.handler(), http.MethodGet, "/v1/templates/"+id, "", "", "service-secret")
			if view.Code != 200 || strings.Contains(view.Body.String(), `"default_available":true`) != tc.valid {
				t.Fatal(view.Code, view.Body.String())
			}
			if admission.creates != 0 || len(queue.ids) != 0 || provider.validations < 2 {
				t.Fatal("preflight created a job or skipped native validation", admission.creates, queue.ids, provider.validations)
			}
			request, _ := json.Marshal(map[string]any{"revision": 2, "provider": "arbitrary-connection", "model": model.ID, "inputs": map[string]string{}})
			estimate := sendTest(s.handler(), http.MethodPost, "/v1/templates/"+id+"/estimate", string(request), "", "service-secret")
			if tc.valid && estimate.Code != 200 || !tc.valid && estimate.Code != 400 {
				t.Fatal("availability and admission diverged", estimate.Code, estimate.Body.String())
			}
			if admission.creates != 0 {
				t.Fatal("estimate persisted a task")
			}
		})
	}
}

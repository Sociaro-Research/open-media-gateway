package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type templateResolutionProvider struct{ *templateAudioProvider }

func (p *templateResolutionProvider) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	if r.Options.Resolution != "" && !slices.Contains(p.model.Resolutions, r.Options.Resolution) {
		return core.ErrOptionsInvalid
	}
	return p.templateAudioProvider.ValidateRequest(ctx, r)
}
func (p *templateResolutionProvider) Quote(_ context.Context, _ core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	prices := map[string]money.USD{"": 1000, "480p": 48000, "720p": 72000, "768p": 76800, "960p": 96000, "1080p": 108000, "2k": 204800}
	price, ok := prices[r.Options.Resolution]
	if !ok {
		return core.TaskCost{}, "", core.ErrOptionsInvalid
	}
	return core.TaskCost{ExpectedUSD: price, Status: "estimated", EstimatedUnits: 1, QuotedAt: time.Now(), Pricing: core.PricingSnapshot{Currency: "USD", Unit: "request", Per: 1, Price: price, Source: "Synthetic resolution tariff"}}, "", nil
}

func TestTemplateEffectiveResolutionAdmissionAndPricing(t *testing.T) {
	for _, tc := range []struct {
		saved, want string
		choices     []string
		price       money.USD
	}{
		{"720p", "768p", []string{"2k", "768p"}, 76800},
		{"720p", "768p", []string{"768p", "2k"}, 76800},
		{"1080p", "1080p", []string{"720p", "1080p"}, 108000},
		{"720p", "480p", []string{"960p", "480p"}, 48000},
		{"unknown", "2k", []string{"2k", "768p"}, 204800},
		{"", "768p", []string{"768p", "2k"}, 76800},
		{"720p", "", nil, 1000},
	} {
		t.Run(fmt.Sprintf("%s/%v", tc.saved, tc.choices), func(t *testing.T) {
			s, repo, _, _ := templateFixture(t)
			m := templateTestModel()
			m.Resolutions = tc.choices
			provider := &templateResolutionProvider{&templateAudioProvider{Adapter: mock.New(time.Second, ""), model: m}}
			admission, queue := &admissionRepository{}, &admissionQueue{}
			service := core.NewTaskServiceWithProviders(admission, queue, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": m.ID}, "mock")
			service.EnableAccounting()
			s.service = service
			id := uuid.NewString()
			d := templates.Definition{Title: "Resolution preference", Provider: "mock", Model: m.ID, MediaType: "video", Prompt: "Image 1 moves", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Resolution: tc.saved, Duration: 5, Ratio: "16:9"}}
			d.Slots, _ = d.InferSlots(m)
			repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
			image := templateCopy(t, s, id, referencePNG(t))
			d.CoverID, d.Slots[0].AssetID = image.ID, image.ID
			body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "content": d})
			if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
				t.Fatal("publish", w.Code, w.Body.String())
			}
			if repo.items[id].Draft.Options.Resolution != tc.saved {
				t.Fatal("publish changed stored preference")
			}
			_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Resolution"}`)
			record := repo.items[id]
			w := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", key)
			var detail struct {
				Catalog     []core.ProviderDescriptor    `json:"catalog"`
				Resolutions map[string]map[string]string `json:"effective_resolutions"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || len(detail.Catalog) != 1 {
				t.Fatal("resolution filtered model", w.Code, w.Body.String())
			}
			resolution, exists := detail.Resolutions["mock"][m.ID]
			if !exists || resolution != tc.want {
				t.Fatal("wrong authoritative resolution", detail.Resolutions)
			}
			call := func(endpoint string) *httptest.ResponseRecorder {
				b, _ := json.Marshal(map[string]any{"revision": record.Revision, "expected_resolution": tc.want, "provider": "mock", "model": m.ID, "inputs": map[string]string{}, "max_expected_cost_usd": 10})
				r := httptest.NewRequest("POST", "/v1/templates/"+id+"/"+endpoint, bytes.NewReader(b))
				r.Header.Set("Authorization", "Bearer "+key)
				r.Header.Set("Idempotency-Key", "one-resolution-intent")
				w := httptest.NewRecorder()
				s.handler().ServeHTTP(w, r)
				return w
			}
			w = call("estimate")
			var quote core.CostEstimate
			json.Unmarshal(w.Body.Bytes(), &quote)
			if w.Code != 200 || quote.Cost.ExpectedUSD != tc.price || admission.creates != 0 {
				t.Fatal("wrong effective tariff", w.Code, w.Body.String())
			}
			previousChoices := provider.model.Resolutions
			changed := "480p"
			if tc.want == changed {
				changed = "1080p"
			}
			provider.model.Resolutions = []string{changed}
			for _, endpoint := range []string{"estimate", "generations", "editing", "validate-media"} {
				stale := call(endpoint)
				if stale.Code != 409 || admission.creates != 0 {
					t.Fatal("stale output resolution accepted", endpoint, stale.Code, stale.Body.String())
				}
			}
			if repo.items[id].Revision != record.Revision {
				t.Fatal("fixture changed template revision")
			}
			provider.model.Resolutions = previousChoices
			w = call("editing")
			var editing struct {
				Options core.MediaOptions `json:"options"`
			}
			json.Unmarshal(w.Body.Bytes(), &editing)
			if w.Code != 200 || editing.Options.Resolution != tc.want {
				t.Fatal("wrong Advanced resolution", w.Code, w.Body.String())
			}
			for i := 0; i < 2; i++ {
				if w := call("generations"); w.Code != 202 {
					t.Fatal("admission", w.Code, w.Body.String())
				}
			}
			if admission.creates != 1 || admission.task.Options.Resolution != tc.want || admission.task.Cost == nil || admission.task.Cost.ExpectedUSD != tc.price {
				t.Fatal("request/pricing/idempotency mismatch")
			}
			var snapshot struct {
				Options core.MediaOptions `json:"options"`
			}
			json.Unmarshal(admission.task.TemplateSnapshot, &snapshot)
			if snapshot.Options.Resolution != tc.want {
				t.Fatal("wrong immutable resolution")
			}
			before := append([]byte(nil), admission.task.TemplateSnapshot...)
			d.Prompt = "Image 1 now jumps"
			body, _ = json.Marshal(map[string]any{"version": record.Version, "content": d})
			if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if repo.items[id].Draft.Options.Resolution != tc.saved || !bytes.Equal(before, admission.task.TemplateSnapshot) {
				t.Fatal("stored preference/snapshot mutated")
			}
			if err := service.ValidateMediaRequest(context.Background(), core.CreateMediaInput{Provider: "mock", Model: m.ID, MediaType: "video", Prompt: "Scene", Options: core.MediaOptions{Resolution: "unsupported-direct", ReferenceMode: "references", ImageURLs: []string{"https://media.example/image.png"}}}); err == nil {
				t.Fatal("direct unsupported resolution accepted")
			}
			m.ReferenceMediaLimits.Image.MinDimension = 700
			w = sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", key)
			detail.Resolutions = nil
			json.Unmarshal(w.Body.Bytes(), &detail)
			if w.Code != 200 || len(detail.Catalog) != 0 || len(detail.Resolutions) != 0 {
				t.Fatal("invalid media became eligible", w.Code, w.Body.String())
			}
		})
	}
}

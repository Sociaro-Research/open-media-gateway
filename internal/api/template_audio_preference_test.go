package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
	"github.com/google/uuid"
)

type templateAudioProvider struct {
	*mock.Adapter
	model core.MediaModel
}

func (p *templateAudioProvider) ListModels(context.Context) ([]core.MediaModel, error) {
	return []core.MediaModel{p.model}, nil
}
func (p *templateAudioProvider) ValidateRequest(_ context.Context, r core.MediaRequest) error {
	if !p.model.SupportsAudio && r.Options.GenerateAudio != nil {
		return fmt.Errorf("%w: explicit audio unsupported", core.ErrOptionsInvalid)
	}
	return nil
}

func TestTemplateAudioPreferenceAdmissionMatrix(t *testing.T) {
	for _, target := range []string{"toggle", "unsupported", "always-on"} {
		for _, state := range []string{"absent", "off", "on"} {
			for _, replacement := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/replacement=%v", target, state, replacement), func(t *testing.T) {
					s, repo, _, _ := templateFixture(t)
					m := templateTestModel()
					m.SupportsAudio, m.AudioAlwaysOn = target != "unsupported", target == "always-on"
					admission, queue := &admissionRepository{}, &admissionQueue{}
					provider := &templateAudioProvider{Adapter: mock.New(time.Second, ""), model: m}
					service := core.NewTaskServiceWithProviders(admission, queue, map[string]core.MediaProvider{"mock": provider}, map[string]string{"mock": m.ID}, "mock")
					service.EnableAccounting()
					s.service = service
					id := uuid.NewString()
					d := templates.Definition{Title: "Optional audio", Provider: "mock", Model: m.ID, MediaType: "video", Prompt: "Image 1 moves", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references", Duration: 5, Resolution: "720p", Ratio: "16:9"}}
					if state != "absent" {
						v := state == "on"
						d.Options.GenerateAudio = &v
					}
					d.Slots, _ = d.InferSlots(m)
					repo.items[id] = templates.Record{ID: id, Version: 1, Draft: d}
					image := templateCopy(t, s, id, referencePNG(t))
					d.CoverID, d.Slots[0].AssetID = image.ID, image.ID
					if replacement {
						d.OverrideSoundID = templateCopy(t, s, id, templateAudioWAV(1)).ID
					}
					// The same effective context is used by editor file and library uploads.
					validation := &referencevalidation.Target{Provider: "mock", Model: m.ID, Options: d.GenerationOptionsFor(m)}
					upload := validationUpload(t, s.handler(), "/v1/system/templates/"+id+"/assets", "", "alice", "synthetic.png", referencePNG(t), validation)
					if upload.Code != 201 {
						t.Fatal("editor file upload", upload.Code, upload.Body.String())
					}
					binding, _ := json.Marshal(map[string]any{"library_reference_id": image.ID, "validation": validation})
					if w := sendTest(s.handler(), "POST", "/v1/system/templates/"+id+"/assets", string(binding), "alice", ""); w.Code != 201 {
						t.Fatal("editor library binding", w.Code, w.Body.String())
					}
					body, _ := json.Marshal(map[string]any{"version": 1, "action": "publish", "content": d})
					if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
						t.Fatal("publish", w.Code, w.Body.String())
					}
					if !reflect.DeepEqual(repo.items[id].Draft.Options.GenerateAudio, d.Options.GenerateAudio) {
						t.Fatal("publish overwrote saved audio preference")
					}
					_, key := issueTestKey(t, s.handler(), "alice", `{"name":"Optional audio"}`)
					record := repo.items[id]
					w := sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", key)
					var detail struct {
						Catalog []core.ProviderDescriptor `json:"catalog"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != 200 || len(detail.Catalog) != 1 || len(detail.Catalog[0].Models) != 1 {
						t.Fatal("audio preference filtered runtime model", w.Code, w.Body.String())
					}
					call := func(endpoint string) *httptest.ResponseRecorder {
						b, _ := json.Marshal(map[string]any{"revision": record.Revision, "provider": "mock", "model": m.ID, "inputs": map[string]string{}, "max_expected_cost_usd": 0})
						r := httptest.NewRequest("POST", "/v1/templates/"+id+"/"+endpoint, bytes.NewReader(b))
						r.Header.Set("Authorization", "Bearer "+key)
						r.Header.Set("Idempotency-Key", "one-audio-intent")
						w := httptest.NewRecorder()
						s.handler().ServeHTTP(w, r)
						return w
					}
					for _, endpoint := range []string{"validate-media", "editing", "estimate"} {
						if w := call(endpoint); w.Code != 200 {
							t.Fatal(endpoint, w.Code, w.Body.String())
						} else if endpoint == "editing" {
							var handoff struct {
								Options core.MediaOptions `json:"options"`
							}
							json.Unmarshal(w.Body.Bytes(), &handoff)
							want := d.Options.GenerateAudio
							if target != "toggle" {
								want = nil
							}
							if !reflect.DeepEqual(handoff.Options.GenerateAudio, want) {
								t.Fatal("wrong handoff audio")
							}
						}
					}
					if admission.creates != 0 {
						t.Fatal("read/estimate admitted work")
					}
					for i := 0; i < 2; i++ {
						if w := call("generations"); w.Code != 202 {
							t.Fatal("generation", w.Code, w.Body.String())
						}
					}
					if admission.creates != 1 || admission.task.Cost == nil || admission.task.Cost.ExpectedUSD != 0 {
						t.Fatal("admission/billing/idempotency changed")
					}
					want := d.Options.GenerateAudio
					if target == "unsupported" {
						want = nil
					}
					if target == "always-on" {
						v := true
						want = &v
					}
					if !reflect.DeepEqual(admission.task.Options.GenerateAudio, want) {
						t.Fatal("wrong admitted audio")
					}
					if (admission.task.Options.OverrideSound != nil) != replacement {
						t.Fatal("replacement audio not resolved")
					}
					var snapshot struct {
						Options core.MediaOptions `json:"options"`
					}
					json.Unmarshal(admission.task.TemplateSnapshot, &snapshot)
					if !reflect.DeepEqual(snapshot.Options.GenerateAudio, want) {
						t.Fatal("wrong immutable snapshot audio")
					}
					accepted := append([]byte(nil), admission.task.TemplateSnapshot...)
					// A subsequent published Save retains optionality and cannot mutate the admitted snapshot.
					d.Prompt = "Image 1 now jumps"
					body, _ = json.Marshal(map[string]any{"version": record.Version, "content": d})
					if w := sendTest(s.handler(), "PUT", "/v1/system/templates/"+id, string(body), "alice", ""); w.Code != 200 {
						t.Fatal("published save", w.Code, w.Body.String())
					}
					if !reflect.DeepEqual(repo.items[id].Draft.Options.GenerateAudio, d.Options.GenerateAudio) || !bytes.Equal(accepted, admission.task.TemplateSnapshot) {
						t.Fatal("saved preference or admitted snapshot changed")
					}
					// Existing media constraints still fail before admission and hide the incompatible target.
					m.ReferenceMediaLimits.Image.MinDimension = 700
					w = sendTest(s.handler(), "GET", "/v1/templates/"+id, "", "", key)
					json.Unmarshal(w.Body.Bytes(), &detail)
					if w.Code != 200 || len(detail.Catalog) != 0 {
						t.Fatal("media constraints relaxed", w.Code, w.Body.String())
					}
				})
			}
		}
	}
}

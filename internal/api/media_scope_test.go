package api

import (
	"context"
	"encoding/json"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"testing"
)

func TestMediaOnlyPublicCatalogAndUnscopedPreparationRejectsEveryRetiredKlingID(t *testing.T) {
	f := newResourceAPIFixture(t)
	retired := []string{kling.ImageRecognition, kling.FaceDetection, kling.ResourceCreateElement, kling.ResourceCreateVoice, kling.SessionInit, kling.SessionAdd, kling.SessionDelete, kling.SessionClear}
	for _, path := range []string{"/v1/providers", "/v1/models"} {
		w := resourceAPIRequest(f.h, "GET", path, f.alice, "", nil)
		resourceAPIAssertStatus(t, w, 200)
		for _, id := range retired {
			if strings.Contains(w.Body.String(), `"`+id+`"`) {
				index := strings.Index(w.Body.String(), `"`+id+`"`)
				start := index - 50
				if start < 0 {
					start = 0
				}
				end := index + len(id) + 60
				if end > w.Body.Len() {
					end = w.Body.Len()
				}
				t.Fatal("retired operation discovered", path, id, w.Body.String()[start:end])
			}
		}
	}
	for _, id := range retired {
		for _, path := range []string{"/v1/generations", "/v1/generations/estimate"} {
			w := resourceAPIRequest(f.h, "POST", path, f.alice, "retired-"+id, map[string]any{"provider": "kling", "model": id, "media_type": "video", "prompt": "forged media label", "options": map[string]any{}})
			resourceAPIAssertStatus(t, w, 400)
		}
	}
	for _, path := range []string{"/v1/provider-resources", "/v1/provider-resources/estimate"} {
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", path, f.alice, "unscoped", map[string]any{"provider": "kling", "model": kling.ResourceCreateVoice, "kind": "voice", "name": "voice"}), 400)
	}
	if f.tasks.creates != 0 {
		t.Fatal("retired operations persisted")
	}
}
func TestMediaOnlyPreparationMetadataIsConsumerScoped(t *testing.T) {
	f := newResourceAPIFixture(t)
	path := "/v1/models/" + kling.TextToSpeech + "/preparations?provider=kling"
	w := resourceAPIRequest(f.h, "GET", path, f.alice, "", nil)
	resourceAPIAssertStatus(t, w, 200)
	var body struct {
		Data []preparationSchema `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].Operation != kling.ResourceCreateVoice {
		t.Fatalf("unrelated preparations returned: %s", w.Body)
	}
	for _, suffix := range []string{"/v1/models/" + kling.ResourceCreateVoice + "/preparations?provider=kling", "/v1/models/" + kling.TextToSpeech + "/preparations?provider=another"} {
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", suffix, f.alice, "", nil), 400)
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "GET", path, "", "", nil), 401)
}

func TestMediaOnlyMiniMaxCatalogAliasesAndDirectPublicRejection(t *testing.T) {
	f := newResourceAPIFixture(t)
	a, err := minimax.New("offline-fixture", "", &http.Client{Transport: resourceAPITransport{t}})
	if err != nil {
		t.Fatal(err)
	}
	service := core.NewTaskServiceWithProviders(f.tasks, f.queue, map[string]core.MediaProvider{"minimax": a}, map[string]string{"minimax": minimax.ContextIR}, "minimax")
	f.s.service = service
	policy := &modelAccessMemory{org: map[string]map[string]bool{}}
	retired := []string{minimax.ContextIR, minimax.ResourceUpload, minimax.ResourceOutputFile, minimax.ResourceSourceTask}
	refs := []modelaccess.Ref{}
	for _, id := range retired {
		refs = append(refs, modelaccess.Ref{Provider: "minimax", Model: id, Name: id})
	}
	if err = policy.Ensure(context.Background(), refs); err != nil {
		t.Fatal(err)
	}
	for i := range policy.models {
		policy.models[i].PublicID = "retired-" + policy.models[i].Model
		policy.models[i].EnabledByDefault = true
	}
	f.s.modelAccess = policy
	f.h = f.s.handler()
	for _, path := range []string{"/v1/providers", "/v1/models"} {
		w := resourceAPIRequest(f.h, "GET", path, f.alice, "", nil)
		resourceAPIAssertStatus(t, w, 200)
		for _, id := range retired {
			if strings.Contains(w.Body.String(), `"`+id+`"`) {
				t.Fatal("retired default/descriptor revived", path, id)
			}
		}
	}
	for _, id := range retired {
		for _, name := range []string{id, "minimax/" + id, "retired-" + id} {
			for _, path := range []string{"/v1/generations", "/v1/generations/estimate", "/v1/videos"} {
				w := resourceAPIRequest(f.h, "POST", path, f.alice, "reject-"+name, map[string]any{"provider": "minimax", "model": name, "media_type": "video", "prompt": "fixture"})
				resourceAPIAssertStatus(t, w, 400)
			}
		}
	}
	if f.tasks.creates != 0 {
		t.Fatal("rejected utility created a task")
	}
}
func TestMediaPreparationAdmissionCarriesCurrentConsumerAlias(t *testing.T) {
	f := newResourceAPIFixture(t)
	ref := f.upload(t, "alice", "voice.wav", resourceAPIWAV(6))
	policy := &modelAccessMemory{org: map[string]map[string]bool{}}
	if err := policy.Ensure(context.Background(), []modelaccess.Ref{{Provider: "kling", Model: kling.TextToSpeech, Name: "Speech"}}); err != nil {
		t.Fatal(err)
	}
	policy.models[0].PublicID = "media-speech-consumer"
	f.s.modelAccess = policy
	f.h = f.s.handler()
	body := resourceAPIVoice(ref.ID)
	body["workflow_model"] = "media-speech-consumer"
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources/estimate", f.alice, "", body), 200)
	if f.tasks.creates != 0 {
		t.Fatal("resource quote created work")
	}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/provider-resources", f.alice, "aliased-consumer", body), 202)
	task := f.tasks.tasks[f.queue.ids[0]]
	if task.PreparationConsumer != kling.TextToSpeech || task.PreparationConsumerPublic != "media-speech-consumer" || task.Model != kling.ResourceCreateVoice {
		t.Fatal("trusted transaction identity missing or dispatch model changed")
	}
}

func TestMediaFacePreparationUsesInspectedOwnedVideoAndConsumerScope(t *testing.T) {
	f := newResourceAPIFixture(t)
	source := f.upload(t, "alice", "face.mp4", testmedia.MP4(6000, 1000, 180))
	foreign := f.upload(t, "bob", "foreign.mp4", testmedia.MP4(6000, 1000, 180))
	body := map[string]any{"provider": "kling", "workflow_model": kling.LipSync, "reference_id": source.ID}
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/media-inputs/faces/estimate", f.alice, "", body), 200)
	if f.tasks.creates != 0 {
		t.Fatal("estimate created work")
	}
	for _, workflow := range []string{"", kling.TextToSpeech, kling.FaceDetection} {
		bad := map[string]any{"provider": "kling", "workflow_model": workflow, "reference_id": source.ID}
		resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/media-inputs/faces", f.alice, "wrong-consumer", bad), 400)
	}
	body["reference_id"] = foreign.ID
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/media-inputs/faces", f.alice, "foreign", body), 404)
	body["reference_id"] = source.ID
	body["duration"] = 6
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/media-inputs/faces", f.alice, "forged", body), 400)
	delete(body, "duration")
	w := resourceAPIRequest(f.h, "POST", "/v1/media-inputs/faces", f.alice, "prepare-faces", body)
	resourceAPIAssertStatus(t, w, 202)
	task := f.tasks.tasks[f.queue.ids[0]]
	if task.Model != kling.FaceDetection || task.PreparationConsumer != kling.LipSync || len(task.Options.Videos()) != 1 {
		t.Fatal("face preparation dispatch/consumer lost")
	}
	evidence := task.Options.ReferenceMediaEvidence[task.Options.Videos()[0]]
	if evidence.ReferenceID != source.ID || evidence.SHA256 == "" || evidence.Duration != 6 || evidence.Width != 540 || evidence.Height != 960 {
		t.Fatal("trusted inspected video evidence missing")
	}
	resourceAPIAssertPrivate(t, w.Body.String(), evidence.SHA256, task.Options.Videos()[0], "PreparationConsumer")
	resourceAPIAssertStatus(t, resourceAPIRequest(f.h, "POST", "/v1/media-inputs/faces", f.alice, "prepare-faces", body), 202)
	if f.tasks.creates != 1 {
		t.Fatal("repeated preparation created duplicate work")
	}
}

func TestMediaOnlyTemplatesCannotExecuteOrTransferRetiredOperations(t *testing.T) {
	s, repo, svc, _ := templateFixture(t)
	retired := []string{kling.ImageRecognition, kling.FaceDetection, kling.ResourceCreateElement, kling.ResourceCreateVoice, kling.SessionInit, kling.SessionAdd, kling.SessionDelete, kling.SessionClear, minimax.ContextIR, minimax.ResourceUpload, minimax.ResourceOutputFile, minimax.ResourceSourceTask}
	for _, id := range retired {
		// A misleading video label cannot make a utility an executable Template model.
		descriptor := core.MediaModel{ID: id, PublicID: "alias-" + id, MediaType: "video", ResourceOperation: &core.ResourceOperationSchema{Kind: "voice"}}
		svc.catalog = []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{descriptor, templateTestModel()}}}
		definition := templates.Definition{Title: "Historical utility", Provider: "mock", Model: id, MediaType: "video", Prompt: "Historical input"}
		record := templates.Record{ID: uuid.NewString(), Version: 1, Revision: 1, Draft: definition, Published: &definition}
		repo.items[record.ID] = record
		for _, model := range []string{id, "alias-" + id} {
			body, _ := json.Marshal(map[string]any{"revision": 1, "provider": "mock", "model": model})
			for _, action := range []string{"generations", "estimate", "editing", "validate-media"} {
				got := sendTest(s.handler(), "POST", "/v1/templates/"+record.ID+"/"+action, string(body), "", "service-secret")
				if got.Code != 400 {
					t.Fatalf("retired %s %s: %d", id, action, got.Code)
				}
			}
		}
		if svc.calls != 0 || repo.saves != 0 || repo.items[record.ID].Published == nil {
			t.Fatal("retired Template mutated or dispatched")
		}
	}
}

package kling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func resourceGenerationFixture(kind, subtype string) core.MediaRequest {
	creation := resourceFixtureRequest(kind, subtype)
	intent := &core.ResourceIntent{Kind: kind, Subtype: subtype, Name: creation.Name, Description: creation.Description, Tags: append([]string(nil), creation.Tags...)}
	for _, group := range [][]core.ResourceSourceInput{creation.Images, creation.Videos, creation.VoiceSources} {
		for _, source := range group {
			intent.ReferenceIDs = append(intent.ReferenceIDs, source.ReferenceID)
		}
	}
	id := ResourceCreateElement
	if kind == "voice" {
		id = ResourceCreateVoice
	}
	return core.MediaRequest{Model: id, TaskID: uuid.NewString(), Options: core.MediaOptions{ResourceIntent: intent, ResourceCreation: &creation}}
}
func resourceGeneratedVoiceFixture(t *testing.T, a *Adapter, operation string) core.MediaRequest {
	t.Helper()
	r := resourceGenerationFixture("voice", "custom")
	r.Options.ResourceCreation.VoiceSources = nil
	r.Options.ResourceIntent.ReferenceIDs = nil
	taskID := uuid.NewString()
	handle := "remote_native_task"
	if operation != TextToVideo26 && operation != ImageToVideo26 {
		var err error
		handle, err = a.signLegacy(resourceFixtureContext(), operation, "remote_legacy_task")
		if err != nil {
			t.Fatal(err)
		}
	}
	r.Options.ResourceIntent.GeneratedTaskID = taskID
	r.Options.ResourceCreation.GeneratedVideo = &core.GeneratedResourceSource{TaskID: taskID, TaskHandle: handle, OutputID: "9007199254740993", SHA256: strings.Repeat("d", 64), Operation: operation, Model: operation, NativeAudio: true, Duration: ptr(10.0)}
	return r
}
func TestResourceGenerationCatalogAndExactPrice(t *testing.T) {
	models := resourceGenerationModels()
	if len(models) != 2 {
		t.Fatal(models)
	}
	for _, m := range models {
		if m.MediaType != "resource" || !m.NoPrompt || !m.NoSeed || !m.NoWatermark || !m.NoLanguage || !m.NoInstructions || m.ResourceOperation == nil || !isResourceGenerationOperation(m.ID) {
			t.Fatal(m)
		}
	}
	if models[0].ResourceOperation.Kind != "element" || !reflect.DeepEqual(models[0].ResourceOperation.Subtypes, []string{"multi_image", "video_character"}) || models[0].Pricing.Known || models[1].ResourceOperation.Kind != "voice" || !models[1].Pricing.Known || models[1].Pricing.ReferencePriceUSD != .007 {
		t.Fatal(models)
	}
	a := &Adapter{}
	r := resourceGenerationFixture("voice", "custom")
	cost, _, err := a.quoteResourceGeneration(resourceFixtureContext(), r)
	if err != nil || cost.ExpectedUSD != 7000 || cost.EstimatedUnits != 1 || cost.Status != "estimated" || cost.Pricing.Unit != "request" || cost.Pricing.Price != 7000 || cost.Pricing.Per != 1 || cost.Pricing.AccountSpecific || cost.ActualUSD != nil {
		t.Fatalf("%+v %v", cost, err)
	}
	if _, _, err := a.quoteResourceGeneration(resourceFixtureContext(), resourceGenerationFixture("element", "multi_image")); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal(err)
	}
	if isResourceGenerationOperation("kling-create-arbitrary") {
		t.Fatal("unreviewed operation")
	}
}
func TestResourceGenerationAdmissionRequiresExactTrustedIntent(t *testing.T) {
	cases := []struct {
		name   string
		change func(*core.MediaRequest)
	}{
		{"missing public intent", func(r *core.MediaRequest) { r.Options.ResourceIntent = nil }},
		{"missing private source", func(r *core.MediaRequest) { r.Options.ResourceCreation = nil }},
		{"wrong kind", func(r *core.MediaRequest) { r.Options.ResourceIntent.Kind = "voice" }},
		{"changed name", func(r *core.MediaRequest) { r.Options.ResourceCreation.Name = "Changed server name" }},
		{"changed description", func(r *core.MediaRequest) { r.Options.ResourceCreation.Description = "Changed" }},
		{"changed subtype", func(r *core.MediaRequest) { r.Options.ResourceCreation.Subtype = "video_character" }},
		{"changed tags", func(r *core.MediaRequest) { r.Options.ResourceCreation.Tags = []string{"o_102"} }},
		{"changed source identity", func(r *core.MediaRequest) { r.Options.ResourceCreation.Images[0].ReferenceID = uuid.NewString() }},
		{"changed frontal order", func(r *core.MediaRequest) {
			r.Options.ResourceCreation.Images[0], r.Options.ResourceCreation.Images[1] = r.Options.ResourceCreation.Images[1], r.Options.ResourceCreation.Images[0]
		}},
		{"raw ID instead of local reference", func(r *core.MediaRequest) { r.Options.ResourceIntent.ReferenceIDs[0] = "9007199254740993" }},
		{"missing inspected hash", func(r *core.MediaRequest) { r.Options.ResourceCreation.Images[0].SHA256 = "" }},
		{"prompt", func(r *core.MediaRequest) { r.Prompt = "Unadvertised generation prompt" }},
		{"raw image URL", func(r *core.MediaRequest) { r.Options.ImageURLs = []string{"https://cdn.example/raw.png"} }},
		{"raw video URL", func(r *core.MediaRequest) { r.Options.VideoURL = "https://cdn.example/raw.mp4" }},
		{"raw audio URL", func(r *core.MediaRequest) { r.Options.AudioURL = "https://cdn.example/raw.wav" }},
		{"raw parameter", func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"voice_id": "arbitrary"} }},
		{"raw voice", func(r *core.MediaRequest) { r.Options.Voice = "arbitrary" }},
		{"resolution", func(r *core.MediaRequest) { r.Options.Resolution = "1080p" }},
		{"duration", func(r *core.MediaRequest) { r.Options.Duration = 10 }},
		{"watermark", func(r *core.MediaRequest) { r.Options.Watermark = ptr(false) }},
		{"seed", func(r *core.MediaRequest) { r.Options.Seed = ptr(int64(1)) }},
		{"output count", func(r *core.MediaRequest) { r.Options.OutputCount = 1 }},
		{"generic managed references", func(r *core.MediaRequest) {
			r.Options.ResourceReferences = []core.ResourceReference{{BindingID: uuid.NewString(), ReferenceID: "voice"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := resourceGenerationFixture("element", "multi_image")
			tc.change(&r)
			if err := (&Adapter{}).validateResourceGenerationRequest(resourceFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
}
func TestResourceGenerationSubmitsCorrelatedClonedRequest(t *testing.T) {
	for _, tc := range []struct{ kind, subtype string }{{"element", "multi_image"}, {"element", "video_character"}, {"voice", "custom"}} {
		r := resourceGenerationFixture(tc.kind, tc.subtype)
		before, _ := json.Marshal(r.Options.ResourceCreation)
		originalCorrelation := r.Options.ResourceCreation.ExternalTaskID
		a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
			if req.Method != "POST" || req.URL.Path != resourcePath(tc.kind, false) {
				t.Fatal(req.URL)
			}
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["external_task_id"] != r.TaskID || body["task_id"] != nil || body["resource_intent"] != nil || body["_resource_creation"] != nil {
				t.Fatal(body)
			}
			return response(200, `{"code":0,"data":{"task_id":"new-resource-task","task_status":"submitted"}}`), nil
		}))
		task, err := a.generateResource(resourceFixtureContext(), r)
		if err != nil || task.ID == "" || len(task.Resources) != 0 || len(task.Artifacts) != 0 || task.ResultURL != "" {
			t.Fatalf("%+v %v", task, err)
		}
		after, _ := json.Marshal(r.Options.ResourceCreation)
		if string(after) != string(before) || r.Options.ResourceCreation.ExternalTaskID != originalCorrelation {
			t.Fatal("submission mutated admitted resource evidence")
		}
		decoded, err := a.parseLegacy(resourceFixtureContext(), task.ID)
		if err != nil || decoded.Operation != resourceOperation(tc.kind, "create") {
			t.Fatal(decoded, err)
		}
	}
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") }))
	r := resourceGenerationFixture("voice", "custom")
	r.TaskID = "not-a-gateway-uuid"
	if _, err := a.generateResource(resourceFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
		t.Fatal(err, calls)
	}
}
func TestResourceGenerationUsesOutputVideoIDRatherThanTaskID(t *testing.T) {
	for _, operation := range []string{TextToVideo26, ImageToVideo26, AvatarImageToVideo, "kling-lip-sync"} {
		a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(req.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			if payload["video_id"] != "9007199254740993" || payload["video_id"] == "remote_legacy_task" || payload["video_id"] == "remote_native_task" || payload["voice_url"] != nil {
				t.Fatal(payload)
			}
			return response(200, `{"code":0,"data":{"task_id":"voice-task","task_status":"submitted"}}`), nil
		}))
		r := resourceGeneratedVoiceFixture(t, a, operation)
		if _, err := a.generateResource(resourceFixtureContext(), r); err != nil {
			t.Fatal(operation, err)
		}
		if r.Options.ResourceCreation.GeneratedVideo.RemoteID != "" {
			t.Fatal("derived remote output ID mutated persisted evidence")
		}
	}
}
func TestResourceGenerationGeneratedSourcesRejectUnknownOrForeignProof(t *testing.T) {
	cases := []struct {
		name   string
		change func(*core.MediaRequest)
	}{
		{"raw remote ID", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.RemoteID = "9007199254740993" }},
		{"missing output ID", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.OutputID = "" }},
		{"unknown output hash", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.SHA256 = "" }},
		{"missing task proof", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.TaskHandle = "" }},
		{"wrong local task", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.TaskID = uuid.NewString() }},
		{"changed operation", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.Operation = TextToVideo26 }},
		{"unknown duration", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.Duration = nil }},
		{"unsigned avatar task", func(r *core.MediaRequest) { r.Options.ResourceCreation.GeneratedVideo.TaskHandle = "raw_avatar_task" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") }))
			r := resourceGeneratedVoiceFixture(t, a, AvatarImageToVideo)
			tc.change(&r)
			if _, err := a.generateResource(resourceFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
				t.Fatal(err, calls)
			}
		})
	}
	a := &Adapter{apiKey: "fixture-secret"}
	r := resourceGeneratedVoiceFixture(t, a, AvatarImageToVideo)
	foreign := core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "foreign-owner"), core.ProviderConnectionScope{ID: "kling--fixture", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
	if err := a.validateResourceGenerationRequest(foreign, r); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal(err)
	}
}
func TestResourceGenerationImmediateAndPolledResourcesRemainTyped(t *testing.T) {
	for _, immediate := range []bool{true, false} {
		calls := 0
		a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
			calls++
			if req.Method == "POST" && !immediate {
				return response(200, `{"code":0,"data":{"task_id":"resource-task","task_status":"submitted"}}`), nil
			}
			return response(200, `{"code":0,"data":{"task_id":"resource-task","task_status":"succeed","task_result":{"voices":[`+resourceVoiceResult+`]}}}`), nil
		}))
		r := resourceGenerationFixture("voice", "custom")
		task, err := a.generateResource(resourceFixtureContext(), r)
		if err != nil {
			t.Fatal(err)
		}
		var resources []core.ProviderResource
		var usage *core.ProviderUsage
		if immediate {
			resources, usage = task.Resources, task.Usage
		} else {
			result, err := a.getResourceGenerationTask(resourceFixtureContext(), "voice", task.ID)
			if err != nil || result.Status != core.ProviderSucceeded || result.ResultURL != "" || len(result.Artifacts) != 0 {
				t.Fatal(result, err)
			}
			resources, usage = result.Resources, result.Usage
		}
		if len(resources) != 1 || resources[0].RemoteID != "9007199254740993" || resources[0].PreviewURL == "" || task.ResultURL != "" || len(task.Artifacts) != 0 || usage == nil || usage.EstimatedCost != nil {
			t.Fatal(task, resources, usage)
		}
		cost, _, err := a.quoteResourceGeneration(resourceFixtureContext(), r)
		if err != nil || !cost.MeterUsage(*usage) || cost.MeteredUSD == nil || *cost.MeteredUSD != 7000 || cost.ActualUSD != nil || cost.Status != "pending_billing" {
			t.Fatal(cost, err)
		}
	}
}
func TestResourceGenerationPollingRejectsDeletionAndWrongKind(t *testing.T) {
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") }))
	deletion, err := a.signLegacy(resourceFixtureContext(), resourceOperation("voice", "delete:succeed"), "delete-task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.getResourceGenerationTask(resourceFixtureContext(), "voice", deletion); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
		t.Fatal(err, calls)
	}
	element, err := a.signLegacy(resourceFixtureContext(), resourceOperation("element", "create"), "element-task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.getResourceGenerationTask(resourceFixtureContext(), "voice", element); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
		t.Fatal(err, calls)
	}
}

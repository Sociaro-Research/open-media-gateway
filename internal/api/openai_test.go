package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/references"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/mock"
)

type admissionRepository struct {
	core.TaskRepository
	task    core.Task
	hash    string
	creates int
}

func (r *admissionRepository) CreateOrGet(_ context.Context, t core.Task, key, hash string) (core.Task, bool, error) {
	if r.task.ID != "" {
		if hash != r.hash {
			return core.Task{}, false, core.ErrIdempotencyConflict
		}
		return r.task, false, nil
	}
	if t.AdmissionError != nil {
		return core.Task{}, false, t.AdmissionError
	}
	r.task = t
	r.hash = hash
	r.creates++
	return t, true, nil
}
func (r *admissionRepository) MarkEnqueued(context.Context, string) error { return nil }

type admissionQueue struct {
	core.TaskQueue
	ids []string
}

func (q *admissionQueue) Enqueue(_ context.Context, id string) error {
	q.ids = append(q.ids, id)
	return nil
}

func TestOpenAINormalizationPreservesCoreIdempotency(t *testing.T) {
	repo := &admissionRepository{}
	queue := &admissionQueue{}
	service := core.NewTaskServiceWithProviders(repo, queue, map[string]core.MediaProvider{"mock": mock.New(time.Second, "")}, map[string]string{"mock": "mock-video-v1"}, "mock")
	service.EnableAccounting()
	h := New(service, healthStub{}, "secret")
	body := `{"model":"mock/mock-video-v1","prompt":"test","size":"1280x720","seconds":"5"}`
	w := openAIRequest(h, "/v1/videos", body)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	if repo.task.Status != core.TaskQueued || len(queue.ids) != 1 || repo.task.Cost == nil || repo.task.Cost.ExpectedUSD != 0 {
		t.Fatal("creation must only admit/queue a free task", repo.task)
	}
	w = openAIRequest(h, "/v1/generations/estimate", body)
	if w.Code != 200 || repo.creates != 1 || len(queue.ids) != 1 || !strings.Contains(w.Body.String(), `"can_generate":true`) {
		t.Fatal("canonical estimate must not submit another job", w.Code, w.Body)
	}
	w = openAIRequest(h, "/v1/generations", body)
	if w.Code != 202 || repo.creates != 1 {
		t.Fatal("same normalized request must replay the original job", w.Code, w.Body)
	}
	w = openAIRequest(h, "/v1/videos", strings.Replace(body, `"test"`, `"different"`, 1))
	if w.Code != 409 || repo.creates != 1 {
		t.Fatal("changed request must conflict", w.Code, w.Body)
	}
}

func TestOpenAIMultipartAndReferenceOwnership(t *testing.T) {
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	repo := &referenceMemory{items: map[string]references.Image{}}
	library := references.New(repo, store)
	svc := openAIFixture()
	h := NewWithReferenceLibrary(svc, healthStub{}, "secret", store, "signature-secret", time.Hour, nil, nil, library)
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("model", "mock/image")
	_ = form.WriteField("timeout_seconds", "600")
	_ = form.WriteField("prompt", "edit")
	f, err := form.CreateFormFile("image[]", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(referencePNG(t))
	_ = form.Close()
	r := httptest.NewRequest("POST", "https://gateway.example/v1/images/edits", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 || svc.input.Options.TimeoutSeconds == nil || *svc.input.Options.TimeoutSeconds != 600 || len(svc.input.Options.ImageURLs) != 1 || !strings.HasPrefix(svc.input.Options.ImageURLs[0], "https://gateway.example/references/") || strings.Contains(svc.input.Options.ImageURLs[0], "base64") {
		t.Fatal(w.Code, w.Body, svc.input)
	}
	var item references.Image
	for _, v := range repo.items {
		item = v
	}
	if item.ID == "" || item.SizeBytes == 0 {
		t.Fatal(repo)
	}
	request := httptest.NewRequest("POST", "https://gateway.example/v1/images/edits", nil)
	s := &Server{references: library, mediaSigner: newMediaURLSigner("signature-secret", time.Hour)}
	data, _ := json.Marshal(map[string]string{"file_id": item.ID})
	ownerRequest := request.WithContext(identity.WithOwnerID(request.Context(), identity.SystemOwnerID))
	if urls, err := s.inputReferenceURLs(ownerRequest, data); err != nil || len(urls) != 1 {
		t.Fatal(urls, err)
	}
	if _, err := s.inputReferenceURLs(request.WithContext(identity.WithOwnerID(request.Context(), "another-owner")), data); err == nil {
		t.Fatal("cross-owner file ID accepted")
	}
}

func TestOpenAIGenerationIsolationAndDownload(t *testing.T) {
	s, _ := newPersonalTestServer()
	store, err := storage.NewLocal(t.TempDir(), http.DefaultClient, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	s.storage = store
	h := s.handler()
	_, alice := issueTestKey(t, h, "alice", `{"name":"Alice"}`)
	_, bob := issueTestKey(t, h, "bob", `{"name":"Bob"}`)
	w := sendTest(h, "POST", "/v1/generations", `{"model":"mock/mock-video-v1","prompt":"private"}`, "", alice)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	service := s.service.(*accountService)
	task := &service.tasks[0]
	data := referencePNG(t)
	stored, err := store.StoreReference(identity.WithOwnerID(context.Background(), "alice"), task.ID, data, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	task.Status = core.TaskSucceeded
	task.StorageKey = &stored.StorageKey
	for _, base := range []string{"/v1/generations/", "/v1/videos/"} {
		for _, suffix := range []string{"", "/content"} {
			path := base + task.ID + suffix
			for _, credential := range []string{"", bob} {
				w = sendTest(h, "GET", path, "", "", credential)
				if w.Code != 401 && w.Code != 404 {
					t.Fatal("leaked generation", w.Code, w.Body)
				}
			}
			w = sendTest(h, "GET", path, "", "", alice)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body)
			}
			if suffix != "" && !bytes.Equal(w.Body.Bytes(), data) {
				t.Fatal("download does not match stored result")
			}
		}
	}
	task.MediaType = "image"
	if w = sendTest(h, "GET", "/v1/videos/"+task.ID, "", "", alice); w.Code != 404 {
		t.Fatal("image exposed as video")
	}
}

type openAIServiceFixture struct {
	serviceStub
	input   core.CreateMediaInput
	key     string
	calls   int
	err     error
	catalog []core.ProviderDescriptor
}

func (s *openAIServiceFixture) ListProviders(context.Context) []core.ProviderDescriptor {
	return s.catalog
}
func (s *openAIServiceFixture) CreateMediaRequest(_ context.Context, input core.CreateMediaInput, key string) (core.Task, error) {
	s.input = input
	s.key = key
	s.calls++
	return core.Task{ID: "job-1", Status: core.TaskQueued, Provider: input.Provider, Model: input.Model, MediaType: input.MediaType, Options: input.Options, Prompt: input.Prompt, CreatedAt: time.Unix(1234, 0), UpdatedAt: time.Unix(1234, 0)}, s.err
}
func openAIFixture() *openAIServiceFixture {
	return &openAIServiceFixture{catalog: []core.ProviderDescriptor{{ID: "mock", Models: []core.MediaModel{
		{ID: "image", MediaType: "image", SupportsImageInput: true, Resolutions: []string{"1024*1024"}},
		{ID: "video", MediaType: "video", Resolutions: []string{"720p"}, Ratios: []string{"16:9", "9:16"}},
		{ID: "speech", MediaType: "speech"}, {ID: "music", MediaType: "music"},
	}}}}
}
func openAIRequest(h http.Handler, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer secret")
	r.Header.Set("Idempotency-Key", "retry-1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestOpenAIRequestsUseSharedAsyncAdmission(t *testing.T) {
	for _, tc := range []struct {
		path, body, media string
		check             func(*testing.T, core.CreateMediaInput)
	}{
		{"/v1/images/generations", `{"model":"mock/image","prompt":"sunrise","size":"1024x1024","quality":"high","output_format":"png","background":"transparent","n":1,"options":{"seed":42}}`, "image", func(t *testing.T, i core.CreateMediaInput) {
			if i.Options.Resolution != "1024*1024" || i.Options.Quality != "high" || i.Options.Format != "png" || i.Options.Seed == nil || *i.Options.Seed != 42 {
				t.Fatal(i)
			}
		}},
		{"/v1/audio/speech", `{"model":"mock/speech","input":"Hello","voice":"alloy","instructions":"Warmly","response_format":"mp3"}`, "speech", func(t *testing.T, i core.CreateMediaInput) {
			if i.Prompt != "Hello" || i.Options.Voice != "alloy" || i.Options.Format != "mp3" || i.Options.Instructions != "Warmly" {
				t.Fatal(i)
			}
		}},
		{"/v1/videos", `{"model":"mock/video","prompt":"sunrise","size":"1280x720","seconds":"5","options":{"generate_audio":true,"return_last_frame":true,"parameters":{"custom":7}}}`, "video", func(t *testing.T, i core.CreateMediaInput) {
			if i.Options.Duration != 5 || i.Options.Resolution != "720p" || i.Options.Ratio != "16:9" || i.Options.Parameters["custom"] != float64(7) {
				t.Fatal(i)
			}
		}},
		{"/v1/images/edits", `{"model":"mock/image","prompt":"edit","image":[{"image_url":"https://example.com/a.png"},"https://example.com/b.png"]}`, "image", func(t *testing.T, i core.CreateMediaInput) {
			if len(i.Options.ImageURLs) != 2 {
				t.Fatal(i)
			}
		}},
		{"/v1/audio/generations", `{"model":"mock/music","options":{"lyrics":"Sing this","language":"en","gender":"female"},"webhook_url":"https://example.com/events","max_expected_cost_usd":0}`, "music", func(t *testing.T, i core.CreateMediaInput) {
			if i.Options.Lyrics != "Sing this" || i.WebhookURL == "" || i.MaxExpectedUSD == nil || *i.MaxExpectedUSD != 0 {
				t.Fatal(i)
			}
		}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			svc := openAIFixture()
			w := openAIRequest(New(svc, healthStub{}, "secret"), tc.path, tc.body)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body)
			}
			if svc.calls != 1 || svc.key != "retry-1" || svc.input.Provider != "mock" || svc.input.MediaType != tc.media {
				t.Fatal(svc)
			}
			tc.check(t, svc.input)
			var view map[string]any
			_ = json.Unmarshal(w.Body.Bytes(), &view)
			if view["status"] != "queued" || view["created_at"] != float64(1234) || view["error"] != nil || view["status_url"] != w.Header().Get("Location") {
				t.Fatal(view)
			}
		})
	}
}

func TestOpenAIInvalidControlsNeverSubmit(t *testing.T) {
	for _, tc := range []struct{ path, body, param string }{
		{"/v1/audio/speech", `{"model":"mock/speech","input":"hello","stream":true}`, "stream"},
		{"/v1/audio/speech", `{"model":"mock/speech","input":"hello","speed":2}`, "speed"},
		{"/v1/audio/speech", `{"model":"mock/speech","input":"hello","prompt":"different"}`, "prompt"},
		{"/v1/images/generations", `{"model":"mock/image","prompt":"x","n":2}`, "n"},
		{"/v1/images/generations", `{"model":"mock/image","prompt":"x","response_format":"b64_json"}`, "response_format"},
		{"/v1/images/generations", `{"model":"mock/image","prompt":"x","size":"1024x1024","options":{"resolution":"2048*2048"}}`, "options.resolution"},
		{"/v1/images/generations", `{"model":"mock/speech","prompt":"x"}`, "model"},
		{"/v1/images/edits", `{"model":"mock/image","prompt":"x"}`, "image"},
		{"/v1/videos", `{"model":"mock/video","prompt":"x","seconds":5}`, "seconds"},
		{"/v1/videos", `{"model":"mock/video","prompt":"x","seconds":"0"}`, "seconds"},
		{"/v1/videos", `{"model":"mock/video","prompt":"x","size":"1280x800"}`, "size"},
		{"/v1/generations", `null`, "body"}, {"/v1/generations", `{} {}`, "body"},
	} {
		t.Run(tc.body, func(t *testing.T) {
			svc := openAIFixture()
			w := openAIRequest(New(svc, healthStub{}, "secret"), tc.path, tc.body)
			if w.Code != 400 || svc.calls != 0 {
				t.Fatal(w.Code, w.Body, svc.calls)
			}
			var v struct{ Error struct{ Param, Type string } }
			_ = json.Unmarshal(w.Body.Bytes(), &v)
			if v.Error.Param != tc.param || v.Error.Type != "invalid_request_error" {
				t.Fatal(w.Body)
			}
		})
	}
}

func TestOpenAIModelResolutionAndDisclosure(t *testing.T) {
	svc := openAIFixture()
	svc.catalog = append(svc.catalog, core.ProviderDescriptor{ID: "other", Models: []core.MediaModel{{ID: "image", MediaType: "image"}}})
	h := New(svc, healthStub{}, "secret")
	if w := openAIRequest(h, "/v1/images/generations", `{"model":"image","prompt":"x"}`); w.Code != 400 || svc.calls != 0 {
		t.Fatal(w.Code, w.Body)
	}
	if w := openAIRequest(h, "/v1/images/generations", `{"model":"other/image","prompt":"x"}`); w.Code != 202 || svc.input.Provider != "other" {
		t.Fatal(w.Code, w.Body)
	}
	for _, path := range []string{"/v1/models", "/v1/models/other/image"} {
		w := sendTest(h, "GET", path, "", "", "secret")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"created":null`) || !strings.Contains(w.Body.String(), `"id":"other/image"`) {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := sendTest(h, "GET", "/v1/models/image", "", "", "secret"); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestGenerationStatusContract(t *testing.T) {
	for _, tc := range []struct {
		status core.TaskStatus
		want   string
	}{{core.TaskQueued, "queued"}, {core.TaskRunning, "in_progress"}, {core.TaskSucceeded, "completed"}, {core.TaskFailed, "failed"}} {
		task := core.Task{ID: "job-1", MediaType: "video", Status: tc.status, CreatedAt: time.Unix(1234, 0), UpdatedAt: time.Unix(5678, 0)}
		h := New(serviceStub{task: task}, healthStub{}, "secret")
		w := sendTest(h, "GET", "/v1/videos/job-1", "", "", "secret")
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"`+tc.want+`"`) || !strings.Contains(w.Body.String(), `"object":"video"`) {
			t.Fatal(w.Code, w.Body)
		}
		w = sendTest(h, "GET", "/v1/generations/job-1", "", "", "secret")
		if !strings.Contains(w.Body.String(), `"status":"`+tc.want+`"`) || !strings.Contains(w.Body.String(), `"created_at":1234`) {
			t.Fatal(w.Body)
		}
	}
	svc := openAIFixture()
	w := openAIRequest(New(svc, healthStub{}, "secret"), "/v1/videos", `{"model":"mock/video","prompt":"x","options":{}}`)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"object":"video"`) || !strings.Contains(w.Body.String(), `"created_at":1234`) || w.Header().Get("Location") != "/v1/videos/job-1" {
		t.Fatal(w.Code, w.Body)
	}
}

func TestOpenAIAdmissionPolicyAndPriceProjection(t *testing.T) {
	svc := openAIFixture()
	s := &Server{service: svc, apiKey: "secret", system: &systemFixture{settings: system.Defaults()}}
	settings := system.Defaults()
	s.system = &systemFixture{settings: settings}
	w := openAIRequest(s.handler(), "/v1/images/generations", `{"model":"mock/image","prompt":"x"}`)
	if w.Code != 202 || svc.calls != 1 {
		t.Fatal(w.Code, w.Body)
	}
	for _, err := range []error{core.ErrBudgetExceeded, core.ErrEstimateChanged, core.ErrIdempotencyConflict, core.ErrKeyUnavailable} {
		svc = openAIFixture()
		svc.err = err
		w = openAIRequest(New(svc, healthStub{}, "secret"), "/v1/audio/speech", `{"model":"mock/speech","input":"x"}`)
		if w.Code == 202 || svc.calls != 1 {
			t.Fatal(w.Code, w.Body)
		}
	}
	cost := core.TaskCost{ExpectedUSD: 1000000, MarkupPercent: 2500, Pricing: core.PricingSnapshot{Source: "PRIVATE TARIFF"}}
	r := httptest.NewRequest("GET", "/v1/generations/job", nil)
	r = r.WithContext(identity.WithOwnerID(r.Context(), "member"))
	w = httptest.NewRecorder()
	s = &Server{}
	s.writeViewerJSON(w, r, 200, core.GenerationView(core.Task{Cost: &cost}))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"expected_usd":1.25`) || strings.Contains(w.Body.String(), "PRIVATE TARIFF") || strings.Contains(w.Body.String(), "markup_percent") {
		t.Fatal(w.Code, w.Body)
	}
}

// Removed routes must stay absent, including for authenticated clients. The same
// video body always selects the single public contract, regardless of old headers.
func TestOnlyUnifiedGenerationRoutes(t *testing.T) {
	h := New(openAIFixture(), healthStub{}, "secret")
	for _, path := range []string{"/v1/media", "/v1/media/estimate", "/v1/tasks", "/v1/tasks/job-1", "/v1/tasks/job-1/media", "/v1/tasks/job-1/media-access", "/v1/tasks/job-1/last-frame"} {
		for _, method := range []string{"GET", "POST", "DELETE", "HEAD"} {
			w := sendTest(h, method, path, `{}`, "", "secret")
			if w.Code != http.StatusNotFound {
				t.Fatalf("removed route %s %s: %d", method, path, w.Code)
			}
		}
	}
	for _, style := range []string{"", "openai", "legacy"} {
		r := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(`{"model":"mock/video","prompt":"test"}`))
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Gateway-API-Style", style)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 202 || body["object"] != "video" || body["status"] != "queued" || body["created_at"] != float64(1234) {
			t.Fatalf("alternate contract for header %q: %d %s", style, w.Code, w.Body)
		}
	}
}

func TestHistoricalGenerationLinksUseCurrentRoutes(t *testing.T) {
	oldResult, oldFrame, vendor := "/v1/tasks/job-1/media", "/v1/tasks/job-1/last-frame", "https://provider.example/private"
	task := core.Task{ID: "job-1", MediaType: "video", Status: core.TaskSucceeded, ResultURL: &oldResult, LastFrameURL: &oldFrame, ProviderResultURL: &vendor}
	view := core.GenerationView(task)
	if view.ResultURL == nil || *view.ResultURL != "/v1/videos/job-1/content" || view.LastFrameURL == nil || *view.LastFrameURL != "/v1/generations/job-1/last-frame" {
		t.Fatal(view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "/v1/tasks") || strings.Contains(string(encoded), vendor) {
		t.Fatal(string(encoded))
	}
	now := time.Now()
	task.ResultDeletedAt = &now
	view = core.GenerationView(task)
	if view.ResultURL != nil || view.LastFrameURL != nil || view.ProviderResultURL != nil {
		t.Fatal("deleted result links exposed")
	}
}

func TestRemovedGenerationInputsAreRejected(t *testing.T) {
	for _, body := range []string{
		`{"model":"video","prompt":"x"}`,
		`{"model":"mock/video","prompt":"x","provider":"mock"}`,
		`{"model":"mock/video","prompt":"x","media_type":"video"}`,
		`{"model":"mock/video","prompt":"x","options":{"duration":0}}`,
		`{"model":"mock/video","prompt":"x","seconds":"5","options":{"duration":5}}`,
		`{"model":"mock/video","prompt":"x","options":{"Duration":5}}`,
		`{"model":"mock/video","prompt":"x","options":{"resolution":null}}`,
		`{"model":"mock/video","prompt":"x","options":{"image_urls":[]}}`,
		`{"model":"mock/video","prompt":"x","options":{"audio_url":""}}`,
		`{"model":"mock/video","prompt":"x","options":{"video_url":""}}`,
		`{"model":"mock/speech","prompt":"hello"}`,
		`{"model":"mock/speech","input":"hello","prompt":"hello"}`,
		`{"model":"mock/speech","input":"hello","options":{"voice":"alloy"}}`,
		`{"model":"mock/speech","input":"hello","options":{"format":"mp3"}}`,
		`{"model":"mock/image","prompt":"x","options":{"quality":"high"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			svc := openAIFixture()
			w := openAIRequest(New(svc, healthStub{}, "secret"), "/v1/generations", body)
			if w.Code != 400 || svc.calls != 0 {
				t.Fatalf("old request accepted: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestOnlyBearerCredentialsAccepted(t *testing.T) {
	s, _ := newPersonalTestServer()
	h := s.handler()
	for _, path := range []string{"/v1/models", "/v1/api-keys"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-API-Key", "service-secret")
		r.Header.Set("X-Test-User", "alice")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("old header accepted or used session: %s %d", path, w.Code)
		}
	}
	if w := sendTest(h, "POST", "/v1/api-keys", `{"name":"Old","storage_mode":"shared"}`, "alice", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestModelIDIsStableAndRoundTrips(t *testing.T) {
	svc := openAIFixture()
	before := catalogModels(svc.catalog)
	svc.catalog = append(svc.catalog, core.ProviderDescriptor{ID: "other", Models: []core.MediaModel{{ID: "image", MediaType: "image"}}})
	after := catalogModels(svc.catalog)
	for i, m := range before {
		if m.ID != after[i].ID {
			t.Fatal("catalog changed existing model ID")
		}
		w := openAIRequest(New(svc, healthStub{}, "secret"), "/v1/generations", `{"model":"`+m.ID+`","options":{}}`)
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body)
		}
		var got struct{ Model string }
		_ = json.Unmarshal(w.Body.Bytes(), &got)
		if got.Model != m.ID {
			t.Fatal("response ID differs from request", got.Model, m.ID)
		}
	}
}

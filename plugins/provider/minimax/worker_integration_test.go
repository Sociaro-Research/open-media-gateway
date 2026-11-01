package minimax_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"github.com/google/uuid"
)

type workerRoundTrip func(*http.Request) (*http.Response, error)

func (f workerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func workerJSON(v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b))}
}

type workerScope struct{ *minimax.Adapter }

func (p workerScope) scope(ctx context.Context) context.Context {
	return core.WithProviderConnectionScope(ctx, core.ProviderConnectionScope{ID: "minimax", OwnerID: identity.ProviderOwnerID(ctx), Revision: "integration-revision"})
}
func (p workerScope) Quote(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.TaskCost, string, error) {
	q, _, e := p.Adapter.Quote(p.scope(ctx), m, r)
	return q, "integration-revision", e
}
func (p workerScope) Generate(ctx context.Context, r core.MediaRequest) (core.ProviderTask, error) {
	return p.Adapter.Generate(p.scope(ctx), r)
}
func (p workerScope) GetTask(ctx context.Context, id string) (core.ProviderResult, error) {
	return p.Adapter.GetTask(p.scope(ctx), id)
}
func (p workerScope) ValidateRequest(ctx context.Context, r core.MediaRequest) error {
	return p.Adapter.ValidateRequest(p.scope(ctx), r)
}
func (p workerScope) OpenArtifact(ctx context.Context, ref string) (core.ArtifactSource, error) {
	return p.Adapter.OpenArtifact(p.scope(ctx), ref)
}

type workerQueue struct {
	mu    sync.Mutex
	known map[string]bool
	items chan string
}

func newWorkerQueue() *workerQueue {
	return &workerQueue{known: map[string]bool{}, items: make(chan string, 32)}
}
func (q *workerQueue) Enqueue(ctx context.Context, id string) error {
	q.mu.Lock()
	if q.known[id] {
		q.mu.Unlock()
		return nil
	}
	q.known[id] = true
	q.mu.Unlock()
	select {
	case q.items <- id:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (q *workerQueue) Dequeue(ctx context.Context) (string, error) {
	select {
	case id := <-q.items:
		return id, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
func (q *workerQueue) Ack(_ context.Context, id string) error {
	q.mu.Lock()
	delete(q.known, id)
	q.mu.Unlock()
	return nil
}
func (q *workerQueue) Requeue(ctx context.Context, id string) error {
	select {
	case q.items <- id:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*workerQueue) Recover(context.Context) error       { return nil }
func (*workerQueue) Renew(context.Context, string) error { return nil }

type workerRepo struct {
	mu                sync.Mutex
	task              core.Task
	claimed, enqueued bool
	due               time.Time
	done              chan struct{}
	once              sync.Once
}

func newWorkerRepo() *workerRepo { return &workerRepo{done: make(chan struct{})} }
func (r *workerRepo) CreateOrGet(_ context.Context, t core.Task, _, _ string) (core.Task, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.task = t
	return t, true, nil
}
func (r *workerRepo) Get(ctx context.Context, id string) (core.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.ID != id || identity.OwnerID(ctx) != r.task.OwnerID {
		return core.Task{}, core.ErrTaskNotFound
	}
	return r.task, nil
}
func (r *workerRepo) ListRecent(ctx context.Context, _ int) ([]core.Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.ID == "" || identity.OwnerID(ctx) != r.task.OwnerID {
		return nil, nil
	}
	return []core.Task{r.task}, nil
}
func (r *workerRepo) MarkEnqueued(context.Context, string) error {
	r.mu.Lock()
	r.enqueued = true
	r.mu.Unlock()
	return nil
}
func (r *workerRepo) MarkRunning(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.task.Status = core.TaskRunning
	now := time.Now()
	r.task.WorkerStartedAt = &now
	return nil
}
func (r *workerRepo) SetProviderTaskID(_ context.Context, _ string, id string) error {
	r.mu.Lock()
	r.task.ProviderTaskID = &id
	r.mu.Unlock()
	return nil
}
func (r *workerRepo) SetProviderUsage(_ context.Context, _ string, u core.ProviderUsage) error {
	r.mu.Lock()
	r.task.Usage = &u
	r.mu.Unlock()
	return nil
}
func (r *workerRepo) SetProviderResultURL(_ context.Context, _ string, url string) error {
	r.mu.Lock()
	r.task.ProviderResultURL = &url
	r.mu.Unlock()
	return nil
}
func (r *workerRepo) MarkSucceeded(_ context.Context, _ string, m core.StoredMedia, _ string) error {
	r.mu.Lock()
	r.task.Status = core.TaskSucceeded
	r.task.StorageKey = &m.StorageKey
	r.task.ResultType = &m.ContentType
	r.task.ResultSize = &m.SizeBytes
	r.task.ResultSHA256 = &m.SHA256
	r.task.StoredArtifacts = append([]core.StoredArtifact(nil), m.Artifacts...)
	r.task.HasStoredResult = true
	now := time.Now()
	r.task.StoredAt = &now
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
	return nil
}
func (r *workerRepo) MarkFailed(_ context.Context, _ string, message string) error {
	r.mu.Lock()
	r.task.Status = core.TaskFailed
	r.task.Error = &message
	r.mu.Unlock()
	r.once.Do(func() { close(r.done) })
	return nil
}
func (*workerRepo) ListRecoverable(context.Context) ([]string, error) { return nil, nil }
func (r *workerRepo) AcquireTask(context.Context, string, time.Duration) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed || r.task.Status == core.TaskSucceeded || r.task.Status == core.TaskFailed {
		return false, nil
	}
	r.claimed = true
	return true, nil
}
func (*workerRepo) RenewTask(context.Context, string, time.Duration) error { return nil }
func (r *workerRepo) ReleaseTask(context.Context, string) error {
	r.mu.Lock()
	r.claimed = false
	r.mu.Unlock()
	return nil
}
func (r *workerRepo) ScheduleTask(_ context.Context, _ string, delay time.Duration, n int) error {
	r.mu.Lock()
	r.due = time.Now().Add(delay)
	r.task.WorkerPollErrors = n
	r.enqueued = false
	r.mu.Unlock()
	return nil
}
func (r *workerRepo) MarkEnqueuedBatch(_ context.Context, ids []string) error {
	r.mu.Lock()
	if len(ids) > 0 {
		r.enqueued = true
	}
	r.mu.Unlock()
	return nil
}
func (r *workerRepo) ListRecoverableBatch(context.Context, string, int) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.task.ID != "" && !r.claimed && !r.enqueued && r.task.Status != core.TaskSucceeded && r.task.Status != core.TaskFailed && time.Now().After(r.due) {
		return []string{r.task.ID}, nil
	}
	return nil, nil
}

type workerHealth struct{}

func (workerHealth) Check(context.Context) error { return nil }
func workerRequest(h http.Handler, method, path string, body []byte, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		r.Header.Set("Authorization", "Bearer gateway-fixture")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// These tests use the real public HTTP decoder, TaskService, concurrent Worker
// and Local archive/open/content path. Only persistence/queue and vendor/CDN HTTP
// are deterministic fixtures; PostgreSQL restart durability is a separate check.
func TestMiniMaxWorkerArchiveAndPublicContent(t *testing.T) {
	for _, scenario := range []string{"image", "multiple_images", "video_url", "protected_video", "context_text", "video_agent", "regeneration"} {
		t.Run(scenario, func(t *testing.T) {
			pngData := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 512)...)
			videoData := append([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p'}, []byte("isom0000isom")...)
			enhanced := "Structured private enhanced prompt: native-scene-evidence"
			expected := pngData
			mediaType, model, path, count := "image", "image-01", "/v1/images/generations", 1
			if scenario == "multiple_images" {
				count = 2
			}
			if scenario == "video_url" {
				mediaType, model, path = "video", "MiniMax-H3", "/v1/videos"
				expected = videoData
			}
			if scenario == "protected_video" {
				mediaType, model, path = "video", "MiniMax-Hailuo-2.3", "/v1/videos"
				expected = videoData
			}
			if scenario == "video_agent" {
				mediaType, model, path = "video", "video-agent-394514820878671878", "/v1/videos"
				expected = videoData
			}
			if scenario == "regeneration" {
				mediaType, model, path = "video", minimax.Regeneration, "/v1/videos"
				expected = videoData
			}
			if scenario == "context_text" {
				mediaType, model, path = "text", minimax.ContextIR, "/v1/generations"
				expected = []byte(enhanced)
			}
			native := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.minimax.io" || r.Header.Get("Authorization") != "Bearer provider-fixture-secret" {
					t.Fatal("unexpected authenticated upstream destination")
				}
				switch r.URL.Path {
				case "/v1/image_generation":
					var p struct {
						N int `json:"n"`
					}
					if e := json.NewDecoder(r.Body).Decode(&p); e != nil || p.N != count {
						t.Fatal("canonical n mapping", p, e)
					}
					urls := []string{"https://cdn.fixture/one.png"}
					if count == 2 {
						urls = append(urls, "https://cdn.fixture/two.png")
					}
					return workerJSON(map[string]any{"data": map[string]any{"image_urls": urls}, "metadata": map[string]int{"success_count": count, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v2/video_generation":
					if scenario == "regeneration" {
						return workerJSON(map[string]string{"task_id": "777"}), nil
					}
					return workerJSON(map[string]string{"task_id": "123"}), nil
				case "/v2/video_regeneration", "/v2/h3_context_ir":
					return workerJSON(map[string]string{"task_id": "123"}), nil
				case "/v2/query/video_generation/123", "/v2/query/video_generation/777":
					kind, modality := "generation", "video"
					content := map[string]string{"url": "https://cdn.fixture/video.mp4"}
					resolution := "768P"
					id := "123"
					if strings.HasSuffix(r.URL.Path, "/777") {
						id = "777"
					} else if scenario == "regeneration" {
						kind, resolution = "regeneration", "2K"
					}
					usage := map[string]int{"input_seconds": 0, "output_seconds": 5, "input_image_count": 0}
					if scenario == "context_text" {
						kind, modality = "h3_context_ir", "text"
						content = map[string]string{"prompt": enhanced}
						resolution = ""
						usage = map[string]int{"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150}
					}
					return workerJSON(map[string]any{"task": map[string]any{"id": id, "model": "MiniMax-H3", "status": "succeeded", "task_type": kind, "modality": modality, "content": content, "resolution": resolution, "duration": 5, "usage": usage}}), nil
				case "/v1/video_template_generation":
					return workerJSON(map[string]any{"task_id": "123", "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/query/video_template_generation":
					return workerJSON(map[string]any{"task_id": "123", "status": "Success", "video_url": "https://cdn.fixture/video.mp4", "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/video_generation":
					return workerJSON(map[string]any{"task_id": "123", "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/query/video_generation":
					return workerJSON(map[string]any{"task_id": "123", "status": "Success", "file_id": "456", "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/files/retrieve":
					return workerJSON(map[string]any{"file": map[string]any{"file_id": "456", "purpose": "video_generation", "bytes": len(videoData)}, "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/files/retrieve_content":
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(videoData)), Body: io.NopCloser(bytes.NewReader(videoData))}, nil
				default:
					t.Fatalf("unexpected upstream route %s", r.URL.Path)
					return nil, nil
				}
			})}
			adapter, e := minimax.New("provider-fixture-secret", "", native)
			if e != nil {
				t.Fatal(e)
			}
			provider := workerScope{adapter}
			cdn := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "cdn.fixture" || r.Header.Get("Authorization") != "" {
					t.Fatal("CDN received credentials or wrong destination")
				}
				data, typ := pngData, "image/png"
				if strings.HasSuffix(r.URL.Path, ".mp4") {
					data, typ = videoData, "video/mp4"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {typ}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
			})}
			store, e := storage.NewLocal(t.TempDir(), cdn, 1<<20)
			if e != nil {
				t.Fatal(e)
			}
			repo, q := newWorkerRepo(), newWorkerQueue()
			service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": provider}, map[string]string{"minimax": model}, "minimax")
			handler := api.NewWithStorage(service, workerHealth{}, "gateway-fixture", store, "signing-fixture", time.Hour)
			payload := map[string]any{"model": "minimax/" + model, "prompt": "Prepare this scene"}
			if scenario == "regeneration" {
				source, err := provider.Generate(context.Background(), core.MediaRequest{Model: "MiniMax-H3", Prompt: "Original source scene"})
				if err != nil {
					t.Fatal(err)
				}
				delete(payload, "prompt")
				duration := 5.
				ctx := identity.WithOwnerID(context.Background(), identity.SystemOwnerID)
				job, e := provider.Adapter.CreateResource(provider.scope(ctx), core.ResourceCreateRequest{Kind: "file_resource", Subtype: "regeneration_source", Name: "Owned source", GeneratedVideo: &core.GeneratedResourceSource{TaskID: uuid.NewString(), TaskHandle: source.ID, Model: "MiniMax-H3", Duration: &duration}})
				if e != nil {
					t.Fatal(e)
				}
				binding := uuid.NewString()
				snapshot := core.ResourceSnapshot{BindingID: binding, ReferenceID: "source_task", Kind: "file_resource", Subtype: "regeneration_source", RemoteID: job.Handle, ConnectionIdentity: "integration-revision"}
				service.SetReferenceResourceResolver(fixtureSourceResolver{snapshot: snapshot})
				payload["options"] = map[string]any{"resource_references": []map[string]string{{"binding_id": binding, "reference_id": "source_task"}}}
			}
			if mediaType == "image" {
				payload["n"] = count
			}

			data, _ := json.Marshal(payload)
			posted := workerRequest(handler, "POST", path, data, true)
			if posted.Code != http.StatusAccepted && posted.Code != http.StatusOK {
				t.Fatalf("public admission %d: %s", posted.Code, posted.Body.String())
			}
			var admitted struct {
				ID string `json:"id"`
			}
			if e = json.Unmarshal(posted.Body.Bytes(), &admitted); e != nil || admitted.ID == "" {
				t.Fatal(posted.Body.String(), e)
			}
			runCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			w := worker.New(repo, q, provider, store, time.Millisecond, 3*time.Second, 2, time.Millisecond)
			w.OperationConcurrency = 1
			w.ArchiveConcurrency = 1
			finished := make(chan error, 1)
			go func() { finished <- w.Run(runCtx) }()
			select {
			case <-repo.done:
			case <-runCtx.Done():
				t.Fatal("worker did not archive", runCtx.Err())
			}
			cancel()
			if e := <-finished; e != nil {
				t.Fatal(e)
			}
			task, e := repo.Get(context.Background(), admitted.ID)
			if e != nil || task.Status != core.TaskSucceeded {
				t.Fatal(task.Status, task.Error, e)
			}
			public := workerRequest(handler, "GET", "/v1/generations/"+admitted.ID, nil, true)
			if public.Code != 200 {
				t.Fatal(public.Code, public.Body.String())
			}
			for _, private := range []string{"provider-fixture-secret", "https://cdn.fixture", enhanced, `"source_reference"`, `"storage_key"`, "minimax:"} {
				if strings.Contains(public.Body.String(), private) {
					t.Fatal("private artifact material exposed", private)
				}
			}
			var view struct {
				Artifacts  []map[string]any `json:"artifacts"`
				ContentURL string           `json:"content_url"`
			}
			if e = json.Unmarshal(public.Body.Bytes(), &view); e != nil {
				t.Fatal(e)
			}
			wantArtifacts := count
			if scenario == "video_agent" {
				wantArtifacts = 0
			}
			if len(view.Artifacts) != wantArtifacts {
				t.Fatalf("artifacts=%d want=%d: %s", len(view.Artifacts), wantArtifacts, public.Body.String())
			}
			urls := []string{view.ContentURL}
			allowed := map[string]bool{"id": true, "media_type": true, "format": true, "duration": true, "group_id": true, "content_url": true, "content_type": true, "size_bytes": true, "sha256": true}
			for _, artifact := range view.Artifacts {
				for key := range artifact {
					if !allowed[key] {
						t.Fatal("private artifact field", key)
					}
				}
				urls = append(urls, artifact["content_url"].(string))
			}
			digest := sha256.Sum256(expected)
			for _, url := range urls {
				got := workerRequest(handler, "GET", url, nil, true)
				if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), expected) {
					t.Fatalf("archived GET %d bytes=%d expected=%d", got.Code, got.Body.Len(), len(expected))
				}
				head := workerRequest(handler, "HEAD", url, nil, true)
				if head.Code != 200 || head.Body.Len() != 0 {
					t.Fatal("HEAD", head.Code, head.Body.Len())
				}
				unauth := workerRequest(handler, "GET", url, nil, false)
				if unauth.Code != 401 && unauth.Code != 403 {
					t.Fatal("unauthenticated artifact accepted", unauth.Code)
				}
			}
			if task.ResultSHA256 == nil || *task.ResultSHA256 != hex.EncodeToString(digest[:]) {
				t.Fatal("archived checksum mismatch")
			}
			if _, e := repo.Get(identity.WithOwnerID(context.Background(), "foreign-owner"), admitted.ID); !errors.Is(e, core.ErrTaskNotFound) {
				t.Fatal("foreign task access", e)
			}
		})
	}
}

type fixtureSourceResolver struct{ snapshot core.ResourceSnapshot }

func (f fixtureSourceResolver) ResolveResourceReferences(ctx context.Context, _ string, refs []core.ResourceReference) ([]core.ResourceSnapshot, error) {
	if identity.OwnerID(ctx) != identity.SystemOwnerID || len(refs) != 1 || refs[0].BindingID != f.snapshot.BindingID {
		return nil, core.ErrTaskNotFound
	}
	return []core.ResourceSnapshot{f.snapshot}, nil
}

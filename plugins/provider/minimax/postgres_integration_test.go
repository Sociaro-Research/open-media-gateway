package minimax_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/queue"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/worker"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/minimax"
	"github.com/google/uuid"
)

func minimaxPostgres(t *testing.T) (context.Context, *sql.DB, string, *queue.RedisQueue) {
	t.Helper()
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("requires disposable PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	schema := "minimax_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Exec("DROP SCHEMA " + schema + " CASCADE") })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	privateDSN := u.String()
	db, err := postgres.Open(ctx, privateDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	redisQueue, err := queue.Open(ctx, redisURL, "minimax-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { redisQueue.Close() })
	return ctx, db, privateDSN, redisQueue
}

// Provider/CDN HTTP is deterministic. Admission, outbox, claims, queue, usage,
// archival, restart reads and public content are the real PostgreSQL/Redis path.
func TestMiniMaxPostgresArchiveRestartAndRedaction(t *testing.T) {
	for _, scenario := range []string{"image", "multiple_images", "video_url", "protected_video", "context_text", "video_agent"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, db, dsn, q := minimaxPostgres(t)
			repo := postgres.NewTaskRepository(db)
			png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 512)...)
			video := append([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p'}, []byte("isom0000isom")...)
			text := "Private prepared scene: PostgreSQL context fixture"
			model, path, expected, count := "image-01", "/v1/images/generations", png, 1
			switch scenario {
			case "multiple_images":
				count = 2
			case "video_url":
				model, path, expected = "MiniMax-H3", "/v1/videos", video
			case "protected_video":
				model, path, expected = "MiniMax-Hailuo-2.3", "/v1/videos", video
			case "context_text":
				model, path, expected = minimax.ContextIR, "/v1/generations", []byte(text)
			case "video_agent":
				model, path, expected = "video-agent-394514820878671878", "/v1/videos", video
			}
			var submissions atomic.Int32
			native := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api.minimax.io" || r.Header.Get("Authorization") != "Bearer pg-provider-fixture" {
					t.Fatal("unexpected provider destination/auth")
				}
				if r.Method == "POST" {
					submissions.Add(1)
				}
				switch r.URL.Path {
				case "/v1/image_generation":
					urls := []string{"https://cdn.fixture/one.png"}
					if count == 2 {
						urls = append(urls, "https://cdn.fixture/two.png")
					}
					return workerJSON(map[string]any{"data": map[string]any{"image_urls": urls}, "metadata": map[string]int{"success_count": count, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v2/video_generation", "/v2/h3_context_ir", "/v1/video_generation", "/v1/video_template_generation":
					return workerJSON(map[string]any{"task_id": "123", "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v2/query/video_generation/123":
					kind, modality, res := "generation", "video", "768P"
					content := map[string]string{"url": "https://cdn.fixture/video.mp4"}
					usage := map[string]int{"input_seconds": 0, "output_seconds": 5, "input_image_count": 0}
					if scenario == "context_text" {
						kind, modality, res = "h3_context_ir", "text", ""
						content = map[string]string{"prompt": text}
						usage = map[string]int{"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150}
					}
					return workerJSON(map[string]any{"task": map[string]any{"id": "123", "model": "MiniMax-H3", "task_type": kind, "modality": modality, "resolution": res, "duration": 5, "status": "succeeded", "content": content, "usage": usage}}), nil
				case "/v1/query/video_generation":
					return workerJSON(map[string]any{"task_id": "123", "status": "Success", "file_id": "456", "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/query/video_template_generation":
					return workerJSON(map[string]any{"task_id": "123", "status": "Success", "video_url": "https://cdn.fixture/video.mp4", "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/files/retrieve":
					return workerJSON(map[string]any{"file": map[string]any{"file_id": "456", "purpose": "video_generation", "bytes": len(video)}, "base_resp": map[string]int{"status_code": 0}}), nil
				case "/v1/files/retrieve_content":
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, ContentLength: int64(len(video)), Body: io.NopCloser(bytes.NewReader(video))}, nil
				default:
					t.Fatalf("unexpected provider path %s", r.URL.Path)
					return nil, nil
				}
			})}
			adapter, err := minimax.New("pg-provider-fixture", "", native)
			if err != nil {
				t.Fatal(err)
			}
			provider := workerScope{adapter}
			cdn := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "cdn.fixture" || r.Header.Get("Authorization") != "" {
					t.Fatal("CDN credential leak")
				}
				b, typ := png, "image/png"
				if strings.HasSuffix(r.URL.Path, ".mp4") {
					b, typ = video, "video/mp4"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {typ}}, ContentLength: int64(len(b)), Body: io.NopCloser(bytes.NewReader(b))}, nil
			})}
			root := t.TempDir()
			store, err := storage.NewLocal(root, cdn, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": provider}, map[string]string{"minimax": model}, "minimax")
			service.SetAdmissionScopeResolver(pgResourceScope)
			// Unknown Agent prices remain explicit; no zero-price tariff is introduced.
			if scenario != "video_agent" {
				service.EnableAccounting()
			}
			handler := api.NewWithStorage(service, workerHealth{}, "gateway-fixture", store, "signing-fixture", time.Hour)
			owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
			if scenario == "context_text" {
				cap := money.USD(1000000)
				_, err = service.CreateMediaRequest(owner, core.CreateMediaInput{Model: model, Prompt: "Prepared scene", MaxExpectedUSD: &cap}, "capped")
				if !errors.Is(err, core.ErrPricingUnavailable) || submissions.Load() != 0 {
					t.Fatal("unknown total satisfied explicit cap", err, submissions.Load())
				}
				var rows int
				if err = db.QueryRowContext(ctx, "SELECT count(*) FROM tasks").Scan(&rows); err != nil || rows != 0 {
					t.Fatal("capped admission persisted", rows, err)
				}
			}
			payload := map[string]any{"model": "minimax/" + model, "prompt": "Prepare this scene"}
			if model == "image-01" {
				payload["n"] = count
			}
			body, _ := json.Marshal(payload)
			posted := workerRequest(handler, "POST", path, body, true)
			if posted.Code != 202 {
				t.Fatalf("admission %d: %s", posted.Code, posted.Body.String())
			}
			var admitted struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal(posted.Body.Bytes(), &admitted); err != nil || admitted.ID == "" {
				t.Fatal("missing admitted task", err)
			}
			workCtx, cancel := context.WithCancel(ctx)
			w := worker.New(repo, q, provider, store, time.Millisecond, 2*time.Second, 2, time.Millisecond)
			w.OperationConcurrency = 1
			w.ArchiveConcurrency = 1
			finished := make(chan error, 1)
			go func() { finished <- w.Run(workCtx) }()
			var task core.Task
			for {
				task, err = repo.Get(owner, admitted.ID)
				if err != nil {
					cancel()
					<-finished
					t.Fatal(err)
				}
				if task.Status == core.TaskSucceeded || task.Status == core.TaskFailed {
					break
				}
				select {
				case <-ctx.Done():
					cancel()
					<-finished
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			cancel()
			if err = <-finished; err != nil {
				t.Fatal(err)
			}
			if task.Status != core.TaskSucceeded {
				t.Fatal("worker failed", task.Error)
			}
			if submissions.Load() != 1 {
				t.Fatal("submission count", submissions.Load())
			}
			if scenario == "context_text" {
				if task.Cost == nil || !task.Cost.QuantityUnknown || task.Cost.MeteredUSD == nil || *task.Cost.MeteredUSD != 270 {
					t.Fatal("token tariff not retained/metered", task.Cost)
				}
				var raw string
				if err = db.QueryRowContext(ctx, "SELECT cost::text FROM tasks WHERE id=$1", task.ID).Scan(&raw); err != nil || strings.Contains(raw, `"expected_usd"`) || strings.Contains(raw, `"estimated_units"`) {
					t.Fatal("unknown quote serialized as zero", err)
				}
			}
			// Reopen both persistence and storage, as a new API/worker process would.
			reopened, err := postgres.Open(ctx, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			restartedRepo := postgres.NewTaskRepository(reopened)
			restartedStore, err := storage.NewLocal(root, cdn, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			restartedService := core.NewTaskServiceWithProviders(restartedRepo, q, map[string]core.MediaProvider{"minimax": provider}, map[string]string{"minimax": model}, "minimax")
			restarted := api.NewWithStorage(restartedService, workerHealth{}, "gateway-fixture", restartedStore, "signing-fixture", time.Hour)
			status := workerRequest(restarted, "GET", "/v1/generations/"+task.ID, nil, true)
			if status.Code != 200 {
				t.Fatal(status.Code)
			}
			for _, private := range []string{"pg-provider-fixture", "https://cdn.fixture", text, "source_reference", "minimax:"} {
				if strings.Contains(status.Body.String(), private) {
					t.Fatal("private output exposed", private)
				}
			}
			var view struct {
				ContentURL string `json:"content_url"`
				Artifacts  []struct {
					ContentURL string `json:"content_url"`
				} `json:"artifacts"`
			}
			if err = json.Unmarshal(status.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			urls := []string{view.ContentURL}
			for _, a := range view.Artifacts {
				urls = append(urls, a.ContentURL)
			}
			if scenario == "multiple_images" && len(view.Artifacts) != 2 {
				t.Fatal("batch persistence lost artifacts", len(view.Artifacts))
			}
			for _, contentURL := range urls {
				got := workerRequest(restarted, "GET", contentURL, nil, true)
				if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), expected) {
					t.Fatalf("restart content %d bytes%d", got.Code, got.Body.Len())
				}
				head := workerRequest(restarted, "HEAD", contentURL, nil, true)
				if head.Code != 200 || head.Body.Len() != 0 {
					t.Fatal("restart HEAD", head.Code)
				}
				if denied := workerRequest(restarted, "GET", contentURL, nil, false); denied.Code != 401 && denied.Code != 403 {
					t.Fatal("unauthenticated content", denied.Code)
				}
			}
			if _, err = restartedRepo.Get(identity.WithOwnerID(ctx, uuid.NewString()), task.ID); !errors.Is(err, core.ErrTaskNotFound) {
				t.Fatal("foreign owner read", err)
			}
		})
	}
}

// Force an interruption after an immediate paid response and durable provider
// handle, while the first output is awaiting archival. No provider call repeats.
type interruptBeforeImageArchive struct {
	once           sync.Once
	cancel         context.CancelFunc
	destination    worker.ResultStorage
	passBeforeStop int
	calls          int
}

func (s *interruptBeforeImageArchive) Archive(ctx context.Context, id, source string) (core.StoredMedia, error) {
	s.calls++
	if s.calls <= s.passBeforeStop {
		return s.destination.Archive(ctx, id, source)
	}
	s.once.Do(s.cancel)
	<-ctx.Done()
	return core.StoredMedia{}, ctx.Err()
}

func TestMiniMaxPostgresImmediateImageBatchSurvivesArchiveInterruption(t *testing.T) {
	for _, tc := range []struct {
		name string
		pass int
	}{{"before first artifact", 0}, {"between batch artifacts", 1}} {
		t.Run(tc.name, func(t *testing.T) { testImmediateImageBatchRecovery(t, tc.pass) })
	}
}
func testImmediateImageBatchRecovery(t *testing.T, passBeforeStop int) {
	ctx, db, dsn, q := minimaxPostgres(t)
	owner := identity.WithOwnerID(ctx, identity.SystemOwnerID)
	repo := postgres.NewTaskRepository(db)
	var submissions atomic.Int32
	native := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method != "POST" || r.URL.Path != "/v1/image_generation" {
			t.Fatal("immediate image recovery must not use an invented task endpoint")
		}
		submissions.Add(1)
		return workerJSON(map[string]any{"data": map[string]any{"image_urls": []string{"https://cdn.fixture/one.png", "https://cdn.fixture/two.png"}}, "metadata": map[string]int{"success_count": 2, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
	})}
	adapter, err := minimax.New("image-recovery-fixture", "", native)
	if err != nil {
		t.Fatal(err)
	}
	provider := workerScope{adapter}
	service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"minimax": provider}, map[string]string{"minimax": "image-01"}, "minimax")
	service.SetAdmissionScopeResolver(pgResourceScope)
	service.EnableAccounting()
	handler := api.NewWithStorage(service, workerHealth{}, "gateway-fixture", nil, "signing-fixture", time.Hour)
	imageRequest, _ := json.Marshal(map[string]any{"model": "minimax/image-01", "prompt": "Two archived images", "n": 2})
	posted := workerRequest(handler, "POST", "/v1/images/generations", imageRequest, true)
	if posted.Code != 202 {
		t.Fatalf("image recovery public admission %d:%s", posted.Code, posted.Body.String())
	}
	var accepted struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(posted.Body.Bytes(), &accepted)
	task, err := repo.Get(owner, accepted.ID)
	if err != nil {
		t.Fatal(err)
	}
	images := map[string][]byte{"/one.png": []byte("\x89PNG\r\n\x1a\none"), "/two.png": []byte("\x89PNG\r\n\x1a\ntwo")}
	expectedByID := map[string][]byte{"1": images["/one.png"], "2": images["/two.png"]}
	cdn := &http.Client{Transport: workerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "cdn.fixture" || r.Header.Get("Authorization") != "" {
			t.Fatal("image recovery leaked provider credentials")
		}
		image, ok := images[r.URL.Path]
		if !ok {
			t.Fatal("unknown image recovery CDN path")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, ContentLength: int64(len(image)), Body: io.NopCloser(bytes.NewReader(image))}, nil
	})}
	root := t.TempDir()
	store, err := storage.NewLocal(root, cdn, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	interrupted := &interruptBeforeImageArchive{cancel: cancel, destination: store, passBeforeStop: passBeforeStop}
	first := worker.New(repo, q, provider, interrupted, time.Millisecond, 2*time.Second, 2, time.Millisecond)
	first.OperationConcurrency = 1
	first.ArchiveConcurrency = 1
	first.ShutdownTimeout = time.Millisecond
	if err = first.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	pending, err := repo.Get(owner, task.ID)
	if err != nil || pending.Status != core.TaskRunning || pending.ProviderTaskID == nil || len(pending.StoredArtifacts) > passBeforeStop || submissions.Load() != 1 {
		t.Fatal("interruption did not retain accepted pending response", err, submissions.Load())
	}
	// Reopen persistence and instantiate a new adapter: process-local image data
	// cannot satisfy recovery, and a second generation would duplicate charges.
	reopened, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	adapter, err = minimax.New("image-recovery-fixture", "", native)
	if err != nil {
		t.Fatal(err)
	}
	provider = workerScope{adapter}
	store, err = storage.NewLocal(root, cdn, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	restartedRepo := postgres.NewTaskRepository(reopened)
	result := runResourceWorker(t, ctx, restartedRepo, q, provider, store, task.ID)
	if result.Status != core.TaskSucceeded || len(result.StoredArtifacts) != 2 || submissions.Load() != 1 {
		message := ""
		if result.Error != nil {
			message = *result.Error
		}
		t.Fatalf("accepted synchronous image batch must recover both outputs without resubmission: status=%s outputs=%d submissions=%d error=%s", result.Status, len(result.StoredArtifacts), submissions.Load(), message)
	}
	if result.StoredArtifacts[0].ID != "1" || result.StoredArtifacts[1].ID != "2" {
		t.Fatal("recovery changed ordered stable artifact IDs")
	}
	if result.Cost == nil || result.Cost.MeteredUSD == nil || *result.Cost.MeteredUSD != 7000 {
		t.Fatal("recovered image usage did not retain exact two-image metering")
	}
	restartedService := core.NewTaskServiceWithProviders(restartedRepo, q, map[string]core.MediaProvider{"minimax": provider}, map[string]string{"minimax": "image-01"}, "minimax")
	restarted := api.NewWithStorage(restartedService, workerHealth{}, "gateway-fixture", store, "signing-fixture", time.Hour)
	view := workerRequest(restarted, "GET", "/v1/generations/"+task.ID, nil, true)
	if view.Code != 200 || strings.Contains(view.Body.String(), "minimax:") || strings.Contains(view.Body.String(), "cdn.fixture") || strings.Contains(view.Body.String(), "image-recovery-fixture") {
		t.Fatal("recovered public result leaked private receipt")
	}
	var public struct {
		Artifacts []struct {
			ID         string `json:"id"`
			ContentURL string `json:"content_url"`
		} `json:"artifacts"`
	}
	_ = json.Unmarshal(view.Body.Bytes(), &public)
	if len(public.Artifacts) != 2 {
		t.Fatal("recovered public result omitted an image")
	}
	for _, artifact := range public.Artifacts {
		for _, method := range []string{"GET", "HEAD"} {
			response := workerRequest(restarted, method, artifact.ContentURL, nil, true)
			if response.Code != 200 || method == "GET" && !bytes.Equal(response.Body.Bytes(), expectedByID[artifact.ID]) {
				t.Fatal("recovered image content unavailable", method, response.Code)
			}
			if denied := workerRequest(restarted, method, artifact.ContentURL, nil, false); denied.Code != 401 && denied.Code != 403 {
				t.Fatal("unauthenticated recovered content", method, denied.Code)
			}
		}
	}
	if _, err = restartedRepo.Get(identity.WithOwnerID(ctx, uuid.NewString()), task.ID); !errors.Is(err, core.ErrTaskNotFound) {
		t.Fatal("foreign owner read of recovered image task", err)
	}
}

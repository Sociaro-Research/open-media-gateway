package agnes_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/api"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhook"
	"github.com/Sociaro-Research/media-gateway-dev/internal/worker"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/agnes"
	"github.com/google/uuid"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// Real Gateway admission, migrations, durable queue, worker, local archival,
// authenticated/signed downloads and webhook delivery; only Agnes HTTP is fake.
func TestAgnesGatewayWorkflow(t *testing.T) {
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("requires isolated PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "agnes_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = base.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer base.Exec("DROP SCHEMA " + schema + " CASCADE")
	u, _ := url.Parse(dsn)
	params := u.Query()
	params.Set("search_path", schema)
	u.RawQuery = params.Encode()
	db, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = postgres.Migrate(db); err != nil {
		t.Fatal(err)
	}
	jobs, err := queue.Open(ctx, redisURL, "agnes-jobs-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	callbacks, err := queue.Open(ctx, redisURL, "agnes-events-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer callbacks.Close()
	var submissions, polls atomic.Int32
	p, err := agnes.New("fixture-key", agnes.DefaultBaseURL, &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/v1/models":
			return jsonResponse(`{"data":[{"id":"agnes-image-2.5-flash"},{"id":"agnes-video-2.5"}]}`), nil
		case "/v1/images/generations":
			submissions.Add(1)
			return jsonResponse(`{"data":[{"url":"https://cdn.example/image.png"}]}`), nil
		case "/v1/videos":
			submissions.Add(1)
			return jsonResponse(`{"video_id":"fixture-video","model":"agnes-video-2.5","status":"queued"}`), nil
		case "/agnesapi":
			if r.URL.Query().Get("model_name") != "agnes-video-2.5" {
				t.Error("poll lost model name")
			}
			if polls.Add(1)%2 == 1 {
				return jsonResponse(`{"status":"in_progress"}`), nil
			}
			return jsonResponse(`{"status":"completed","url":"https://cdn.example/video.mp4","seconds":"5"}`), nil
		default:
			t.Errorf("unexpected provider endpoint %s", r.URL)
			return jsonResponse(`{}`), nil
		}
	})}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	imageBytes := []byte("\x89PNG\r\n\x1a\nfixture-image")
	videoBytes := []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2fixture-video")
	media, err := storage.NewLocal(t.TempDir(), &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Error("provider key was forwarded to result CDN")
		}
		data, kind := imageBytes, "image/png"
		if strings.HasSuffix(r.URL.Path, ".mp4") {
			data, kind = videoBytes, "video/mp4"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewTaskRepository(db)
	service := core.NewTaskServiceWithProviders(repo, jobs, map[string]core.MediaProvider{"agnes": p}, map[string]string{"agnes": agnes.DefaultModel}, "agnes")
	service.EnableAccounting()
	h := api.NewWithStorage(service, nil, "gateway-fixture-key", media, "media-fixture-secret", time.Hour)
	workCtx, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 2)
	w := worker.New(repo, jobs, p, media, 5*time.Millisecond, 5*time.Second, 2, 5*time.Millisecond)
	go func() { done <- w.Run(workCtx) }()
	delivered := make(chan string, 16)
	sender := webhook.NewSender("webhook-fixture-secret", &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte("webhook-fixture-secret"))
		mac.Write([]byte(r.Header.Get("X-Media-Gateway-Timestamp") + "."))
		mac.Write(body)
		if r.Header.Get("X-Media-Gateway-Signature") != "v1="+hex.EncodeToString(mac.Sum(nil)) {
			t.Error("invalid webhook signature")
		}
		if r.Header.Get("X-Media-Gateway-Event") == "generation.completed" {
			var event struct {
				TaskID string          `json:"task_id"`
				Data   core.Generation `json:"data"`
			}
			_ = json.Unmarshal(body, &event)
			if event.Data.ContentURL == "" || strings.Contains(string(body), "https://cdn.example/image.png") || strings.Contains(string(body), "https://cdn.example/video.mp4") {
				t.Error("webhook exposed upstream result or omitted content URL")
			}
			delivered <- event.Data.ID
		}
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	})})
	go func() { done <- webhook.NewWorker(repo, callbacks, sender, 2, time.Millisecond).Run(workCtx) }()
	defer func() {
		stop()
		for i := 0; i < 2; i++ {
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(3 * time.Second):
				t.Error("worker did not stop")
			}
		}
	}()
	request := func(method, path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://gateway.example"+path, strings.NewReader(body))
		if key != "" {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		r.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		h.ServeHTTP(result, r)
		return result
	}
	for i, tc := range []struct{ path, model, size, mediaType string }{
		{"/v1/images/generations", agnes.DefaultModel, "2K", "image"},
		{"/v1/images/edits", agnes.DefaultModel, "2K", "image"},
		{"/v1/videos", "agnes-video-2.5", "720P", "video"},
		{"/v1/generations", agnes.DefaultModel, "1K", "image"},
	} {
		body := map[string]any{"model": "agnes/" + tc.model, "prompt": "fixture", "size": tc.size, "webhook_url": "https://callback.example/events"}
		if tc.mediaType == "video" {
			body["options"] = map[string]any{"parameters": map[string]any{"video_require_audio": false}}
		}
		if tc.path == "/v1/images/edits" {
			body["image"] = []string{"https://cdn.example/reference.png"}
		}
		encoded, _ := json.Marshal(body)
		result := request("POST", tc.path, string(encoded), "gateway-fixture-key")
		if result.Code != 202 {
			t.Fatal(tc, result.Code, result.Body)
		}
		var generation core.Generation
		if err = json.Unmarshal(result.Body.Bytes(), &generation); err != nil {
			t.Fatal(err)
		}
		ticker := time.NewTicker(5 * time.Millisecond)
		for {
			task, err := repo.Get(ctx, generation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if task.Status == core.TaskFailed {
				t.Fatal(task.Error)
			}
			if task.Status == core.TaskSucceeded {
				if task.StorageKey == nil || task.Cost == nil || task.Cost.ActualUSD != nil {
					t.Fatal(task)
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("generation timeout", ctx.Err())
			case <-ticker.C:
			}
		}
		ticker.Stop()
		result = request("GET", "/v1/generations/"+generation.ID, "", "gateway-fixture-key")
		if result.Code != 200 || strings.Contains(result.Body.String(), "https://cdn.example/image.png") || strings.Contains(result.Body.String(), "https://cdn.example/video.mp4") {
			t.Fatal(result.Code, result.Body)
		}
		result = request("GET", "/v1/generations/"+generation.ID+"/content", "", "gateway-fixture-key")
		expected := imageBytes
		if tc.mediaType == "video" {
			expected = videoBytes
		}
		if result.Code != 200 || !bytes.Equal(result.Body.Bytes(), expected) {
			t.Fatal("download differs", i, result.Code)
		}
		if unauthorized := request("GET", "/v1/generations/"+generation.ID+"/content", "", ""); unauthorized.Code != 401 {
			t.Fatal("unauthorized content accessible", unauthorized.Code)
		}
		access := request("GET", "/v1/generations/"+generation.ID+"/media-access", "", "gateway-fixture-key")
		var signed struct {
			DownloadURL string `json:"download_url"`
		}
		_ = json.Unmarshal(access.Body.Bytes(), &signed)
		if signed.DownloadURL == "" {
			t.Fatal(access.Body)
		}
		result = request("GET", signed.DownloadURL, "", "")
		if result.Code != 200 || !bytes.Equal(result.Body.Bytes(), expected) {
			t.Fatal("signed download failed", result.Code, result.Body)
		}
		select {
		case deliveredID := <-delivered:
			if deliveredID != generation.ID {
				t.Fatal("webhook belongs to a different generation", deliveredID)
			}
		case <-ctx.Done():
			t.Fatal("completed webhook was not delivered")
		}
	}
	if submissions.Load() != 4 || polls.Load() != 2 {
		t.Fatal("unexpected generation/poll count", submissions.Load(), polls.Load())
	}
}

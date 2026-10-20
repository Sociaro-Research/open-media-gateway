package sonilo_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/api"
	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/postgres"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
	"github.com/Sociaro-Research/media-gateway-dev/internal/queue"
	"github.com/Sociaro-Research/media-gateway-dev/internal/storage"
	"github.com/Sociaro-Research/media-gateway-dev/internal/webhook"
	"github.com/Sociaro-Research/media-gateway-dev/internal/worker"
	"github.com/Sociaro-Research/media-gateway-dev/plugins/provider/sonilo"
	"github.com/google/uuid"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func jsonResponse(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// Real admission, PostgreSQL, Redis, worker, audio/video archival, signed
// downloads and webhooks for every operation; only Sonilo HTTP is a fixture.
func TestSoniloGatewayWorkflow(t *testing.T) {
	dsn, redisURL := os.Getenv("GATEWAY_TEST_DATABASE_URL"), os.Getenv("GATEWAY_TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("requires isolated PostgreSQL and Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	base, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	schema := "sonilo_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	jobs, err := queue.Open(ctx, redisURL, "sonilo-jobs-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.Close()
	callbacks, err := queue.Open(ctx, redisURL, "sonilo-events-"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	defer callbacks.Close()
	var submissions, polls atomic.Int32
	var fixtures sync.Map
	services := []string{"text_to_music", "text_to_sfx", "video_to_music", "video_to_sfx", "video_to_video_music", "video_to_video_sfx", "video_to_sound", "video_to_video_sound", "audio_ducking"}
	client := &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/v1/account/services" {
			data, _ := json.Marshal(map[string]any{"available_services": services})
			return jsonResponse(string(data)), nil
		}
		if r.Method == "POST" {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			op := strings.TrimPrefix(r.URL.Path, "/v1/")
			id := fmt.Sprintf("fixture-%d", submissions.Add(1))
			video := strings.HasPrefix(op, "video-to-video-") || op == "audio-ducking" && strings.HasSuffix(r.FormValue("voice_url"), ".mp4")
			body := map[string]any{"task_id": id, "type": strings.ReplaceAll(op, "-", "_"), "status": "succeeded", "duration_seconds": 2.5}
			media := map[string]any{"url": "https://cdn.example/audio.wav", "content_type": "audio/wav", "stream_index": 0}
			if video {
				media = map[string]any{"url": "https://cdn.example/video.mp4", "content_type": "video/mp4"}
			}
			switch {
			case op == "audio-ducking" || strings.HasSuffix(op, "sound"):
				body["output_url"] = media["url"]
				body["output_type"] = "audio"
				if video {
					body["output_type"] = "video"
				}
			case video:
				body["video"] = media
			case strings.HasSuffix(op, "music"):
				body["audio"] = []any{media}
			default:
				body["audio"] = media
			}
			fixtures.Store(id, body)
			return jsonResponse(fmt.Sprintf(`{"task_id":%q,"status":"processing"}`, id)), nil
		}
		polls.Add(1)
		body, ok := fixtures.Load(strings.TrimPrefix(r.URL.Path, "/v1/tasks/"))
		if !ok {
			t.Errorf("unexpected task %s", r.URL)
			return jsonResponse(`{}`), nil
		}
		data, _ := json.Marshal(body)
		return jsonResponse(string(data)), nil
	})}
	p, err := sonilo.Definition().Factory(providerkit.Credentials{APIKey: "fixture-key", Extra: map[string]string{"video_music_price": "0.01", "video_sfx_price": "0.01", "sound_price": "0.02", "video_sound_price": "0.02"}}, nil, client)
	if err != nil {
		t.Fatal(err)
	}
	audioBytes := []byte("RIFFfixture-audioWAVE")
	videoBytes := []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2fixture-video")
	media, err := storage.NewLocal(t.TempDir(), &http.Client{Transport: fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Error("provider key was forwarded to result CDN")
		}
		data, kind := audioBytes, "audio/wav"
		if strings.HasSuffix(r.URL.Path, ".mp4") {
			data, kind = videoBytes, "video/mp4"
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {kind}}, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewTaskRepository(db)
	service := core.NewTaskServiceWithProviders(repo, jobs, map[string]core.MediaProvider{"sonilo": p}, map[string]string{"sonilo": sonilo.DefaultModel}, "sonilo")
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
			if event.Data.ContentURL == "" || strings.Contains(string(body), "https://cdn.example/audio.wav") || strings.Contains(string(body), "https://cdn.example/video.mp4") {
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
	for i, tc := range []struct{ path, model, mediaType string }{
		{"/v1/audio/generations", "text-to-music", "music"},
		{"/v1/generations", "text-to-sfx", "audio"},
		{"/v1/audio/generations", "video-to-music", "music"},
		{"/v1/generations", "video-to-sfx", "audio"},
		{"/v1/videos", "video-to-video-music", "video"},
		{"/v1/videos", "video-to-video-sfx", "video"},
		{"/v1/generations", "video-to-sound", "audio"},
		{"/v1/videos", "video-to-video-sound", "video"},
		{"/v1/generations", "audio-ducking", "audio"},
		{"/v1/videos", "video-ducking", "video"},
		{"/v1/generations", "text-to-music", "music"},
	} {
		body := map[string]any{"model": "sonilo/" + tc.model, "webhook_url": "https://callback.example/events"}
		opts := map[string]any{}
		if strings.HasPrefix(tc.model, "text-") {
			body["prompt"] = "fixture"
		}
		if strings.HasPrefix(tc.model, "video-") {
			opts["video_urls"] = []string{"https://cdn.example/reference.mp4"}
		}
		if strings.HasSuffix(tc.model, "ducking") {
			opts["parameters"] = map[string]any{"music_url": "https://cdn.example/music.wav"}
			if tc.model == "audio-ducking" {
				opts["audio_urls"] = []string{"https://cdn.example/voice.wav"}
			}
		}
		if tc.model == "text-to-music" {
			opts["duration"] = 30
			opts["format"] = "wav"
		}
		if tc.model == "text-to-sfx" {
			opts["parameters"] = map[string]any{"duration_seconds": 0.5}
			opts["format"] = "wav"
		}
		body["options"] = opts
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
		if result.Code != 200 || strings.Contains(result.Body.String(), "https://cdn.example/audio.wav") || strings.Contains(result.Body.String(), "https://cdn.example/video.mp4") {
			t.Fatal(result.Code, result.Body)
		}
		result = request("GET", "/v1/generations/"+generation.ID+"/content", "", "gateway-fixture-key")
		expected := audioBytes
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
	if submissions.Load() != 11 || polls.Load() != 11 {
		t.Fatal("unexpected generation/poll count", submissions.Load(), polls.Load())
	}
}

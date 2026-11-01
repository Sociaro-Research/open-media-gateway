package kling_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
)

func TestKlingReadyPrimaryArchivedWithoutOptionalWatermarkFetch(t *testing.T) {
	for _, tc := range []struct {
		name, watermark, duration string
	}{
		{name: "missing alternate", duration: "5"},
		{name: "unsafe alternate", watermark: "https://127.0.0.1/alternate.mp4", duration: "5"},
		{name: "unavailable alternate", watermark: "https://cdn.fixture/unavailable.mp4", duration: "5"},
		{name: "unknown duration and unavailable alternate", watermark: "https://cdn.fixture/unavailable.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, _, q := sessionPGServices(t)
			video := testmedia.MP4(120, 30, 120)
			var primaryFetches, alternateFetches atomic.Int32
			providerHTTP := &http.Client{Transport: sessionPGRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "api-singapore.klingai.com" || r.Header.Get("Authorization") != "Bearer ready-primary-fixture" {
					return nil, errors.New("unexpected provider destination or credentials")
				}
				switch r.Method {
				case http.MethodPost:
					if r.URL.Path != "/text-to-video/kling-3.0" {
						return nil, errors.New("unexpected generation route")
					}
					return sessionPGJSON(map[string]any{"code": 0, "data": map[string]any{"id": "task_fixture", "status": "submitted"}}), nil
				case http.MethodGet:
					if r.URL.Path != "/tasks" || r.URL.Query().Get("task_ids") != "task_fixture" {
						return nil, errors.New("unexpected task query")
					}
					output := map[string]any{"type": "video", "url": "https://cdn.fixture/final.mp4"}
					if tc.duration != "" {
						output["duration"] = tc.duration
					}
					if tc.watermark != "" {
						output["watermark_url"] = tc.watermark
					}
					return sessionPGJSON(map[string]any{"code": 0, "data": []any{map[string]any{"id": "task_fixture", "status": "succeeded", "outputs": []any{output}}}}), nil
				}
				return nil, errors.New("unexpected provider method")
			})}
			cdn := &http.Client{Transport: sessionPGRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://cdn.fixture/final.mp4" {
					alternateFetches.Add(1)
					return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(nil))}, nil
				}
				primaryFetches.Add(1)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, ContentLength: int64(len(video)), Body: io.NopCloser(bytes.NewReader(video))}, nil
			})}
			adapter, err := kling.New("ready-primary-fixture", kling.DefaultBaseURL, providerHTTP)
			if err != nil {
				t.Fatal(err)
			}
			var revision atomic.Value
			revision.Store("ready-primary-revision")
			provider := sessionPGProvider{Adapter: adapter, cdn: cdn, revision: &revision}
			store, err := storage.NewLocal(t.TempDir(), cdn, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			repo := postgres.NewTaskRepository(db)
			service := core.NewTaskServiceWithProviders(repo, q, map[string]core.MediaProvider{"kling": provider}, map[string]string{"kling": kling.TextToVideo}, "kling")
			service.EnableAccounting()
			h := api.NewWithStorage(service, sessionPGHealth{}, "session-gateway-fixture", store, "ready-primary-signing-fixture", time.Hour)
			posted := sessionPGRequest(h, http.MethodPost, "/v1/videos", "ready-primary-once", map[string]any{"model": "kling/" + kling.TextToVideo, "prompt": "Synthetic ready-primary fixture"}, true)
			id := sessionPGID(t, posted)
			task := sessionPGRun(t, ctx, repo, q, provider, store, id)
			if task.Status != core.TaskSucceeded || task.ResultURL == nil || len(task.StoredArtifacts) != 1 {
				t.Fatalf("primary was not archived: status=%s artifacts=%d error=%v", task.Status, len(task.StoredArtifacts), task.Error)
			}
			if tc.duration == "" && (task.Usage != nil || task.Cost == nil || task.Cost.MeteredUSD != nil || task.Cost.ActualUSD != nil) {
				t.Fatal("unknown duration was treated as measured or billed usage")
			}
			got := sessionPGRequest(h, http.MethodGet, *task.ResultURL, "", nil, true)
			if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), video) {
				t.Fatalf("authenticated primary unreadable: status=%d bytes=%d", got.Code, got.Body.Len())
			}
			unauth := sessionPGRequest(h, http.MethodGet, *task.ResultURL, "", nil, false)
			if unauth.Code != 401 && unauth.Code != 403 {
				t.Fatal("unauthenticated primary was accessible", unauth.Code)
			}
			if primaryFetches.Load() != 1 || alternateFetches.Load() != 0 {
				t.Fatalf("unexpected media fetches: primary=%d alternate=%d", primaryFetches.Load(), alternateFetches.Load())
			}
			var view map[string]any
			status := sessionPGRequest(h, http.MethodGet, "/v1/generations/"+id, "", nil, true)
			if status.Code != 200 || json.Unmarshal(status.Body.Bytes(), &view) != nil {
				t.Fatal("authenticated task status unreadable", status.Code)
			}
		})
	}
}

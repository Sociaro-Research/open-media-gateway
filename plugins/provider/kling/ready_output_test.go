package kling

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/storage"
)

func TestReadyPrimarySurvivesOptionalOutputMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, duration, inputDuration, watermark string
		wantDuration                             float64
		wantUsage                                bool
		wantArtifacts                            int
	}{
		{name: "normal", duration: "5", wantDuration: 5, wantUsage: true, wantArtifacts: 1},
		{name: "duration absent", wantArtifacts: 1},
		{name: "duration invalid", duration: "NaN", wantArtifacts: 1},
		{name: "input duration invalid", duration: "5", inputDuration: "invalid", wantDuration: 5, wantUsage: true, wantArtifacts: 1},
		{name: "optional watermark unsafe", duration: "5", watermark: "https://127.0.0.1/optional.mp4", wantDuration: 5, wantUsage: true, wantArtifacts: 1},
		{name: "optional watermark valid", duration: "5", watermark: "https://cdn.fixture/watermark.mp4", wantDuration: 5, wantUsage: true, wantArtifacts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != http.MethodGet || req.URL.Path != "/tasks" {
					t.Fatal("unexpected provider request", req.Method, req.URL.Path)
				}
				output := map[string]any{"type": "video", "url": "https://cdn.fixture/final.mp4"}
				if tc.duration != "" {
					output["duration"] = tc.duration
				}
				if tc.watermark != "" {
					output["watermark_url"] = tc.watermark
				}
				body := map[string]any{"code": 0, "data": []any{map[string]any{"id": "task_fixture", "status": "succeeded", "outputs": []any{output}, "usage": map[string]string{"input_duration": tc.inputDuration}}}}
				encoded, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				return response(200, string(encoded)), nil
			})
			result, err := a.GetTask(context.Background(), "task_fixture")
			if err != nil || result.Status != core.ProviderSucceeded || result.ResultURL != "https://cdn.fixture/final.mp4" || len(result.Artifacts) != tc.wantArtifacts || result.Artifacts[0].Duration != tc.wantDuration || (result.Usage != nil) != tc.wantUsage || calls != 1 {
				t.Fatalf("ready primary lost: status=%s artifacts=%d usage=%t calls=%d err=%v", result.Status, len(result.Artifacts), result.Usage != nil, calls, err)
			}
			if result.Usage != nil {
				if result.Usage.EstimatedCost != nil {
					t.Fatal("unknown vendor charge was fabricated")
				}
				if tc.inputDuration == "invalid" {
					for _, metric := range result.Usage.Metrics {
						if metric.Name == "input_video_duration" {
							t.Fatal("invalid input duration was metered")
						}
					}
				}
			}
			if tc.watermark != "" && len(result.Artifacts) != 1 {
				t.Fatal("optional alternate watermark was retained")
			}
		})
	}
}

func TestReadyPrimaryArchivesWithoutFetchingOptionalWatermark(t *testing.T) {
	video := append([]byte{0, 0, 0, 20, 'f', 't', 'y', 'p'}, []byte("isom0000isom")...)
	for _, tc := range []struct {
		name, watermark string
	}{
		{name: "no alternate"},
		{name: "unsafe alternate", watermark: "https://127.0.0.1/alternate.mp4"},
		{name: "unavailable alternate", watermark: "https://cdn.fixture/unavailable.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := adapter(t, func(*http.Request) (*http.Response, error) {
				output := map[string]any{"type": "video", "url": "https://cdn.fixture/final.mp4", "duration": "5"}
				if tc.watermark != "" {
					output["watermark_url"] = tc.watermark
				}
				body, err := json.Marshal(map[string]any{"code": 0, "data": []any{map[string]any{"id": "task_fixture", "status": "succeeded", "outputs": []any{output}}}})
				if err != nil {
					t.Fatal(err)
				}
				return response(200, string(body)), nil
			})
			result, err := a.GetTask(context.Background(), "task_fixture")
			if err != nil || result.Status != core.ProviderSucceeded || len(result.Artifacts) != 1 {
				t.Fatalf("ready primary unavailable: %+v err=%v", result, err)
			}
			fetches := 0
			cdn := &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
				fetches++
				if req.URL.String() != "https://cdn.fixture/final.mp4" {
					t.Fatal("optional watermark was fetched", req.URL.Host, req.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp4"}}, ContentLength: int64(len(video)), Body: io.NopCloser(bytes.NewReader(video))}, nil
			})}
			store, err := storage.NewLocal(t.TempDir(), cdn, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			media, err := store.Archive(context.Background(), "task_fixture", result.Artifacts[0].URL)
			if err != nil {
				t.Fatal(err)
			}
			opened, err := store.Open(context.Background(), media.StorageKey)
			if err != nil {
				t.Fatal(err)
			}
			defer opened.Reader.Close()
			got, err := io.ReadAll(opened.Reader)
			if err != nil || !bytes.Equal(got, video) || fetches != 1 {
				t.Fatalf("primary archive unreadable or alternate fetched: bytes=%d fetches=%d err=%v", len(got), fetches, err)
			}
		})
	}
}

func TestReadyPrimaryIgnoresMalformedOptionalJSONTypes(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return response(200, `{"code":0,"data":[{"id":"task_fixture","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.fixture/final.mp4","duration":{"unexpected":5},"watermark_url":["https://cdn.fixture/alternate.mp4"]}],"usage":{"input_duration":{"unexpected":2}},"billing":[{"charge_type":"unit","amount":5}]}]}`), nil
	})
	result, err := a.GetTask(context.Background(), "task_fixture")
	if err != nil || result.Status != core.ProviderSucceeded || result.ResultURL != "https://cdn.fixture/final.mp4" || len(result.Artifacts) != 1 || result.Artifacts[0].Duration != 0 || result.Usage != nil {
		t.Fatalf("malformed optional JSON hid ready primary or fabricated usage: %+v err=%v", result, err)
	}
}

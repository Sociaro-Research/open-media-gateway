package agnes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}, "X-Request-Id": {"fixture-request"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func adapter(t *testing.T, f transportFunc) *Adapter {
	t.Helper()
	a, err := New("fixture-secret", DefaultBaseURL, &http.Client{Transport: f, Timeout: time.Second}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestImageContractsForEveryModel(t *testing.T) {
	for _, model := range []string{"agnes-image-2.0-flash", "agnes-image-2.1-flash", "agnes-image-2.5-flash"} {
		for _, edit := range []bool{false, true} {
			t.Run(model+map[bool]string{false: "/text", true: "/edit"}[edit], func(t *testing.T) {
				a := adapter(t, func(r *http.Request) (*http.Response, error) {
					if r.Method != "POST" || r.URL.String() != DefaultBaseURL+"/images/generations" || r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("User-Agent") == "" {
						t.Fatal(r.Method, r.URL, "invalid request metadata")
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					extra := body["extra_body"].(map[string]any)
					if body["model"] != model || body["prompt"] != "fixture prompt" || extra["response_format"] != "url" || body["response_format"] != nil || body["n"] != nil {
						t.Fatal(body)
					}
					if edit {
						if len(extra["image"].([]any)) != 2 {
							t.Fatal(body)
						}
					} else if extra["image"] != nil {
						t.Fatal(body)
					}
					if model == "agnes-image-2.0-flash" {
						if body["size"] != "1024x1024" || body["ratio"] != nil {
							t.Fatal(body)
						}
					} else if body["size"] != "2K" || body["ratio"] != "16:9" {
						t.Fatal(body)
					}
					return response(200, `{"data":[{"url":"https://cdn.example/result.png"}],"usage":{"image_count":1}}`), nil
				})
				opts := core.MediaOptions{}
				if model != "agnes-image-2.0-flash" {
					opts.Resolution, opts.Ratio = "2K", "16:9"
				}
				if edit {
					opts.ImageURLs = []string{"https://cdn.example/a.png", "https://cdn.example/b.png"}
				}
				task, err := a.Generate(context.Background(), core.MediaRequest{Model: model, Prompt: "fixture prompt", Options: opts})
				if err != nil || task.ID != "fixture-request" || task.ResultURL != "https://cdn.example/result.png" || task.Usage == nil || len(task.Usage.Metrics) != 1 {
					t.Fatal(task, err)
				}
			})
		}
	}
}

func TestBase64ImageAndBadResults(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":[]}`, `{"data":[{},{}]}`, `{"data":[{}]}`, `{"data":[{"url":"https://127.0.0.1/result"}]}`, `{"data":[{"b64_json":"aHRtbA=="}]}`, `{"data":[{"b64_json":"!bad"}]}`} {
		a := adapter(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		if _, err := a.Generate(context.Background(), core.MediaRequest{Model: DefaultModel, Prompt: "fixture"}); err == nil {
			t.Fatal("accepted malformed result", body)
		}
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\nfixture"))
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return response(200, `{"data":[{"b64_json":"`+encoded+`"}]}`), nil
	})
	task, err := a.Generate(context.Background(), core.MediaRequest{Model: DefaultModel, Prompt: "fixture"})
	if err != nil || task.ResultURL != "data:image/png;base64,"+encoded {
		t.Fatal(task, err)
	}
}

func TestVideoModesAndRestartSafePolling(t *testing.T) {
	for _, model := range []string{"agnes-video-2.5", "agnes-video-2.5-flash"} {
		for _, mode := range []string{"none", "first_frame", "last_frame", "first_last_frame", "references", ""} {
			t.Run(model+"/"+mode, func(t *testing.T) {
				opts := core.MediaOptions{ReferenceMode: mode, Duration: 8, Resolution: "720P", Ratio: "9:16"}
				switch mode {
				case "first_frame", "last_frame", "references":
					opts.ImageURLs = []string{"https://cdn.example/a.png"}
				case "first_last_frame":
					opts.ImageURLs = []string{"https://cdn.example/a.png", "https://cdn.example/b.png"}
				case "":
					opts.AudioURLs = []string{"https://cdn.example/a.mp3"}
				}
				a := adapter(t, func(r *http.Request) (*http.Response, error) {
					if r.Method != "POST" || r.URL.Path != "/v1/videos" {
						t.Fatal(r.URL)
					}
					var b map[string]any
					_ = json.NewDecoder(r.Body).Decode(&b)
					if b["seconds"] != "8" || b["size"] != "720P" || b["aspect_ratio"] != "9:16" || b["n"] != float64(1) {
						t.Fatal(b)
					}
					switch mode {
					case "first_frame":
						if b["mode"] != "keyframe" || b["first_frame"] != opts.ImageURLs[0] || b["last_frame"] != nil {
							t.Fatal(b)
						}
					case "last_frame":
						if b["mode"] != "keyframe" || b["last_frame"] != opts.ImageURLs[0] || b["first_frame"] != nil {
							t.Fatal(b)
						}
					case "first_last_frame":
						if b["mode"] != "keyframe" || b["last_frame"] != opts.ImageURLs[1] {
							t.Fatal(b)
						}
					case "references":
						if b["mode"] != "reference" || len(b["images"].([]any)) != 1 {
							t.Fatal(b)
						}
					case "":
						if b["mode"] != "reference" || len(b["audios"].([]any)) != 1 {
							t.Fatal(b)
						}
					default:
						if b["mode"] != "text" {
							t.Fatal(b)
						}
					}
					return response(200, `{"id":"wrong-task-id","video_id":"video +/&=","model":"`+model+`","status":"queued"}`), nil
				})
				task, err := a.Generate(context.Background(), core.MediaRequest{Model: model, Prompt: "fixture", Options: opts})
				if err != nil {
					t.Fatal(err)
				}
				// A new process receives only the persisted ID; no in-memory map.
				fresh := adapter(t, func(r *http.Request) (*http.Response, error) {
					if r.Method != "GET" || r.URL.Path != "/agnesapi" || r.URL.Query().Get("model_name") != model || r.URL.Query().Get("video_id") != "video +/&=" {
						t.Fatal(r.URL)
					}
					return response(200, `{"status":"completed","internal_status":"pending","url":"https://cdn.example/video.mp4","seconds":"8"}`), nil
				})
				result, err := fresh.GetTask(context.Background(), task.ID)
				if err != nil || result.Status != core.ProviderSucceeded || result.Usage == nil || result.Usage.Metrics[0].Quantity != 8 {
					t.Fatal(result, err)
				}
			})
		}
	}
}

func TestVideoReferenceControls(t *testing.T) {
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		v := b["videos"].([]any)[0].(map[string]any)
		if v["url"] != "https://cdn.example/input.mp4" || v["start_seconds"] != float64(35) || v["require_audio"] != true || b["mode"] != "reference" {
			t.Fatal(b)
		}
		return response(200, `{"video_id":"video-id","status":"queued"}`), nil
	})
	_, err := a.Generate(context.Background(), core.MediaRequest{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{VideoURLs: []string{"https://cdn.example/input.mp4"}, Parameters: map[string]any{"video_start_seconds": 35, "video_require_audio": true}}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskStatesAndSanitizedErrors(t *testing.T) {
	handle, _ := json.Marshal(taskHandle{VideoID: "fixture-video", Model: "agnes-video-2.5"})
	id := base64.RawURLEncoding.EncodeToString(handle)
	for status, expected := range map[string]core.ProviderStatus{"queued": core.ProviderQueued, "in_progress": core.ProviderRunning, "failed": core.ProviderFailed} {
		a := adapter(t, func(*http.Request) (*http.Response, error) {
			return response(200, `{"status":"`+status+`","url":"https://cdn.example/premature.mp4","error":{"message":"fixture-secret private prompt"}}`), nil
		})
		result, err := a.GetTask(context.Background(), id)
		if err != nil || result.Status != expected || result.ResultURL != "" || strings.Contains(result.Error, "fixture-secret") {
			t.Fatal(result, err)
		}
	}
	for _, body := range []string{`{`, `{}`, `{"status":"pending"}`, `{"status":"completed"}`} {
		a := adapter(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		if _, err := a.GetTask(context.Background(), id); err == nil {
			t.Fatal(body)
		}
	}
	for _, body := range []string{`{}`, `{"id":"task-only"}`, `{"video_id":"v","status":"failed"}`} {
		a := adapter(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		if _, err := a.Generate(context.Background(), core.MediaRequest{Model: "agnes-video-2.5", Prompt: "fixture"}); err == nil {
			t.Fatal(body)
		}
	}
	for _, code := range []int{400, 401, 403, 404, 429, 500, 502, 503, 504, 302} {
		calls := 0
		a := adapter(t, func(*http.Request) (*http.Response, error) {
			calls++
			return response(code, `{"error":"fixture-secret private prompt https://signed.example/secret"}`), nil
		})
		_, err := a.Generate(context.Background(), core.MediaRequest{Model: DefaultModel, Prompt: "fixture"})
		if err == nil || calls != 1 || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "private prompt") {
			t.Fatal(err, calls)
		}
	}
}

func TestUnavailableVideoSubmissionIsClearSafeAndNotRetried(t *testing.T) {
	for _, tc := range []struct {
		body, expected string
	}{
		{`{"code":503,"message":"fixture-secret private prompt https://signed.example/secret"}`, "service temporarily unavailable"},
		{`{"error":{"type":"server_overloaded","message":"fixture-secret"}}`, "service temporarily unavailable or overloaded"},
		{`{"error":{"code":"no_available_channel"}}`, "no available route or capacity"},
		{`{"code":"insufficient_quota","message":"private prompt"}`, "account quota or balance is insufficient"},
		{`{"error":{"code":"fixture-secret","message":"private prompt"}}`, "service temporarily unavailable"},
		{"<html>fixture-secret private prompt</html>", "service temporarily unavailable"},
		{strings.Repeat("private prompt", 1000), "service temporarily unavailable"},
	} {
		calls := 0
		a := adapter(t, func(r *http.Request) (*http.Response, error) {
			calls++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["mode"] != "keyframe" || payload["seconds"] != "12" || payload["aspect_ratio"] != "1:1" || payload["first_frame"] != "https://signed.example/frame?signature=private" {
				t.Fatal("incorrect keyframe payload")
			}
			return response(503, tc.body), nil
		})
		task, err := a.Generate(context.Background(), core.MediaRequest{Model: "agnes-video-2.5-flash", Prompt: "private prompt", Options: core.MediaOptions{Duration: 12, Resolution: "720P", Ratio: "1:1", ReferenceMode: "first_frame", ImageURLs: []string{"https://signed.example/frame?signature=private"}}})
		if err == nil || calls != 1 || task.ID != "" {
			t.Fatal("failed submission was retried or treated as accepted", err, calls)
		}
		message := err.Error()
		if !strings.Contains(message, tc.expected) || !strings.Contains(message, "HTTP 503") || !strings.Contains(message, "submission was not retried") || !strings.Contains(message, "check provider history") {
			t.Fatal(message)
		}
		for _, private := range []string{"fixture-secret", "private prompt", "signed.example", "signature=private"} {
			if strings.Contains(message, private) {
				t.Fatal("private data leaked in error")
			}
		}
	}
}

func TestValidationRejectsUnsupportedCombinationsBeforeHTTP(t *testing.T) {
	falseValue := false
	for _, r := range []core.MediaRequest{
		{Model: "agnes-3.0-flash", Prompt: "fixture"},
		{Model: DefaultModel, Prompt: "fixture", Options: core.MediaOptions{Format: "png"}},
		{Model: DefaultModel, Prompt: "fixture", Options: core.MediaOptions{Parameters: map[string]any{"n": 2}}},
		{Model: "agnes-image-2.0-flash", Prompt: "fixture", Options: core.MediaOptions{Ratio: "16:9"}},
		{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{Duration: 3}},
		{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{GenerateAudio: &falseValue}},
		{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{ReferenceMode: "first_frame"}},
		{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{ReferenceMode: "references"}},
		{Model: "agnes-video-2.5-flash", Prompt: "fixture", Options: core.MediaOptions{Resolution: "1080P"}},
		{Model: "agnes-video-2.5-flash", Prompt: "fixture", Options: core.MediaOptions{VideoURL: "https://cdn.example/input.mp4"}},
		{Model: "agnes-video-2.5-flash", Prompt: "fixture", Options: core.MediaOptions{ImageURLs: []string{"a", "b", "c", "d", "e", "f"}}},
		{Model: DefaultModel, Prompt: "fixture", Options: core.MediaOptions{ImageURLs: []string{"http://cdn.example/input.png"}}},
		{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{Parameters: map[string]any{"video_start_seconds": 5}}},
	} {
		a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid request reached HTTP"); return nil, nil })
		if _, err := a.Generate(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal(r, err)
		}
	}
}

func TestCatalogDiscoveryFallbackAndExplicitCredentialCheck(t *testing.T) {
	calls := 0
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/models" {
			t.Fatal(r.URL)
		}
		return response(200, `{"data":[{"id":"agnes-image-2.5-flash"},{"id":"agnes-video-2.5-flash"},{"id":"agnes-video-9"},{"id":"agnes-3.0-flash"}]}`), nil
	})
	models, err := a.ListModels(context.Background())
	if err != nil || len(models) != 2 {
		t.Fatal(models, err)
	}
	models[0].Resolutions[0] = "corrupted"
	models, err = a.ListModels(context.Background())
	if err != nil || models[0].Resolutions[0] != "1K" || calls != 1 {
		t.Fatal(models, err, calls)
	}
	if err = a.CheckCredentials(context.Background()); err != nil || calls != 2 {
		t.Fatal("explicit authenticated query failed", err, calls)
	}
	a = adapter(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline fixture-secret") })
	models, err = a.ListModels(context.Background())
	if err != nil || len(models) != 5 {
		t.Fatal(models, err)
	}
	a = adapter(t, func(*http.Request) (*http.Response, error) { return response(200, `{}`), nil })
	if _, err = a.ListModels(context.Background()); err == nil {
		t.Fatal("malformed discovery accepted")
	}
}

func TestPricingPromotionsReferencesAndUsageCompleteness(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("quote performed HTTP"); return nil, nil })
	for _, model := range []string{DefaultModel, "agnes-image-2.0-flash", "agnes-image-2.1-flash", "agnes-video-2.5-flash"} {
		cost, _, err := a.Quote(context.Background(), describeModel(model), core.MediaRequest{Model: model, Prompt: "fixture"})
		if err != nil || cost.ExpectedUSD != 0 || cost.ActualUSD != nil || cost.Status != "estimated" || cost.ActualSource != "" {
			t.Fatal(cost, err)
		}
	}
	for size, expected := range map[string]money.USD{"720P": 125000, "1080P": 200000, "1K": 200000, "2K": 275000} {
		cost, _, err := a.Quote(context.Background(), core.MediaModel{}, core.MediaRequest{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{Resolution: size}})
		if err != nil || cost.ExpectedUSD != expected {
			t.Fatal(size, cost, err)
		}
	}
	images := []string{"https://cdn.example/1.png", "https://cdn.example/2.png", "https://cdn.example/3.png", "https://cdn.example/4.png", "https://cdn.example/5.png", "https://cdn.example/6.png", "https://cdn.example/7.png"}
	cost, _, err := a.Quote(context.Background(), core.MediaModel{}, core.MediaRequest{Model: "agnes-video-2.5", Prompt: "fixture", Options: core.MediaOptions{Duration: 8, ImageURLs: images, VideoURL: "https://cdn.example/in.mp4"}})
	if err != nil || cost.ExpectedUSD != 510000 || cost.EstimatedUnits != 20 {
		t.Fatal(cost, err)
	}
	usage := completedUsage(nil, json.RawMessage(`"8"`))
	if cost.MeterUsage(*usage) {
		t.Fatal("unreported input duration was treated as metered")
	}
	usage = completedUsage(map[string]any{"input_video_duration": 3.0}, json.RawMessage(`"8"`))
	if !cost.MeterUsage(*usage) || cost.MeteredUSD == nil || *cost.MeteredUSD != 285000 || cost.ActualUSD != nil {
		t.Fatal(cost, usage)
	}
}

func TestEndpointTransportAndCancellation(t *testing.T) {
	for _, base := range []string{"http://apihub.agnes-ai.com/v1", "https://evil.example/v1", "https://apihub.agnes-ai.com.evil.example/v1", "https://user@apihub.agnes-ai.com/v1", "https://apihub.agnes-ai.com/v1?q=1"} {
		if _, err := New("fixture", base, nil, 0); err == nil {
			t.Fatal(base)
		}
	}
	if _, err := New("", DefaultBaseURL, nil, 0); err == nil {
		t.Fatal("missing key accepted")
	}
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return nil, errors.New("https://fixture-secret@private.example")
	})
	if err := a.client.CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Generate(ctx, core.MediaRequest{Model: DefaultModel, Prompt: "fixture"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	a = adapter(t, func(*http.Request) (*http.Response, error) {
		r := response(200, `{}`)
		r.Header.Set("Content-Type", "text/html")
		return r, nil
	})
	if _, err := a.Generate(context.Background(), core.MediaRequest{Model: DefaultModel, Prompt: "fixture"}); err == nil {
		t.Fatal("HTML accepted")
	}
	if _, err := a.GetTask(context.Background(), "not-a-task-handle"); err == nil {
		t.Fatal("invalid handle accepted")
	}
}

func TestPicturePromptPreviewCapability(t *testing.T) {
	for _, m := range catalog() {
		want := ""
		if m.MediaType == "video" {
			want = "picture"
		}
		if m.ReferencePromptSyntax != want {
			t.Fatalf("%s: %s", m.ID, m.ReferencePromptSyntax)
		}
	}
}

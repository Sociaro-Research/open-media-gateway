package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func adapter(t *testing.T, f transport) *Adapter {
	t.Helper()
	a, err := New("fixture-secret", DefaultBaseURL, &http.Client{Transport: f})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func ptr[T any](v T) *T { return &v }

// Fixtures describe only the current official routes, not legacy /v1/videos
// or a reseller contract. No real key, task, prompt or output is used.
func TestRequestsAndRestartSafePolling(t *testing.T) {
	for _, mode := range []string{"text", "first_frame", "first_last_frame"} {
		t.Run(mode, func(t *testing.T) {
			r := core.MediaRequest{Model: TextToVideo, Prompt: "Synthetic fixture", Options: core.MediaOptions{Duration: 8, Resolution: "1080p", GenerateAudio: ptr(true), Parameters: map[string]any{"multi_shot": false}}}
			path := "/text-to-video/kling-3.0"
			if mode == "text" {
				r.Options.Ratio = "9:16"
			} else {
				r.Model, r.Options.ReferenceMode = ImageToVideo, mode
				r.Options.ImageURLs = []string{"https://cdn.example/first.png"}
				path = "/image-to-video/kling-3.0"
				if mode == "first_last_frame" {
					r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/last.jpg")
				}
			}
			submissions, polls := 0, 0
			f := transport(func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "api-singapore.klingai.com" || req.Header.Get("Authorization") != "Bearer fixture-secret" || req.Header.Get("User-Agent") != "Sociaro-Media-Gateway/Kling" || req.Header.Get("Content-Type") != "application/json" {
					t.Fatal("incorrect destination or headers")
				}
				if req.Method == "POST" {
					submissions++
					if req.URL.Path != path {
						t.Fatal(req.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					s, _ := body["settings"].(map[string]any)
					if s["duration"] != float64(8) || s["resolution"] != "1080p" || s["audio"] != "native" || s["multi_shot"] != false || body["model"] != nil || body["model_name"] != nil || body["options"] != nil {
						t.Fatal(body)
					}
					if mode == "text" {
						if body["prompt"] != r.Prompt || s["aspect_ratio"] != "9:16" || body["contents"] != nil {
							t.Fatal(body)
						}
					} else {
						contents := body["contents"].([]any)
						if len(contents) != len(r.Options.ImageURLs)+1 || body["prompt"] != nil || s["aspect_ratio"] != nil {
							t.Fatal(body)
						}
						if contents[0].(map[string]any)["text"] != r.Prompt || contents[1].(map[string]any)["type"] != "first_frame" || contents[1].(map[string]any)["url"] != r.Options.ImageURLs[0] {
							t.Fatal(contents)
						}
						if mode == "first_last_frame" && contents[2].(map[string]any)["type"] != "last_frame" {
							t.Fatal(contents)
						}
					}
					return response(200, `{"code":0,"data":{"id":"task_123","status":"submitted"}}`), nil
				}
				polls++
				if req.Method != "GET" || req.URL.Path != "/tasks" || req.URL.Query().Get("task_ids") != "task_123" {
					t.Fatal(req.URL)
				}
				status, extra := "submitted", ""
				if polls == 2 {
					status = "processing"
				}
				if polls == 3 {
					status, extra = "succeeded", `,"outputs":[{"type":"video","url":"https://cdn.example/result.mp4","duration":"8"}],"billing":[{"charge_type":"unit","package_type":"video","amount":"9.6"}]`
				}
				return response(200, fmt.Sprintf(`{"code":0,"data":[{"id":"task_123","status":%q%s}]}`, status, extra)), nil
			})
			a := adapter(t, f)
			task, err := a.Generate(context.Background(), r)
			if err != nil || task.ID != "task_123" || task.ResultURL != "" {
				t.Fatal(task, err)
			}
			for _, want := range []core.ProviderStatus{core.ProviderQueued, core.ProviderRunning, core.ProviderSucceeded} {
				// A fresh adapter has no in-memory task state and must never resubmit.
				result, err := adapter(t, f).GetTask(context.Background(), task.ID)
				if err != nil || result.Status != want {
					t.Fatal(result, err)
				}
				if want == core.ProviderSucceeded && (result.ResultURL != "https://cdn.example/result.mp4" || result.Usage == nil || result.Usage.Metrics[0].Quantity != 8 || result.Usage.EstimatedCost != nil || result.Usage.CostStatus != "metered") {
					t.Fatal(result)
				}
			}
			if submissions != 1 || polls != 3 {
				t.Fatal(submissions, polls)
			}
		})
	}
}

func TestDefaultsAndNonbillableCatalogCheck(t *testing.T) {
	calls := 0
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		s := body["settings"].(map[string]any)
		if s["duration"] != float64(5) || s["resolution"] != "720p" || s["audio"] != "off" || s["aspect_ratio"] != "16:9" || s["multi_shot"] != true {
			t.Fatal(s)
		}
		return response(200, `{"code":0,"data":{"id":"task_123","status":"processing"}}`), nil
	})
	models, err := a.ListModels(context.Background())
	if err != nil || !exactReviewedOperationIDs(models) || calls != 0 {
		t.Fatal(models, err, calls)
	}
	models[0].Resolutions[0] = "altered"
	if a.ReviewedCatalogModels()[0].Resolutions[0] != "720p" {
		t.Fatal("catalog mutated")
	}
	if _, err := a.Generate(context.Background(), core.MediaRequest{Model: TextToVideo, Prompt: "Synthetic fixture"}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidInputsFailBeforeSubmission(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid input reached network"); return nil, nil })
	for name, mutate := range map[string]func(*core.MediaRequest){
		"legacy model":         func(r *core.MediaRequest) { r.Model = "kling-v1" },
		"bare upstream ID":     func(r *core.MediaRequest) { r.Model = "kling-4.0" },
		"empty prompt":         func(r *core.MediaRequest) { r.Prompt = " " },
		"long prompt":          func(r *core.MediaRequest) { r.Prompt = strings.Repeat("猫", 3073) },
		"short duration":       func(r *core.MediaRequest) { r.Options.Duration = 2 },
		"long duration":        func(r *core.MediaRequest) { r.Options.Duration = 16 },
		"resolution":           func(r *core.MediaRequest) { r.Options.Resolution = "4K" },
		"ratio":                func(r *core.MediaRequest) { r.Options.Ratio = "4:3" },
		"seed":                 func(r *core.MediaRequest) { r.Options.Seed = ptr(int64(1)) },
		"watermark":            func(r *core.MediaRequest) { r.Options.Watermark = ptr(false) },
		"negative prompt":      func(r *core.MediaRequest) { r.Options.NegativePrompt = "fixture" },
		"video":                func(r *core.MediaRequest) { r.Options.VideoURLs = []string{"https://cdn.example/video.mp4"} },
		"audio":                func(r *core.MediaRequest) { r.Options.AudioURL = "https://cdn.example/audio.mp3" },
		"unknown parameter":    func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"model_name": "kling-v1"} },
		"wrong parameter type": func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"multi_shot": "true"} },
		"text with frame":      func(r *core.MediaRequest) { r.Options.ImageURLs = []string{"https://cdn.example/a.png"} },
		"missing frame":        func(r *core.MediaRequest) { r.Model = ImageToVideo },
		"last only": func(r *core.MediaRequest) {
			r.Model = ImageToVideo
			r.Options.ReferenceMode = "last_frame"
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
		},
		"image ratio": func(r *core.MediaRequest) {
			r.Model = ImageToVideo
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
			r.Options.Ratio = "16:9"
		},
		"private frame": func(r *core.MediaRequest) {
			r.Model = ImageToVideo
			r.Options.ImageURLs = []string{"https://127.0.0.1/a.png"}
		},
		"inline unsupported": func(r *core.MediaRequest) {
			r.Model = ImageToVideo
			r.Options.ImageURLs = []string{"data:image/png;base64,AAAA"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := core.MediaRequest{Model: TextToVideo, Prompt: "Synthetic fixture"}
			mutate(&r)
			_, err := a.Generate(context.Background(), r)
			if !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
			if _, _, err = a.Quote(context.Background(), core.MediaModel{}, r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
}

func TestPriceMatrixAndMeteringIsNotBilling(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("quotes must be local"); return nil, nil })
	for _, model := range []string{TextToVideo, ImageToVideo} {
		for _, row := range []struct {
			resolution string
			audio      bool
			rate       money.USD
		}{
			{"720p", false, 84000}, {"1080p", false, 112000}, {"4k", false, 420000},
			{"720p", true, 126000}, {"1080p", true, 168000}, {"4k", true, 420000},
		} {
			for _, seconds := range []int{3, 5, 15} {
				r := core.MediaRequest{Model: model, Prompt: "Synthetic fixture", Options: core.MediaOptions{Duration: seconds, Resolution: row.resolution, GenerateAudio: &row.audio}}
				if model == ImageToVideo {
					r.Options.ImageURLs = []string{"https://cdn.example/first.png"}
				}
				cost, _, err := a.Quote(context.Background(), core.MediaModel{}, r)
				if err != nil || cost.ExpectedUSD != row.rate*money.USD(seconds) || cost.Pricing.Price != row.rate || cost.ActualUSD != nil || cost.Status != "estimated" || cost.Pricing.Source != priceSource {
					t.Fatal(cost, err)
				}
				if !cost.MeterUsage(*core.NormalizeProviderUsage(map[string]any{"output_video_duration": float64(seconds)})) || cost.ActualUSD != nil || cost.Status != "pending_billing" {
					t.Fatal(cost)
				}
			}
		}
	}
}

func TestMalformedAndFailedTasks(t *testing.T) {
	for name, body := range map[string]string{
		"missing code": `{"data":[]}`, "missing data": `{"code":0}`, "empty": `{"code":0,"data":[]}`,
		"wrong ID":       `{"code":0,"data":[{"id":"other","status":"processing"}]}`,
		"unknown status": `{"code":0,"data":[{"id":"task_123","status":"new_unknown"}]}`,
		"missing output": `{"code":0,"data":[{"id":"task_123","status":"succeeded"}]}`,
		"unsafe output":  `{"code":0,"data":[{"id":"task_123","status":"succeeded","outputs":[{"type":"video","url":"https://169.254.169.254/video.mp4","duration":"5"}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			a := adapter(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
			if _, err := a.GetTask(context.Background(), "task_123"); err == nil {
				t.Fatal("malformed task accepted")
			}
		})
	}
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return response(200, `{"code":0,"data":[{"id":"task_123","status":"failed","message":"fixture-secret private prompt"}]}`), nil
	})
	result, err := a.GetTask(context.Background(), "task_123")
	if err != nil || result.Status != core.ProviderFailed || strings.Contains(result.Error, "fixture-secret") || strings.Contains(result.Error, "private prompt") {
		t.Fatal(result, err)
	}
	for _, id := range []string{"", "../tasks", "a,b", "a?api_key=fixture-secret", strings.Repeat("a", 1025)} {
		if _, err := a.GetTask(context.Background(), id); err == nil {
			t.Fatal("invalid handle accepted")
		}
	}
}

func TestSafeErrorsBoundedResponsesAndNoRetry(t *testing.T) {
	for _, status := range []int{302, 400, 401, 403, 404, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			a := adapter(t, func(*http.Request) (*http.Response, error) {
				calls++
				r := response(status, `{"message":"fixture-secret private prompt"}`)
				r.Header.Set("Location", "https://other.example/")
				return r, nil
			})
			_, err := a.Generate(context.Background(), core.MediaRequest{Model: TextToVideo, Prompt: "private prompt"})
			if err == nil || calls != 1 || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "private prompt") {
				t.Fatal(err, calls)
			}
		})
	}
	for _, body := range []string{`{"code":1002,"message":"fixture-secret private prompt"}`, `{"data":{"id":"task_123"}}`, strings.Repeat("x", maxResponseBytes+1)} {
		a := adapter(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil })
		_, err := a.Generate(context.Background(), core.MediaRequest{Model: TextToVideo, Prompt: "private prompt"})
		if err == nil || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "private prompt") {
			t.Fatal(err)
		}
	}
	a := adapter(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture-secret transport error") })
	if _, err := a.GetTask(context.Background(), "task_123"); err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.GetTask(ctx, "task_123"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBillingDenominationsAndContentAreNotPromoted(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return response(200, `{"code":0,"data":[{"id":"task_123","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/video.mp4?private=fixture-secret","duration":"5"}],"message":"private prompt","billing":[{"charge_type":"cash","cash_type":"balance","currency":"USD","amount":"0.42"},{"charge_type":"cash","cash_type":"test_balance","currency":"CNY","amount":"1.5"},{"charge_type":"unit","package_type":"video","amount":"3.0"},{"charge_type":"cash","currency":"USD","amount":"fixture-secret"}]}]}`), nil
	})
	r, err := a.GetTask(context.Background(), "task_123")
	if err != nil || r.Usage == nil || r.Usage.EstimatedCost != nil || r.Usage.Currency != "" || strings.Contains(string(r.Usage.Raw), "fixture-secret") || strings.Contains(string(r.Usage.Raw), "private prompt") {
		t.Fatal(r, err)
	}
	var raw struct {
		Billing []map[string]string `json:"billing"`
	}
	if json.Unmarshal(r.Usage.Raw, &raw) != nil || len(raw.Billing) != 3 || raw.Billing[1]["currency"] != "CNY" || raw.Billing[2]["charge_type"] != "unit" {
		t.Fatal(string(r.Usage.Raw))
	}
}

func TestFixedOriginAndRedirectPolicy(t *testing.T) {
	for _, base := range []string{"http://api-singapore.klingai.com", "https://api.klingai.com", "https://user@api-singapore.klingai.com", "https://api-singapore.klingai.com.evil.example", "https://api-singapore.klingai.com?key=x", "https://api-singapore.klingai.com/v1"} {
		if _, err := New("fixture-secret", base, nil); err == nil {
			t.Fatal("unsupported origin accepted", base)
		}
	}
	if _, err := New("bad\nkey", DefaultBaseURL, nil); err == nil {
		t.Fatal("invalid key accepted")
	}
	client := &http.Client{}
	a, err := New("fixture-secret", "", client)
	if err != nil || a.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse || client.CheckRedirect != nil {
		t.Fatal("redirect policy is unsafe or changed caller client", err)
	}
}

func exactReviewedOperationIDs(models []core.MediaModel) bool {
	expected := map[string]bool{SessionPreview: true, OmniVideo3: true, OmniVideoO1: true, LipSync: true, MultiElementsEditing: true, TextToVideo: true, ImageToVideo: true, TextToVideo4: true, ImageToVideo4: true, TextToVideo3Turbo: true, ImageToVideo3Turbo: true, TextToVideo25Turbo: true, ImageToVideo25Turbo: true, OmniVideo4: true, ImageGeneration3: true, ImageGeneration21: true, ImageOmni3: true, ImageO1: true, MultiImage21: true, ImageOutpainting: true, ImageMultiShot: true, TextToAudio: true, VideoToAudio: true, TextToSpeech: true, AvatarImageToVideo: true, TextToVideo26: true, ImageToVideo26: true, MotionControl3: true, MotionControl26: true, KlingEffects: true, ApparelReplicator: true, GoodsStudio: true, VideoCommerce: true, VirtualTryOn: true}
	for _, m := range models {
		if !expected[m.ID] {
			return false
		}
		delete(expected, m.ID)
	}
	return len(expected) == 0
}

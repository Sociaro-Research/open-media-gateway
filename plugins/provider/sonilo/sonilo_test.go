package sonilo

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerkit"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func adapter(t *testing.T, f transport) *Adapter {
	t.Helper()
	a, e := New("fixture-secret", DefaultBaseURL, &http.Client{Transport: f})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func requestFor(op operation) core.MediaRequest {
	r := core.MediaRequest{Model: op.id, Prompt: "fixture"}
	m := modelFor(op.id)
	if m.SupportsVideoInput {
		r.Options.VideoURL = "https://cdn.example/reference.mp4"
	}
	if m.SupportsAudioInput {
		r.Options.AudioURL = "https://cdn.example/voice.wav"
	}
	if op.endpoint == "audio-ducking" {
		r.Prompt = ""
		r.Options.Parameters = map[string]any{"music_url": "https://cdn.example/music.wav"}
	}
	return r
}

func TestAllOperationPayloadsPollingAndRestart(t *testing.T) {
	for _, op := range operations {
		t.Run(op.id, func(t *testing.T) {
			r := requestFor(op)
			r.Options.Format = valueOr(first(modelFor(op.id).Formats), "")
			var submissions, polls int
			f := transport(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("Authorization") != "Bearer fixture-secret" || req.Header.Get("User-Agent") != "Sociaro-Media-Gateway/Sonilo" {
					t.Error("missing auth or descriptive agent")
				}
				if req.Method == "POST" {
					submissions++
					if req.URL.Path != "/v1/"+op.endpoint {
						t.Fatal(req.URL)
					}
					if err := req.ParseMultipartForm(1 << 20); err != nil {
						t.Fatal(err)
					}
					form := req.MultipartForm.Value
					if len(req.MultipartForm.File) != 0 {
						t.Fatal("URL inputs must not be fetched by adapter")
					}
					if op.id == "text-to-music" || op.id == "video-to-music" {
						if req.FormValue("mode") != "async" {
							t.Fatal("streaming submission")
						}
					}
					if op.id == "text-to-music" && req.FormValue("duration") != "30" {
						t.Fatal(form)
					}
					if op.id == "text-to-sfx" && req.FormValue("duration") != "8" {
						t.Fatal(form)
					}
					if op.endpoint == "audio-ducking" {
						if req.FormValue("voice_url") == "" || req.FormValue("music_url") == "" || req.FormValue("prompt") != "" {
							t.Fatal(form)
						}
					}
					if strings.HasSuffix(op.id, "sound") && req.FormValue("music_prompt") != "fixture" {
						t.Fatal(form)
					}
					if strings.HasSuffix(op.id, "sfx") && op.media != "video" && req.FormValue("audio_format") == "" {
						t.Fatal(form)
					}
					if form["variants_num"] != nil || form["stems"] != nil || form["callback_url"] != nil {
						t.Fatal("unrequested outputs")
					}
					return response(202, `{"task_id":"fixture-job","status":"processing"}`), nil
				}
				polls++
				if req.URL.Path != "/v1/tasks/fixture-job" {
					t.Fatal(req.URL)
				}
				body := map[string]any{"task_id": "fixture-job", "type": op.service, "status": "succeeded", "duration_seconds": 2.5, "cost": 0, "title": "private generated title"}
				kind, url := "audio/wav", "https://cdn.example/result.wav?signature=private"
				if op.media == "video" {
					kind, url = "video/mp4", "https://cdn.example/result.mp4?signature=private"
				}
				media := map[string]any{"url": url, "content_type": kind, "stream_index": 0}
				switch {
				case op.endpoint == "audio-ducking" || strings.HasSuffix(op.id, "sound"):
					body["output_url"] = url
					body["output_type"] = "audio"
					if op.media == "video" {
						body["output_type"] = "video"
					}
				case op.media == "video":
					body["video"] = media
				case strings.Contains(op.id, "music"):
					body["audio"] = []any{media}
				default:
					body["audio"] = media
				}
				data, _ := json.Marshal(body)
				return response(200, string(data)), nil
			})
			a := adapter(t, f)
			task, e := a.Generate(context.Background(), r)
			if e != nil || task.ID == "" || task.ResultURL != "" {
				t.Fatal(task, e)
			}
			// A new adapter recovers the durable task without any submission memory.
			result, e := adapter(t, f).GetTask(context.Background(), task.ID)
			if e != nil || result.Status != core.ProviderSucceeded || result.ResultURL == "" || submissions != 1 || polls != 1 {
				t.Fatal(result, e, submissions, polls)
			}
			if result.Usage == nil || result.Usage.Metrics[0].Quantity != float64(op.floor) || strings.Contains(string(result.Usage.Raw), "private") {
				t.Fatal("unsafe or incorrect billable usage", result.Usage)
			}
		})
	}
}
func first(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func TestAccountDiscoveryFiltersCachesAndFailsClosed(t *testing.T) {
	var reads atomic.Int32
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		reads.Add(1)
		if r.URL.Path != "/v1/account/services" || r.Method != "GET" {
			t.Fatal(r.URL)
		}
		return response(200, `{"available_services":["text_to_music","video-to-sfx","audio_ducking"]}`), nil
	})
	models, e := a.ListModels(context.Background())
	if e != nil || len(models) != 4 {
		t.Fatal(models, e)
	}
	models[0].Parameters[0].Label = "changed"
	models, e = a.ListModels(context.Background())
	if e != nil || reads.Load() != 1 || models[0].Parameters[0].Label == "changed" {
		t.Fatal(models, e, reads.Load())
	}
	for _, body := range []string{`{}`, `{"available_services":null}`, `{"available_services":42}`} {
		_, e = adapter(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil }).ListModels(context.Background())
		if e == nil {
			t.Fatal("malformed services accepted", body)
		}
	}
	models, e = adapter(t, func(*http.Request) (*http.Response, error) { return response(200, `{"available_services":[]}`), nil }).ListModels(context.Background())
	if e != nil || models == nil || len(models) != 0 {
		t.Fatal(models, e)
	}
}

func TestInvalidInputsNeverSubmitOrQuote(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached vendor")
		return nil, nil
	})
	for _, r := range []core.MediaRequest{
		{Model: "unknown", Prompt: "x"}, {Model: DefaultModel}, {Model: DefaultModel, Prompt: strings.Repeat("x", 2001)},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{Duration: 4}},
		{Model: "text-to-sfx", Prompt: "x", Options: core.MediaOptions{Duration: 1}},
		{Model: "text-to-sfx", Prompt: "x", Options: core.MediaOptions{Parameters: map[string]any{"duration_seconds": 0.4}}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{Parameters: map[string]any{"variants_num": 2}}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{Parameters: map[string]any{"stems": true}}},
		{Model: "video-to-sfx", Options: core.MediaOptions{VideoURL: "http://127.0.0.1/video"}},
		{Model: "video-to-sfx", Options: core.MediaOptions{VideoURLs: []string{"https://cdn.example/a", "https://cdn.example/b"}}},
		{Model: "video-to-sound", Options: core.MediaOptions{VideoURL: "https://cdn.example/a", Parameters: map[string]any{"segments": `[{"start":0,"end":3,"prompt":"a"},{"start":4,"end":6,"prompt":"b"}]`}}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{Parameters: map[string]any{"segments": `[{"start":0,"prompt":"a","label":"chorus"},{"start":3,"prompt":"b"}]`}}},
		{Model: "audio-ducking", Options: core.MediaOptions{AudioURL: "https://cdn.example/a", Parameters: map[string]any{"music_url": "http://169.254.169.254/a"}}},
	} {
		if _, e := a.Generate(context.Background(), r); e == nil {
			t.Fatal("invalid request accepted", r)
		}
		if _, _, e := a.Quote(context.Background(), core.MediaModel{}, r); e == nil {
			t.Fatal("invalid quote accepted", r)
		}
	}
}

func TestTimelinesAndFractionalEffect(t *testing.T) {
	for _, tc := range []struct {
		value string
		music bool
		valid bool
	}{
		{`[{"start":0,"prompt":"a"},{"start":5,"prompt":"b","label":"outro"}]`, true, true},
		{`[{"start":0,"end":3,"prompt":"a"},{"start":3,"end":7.5,"prompt":"b"}]`, false, true},
		{`[{"start":0,"prompt":"a","label":"made-up"}]`, true, false},
		{`[{"start":null,"prompt":"a"}]`, true, false},
		{`[{"start":0,"end":0,"prompt":"a"}]`, false, false},
		{`[{"start":0,"prompt":"a","end":5}]`, true, false},
	} {
		if e := validateSegments(tc.value, tc.music, 30); (e == nil) != tc.valid {
			t.Fatal(tc, e)
		}
	}
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		if e := r.ParseMultipartForm(1 << 20); e != nil {
			t.Fatal(e)
		}
		if r.FormValue("duration") != "0.5" {
			t.Fatal("fractional duration lost")
		}
		return response(202, `{"task_id":"half-second","status":"processing"}`), nil
	})
	r := core.MediaRequest{Model: "text-to-sfx", Prompt: "click", Options: core.MediaOptions{Parameters: map[string]any{"duration_seconds": 0.5}}}
	if _, e := a.Generate(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	c, _, e := a.Quote(context.Background(), core.MediaModel{}, r)
	if e != nil || c.EstimatedUnits != 3 || c.ExpectedUSD.String() != "0.005400" {
		t.Fatal(c, e)
	}
}

func TestSafeErrorsMalformedResponsesAndCancellation(t *testing.T) {
	for _, status := range []int{301, 400, 401, 402, 403, 404, 413, 422, 429, 500} {
		calls := 0
		a := adapter(t, func(*http.Request) (*http.Response, error) {
			calls++
			return response(status, `{"message":"private prompt fixture-secret https://signed.example?secret=x"}`), nil
		})
		_, e := a.Generate(context.Background(), requestFor(operations[0]))
		if e == nil || strings.Contains(e.Error(), "private") || strings.Contains(e.Error(), "fixture-secret") || calls != 1 {
			t.Fatal(status, e, calls)
		}
	}
	for _, body := range []string{`{}`, `{"task_id":"../escape","status":"processing"}`, `{"task_id":"id","status":"succeeded"}`} {
		if _, e := adapter(t, func(*http.Request) (*http.Response, error) { return response(200, body), nil }).Generate(context.Background(), requestFor(operations[0])); e == nil {
			t.Fatal("invalid accepted response", body)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := adapter(t, func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })
	if _, e := a.Generate(ctx, requestFor(operations[0])); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	for _, base := range []string{"http://api.sonilo.com/v1", "https://api.sonilo.com.evil/v1", "https://key@api.sonilo.com/v1", "https://api.sonilo.com/v1?secret=x"} {
		if _, e := New("key", base, nil); e == nil {
			t.Fatal("unsafe endpoint accepted", base)
		}
	}
}

func TestQuotesReserveInputLimitAndRequireUnpublishedTariffs(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("quotation should not generate or call account API")
		return nil, nil
	})
	for _, op := range operations {
		r := requestFor(op)
		c, _, e := a.Quote(context.Background(), core.MediaModel{}, r)
		if op.rate == "" {
			if !errors.Is(e, core.ErrPricingUnavailable) {
				t.Fatal(c, e)
			}
			continue
		}
		if e != nil || c.ActualUSD != nil || c.ExpectedUSD <= 0 || c.Status != "estimated" {
			t.Fatal(c, e)
		}
		if op.id != "text-to-music" && op.id != "text-to-sfx" && c.EstimatedUnits != float64(op.maxDuration) {
			t.Fatal("unprobed input was underreserved", c)
		}
	}
	if e := a.setPrices(providerkit.Credentials{Extra: map[string]string{"sound_price": "0.02"}}); e != nil {
		t.Fatal(e)
	}
	c, _, e := a.Quote(context.Background(), core.MediaModel{}, requestFor(operations[6]))
	if e != nil || c.ExpectedUSD.String() != "9.600000" {
		t.Fatal(c, e)
	}
	if e := a.setPrices(providerkit.Credentials{Extra: map[string]string{"sound_price": "-1"}}); e == nil {
		t.Fatal("negative tariff")
	}
}

func TestPollingStatesSafeFailuresAndSelectedMix(t *testing.T) {
	ref, _ := json.Marshal(taskReference{ID: "task", Model: "video-to-music", Preserve: true, Duck: true})
	id := "sonilo:" + base64.RawURLEncoding.EncodeToString(ref)
	for _, tc := range []struct {
		body    string
		status  core.ProviderStatus
		url     string
		invalid bool
	}{
		{`{"task_id":"task","type":"video_to_music","status":"processing"}`, core.ProviderRunning, "", false},
		{`{"task_id":"task","type":"video_to_music","status":"failed","error":{"code":"malicious-secret","message":"private prompt key"},"refunded":true}`, core.ProviderFailed, "", false},
		{`{"task_id":"task","type":"video_to_music","status":"succeeded","audio":[{"url":"https://cdn.example/clean"}],"mux":[{"url":"https://cdn.example/mux"}],"ducked":[{"url":"https://cdn.example/ducked"}]}`, core.ProviderSucceeded, "https://cdn.example/ducked", false},
		{`{"task_id":"task","type":"video_to_music","status":"succeeded","audio":[{"url":"https://cdn.example/clean"}],"mux":[{"url":"https://cdn.example/mux"}]}`, core.ProviderSucceeded, "https://cdn.example/mux", false},
		{`{"task_id":"task","type":"video_to_music","status":"succeeded","audio":[{"url":"https://cdn.example/clean"}]}`, "", "", true},
		{`{"task_id":"other","type":"video_to_music","status":"processing"}`, "", "", true},
		{`{"task_id":"task","type":"text_to_sfx","status":"processing"}`, "", "", true},
		{`{"task_id":"task","type":"video_to_music","status":"unknown"}`, "", "", true},
		{`{"task_id":"task","type":"video_to_music","status":"succeeded","audio":[{"url":"http://localhost/a"}],"mux":[{"url":"http://localhost/a"}]}`, "", "", true},
	} {
		result, err := adapter(t, func(*http.Request) (*http.Response, error) { return response(200, tc.body), nil }).GetTask(context.Background(), id)
		if (err != nil) != tc.invalid || !tc.invalid && (result.Status != tc.status || result.ResultURL != tc.url) {
			t.Fatal(tc, result, err)
		}
		if strings.Contains(result.Error, "secret") || strings.Contains(result.Error, "private") {
			t.Fatal("vendor error content leaked", result)
		}
	}
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid task reference reached upstream")
		return nil, nil
	})
	for _, id := range []string{"../escape", "sonilo:invalid", "sonilo:" + base64.RawURLEncoding.EncodeToString([]byte(`{"id":"../escape","model":"text-to-music"}`))} {
		if _, err := a.GetTask(context.Background(), id); err == nil {
			t.Fatal("invalid task reference accepted")
		}
	}
}

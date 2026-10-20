package openai

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
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, kind, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {kind}, "X-Request-Id": {"req-fixture"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func adapter(t *testing.T, f transportFunc) *Adapter {
	t.Helper()
	a, err := New("fixture-secret", DefaultBaseURL, &http.Client{Transport: f, Timeout: time.Second}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

const imageResponse = `{"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":110,"output_tokens":440,"input_tokens_details":{"text_tokens":10,"image_tokens":100},"output_tokens_details":{"text_tokens":1,"image_tokens":439}}}`

func TestCatalogUsesReviewedAccountModelsAndCache(t *testing.T) {
	calls := 0
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/models" {
			t.Fatal(r.URL)
		}
		return response(200, "application/json", `{"data":[{"id":"gpt-image-2.5-flare"},{"id":"gpt-4o-mini-tts"},{"id":"sora-2"},{"id":"dall-e-3"},{"id":"gpt-image-999"}]}`), nil
	})
	models, err := a.ListModels(context.Background())
	if err != nil || len(models) != 2 {
		t.Fatal(models, err)
	}
	models[0].Qualities[0] = "corrupted"
	models, err = a.ListModels(context.Background())
	if err != nil || models[0].Qualities[0] != "medium" || calls != 1 {
		t.Fatal(models, err, calls)
	}
	if err = a.CheckCredentials(context.Background()); err != nil || calls != 2 {
		t.Fatal(err, calls)
	}
}

func TestAllImageModelsGenerateAndEdit(t *testing.T) {
	for _, id := range modelIDs {
		if !strings.Contains(id, "image") {
			continue
		}
		for _, edit := range []bool{false, true} {
			t.Run(id+map[bool]string{true: "/edit", false: "/generate"}[edit], func(t *testing.T) {
				a := adapter(t, func(r *http.Request) (*http.Response, error) {
					if r.Header.Get("Authorization") != "Bearer fixture-secret" || r.Header.Get("User-Agent") == "" {
						t.Fatal("missing request headers")
					}
					var p map[string]any
					if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
						t.Fatal(err)
					}
					expected := "/v1/images/generations"
					if edit {
						expected = "/v1/images/edits"
						images := p["images"].([]any)
						if images[0].(map[string]any)["image_url"] != "https://example.test/ref.png" {
							t.Fatal(p)
						}
					}
					if r.URL.Path != expected || p["model"] != id || p["quality"] != "medium" || p["n"] != float64(1) || p["size"] != "1024x1024" || p["output_format"] != "png" {
						t.Fatal(p, r.URL)
					}
					if _, ok := p["response_format"]; ok {
						t.Fatal("GPT Image does not accept response_format")
					}
					return response(200, "application/json", imageResponse), nil
				})
				req := core.MediaRequest{Model: id, Prompt: "draw a tree"}
				if edit {
					req.Options.ImageURLs = []string{"https://example.test/ref.png"}
				}
				result, err := a.Generate(context.Background(), req)
				if err != nil || result.ResultURL != "data:image/png;base64,aW1hZ2U=" || result.ID != "req-fixture" {
					t.Fatal(result, err)
				}
				cost, _, err := a.Quote(context.Background(), describeModel(id), req)
				if err != nil || !cost.MeterUsage(*result.Usage) || cost.ActualUSD != nil || cost.MeteredUSD == nil {
					t.Fatal(cost, err)
				}
			})
		}
	}
}

func TestSpeechModelsAndFormats(t *testing.T) {
	for _, id := range []string{"tts-1", "tts-1-hd", "gpt-4o-mini-tts"} {
		for _, format := range describeModel(id).Formats {
			t.Run(id+"/"+format, func(t *testing.T) {
				a := adapter(t, func(r *http.Request) (*http.Response, error) {
					var p map[string]any
					_ = json.NewDecoder(r.Body).Decode(&p)
					if r.URL.Path != "/v1/audio/speech" || p["input"] != "Hello" || p["voice"] != "alloy" || p["response_format"] != format {
						t.Fatal(p, r.URL)
					}
					if id == "gpt-4o-mini-tts" && p["instructions"] != "Whisper" {
						t.Fatal(p)
					}
					return response(200, audioMIME(format), "audio"), nil
				})
				req := core.MediaRequest{Model: id, Prompt: "Hello", Options: core.MediaOptions{Format: format}}
				if id == "gpt-4o-mini-tts" {
					req.Options.Instructions = "Whisper"
				}
				result, err := a.Generate(context.Background(), req)
				if err != nil || !strings.HasPrefix(result.ResultURL, "data:"+audioMIME(format)+";base64,") || result.Usage != nil {
					t.Fatal(result, err)
				}
			})
		}
	}
}

func TestAudioReplyModelsAndUsage(t *testing.T) {
	for _, id := range []string{"gpt-audio-1.5", "gpt-audio", "gpt-audio-mini"} {
		t.Run(id, func(t *testing.T) {
			a := adapter(t, func(r *http.Request) (*http.Response, error) {
				var p map[string]any
				_ = json.NewDecoder(r.Body).Decode(&p)
				if r.URL.Path != "/v1/chat/completions" || p["store"] != false || p["max_completion_tokens"] != float64(4096) || len(p["messages"].([]any)) != 2 {
					t.Fatal(p, r.URL)
				}
				return response(200, "application/json", `{"choices":[{"finish_reason":"stop","message":{"audio":{"data":"YXVkaW8="}}}],"usage":{"prompt_tokens":10,"completion_tokens":100,"completion_tokens_details":{"audio_tokens":80}}}`), nil
			})
			req := core.MediaRequest{Model: id, Prompt: "Tell a short story", Options: core.MediaOptions{Instructions: "Be cheerful"}}
			result, err := a.Generate(context.Background(), req)
			if err != nil || result.ResultURL != "data:audio/mpeg;base64,YXVkaW8=" {
				t.Fatal(result, err)
			}
			cost, _, err := a.Quote(context.Background(), describeModel(id), req)
			if err != nil || !cost.MeterUsage(*result.Usage) {
				t.Fatal(cost, err)
			}
			expected := (10*rates(id)["text_input_tokens"] + 80*rates(id)["audio_output_tokens"] + 20*rates(id)["text_output_tokens"]) / 1e6
			if mathDifference(cost.MeteredUSD.Float64(), expected) > 0.000002 {
				t.Fatal(cost.MeteredUSD, expected)
			}
		})
	}
}
func mathDifference(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func TestErrorsAndMalformedResponsesNeverExposeSecretsOrRetry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		kind, body string
	}{
		{"auth", 401, "application/json", `{"error":{"message":"fixture-secret secret prompt"}}`},
		{"quota", 429, "application/json", `{"error":{"message":"fixture-secret"}}`},
		{"server", 503, "text/html", "secret"}, {"redirect", 302, "text/html", "secret"},
		{"malformed", 200, "application/json", "{"}, {"empty", 200, "application/json", `{"data":[]}`},
		{"badbase64", 200, "application/json", `{"data":[{"b64_json":"?"}]}`}, {"html", 200, "text/html", "image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			a := adapter(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(tc.status, tc.kind, tc.body), nil
			})
			_, err := a.Generate(context.Background(), core.MediaRequest{Model: DefaultModel, Prompt: "secret prompt"})
			if err == nil || strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "secret prompt") || calls != 1 {
				t.Fatal(err, calls)
			}
		})
	}
	a := adapter(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture-secret") })
	_, err := a.Generate(context.Background(), core.MediaRequest{Model: DefaultModel, Prompt: "draw"})
	if err == nil || strings.Contains(err.Error(), "fixture-secret") {
		t.Fatal(err)
	}
	if _, err := a.GetTask(context.Background(), "id"); err == nil {
		t.Fatal("synchronous API must not fake polling")
	}
}

func TestInvalidOptionsFailBeforePaidCall(t *testing.T) {
	flag := false
	for _, r := range []core.MediaRequest{
		{Model: "sora-2", Prompt: "x"},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{Watermark: &flag}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{Background: "transparent", Format: "jpeg"}},
		{Model: "gpt-image-1", Prompt: "x", Options: core.MediaOptions{Quality: "max"}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{Resolution: "17x17"}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{ImageURLs: []string{"file:///etc/passwd"}}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{ImageURLs: []string{"data:image/gif;base64,YQ=="}}},
		{Model: DefaultModel, Prompt: "x", Options: core.MediaOptions{ImageURLs: make([]string, 17)}},
		{Model: "tts-1", Prompt: "x", Options: core.MediaOptions{Instructions: "whisper"}},
		{Model: "tts-1", Prompt: strings.Repeat("x", 4097)},
		{Model: "gpt-audio-1.5", Prompt: "x", Options: core.MediaOptions{Format: "aac"}},
	} {
		a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("paid call for invalid input"); return nil, nil })
		if _, err := a.Generate(context.Background(), r); err == nil {
			t.Fatal(r)
		}
	}
}

func TestBaseURLAndCancellation(t *testing.T) {
	for _, url := range []string{"http://api.openai.com/v1", "https://api.openai.com.evil.test/v1", "https://api.openai.com/v1?key=secret", "https://user@api.openai.com/v1", "https://api.openai.com:444/v1"} {
		if _, err := New("key", url, nil, 0); err == nil {
			t.Fatal(url)
		}
	}
	var calls atomic.Int32
	a := adapter(t, func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Generate(ctx, core.MediaRequest{Model: DefaultModel, Prompt: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPricingCalculatorAndMissingUsage(t *testing.T) {
	for _, tc := range []struct {
		id, size, quality string
		tokens            float64
	}{
		{"gpt-image-2", "1024x1024", "low", 196}, {"gpt-image-2", "1024x1024", "medium", 1756},
		{"gpt-image-2.5-flare", "1024x1024", "medium", 439}, {"gpt-image-2.5-sunburst", "1024x1024", "max", 7024},
		{"gpt-image-1.5", "1536x1024", "high", 6208},
	} {
		if got := outputTokens(tc.id, tc.size, tc.quality); got != tc.tokens {
			t.Fatal(tc, got)
		}
	}
	a := adapter(t, nil)
	c, _, err := a.Quote(context.Background(), core.MediaModel{}, core.MediaRequest{Model: DefaultModel, Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	usage := imageUsage(json.RawMessage(`{"output_tokens":123}`), "gpt-image-2")
	if c.MeterUsage(*usage) {
		t.Fatal("missing input details must not become zero cost")
	}
	if imageUsage(nil, "gpt-image-2") != nil {
		t.Fatal("missing usage was invented")
	}
	if _, err := encodedResult(base64.StdEncoding.EncodeToString([]byte("x")), "image/png"); err != nil {
		t.Fatal(err)
	}
}

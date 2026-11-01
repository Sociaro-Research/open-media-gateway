package minimax

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/httpclient"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func response(status int, value any) *http.Response {
	b, _ := json.Marshal(value)
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(b)))}
}
func fixture(t *testing.T, f transport) *Adapter {
	t.Helper()
	a, e := New("fixture-key", "", &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.minimax.io" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Fatal("unexpected authenticated destination")
		}
		return f(r)
	})})
	if e != nil {
		t.Fatal(e)
	}

	return a
}
func body(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var p map[string]any
	if e := json.NewDecoder(r.Body).Decode(&p); e != nil {
		t.Fatal(e)
	}
	return p
}
func success(id string) map[string]any {
	return map[string]any{"task_id": id, "base_resp": map[string]int{"status_code": 0}}
}
func owned(owner string) context.Context {
	return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), owner), core.ProviderConnectionScope{ID: "fixture-connection", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
}

func requestFor(m core.MediaModel, mode core.ReferenceMode) core.MediaRequest {
	r := core.MediaRequest{Model: m.ID, Prompt: "A cinematic scene", Options: core.MediaOptions{ReferenceMode: mode.ID}}
	if m.NoPrompt {
		r.Prompt = ""
	}
	for i := 0; i < mode.MinImages; i++ {
		r.Options.ImageURLs = append(r.Options.ImageURLs, "https://media.example/image.png")
	}
	for i := 0; i < mode.MinVideos; i++ {
		r.Options.VideoURLs = append(r.Options.VideoURLs, "https://media.example/video.mp4")
	}
	for i := 0; i < mode.MinAudios; i++ {
		r.Options.AudioURLs = append(r.Options.AudioURLs, "https://media.example/audio.wav")
	}
	if mode.ID == "references" && len(r.Options.ImageURLs) == 0 && mode.MaxImages > 0 {
		r.Options.ImageURLs = []string{"https://media.example/image.png"}
	}
	return r
}
func TestEveryCatalogGenerationWorkflow(t *testing.T) {
	if len(catalog()) != 33 {
		t.Fatalf("catalog has %d entries", len(catalog()))
	}
	seen := map[string]bool{}
	for _, m := range catalog() {
		if m.ID == Regeneration {
			continue
		} // Dedicated capability-bearing source-task contract test.
		if seen[m.ID] {
			t.Fatal("duplicate", m.ID)
		}
		seen[m.ID] = true
		for _, mode := range m.ReferenceModes {
			t.Run(m.ID+"/"+mode.ID, func(t *testing.T) {
				r := requestFor(m, mode)
				calls := 0
				a := fixture(t, func(h *http.Request) (*http.Response, error) {
					calls++
					op, _ := operationFor(m.ID)
					if h.Method != "POST" || h.URL.Path != op.endpoint {
						t.Fatalf("wrong route %s %s", h.Method, h.URL.Path)
					}
					p := body(t, h)
					switch op.kind {
					case "h3":
						if p["model"] != m.ID || p["duration"] != float64(5) || p["resolution"] != "768P" {
							t.Fatal(p)
						}
						content := p["content"].([]any)
						if len(content) != 1+len(r.Options.ImageURLs) {
							t.Fatal(content)
						}
						if mode.ID == "first_frame" && content[1].(map[string]any)["role"] != "first_frame" {
							t.Fatal(content)
						}
						if mode.ID == "last_frame" && content[1].(map[string]any)["role"] != "last_frame" {
							t.Fatal(content)
						}
						if mode.ID == "first_last_frame" && content[2].(map[string]any)["role"] != "last_frame" {
							t.Fatal(content)
						}
						if m.ID == "MiniMax-H3-Max" && p["extra"].(map[string]any)["prompt_expansion_mode"] != "balanced" {
							t.Fatal(p)
						}
					case "legacy":
						if p["model"] != m.ID {
							t.Fatal(p)
						}
						if mode.ID == "first_frame" && p["first_frame_image"] == nil {
							t.Fatal(p)
						}
						if mode.ID == "last_frame" && p["last_frame_image"] == nil {
							t.Fatal(p)
						}
						if m.ID == "S2V-01" && (p["duration"] != nil || p["subject_reference"] == nil) {
							t.Fatal(p)
						}
					case "agent":
						if p["model"] != nil || p["template_id"] != op.template.id {
							t.Fatal(p)
						}
						if op.template.image && p["media_inputs"] == nil || op.template.text && p["text_inputs"] == nil {
							t.Fatal(p)
						}
					case "image":
						if p["model"] != m.ID || p["response_format"] != "url" || p["n"] != float64(1) {
							t.Fatal(p)
						}
						return response(200, map[string]any{"data": map[string]any{"image_urls": []string{"https://media.example/result.jpg"}}, "metadata": map[string]int{"success_count": 1, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
					}
					return response(200, success("123")), nil
				})
				task, e := a.Generate(owned("alice"), r)
				if e != nil || task.ID == "" || calls != 1 {
					t.Fatalf("task=%+v calls=%d error=%v", task, calls, e)
				}
				if m.MediaType == "image" && task.ResultURL == "" {
					t.Fatal("missing immediate result")
				}
			})
		}
	}
}
func TestH3MultimodalRequestAndPrices(t *testing.T) {
	for _, id := range []string{"MiniMax-H3", "MiniMax-H3-Highspeed", "MiniMax-H3-Lite", "MiniMax-H3-Max"} {
		for _, res := range []string{"768p", "480p", "2k"} {
			m, _ := modelFor(id)
			if !contains(m.Resolutions, res) {
				continue
			}
			t.Run(id+res, func(t *testing.T) {
				r := core.MediaRequest{Model: id, Prompt: "Follow reference image 1 and reference video 1 with reference audio 1", Options: core.MediaOptions{ReferenceMode: "references", Resolution: res, Ratio: "16:9", Duration: 7, ImageURLs: []string{"https://media.example/i1", "https://media.example/i2", "https://media.example/i3", "https://media.example/i4", "https://media.example/i5", "https://media.example/i6"}, VideoURLs: []string{"https://media.example/video"}, AudioURLs: []string{"https://media.example/audio"}}}
				a := fixture(t, func(h *http.Request) (*http.Response, error) {
					p := body(t, h)
					c := p["content"].([]any)
					if len(c) != 9 || c[1].(map[string]any)["role"] != "reference_image" || c[7].(map[string]any)["role"] != "reference_video" || c[8].(map[string]any)["role"] != "reference_audio" {
						t.Fatal(c)
					}
					return response(200, success("123")), nil
				})
				if _, e := a.Generate(owned("alice"), r); e != nil {
					t.Fatal(e)
				}
				q, _, e := a.Quote(context.Background(), m, r)
				if e != nil {
					t.Fatal(e)
				}
				rate := h3Tariff(id, res)
				want := rate.output*7 + rate.input*15 + rate.image*coreMoney(max(0, 6-rate.free))
				if q.ExpectedUSD != want || len(q.Pricing.Components) != 3 {
					t.Fatal(q, want)
				}
				r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{"https://media.example/video": {Type: "video", Duration: 4}}
				q, _, e = a.Quote(context.Background(), m, r)
				want = rate.output*7 + rate.input*4 + rate.image*coreMoney(max(0, 6-rate.free))
				if e != nil || q.ExpectedUSD != want {
					t.Fatal(q, want, e)
				}
			})
		}
	}
}
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
func TestValidationBoundaries(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached provider")
		return nil, nil
	})
	boolp := func(v bool) *bool { return &v }
	cases := []struct {
		name string
		r    core.MediaRequest
	}{
		{"max four seconds", core.MediaRequest{Model: "MiniMax-H3-Max", Prompt: "scene", Options: core.MediaOptions{Duration: 4}}},
		{"H3 low resolution", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{Resolution: "480p"}}},
		{"Lite 2k", core.MediaRequest{Model: "MiniMax-H3-Lite", Prompt: "scene", Options: core.MediaOptions{Resolution: "2k"}}},
		{"Lite adaptive ref", core.MediaRequest{Model: "MiniMax-H3-Lite", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "references", Ratio: "adaptive"}}},
		{"text adaptive", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{Ratio: "adaptive"}}},
		{"frame ignored ratio", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "first_frame", ImageURLs: []string{"https://media.example/i"}, Ratio: "16:9"}}},
		{"frame plus audio", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "first_frame", ImageURLs: []string{"https://media.example/i"}, AudioURLs: []string{"https://media.example/a"}}}},
		{"empty H3 prompt", core.MediaRequest{Model: "MiniMax-H3"}},
		{"prompt boundary", core.MediaRequest{Model: "MiniMax-H3", Prompt: strings.Repeat("界", 7001)}},
		{"silent", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{GenerateAudio: boolp(false)}}},
		{"unknown parameter", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{Parameters: map[string]any{"prompt_expansion_mode": "quality"}}}},
		{"legacy 1080 ten", core.MediaRequest{Model: "MiniMax-Hailuo-02", Prompt: "scene", Options: core.MediaOptions{Resolution: "1080p", Duration: 10}}},
		{"legacy 512 text", core.MediaRequest{Model: "MiniMax-Hailuo-02", Prompt: "scene", Options: core.MediaOptions{Resolution: "512p"}}},
		{"live image text", core.MediaRequest{Model: "image-01-live", Prompt: "scene"}},
		{"width pair", core.MediaRequest{Model: "image-01", Prompt: "scene", Options: core.MediaOptions{Parameters: map[string]any{"width": 1024}}}},
		{"width step", core.MediaRequest{Model: "image-01", Prompt: "scene", Options: core.MediaOptions{Parameters: map[string]any{"width": 1025, "height": 1024}}}},
		{"image format", core.MediaRequest{Model: "image-01", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "references", ImageURLs: []string{"data:image/webp;base64,YQ=="}}}},
		{"foreign file", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ImageURLs: []string{"mm_file://123"}}}},
		{"raw asset", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ImageURLs: []string{"asset://123"}}}},
		{"video seed", core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{Seed: new(int64)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := a.Generate(owned("alice"), tc.r); e == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	for _, n := range []int{7000, 1} {
		r := core.MediaRequest{Model: "MiniMax-H3", Prompt: strings.Repeat("界", n)}
		if e := a.ValidateRequest(context.Background(), r); e != nil {
			t.Fatal(n, e)
		}
	}
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "references", ImageURLs: make([]string, 9), VideoURLs: make([]string, 3), AudioURLs: []string{"https://media.example/a"}}}
	for i := range r.Options.ImageURLs {
		r.Options.ImageURLs[i] = "https://media.example/i"
	}
	for i := range r.Options.VideoURLs {
		r.Options.VideoURLs[i] = "https://media.example/v"
	}
	if e := a.ValidateRequest(context.Background(), r); e == nil {
		t.Fatal("13 files accepted")
	}
}
func TestAuthenticationIsFreeAndLimited(t *testing.T) {
	calls := 0
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/models" || !providerkit.IsCredentialCheck(r.Context()) {
			t.Fatal("not a free marked model query")
		}
		return response(200, map[string]any{"object": "list", "data": []any{map[string]string{"id": "MiniMax-M3"}}}), nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := a.CheckCredentials(context.Background()); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if calls != 1 || a.CredentialStatus().Warning == "" {
		t.Fatal(calls, a.CredentialStatus())
	}
	for _, raw := range []string{"http://api.minimax.io", "https://evil.example", "https://api.minimax.io/redirect", "https://user@api.minimax.io", "https://api.minimax.io?x=1"} {
		if _, e := New("x", raw, nil); e == nil {
			t.Fatal("accepted endpoint", raw)
		}
	}
}
func TestErrorsDoNotExposeProviderTextOrRetry(t *testing.T) {
	for _, code := range []int{1000, 1001, 1002, 1004, 1008, 1013, 1026, 1027, 1039, 2013, 2049} {
		t.Run(string(rune(code)), func(t *testing.T) {
			calls := 0
			a := fixture(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(200, map[string]any{"base_resp": map[string]any{"status_code": code, "status_msg": "fixture-key private-user-prompt"}}), nil
			})
			_, e := a.Generate(owned("alice"), core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"})
			if e == nil || strings.Contains(e.Error(), "fixture-key") || strings.Contains(e.Error(), "private-user-prompt") || calls != 1 {
				t.Fatal(e, calls)
			}
		})
	}
	for _, status := range []int{301, 400, 401, 402, 403, 404, 422, 429, 500} {
		a := fixture(t, func(*http.Request) (*http.Response, error) {
			return response(status, map[string]any{"error": map[string]string{"message": "secret"}}), nil
		})
		if _, e := a.Generate(owned("alice"), core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"}); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal(status, e)
		}
	}
	a := fixture(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("fixture-key transport url") })
	if _, e := a.Generate(owned("alice"), core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"}); e == nil || strings.Contains(e.Error(), "fixture-key") {
		t.Fatal(e)
	}
}
func TestImageBatchAllCountsFormatsAndPartialSafety(t *testing.T) {
	for _, format := range []string{"url", "base64"} {
		for n := 1; n <= 9; n++ {
			a := fixture(t, func(r *http.Request) (*http.Response, error) {
				p := body(t, r)
				if p["n"] != float64(n) || p["response_format"] != format {
					t.Fatal(p)
				}
				data := map[string]any{}
				items := []string{}
				for i := 0; i < n; i++ {
					v := "https://media.example/out.jpg"
					if format == "base64" {
						v = base64.StdEncoding.EncodeToString([]byte("fixture image"))
					}
					items = append(items, v)
				}
				field := "image_urls"
				if format == "base64" {
					field = "image_base64"
				}
				data[field] = items
				return response(200, map[string]any{"data": data, "metadata": map[string]any{"success_count": n, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
			})
			out, e := a.GenerateImages(owned("alice"), core.MediaRequest{Model: "image-01", Prompt: "scene"}, n, format)
			if e != nil || out.Success != n {
				t.Fatal(out, e)
			}
		}
	}
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		return response(200, map[string]any{"data": map[string]any{"image_urls": []string{"https://media.example/one.jpg"}}, "metadata": map[string]any{"success_count": "1", "failed_count": "1"}, "base_resp": map[string]int{"status_code": 0}}), nil
	})
	out, e := a.GenerateImages(owned("alice"), core.MediaRequest{Model: "image-01", Prompt: "scene"}, 2, "url")
	if e != nil || out.Success != 1 || out.Failed != 1 {
		t.Fatal(out, e)
	}
}
func TestOwnerAndAccountHandles(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("foreign resource reached upstream")
		return nil, nil
	})
	token := a.seal(owned("alice"), handle{ID: "123", Kind: "h3", Model: "MiniMax-H3"})
	for _, value := range []string{"123", token + "x", strings.Replace(token, "minimax:", "other:", 1)} {
		if _, e := a.GetTask(owned("alice"), value); e == nil {
			t.Fatal("accepted invalid handle")
		}
	}
	if _, e := a.GetTask(owned("bob"), token); e == nil {
		t.Fatal("cross-owner task leakage")
	}
	b, _ := New("other-key", "", a.client)
	if _, e := b.GetTask(owned("alice"), token); e == nil {
		t.Fatal("cross-account task leakage")
	}
	if _, e := a.CancelOrDeleteTask(owned("bob"), token); e == nil {
		t.Fatal("cross-owner delete")
	}
}
func TestUsageAndMetering(t *testing.T) {
	usage, e := normalizeH3Usage(json.RawMessage(`{"output_seconds":5,"input_seconds":4,"input_image_count":6,"input_audio_seconds":2,"prompt_tokens":90,"completion_tokens":10,"total_tokens":100}`), "MiniMax-H3-Max")
	if e != nil {
		t.Fatal(e)
	}
	a := fixture(t, nil)
	m, _ := modelFor("MiniMax-H3-Max")
	r := core.MediaRequest{Model: m.ID, Prompt: "scene"}
	quote, _, e := a.Quote(context.Background(), m, r)
	if e != nil || !quote.MeterUsage(*usage) || *quote.MeteredUSD != 1268000 {
		t.Fatal(quote, e)
	}
	partial, _ := normalizeH3Usage(json.RawMessage(`{"output_seconds":5}`), m.ID)
	if quote.MeterUsage(*partial) {
		t.Fatal("missing usage treated as zero")
	}
	if _, e := normalizeH3Usage(json.RawMessage(`{"input_seconds":-1}`), m.ID); e == nil {
		t.Fatal("negative usage")
	}
	if !reflect.DeepEqual(usage.Raw, json.RawMessage(`{"output_seconds":5,"input_seconds":4,"input_image_count":6,"input_audio_seconds":2,"prompt_tokens":90,"completion_tokens":10,"total_tokens":100}`)) {
		t.Fatal("raw usage lost")
	}
}

func coreMoney(n int) money.USD { return money.USD(n) }

func TestReviewOptionalLegacyPromptsAndActualTextMode(t *testing.T) {
	a := fixture(t, nil)
	for _, id := range []string{"MiniMax-Hailuo-2.3", "MiniMax-Hailuo-02"} {
		m, _ := modelFor(id)
		for _, mode := range m.ReferenceModes {
			r := requestFor(m, mode)
			r.Prompt = ""
			e := a.ValidateRequest(context.Background(), r)
			if mode.ID == "none" && e == nil || mode.ID != "none" && e != nil {
				t.Fatal(id, mode.ID, e)
			}
		}
	}
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{ReferenceMode: "references"}}
	op, _ := operationFor(r.Model)
	p, e := requestBody(op, r)
	if e != nil || p["ratio"] != "16:9" {
		t.Fatal(p, e)
	}
	r.Options.Ratio = "adaptive"
	if e := a.ValidateRequest(context.Background(), r); e == nil {
		t.Fatal("empty reference mode sent adaptive text-only content")
	}
}
func TestReviewMixedImageEncodingRejected(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		return response(200, map[string]any{"data": map[string]any{"image_urls": []string{"https://media.example/out.jpg"}, "image_base64": []string{"aGVsbG8="}}, "metadata": map[string]int{"success_count": 1, "failed_count": 0}, "base_resp": map[string]int{"status_code": 0}}), nil
	})
	if _, e := a.Generate(owned("alice"), core.MediaRequest{Model: "image-01", Prompt: "scene"}); e == nil {
		t.Fatal("mixed image encoding accepted")
	}
}

func TestPublicNormalizationInfersH3FrameDefaults(t *testing.T) {
	a := fixture(t, nil)
	m, _ := modelFor("MiniMax-H3")
	for count := 1; count <= 2; count++ {
		o := core.MediaOptions{}
		for i := 0; i < count; i++ {
			o.ImageURLs = append(o.ImageURLs, "https://media.example/image.png")
		}
		n, e := core.NormalizeModelOptions(m, o)
		if e != nil {
			t.Fatal(e)
		}
		r := core.MediaRequest{Model: m.ID, Prompt: "Animate", Options: n}
		if e = a.ValidateRequest(context.Background(), r); e != nil {
			t.Fatal(count, n, e)
		}
		op, _ := operationFor(m.ID)
		p, e := requestBody(op, r)
		if e != nil || p["ratio"] != "adaptive" {
			t.Fatal(count, p, e)
		}
	}
}

func TestProtectedOperationsRequireHostScope(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unscoped operation reached provider")
		return nil, nil
	})
	if _, e := a.Generate(identity.WithOwnerID(context.Background(), "alice"), core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"}); e == nil {
		t.Fatal("missing host scope accepted")
	}
	if _, e := a.UploadFile(identity.WithOwnerID(context.Background(), "alice"), "image.png", "image/png", strings.NewReader("x")); e == nil {
		t.Fatal("unscoped upload accepted")
	}
}

func TestEveryDescriptorSurvivesSharedAdmission(t *testing.T) {
	a := fixture(t, nil)
	for _, m := range catalog() {
		if m.ID == Regeneration {
			continue
		} // Dedicated capability-bearing source-task contract test.
		for _, mode := range m.ReferenceModes {
			t.Run(m.ID+"/"+mode.ID, func(t *testing.T) {
				r := requestFor(m, mode)
				normalized, e := core.NormalizeModelOptions(m, r.Options)
				if e != nil {
					t.Fatal("shared admission rejected catalogue contract", e)
				}
				r.Options = normalized
				if e = a.ValidateRequest(context.Background(), r); e != nil {
					t.Fatal("native validation disagrees with normalized metadata", e)
				}
				q, _, e := a.Quote(context.Background(), m, r)
				if m.Pricing.Known {
					if e != nil || q.ExpectedUSD <= 0 {
						t.Fatal("known tariff lacks valid quote", q, e)
					}
				} else if !errors.Is(e, core.ErrPricingUnavailable) {
					t.Fatal("unknown tariff was invented", q, e)
				}
			})
		}
	}
}
func TestImageArtifactCountMatchesQuoteAndSafetyUsage(t *testing.T) {
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		p := body(t, r)
		if p["n"] != float64(3) {
			t.Fatal(p)
		}
		return response(200, map[string]any{"data": map[string]any{"image_urls": []string{"https://media.example/one.jpg", "https://media.example/two.jpg"}}, "metadata": map[string]int{"success_count": 2, "failed_count": 1}, "base_resp": map[string]int{"status_code": 0}}), nil
	})
	r := core.MediaRequest{Model: "image-01", Prompt: "scene", Options: core.MediaOptions{OutputCount: 3}}
	m, _ := modelFor(r.Model)
	q, _, e := a.Quote(context.Background(), m, r)
	if e != nil || q.ExpectedUSD != 10500 {
		t.Fatal(q, e)
	}
	out, e := a.Generate(owned("alice"), r)
	if e != nil || len(out.Artifacts) != 2 || out.Artifacts[0].ID == out.Artifacts[1].ID {
		t.Fatal(out, e)
	}
	if !q.MeterUsage(*out.Usage) || *q.MeteredUSD != 7000 {
		t.Fatal("generated image count not metered", q)
	}
}

func TestSharedHostConnectionScopeIsConcreteAndServerOnly(t *testing.T) {
	a, e := New("fixture-key", "", &http.Client{Transport: transport(func(*http.Request) (*http.Response, error) {
		return response(200, map[string]string{"task_id": "123"}), nil
	})})
	if e != nil {
		t.Fatal(e)
	}
	scoped := func(id, revision string) context.Context {
		return core.WithProviderConnectionScope(owned("alice"), core.ProviderConnectionScope{ID: id, OwnerID: "provider-owner", Revision: revision})
	}
	r := core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"}
	out, e := a.Generate(scoped("connection-a", "revision-one"), r)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.open(scoped("connection-a", "revision-one"), out.ID); e != nil {
		t.Fatal(e)
	}
	for _, ctx := range []context.Context{scoped("connection-b", "revision-one"), scoped("connection-a", "revision-two"), scoped("connection-a", ""), owned("alice")} {
		if _, e = a.open(ctx, out.ID); e == nil {
			t.Fatal("foreign or incomplete host scope accepted")
		}
	}
	// Pure catalogue quotation does not need an execution capability.
	if _, _, e := a.Quote(context.Background(), core.MediaModel{}, r); e != nil {
		t.Fatal(e)
	}
}

func TestCanonicalOutputCountContract(t *testing.T) {
	a := fixture(t, nil)
	for _, id := range []string{"image-01", "image-01-live"} {
		m, _ := modelFor(id)
		if m.MaxOutputs != 9 {
			t.Fatal(m.ID, m.MaxOutputs)
		}
		for _, count := range []int{1, 9} {
			r := core.MediaRequest{Model: id, Prompt: "scene", Options: core.MediaOptions{OutputCount: count}}
			if m.RequiresImageInput {
				r.Options.ImageURLs = []string{"https://media.example/frame.png"}
			}
			o, e := core.NormalizeModelOptions(m, r.Options)
			if e != nil {
				t.Fatal(id, count, e)
			}
			r.Options = o
			if e = a.ValidateRequest(owned("alice"), r); e != nil {
				t.Fatal(id, count, e)
			}
		}
	}
	for _, r := range []core.MediaRequest{{Model: "image-01", Prompt: "scene", Options: core.MediaOptions{OutputCount: 10}}, {Model: "image-01", Prompt: "scene", Options: core.MediaOptions{OutputCount: -1}}, {Model: "MiniMax-H3", Prompt: "scene", Options: core.MediaOptions{OutputCount: 2}}} {
		if e := a.ValidateRequest(owned("alice"), r); e == nil {
			t.Fatal("invalid canonical count accepted")
		}
	}
}

func TestPerGenerationHTTPTimeoutUsesSharedOverride(t *testing.T) {
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > time.Second {
			t.Fatal("shared timeout override not applied")
		}
		return response(200, map[string]string{"task_id": "123"}), nil
	})
	ctx := httpclient.WithRequestTimeout(owned("alice"), 100*time.Millisecond)
	if _, e := a.Generate(ctx, core.MediaRequest{Model: "MiniMax-H3", Prompt: "scene"}); e != nil {
		t.Fatal(e)
	}
}

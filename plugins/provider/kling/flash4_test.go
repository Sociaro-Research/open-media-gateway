package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func flash4Request(id string) core.MediaRequest {
	r := core.MediaRequest{Model: id, Prompt: "Synthetic offline Flash fixture", Options: core.MediaOptions{Duration: 10}}
	if id == ImageToVideo4Flash {
		r.Options.ImageURLs = []string{"https://cdn.example/first.png"}
	}
	return r
}

func TestFlash4CatalogIndependentConstraints(t *testing.T) {
	for _, id := range []string{TextToVideo4Flash, ImageToVideo4Flash, OmniVideo4Flash} {
		m, ok := modelFor(id)
		if !ok || !reflect.DeepEqual(m.Resolutions, []string{"720p"}) || m.MinDuration != 3 || m.MaxDuration != 20 || m.DefaultDuration != 10 || m.DurationStep != 1 || !m.DurationMayBeInferred || m.NoWatermark {
			t.Fatalf("incorrect Flash descriptor: %#v", m)
		}
		if m.PromptPolicy == nil || m.PromptPolicy.ASCIIBudget != 26000 || m.PromptPolicy.NonASCIIBudget != 11000 {
			t.Fatal("missing weighted prompt policy", m)
		}
		if id == ImageToVideo4Flash && (m.MaxImageInputs != 1 || len(m.Ratios) != 0 || len(m.ReferenceModes) != 1 || m.ReferenceModes[0].ID != "first_frame") {
			t.Fatal("Flash inherited keyframes or ratio control", m)
		}
		if id == OmniVideo4Flash {
			if len(m.ResourceInputSchemas) != 1 || m.ResourceInputSchemas[0].Kind != "element" || m.ReferenceMediaLimits.Video.MaxDuration != 20 || m.ReferenceMediaLimits.Video.MaxTotalDuration != 20 {
				t.Fatal("Flash inherited voice or 30-second input support", m)
			}
		}
	}
	standard, _ := modelFor(TextToVideo4)
	if standard.MaxDuration != 30 || !reflect.DeepEqual(standard.Resolutions, []string{"1080p", "720p"}) {
		t.Fatal("standard 4.0 changed", standard)
	}
}

func TestFlash4ThreeNativeRoutesAndScopedPolling(t *testing.T) {
	for _, tc := range []struct{ id, path string }{
		{TextToVideo4Flash, "/text-to-video/kling-4.0-flash"},
		{ImageToVideo4Flash, "/image-to-video/kling-4.0-flash"},
		{OmniVideo4Flash, "/omni-video/kling-4.0-flash"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			r := flash4Request(tc.id)
			r.TaskID = "gateway_flash_fixture"
			r.Options.Duration = 0
			r.Options.Watermark = ptr(true)
			calls := 0
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 2 {
					if req.Method != "GET" || req.URL.Path != "/tasks" || req.URL.Query().Get("task_ids") != "flash_fixture" {
						t.Fatal(req.URL)
					}
					return response(200, `{"code":0,"data":[{"id":"flash_fixture","status":"processing"}]}`), nil
				}
				if req.Method != "POST" || req.URL.Path != tc.path {
					t.Fatal(req.URL)
				}
				var p struct {
					Prompt   string              `json:"prompt"`
					Contents []map[string]string `json:"contents"`
					Settings map[string]any      `json:"settings"`
					Options  struct {
						ExternalTaskID string `json:"external_task_id"`
						Watermark      struct {
							Enabled bool `json:"enabled"`
						} `json:"watermark_info"`
					} `json:"options"`
				}
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				if p.Settings["resolution"] != "720p" || p.Settings["duration"] != nil || p.Settings["multi_shot"] != nil || p.Options.ExternalTaskID != r.TaskID || !p.Options.Watermark.Enabled {
					t.Fatal(p)
				}
				switch tc.id {
				case TextToVideo4Flash:
					if p.Prompt != r.Prompt || len(p.Contents) != 0 || p.Settings["aspect_ratio"] != "16:9" || p.Settings["audio"] != nil {
						t.Fatal(p)
					}
				case ImageToVideo4Flash:
					if p.Prompt != "" || len(p.Contents) != 2 || p.Contents[0]["type"] != "prompt" || p.Contents[1]["type"] != "first_frame" || p.Contents[1]["id"] != "" || p.Contents[1]["url"] != r.Options.ImageURLs[0] || p.Settings["aspect_ratio"] != nil || p.Settings["audio"] != nil {
						t.Fatal(p)
					}
				case OmniVideo4Flash:
					if len(p.Contents) != 1 || p.Settings["audio"] != "native" || p.Settings["aspect_ratio"] != nil {
						t.Fatal(p)
					}
				}
				return response(200, `{"code":0,"data":{"id":"flash_fixture","status":"submitted"}}`), nil
			})
			ctx := audioFixtureContext()
			task, err := a.Generate(ctx, r)
			if err != nil || !strings.HasPrefix(task.ID, legacyTaskPrefix) {
				t.Fatal(task, err)
			}
			handle, err := a.parseLegacy(ctx, task.ID)
			if err != nil || handle.Operation != tc.id || handle.ID != "flash_fixture" {
				t.Fatal(handle, err)
			}
			if _, err := a.GetTask(context.Background(), task.ID); err == nil {
				t.Fatal("unscoped polling accepted")
			}
			result, err := a.GetTask(ctx, task.ID)
			if err != nil || result.Status != core.ProviderRunning || calls != 2 {
				t.Fatal(result, err, calls)
			}
		})
	}
}

func TestFlash4RejectsUnsupportedInputsBeforeHTTP(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid request reached HTTP"); return nil, nil })
	tests := []struct {
		name, id string
		change   func(*core.MediaRequest)
	}{
		{"missing image", ImageToVideo4Flash, func(r *core.MediaRequest) { r.Options.ImageURLs = nil }},
		{"two images", ImageToVideo4Flash, func(r *core.MediaRequest) {
			r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/last.png")
		}},
		{"image ratio", ImageToVideo4Flash, func(r *core.MediaRequest) { r.Options.Ratio = "16:9" }},
		{"image video", ImageToVideo4Flash, func(r *core.MediaRequest) { omni4AddVideos(r, 1, 3) }},
		{"text image", TextToVideo4Flash, func(r *core.MediaRequest) { r.Options.ImageURLs = []string{"https://cdn.example/i.png"} }},
		{"text video", TextToVideo4Flash, func(r *core.MediaRequest) { omni4AddVideos(r, 1, 3) }},
		{"text adaptive", TextToVideo4Flash, func(r *core.MediaRequest) { r.Options.Ratio = "adaptive" }},
		{"1080p", TextToVideo4Flash, func(r *core.MediaRequest) { r.Options.Resolution = "1080p" }},
		{"4k", OmniVideo4Flash, func(r *core.MediaRequest) { r.Options.Resolution = "4k" }},
		{"21 seconds", TextToVideo4Flash, func(r *core.MediaRequest) { r.Options.Duration = 21 }},
		{"2 seconds", ImageToVideo4Flash, func(r *core.MediaRequest) { r.Options.Duration = 2 }},
		{"audio boolean", TextToVideo4Flash, func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(true) }},
		{"text audio parameter", TextToVideo4Flash, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"audio": "native"} }},
		{"omni off audio", OmniVideo4Flash, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"audio": "off"} }},
		{"original no video", OmniVideo4Flash, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"audio": "original"} }},
		{"original element only", OmniVideo4Flash, func(r *core.MediaRequest) {
			r.Options.Parameters = map[string]any{"audio": "original"}
			omni4AddResource(r, "element", "video_character", ptr(8.0))
		}},
		{"voice", OmniVideo4Flash, func(r *core.MediaRequest) { omni4AddResource(r, "voice", "", nil) }},
		{"last frame", OmniVideo4Flash, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/i.png"}
			r.Options.ReferenceRoles = map[string]string{"image_1": "last_frame"}
		}},
		{"middle frame", OmniVideo4Flash, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/i.png"}
			r.Options.ReferenceRoles = map[string]string{"image_1": "middle_frame"}
		}},
		{"two first frames", OmniVideo4Flash, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/i.png", "https://cdn.example/j.png"}
			r.Options.ReferenceRoles = map[string]string{"image_1": "first_frame", "image_2": "first_frame"}
		}},
		{"two base videos", OmniVideo4Flash, func(r *core.MediaRequest) {
			omni4AddVideos(r, 2, 3)
			r.Options.ReferenceRoles = map[string]string{"video_1": "source", "video_2": "source"}
		}},
		{"input 20.01 seconds", OmniVideo4Flash, func(r *core.MediaRequest) { omni4AddVideos(r, 1, 20.01) }},
		{"combined 21 seconds", OmniVideo4Flash, func(r *core.MediaRequest) { omni4AddVideos(r, 3, 7) }},
		{"eight elements", OmniVideo4Flash, func(r *core.MediaRequest) {
			for i := 0; i < 8; i++ {
				omni4AddResource(r, "element", "multi_image", nil)
			}
		}},
		{"four video elements", OmniVideo4Flash, func(r *core.MediaRequest) {
			for i := 0; i < 4; i++ {
				omni4AddResource(r, "element", "video_character", ptr(3.0))
			}
		}},
		{"six combined videos", OmniVideo4Flash, func(r *core.MediaRequest) {
			omni4AddVideos(r, 3, 3)
			for i := 0; i < 3; i++ {
				omni4AddResource(r, "element", "video_character", ptr(3.0))
			}
		}},
		{"sixteen inputs", OmniVideo4Flash, func(r *core.MediaRequest) {
			for i := 0; i < 10; i++ {
				r.Options.ImageURLs = append(r.Options.ImageURLs, fmt.Sprintf("https://cdn.example/%d.png", i))
			}
			for i := 0; i < 6; i++ {
				omni4AddResource(r, "element", "multi_image", nil)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := flash4Request(tc.id)
			tc.change(&r)
			if _, err := a.Generate(audioFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal("unsupported request accepted", err)
			}
		})
	}
	if _, err := a.Generate(context.Background(), flash4Request(TextToVideo4Flash)); err == nil {
		t.Fatal("unscoped submission accepted")
	}
}

func TestFlash4QuotesUseOnlyFlashTariffsAndKnownDurations(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("quote performed HTTP"); return nil, nil })
	a.kling4Prices["720p"] = 999000
	for _, tc := range []struct {
		name    string
		output  int
		input   float64
		element bool
		want    int64
	}{
		{"text-only", 10, 0, false, 940000},
		{"input shorter", 10, 4, false, 1260000},
		{"input equal", 10, 10, false, 1260000},
		{"input longer", 10, 17, false, 1701000},
		{"element only", 10, 8, true, 1260000},
		{"max duration", 20, 20, false, 2520000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := flash4Request(OmniVideo4Flash)
			r.Options.Duration = tc.output
			if tc.element {
				omni4AddResource(&r, "element", "video_character", ptr(tc.input))
			} else if tc.input > 0 {
				omni4AddVideos(&r, 1, tc.input)
			}
			cost, _, err := a.Quote(audioFixtureContext(), core.MediaModel{}, r)
			if err != nil || int64(cost.ExpectedUSD) != tc.want || cost.ActualUSD != nil || cost.Pricing.AccountSpecific {
				t.Fatal(cost, err)
			}
			if tc.input > 0 && cost.Pricing.DurationBillingRule != "output_plus_max_input_output" {
				t.Fatal(cost)
			}
		})
	}
	for _, id := range []string{TextToVideo4Flash, ImageToVideo4Flash, OmniVideo4Flash} {
		r := flash4Request(id)
		r.Options.Duration = 0
		if _, _, err := a.Quote(audioFixtureContext(), core.MediaModel{}, r); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("inferred output guessed", id, err)
		}
	}
	r := flash4Request(OmniVideo4Flash)
	omni4AddVideos(&r, 1, 3)
	r.Options.ReferenceMediaEvidence = nil
	if _, _, err := a.Quote(audioFixtureContext(), core.MediaModel{}, r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("unknown input treated as zero", err)
	}
	r = flash4Request(OmniVideo4Flash)
	omni4AddResource(&r, "element", "video_character", nil)
	if _, _, err := a.Quote(audioFixtureContext(), core.MediaModel{}, r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("unknown element duration treated as zero", err)
	}
}

func TestFlash4WeightedPromptsAndInclusiveLimits(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("validation performed HTTP"); return nil, nil })
	for _, id := range []string{TextToVideo4Flash, ImageToVideo4Flash, OmniVideo4Flash} {
		r := flash4Request(id)
		r.Prompt = strings.Repeat("E", 13000) + strings.Repeat("界", 5500)
		r.Options.Duration = 20
		if err := a.ValidateRequest(audioFixtureContext(), r); err != nil {
			t.Fatal(id, err)
		}
		r.Prompt += "E"
		if err := a.ValidateRequest(audioFixtureContext(), r); !errors.Is(err, core.ErrPromptTooLong) {
			t.Fatal(id, err)
		}
	}
	r := flash4Request(OmniVideo4Flash)
	omni4AddVideos(&r, 2, 10)
	if err := a.ValidateRequest(audioFixtureContext(), r); err != nil {
		t.Fatal("20 combined seconds rejected", err)
	}
	r.Options.ReferenceMediaEvidence[r.Options.VideoURLs[1]] = core.InputMediaEvidence{Type: "video", Duration: 10.01}
	if err := a.ValidateRequest(audioFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("20.01 combined seconds accepted", err)
	}
}

func TestFlash4PollingSeparatesPhysicalAndBillableDurations(t *testing.T) {
	for _, tc := range []struct {
		name, usage   string
		output, input float64
	}{
		{"provider counters", `{"output_duration":"5","input_duration":"8.03"}`, 5, 8.03},
		{"missing", `{}`, 0, 0},
		{"null", `null`, 0, 0},
		{"malformed", `{"output_duration":"bad","input_duration":"8.03"}`, 0, 0},
		{"object counter", `{"output_duration":{"value":"5"},"input_duration":"8.03"}`, 0, 0},
		{"number counter", `{"output_duration":5,"input_duration":"8.03"}`, 0, 0},
		{"negative", `{"output_duration":"-5"}`, 0, 0},
		{"nan", `{"output_duration":"NaN"}`, 0, 0},
		{"infinite", `{"output_duration":"Inf"}`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Query().Get("task_ids") != "flash_usage" {
					t.Fatal(req.URL)
				}
				return response(200, `{"code":0,"data":[{"id":"flash_usage","status":"succeeded","outputs":[{"id":"clip","type":"video","url":"https://cdn.example/result.mp4","duration":"5.125"}],"usage":`+tc.usage+`}]}`), nil
			})
			ctx := audioFixtureContext()
			handle, err := a.signLegacy(ctx, OmniVideo4Flash, "flash_usage")
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.GetTask(ctx, handle)
			if err != nil || result.Status != core.ProviderSucceeded || len(result.Artifacts) != 1 || result.Artifacts[0].Duration != 5.125 {
				t.Fatal(result, err)
			}
			var usage map[string]any
			if result.Usage != nil {
				if err := json.Unmarshal(result.Usage.Raw, &usage); err != nil {
					t.Fatal(err)
				}
				if result.Usage.EstimatedCost != nil {
					t.Fatal("provider counters became confirmed cost")
				}
			}
			if tc.output == 0 {
				if _, ok := usage["output_video_duration"]; ok {
					t.Fatal("physical duration became billed usage", usage)
				}
			} else if usage["output_video_duration"] != tc.output || usage["input_video_duration"] != tc.input {
				t.Fatal(usage)
			}
		})
	}
}

func TestFlash4OmniReferencesAndOriginalAudioMapping(t *testing.T) {
	r := flash4Request(OmniVideo4Flash)
	r.Prompt = "@Image1 follows @Video1 and @managed1"
	r.Options.ImageURLs = []string{"https://cdn.example/first.png", "https://cdn.example/ref.png"}
	omni4AddVideos(&r, 2, 3)
	omni4AddResource(&r, "element", "video_character", ptr(8.0))
	r.Options.ReferenceRoles = map[string]string{"image_1": "first_frame", "video_2": "source"}
	r.Options.Parameters = map[string]any{"audio": "original"}
	r.Options.Ratio = "21:9"
	r.Options.Watermark = ptr(false)
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		var p struct {
			Contents []map[string]string `json:"contents"`
			Settings map[string]any      `json:"settings"`
			Options  map[string]any      `json:"options"`
		}
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if req.URL.Path != "/omni-video/kling-4.0-flash" || p.Settings["audio"] != "original" || p.Settings["aspect_ratio"] != "21:9" || p.Settings["duration"] != float64(10) {
			t.Fatal(req.URL, p)
		}
		want := []string{"prompt", "first_frame", "refer_image", "feature_video", "base_video", "element"}
		if len(p.Contents) != len(want) {
			t.Fatal(p)
		}
		for i, kind := range want {
			if p.Contents[i]["type"] != kind {
				t.Fatal(i, p)
			}
		}
		if p.Contents[0]["text"] != "@image_1 follows @video_1 and @managed1" || p.Contents[3]["url"] != r.Options.VideoURLs[0] || p.Contents[4]["url"] != r.Options.VideoURLs[1] || p.Contents[5]["id"] != "managed1" || p.Contents[5]["element_id"] != r.Options.ResourceSnapshots[0].RemoteID {
			t.Fatal(p)
		}
		wm, ok := p.Options["watermark_info"].(map[string]any)
		if !ok || wm["enabled"] != false {
			t.Fatal("explicit false watermark lost", p)
		}
		return response(200, `{"code":0,"data":{"id":"flash_refs","status":"submitted"}}`), nil
	})
	if _, err := a.Generate(audioFixtureContext(), r); err != nil {
		t.Fatal(err)
	}
}

func TestFlash4ReviewedSourcesAndMediaBounds(t *testing.T) {
	want := map[string]bool{"api/video/4-0-flash/text-to-video.md": false, "api/video/4-0-flash/image-to-video.md": false, "api/video/4-0-flash/video-omni.md": false, "api/video/4-0-flash/elements.md": false}
	for _, s := range reviewedSources() {
		if _, ok := want[strings.TrimPrefix(s.URL, docsBase)]; ok {
			want[strings.TrimPrefix(s.URL, docsBase)] = true
		}
	}
	for p, seen := range want {
		if !seen {
			t.Fatal("missing Flash source", p)
		}
	}
	m, _ := modelFor(OmniVideo4Flash)
	v := m.ReferenceMediaLimits.Video
	i := m.ReferenceMediaLimits.Image
	if v.MaxBytes != 300000000 || !v.MaxBytesInclusive || v.MinDimension != 700 || v.MaxDimension != 4553 || v.MaxPixels != 8294400 || v.MinFPS != 24 || v.MaxFPS != 60 || v.MinRatio != .4 || v.MaxRatio != 2.5 || !reflect.DeepEqual(v.ContentTypes, []string{"video/mp4", "video/quicktime"}) {
		t.Fatal(v)
	}
	if i.MaxBytes != 30000000 || !i.MaxBytesInclusive || i.MinDimension != 300 || i.MinRatio != .4 || i.MaxRatio != 2.5 || !reflect.DeepEqual(i.ContentTypes, []string{"image/jpeg", "image/png"}) {
		t.Fatal(i)
	}
}

func TestFlash4PreservesBillingDenominationsWithoutConfirmingUSD(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		return response(200, `{"code":0,"data":[{"id":"flash_billing","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/result.mp4","duration":"5.125"}],"usage":{"output_duration":"5","input_duration":"8.03"},"billing":[{"charge_type":"cash","cash_type":"balance","currency":"USD","amount":"0.20"},{"charge_type":"cash","cash_type":"test_balance","currency":"CNY","amount":"0.30"},{"charge_type":"cash","cash_type":"reward_balance","currency":"USD","amount":"0.32089","list_price":"0.82089"},{"charge_type":"unit","package_type":"video","amount":"5.212"},{"charge_type":"cash","cash_type":"unreviewed","currency":"USD","amount":"1"}]}]}`), nil
	})
	ctx := audioFixtureContext()
	handle, err := a.signLegacy(ctx, OmniVideo4Flash, "flash_billing")
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.GetTask(ctx, handle)
	if err != nil || r.Usage == nil || r.Usage.EstimatedCost != nil {
		t.Fatal(r, err)
	}
	var raw struct {
		Billing []map[string]string `json:"billing"`
	}
	if err := json.Unmarshal(r.Usage.Raw, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Billing) != 3 || raw.Billing[0]["cash_type"] != "balance" || raw.Billing[1]["cash_type"] != "test_balance" || raw.Billing[1]["currency"] != "CNY" || raw.Billing[2]["charge_type"] != "unit" {
		t.Fatal(raw)
	}
}

func TestFlash4PricingSourceSelectorsAndFutureRows(t *testing.T) {
	entries := pricingEntries(t, "video")
	for _, id := range []string{TextToVideo4Flash, ImageToVideo4Flash, OmniVideo4Flash} {
		entry, ok := entries[id]
		if !ok {
			t.Fatal("Flash tariff missing", id)
		}
		wantCount := 1
		if id == OmniVideo4Flash {
			wantCount = 2
		}
		if len(entry.Rates) != wantCount {
			t.Fatal(id, entry.Rates)
		}
		for _, rate := range entry.Rates {
			want := int64(94000)
			if rate.VideoInput != nil && *rate.VideoInput {
				want = 63000
			}
			if int64(rate.Price) != want || rate.Resolution != "720p" || rate.Unit != "second" || rate.Per != 1 {
				t.Fatal(rate)
			}
			if id == OmniVideo4Flash && (!rate.VideoInputIncludesResources || rate.VideoInput == nil || *rate.VideoInput && rate.DurationBillingRule != "output_plus_max_input_output") {
				t.Fatal(rate)
			}
		}
	}
	source, text := pricingFixture(t, "video")
	for _, changed := range []string{
		strings.ReplaceAll(text, "Kling 4.0 Flash", "Kling 5.0 Future"),
		strings.Replace(text, "0.6 Units ($0.094) /s | - | -", "0.6 Units ($0.094) /s | 0.9 Units ($0.141) /s | -", 1),
		strings.Replace(text, "| Kling 4.0 Flash | No Input Video", "| Kling 4.0 Flash | Unreviewed Condition", 1),
	} {
		if got, err := interpretPrices(source, changed); err == nil || len(got) != 0 {
			t.Fatal("unreviewed source accepted partially", got, err)
		}
	}
}

func TestNativePollingPreservesLegacyUsageAndBillingPresence(t *testing.T) {
	for _, tc := range []struct {
		name, physical, billed, input string
		want, wantInput               float64
	}{
		{"counter differs from file", "5.125", `"5"`, `"8.03"`, 5.125, 8.03},
		{"invalid optional counter", "5.125", `"invalid"`, `"8.03"`, 5.125, 8.03},
		{"null optional counter", "5.125", `null`, `"8.03"`, 5.125, 8.03},
		{"object optional counter", "5.125", `{"value":"5"}`, `"8.03"`, 5.125, 8.03},
		{"number optional counter", "5.125", `5`, `"8.03"`, 5.125, 8.03},
		{"malformed input counter", "5.125", `"5"`, `"invalid"`, 5.125, 0},
		{"non-string input counter", "5.125", `"5"`, `8.03`, 5.125, 0},
		{"no physical duration", "0", `"5"`, `"8.03"`, 0, 0},
		{"invalid physical duration", "invalid", `"5"`, `"8.03"`, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Path != "/tasks" || req.URL.Query().Get("task_ids") != "legacy_native_fixture" {
					t.Fatal("wrong raw legacy poll", req.URL)
				}
				body := `{"code":0,"data":[{"id":"legacy_native_fixture","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/legacy.mp4","duration":"` + tc.physical + `"}],"usage":{"output_duration":` + tc.billed + `,"input_duration":` + tc.input + `},"billing":[{"charge_type":"cash","cash_type":"balance","currency":"USD","amount":"0.20"},{"charge_type":"cash","cash_type":"test_balance","currency":"CNY","amount":"0.30"},{"charge_type":"cash","cash_type":"reward_balance","currency":"USD","amount":"0.40"},{"charge_type":"unit","package_type":"video","amount":"5.21"}]}]}`
				return response(200, body), nil
			})
			result, err := a.GetTask(context.Background(), "legacy_native_fixture")
			if err != nil || result.Status != core.ProviderSucceeded || len(result.Artifacts) != 1 || result.Artifacts[0].Duration != tc.want {
				t.Fatal(result, err)
			}
			var usage map[string]any
			if result.Usage != nil {
				if err := json.Unmarshal(result.Usage.Raw, &usage); err != nil {
					t.Fatal(err)
				}
				if result.Usage.EstimatedCost != nil {
					t.Fatal("raw counters became confirmed cost")
				}
			}
			if tc.want == 0 {
				for _, key := range []string{"output_video_duration", "input_video_duration", "billing"} {
					if _, ok := usage[key]; ok {
						t.Fatal("legacy billing-presence gate changed", usage)
					}
				}
				return
			}
			if usage["output_video_duration"] != tc.want {
				t.Fatal("legacy physical usage changed", usage)
			}
			if tc.wantInput > 0 {
				if usage["input_video_duration"] != tc.wantInput {
					t.Fatal("valid legacy input counter lost", usage)
				}
			} else if _, ok := usage["input_video_duration"]; ok {
				t.Fatal("malformed legacy input counter accepted", usage)
			}
			billing, ok := usage["billing"].([]any)
			if !ok || len(billing) != 3 {
				t.Fatal("legacy cash counter policy widened", usage)
			}
			for _, row := range billing {
				if row.(map[string]any)["cash_type"] == "reward_balance" {
					t.Fatal("unreviewed reward counter retained")
				}
			}
		})
	}
}

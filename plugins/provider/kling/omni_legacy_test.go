package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func legacyOmniFixture(id string) core.MediaRequest {
	return core.MediaRequest{Model: id, TaskID: "gateway_fixture", Prompt: "Synthetic @image1 and @video1 reference prompt", Options: core.MediaOptions{Resolution: "1080p", Duration: 5, ReferenceRoles: map[string]string{}, Parameters: map[string]any{}}}
}
func legacyOmniImages(r *core.MediaRequest, roles ...string) {
	for i, role := range roles {
		r.Options.ImageURLs = append(r.Options.ImageURLs, fmt.Sprintf("https://cdn.example/image%d.png", i+1))
		r.Options.ReferenceRoles[fmt.Sprintf("image_%d", i+1)] = role
	}
}
func legacyOmniVideo(r *core.MediaRequest, role string, duration float64) {
	r.Options.VideoURLs = []string{"https://cdn.example/reference.mp4"}
	r.Options.ReferenceRoles["video_1"] = role
	r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: duration}}
}
func legacyOmniElements(r *core.MediaRequest, subtypes ...string) {
	for i, subtype := range subtypes {
		alias := fmt.Sprintf("character%d", i+1)
		binding := fmt.Sprintf("b8c27fe8-a688-407a-8ccd-%012d", i+1)
		r.Options.ResourceReferences = append(r.Options.ResourceReferences, core.ResourceReference{BindingID: binding, ReferenceID: alias})
		r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, core.ResourceSnapshot{BindingID: binding, ReferenceID: alias, Kind: "element", Subtype: subtype, RemoteID: fmt.Sprintf("9007199254740993%d", i+1), ConnectionIdentity: "fixture-connection-revision"})
	}
}

func TestLegacyOmniNativeRequests(t *testing.T) {
	tests := []struct {
		name, id     string
		setup        func(*core.MediaRequest)
		wantSettings map[string]any
		types        []string
	}{
		{"3 text", OmniVideo3, func(*core.MediaRequest) {}, map[string]any{"resolution": "1080p", "duration": float64(5), "aspect_ratio": "16:9", "audio": "off", "multi_shot": true}, []string{"prompt"}},
		{"3 keyframe and references", OmniVideo3, func(r *core.MediaRequest) {
			legacyOmniImages(r, "first_frame", "last_frame", "reference")
			legacyOmniElements(r, "multi_image")
			r.Options.Parameters = map[string]any{"audio": "native", "multi_shot": false}
			r.Options.Watermark = ptr(false)
		}, map[string]any{"resolution": "1080p", "duration": float64(5), "audio": "native", "multi_shot": false}, []string{"prompt", "first_frame", "last_frame", "refer_image", "element"}},
		{"3 feature default", OmniVideo3, func(r *core.MediaRequest) { legacyOmniVideo(r, "reference", 15.5) }, map[string]any{"resolution": "1080p", "duration": float64(5), "audio": "off", "multi_shot": true}, []string{"prompt", "feature_video"}},
		{"3 base conditional default", OmniVideo3, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "source", 6)
			r.Options.Parameters["audio"] = "original"
		}, map[string]any{"resolution": "1080p", "duration": float64(5), "audio": "original", "multi_shot": false}, []string{"prompt", "base_video"}},
		{"O1 first and references", OmniVideoO1, func(r *core.MediaRequest) { legacyOmniImages(r, "first_frame", "reference"); r.Options.Duration = 3 }, map[string]any{"resolution": "1080p", "duration": float64(3), "audio": "off"}, []string{"prompt", "first_frame", "refer_image"}},
		{"O1 first feature and element", OmniVideoO1, func(r *core.MediaRequest) {
			legacyOmniImages(r, "first_frame")
			legacyOmniVideo(r, "reference", 10)
			legacyOmniElements(r, "multi_image")
			r.Options.Parameters["audio"] = "original"
		}, map[string]any{"resolution": "1080p", "duration": float64(5), "audio": "original"}, []string{"prompt", "first_frame", "feature_video", "element"}},
		{"O1 base reference", OmniVideoO1, func(r *core.MediaRequest) { legacyOmniImages(r, "reference"); legacyOmniVideo(r, "source", 3) }, map[string]any{"resolution": "1080p", "duration": float64(5), "audio": "off"}, []string{"prompt", "refer_image", "base_video"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := legacyOmniFixture(tc.id)
			tc.setup(&r)
			calls := 0
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				calls++
				route := "/omni-video/kling-o1"
				if tc.id == OmniVideo3 {
					route = "/omni-video/kling-3.0-omni"
				}
				if req.Method != "POST" || req.URL.Path != route {
					t.Fatal(req.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(body["settings"], tc.wantSettings) {
					t.Fatalf("settings %#v; want %#v", body["settings"], tc.wantSettings)
				}
				if body["model_name"] != nil || body["model"] != nil || body["resource_references"] != nil {
					t.Fatal("unreviewed wire fields", body)
				}
				contents := body["contents"].([]any)
				if len(contents) != len(tc.types) {
					t.Fatal(contents)
				}
				for i, wantType := range tc.types {
					c := contents[i].(map[string]any)
					if c["type"] != wantType {
						t.Fatal(contents)
					}
					if wantType == "prompt" {
						if c["text"] != "Synthetic @image_1 and @video_1 reference prompt" {
							t.Fatal(c)
						}
					} else if wantType == "element" {
						if c["element_id"] != "90071992547409931" || c["id"] != "character1" || c["url"] != nil {
							t.Fatal(c)
						}
					} else if c["id"] == nil || c["url"] == nil || c["element_id"] != nil {
						t.Fatal(c)
					}
				}
				options := body["options"].(map[string]any)
				if options["external_task_id"] != "gateway_fixture" || options["callback_url"] != nil {
					t.Fatal(options)
				}
				if r.Options.Watermark != nil && options["watermark_info"].(map[string]any)["enabled"] != false {
					t.Fatal(options)
				}
				return response(200, `{"code":0,"data":{"id":"omni_fixture","status":"submitted"}}`), nil
			}))
			task, err := a.generateLegacyOmni(audioFixtureContext(), r)
			if err != nil {
				t.Fatal(err)
			}
			if task.ID == "" || calls != 1 {
				t.Fatal(task, calls)
			}
		})
	}
}

func TestLegacyOmniConditionalLimits(t *testing.T) {
	tests := []struct {
		name, id string
		valid    bool
		setup    func(*core.MediaRequest)
	}{
		{"3 seven image references", OmniVideo3, true, func(r *core.MediaRequest) {
			legacyOmniImages(r, "reference", "reference", "reference", "reference", "reference", "reference", "reference")
		}},
		{"3 eighth image reference", OmniVideo3, false, func(r *core.MediaRequest) {
			for i := 0; i < 8; i++ {
				legacyOmniImages(r, "reference")
			}
		}},
		{"3 mixed elements budget", OmniVideo3, true, func(r *core.MediaRequest) {
			legacyOmniImages(r, "reference", "reference", "reference")
			legacyOmniElements(r, "multi_image", "video_character", "video_character", "video_character")
		}},
		{"3 mixed elements over image budget", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniImages(r, "reference", "reference", "reference", "reference")
			legacyOmniElements(r, "multi_image", "video_character")
		}},
		{"3 fourth video character", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniElements(r, "video_character", "video_character", "video_character", "video_character")
		}},
		{"3 keyframe fourth element", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniImages(r, "first_frame")
			legacyOmniElements(r, "multi_image", "multi_image", "multi_image", "multi_image")
		}},
		{"3 feature one video character", OmniVideo3, true, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			legacyOmniElements(r, "video_character")
		}},
		{"3 feature two video characters", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			legacyOmniElements(r, "video_character", "video_character")
		}},
		{"3 feature character plus image", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			legacyOmniElements(r, "video_character")
			legacyOmniImages(r, "reference")
		}},
		{"3 feature character plus image element", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			legacyOmniElements(r, "video_character", "multi_image")
		}},
		{"3 feature image-element budget", OmniVideo3, true, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			legacyOmniImages(r, "reference", "reference")
			legacyOmniElements(r, "multi_image", "multi_image")
		}},
		{"3 feature image-element budget exceeded", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			legacyOmniImages(r, "reference", "reference", "reference")
			legacyOmniElements(r, "multi_image", "multi_image")
		}},
		{"3 feature requires multishot", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			r.Options.Parameters["multi_shot"] = false
		}},
		{"3 feature disallows native sound", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			r.Options.Parameters["audio"] = "native"
		}},
		{"3 feature disallows original sound", OmniVideo3, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			r.Options.Parameters["audio"] = "original"
		}},
		{"3 base disallows keyframes", OmniVideo3, false, func(r *core.MediaRequest) { legacyOmniVideo(r, "source", 3); legacyOmniImages(r, "first_frame") }},
		{"3 base disallows multishot", OmniVideo3, false, func(r *core.MediaRequest) { legacyOmniVideo(r, "source", 3); r.Options.Parameters["multi_shot"] = true }},
		{"3 base disallows native sound", OmniVideo3, false, func(r *core.MediaRequest) { legacyOmniVideo(r, "source", 3); r.Options.Parameters["audio"] = "native" }},
		{"3 input duration max inclusive", OmniVideo3, true, func(r *core.MediaRequest) { legacyOmniVideo(r, "reference", 15.5) }},
		{"3 input duration max exceeded", OmniVideo3, false, func(r *core.MediaRequest) { legacyOmniVideo(r, "reference", 15.6) }},
		{"3 4k native", OmniVideo3, true, func(r *core.MediaRequest) {
			r.Options.Resolution = "4k"
			r.Options.Duration = 15
			r.Options.Parameters["audio"] = "native"
		}},
		{"O1 4k unsupported", OmniVideoO1, false, func(r *core.MediaRequest) { r.Options.Resolution = "4k" }},
		{"O1 video element unsupported", OmniVideoO1, false, func(r *core.MediaRequest) { legacyOmniElements(r, "video_character") }},
		{"O1 native sound unsupported", OmniVideoO1, false, func(r *core.MediaRequest) { r.Options.Parameters["audio"] = "native" }},
		{"O1 multishot unsupported", OmniVideoO1, false, func(r *core.MediaRequest) { r.Options.Parameters["multi_shot"] = false }},
		{"O1 first only 3s unsupported", OmniVideoO1, false, func(r *core.MediaRequest) { legacyOmniImages(r, "first_frame"); r.Options.Duration = 3 }},
		{"O1 first only 10s", OmniVideoO1, true, func(r *core.MediaRequest) { legacyOmniImages(r, "first_frame"); r.Options.Duration = 10 }},
		{"O1 first and references 3s", OmniVideoO1, true, func(r *core.MediaRequest) { legacyOmniImages(r, "first_frame", "reference"); r.Options.Duration = 3 }},
		{"O1 first and element does not lift frame duration rule", OmniVideoO1, false, func(r *core.MediaRequest) {
			legacyOmniImages(r, "first_frame")
			legacyOmniElements(r, "multi_image")
			r.Options.Duration = 3
		}},
		{"O1 first last pair", OmniVideoO1, true, func(r *core.MediaRequest) { legacyOmniImages(r, "first_frame", "last_frame"); r.Options.Duration = 10 }},
		{"O1 first last plus image unsupported", OmniVideoO1, false, func(r *core.MediaRequest) { legacyOmniImages(r, "first_frame", "last_frame", "reference") }},
		{"O1 first last plus element unsupported", OmniVideoO1, false, func(r *core.MediaRequest) {
			legacyOmniImages(r, "first_frame", "last_frame")
			legacyOmniElements(r, "multi_image")
		}},
		{"O1 feature plus last unsupported", OmniVideoO1, false, func(r *core.MediaRequest) {
			legacyOmniVideo(r, "reference", 3)
			legacyOmniImages(r, "first_frame", "last_frame")
		}},
		{"O1 input duration max exceeded", OmniVideoO1, false, func(r *core.MediaRequest) { legacyOmniVideo(r, "reference", 10.01) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := legacyOmniFixture(tc.id)
			tc.setup(&r)
			err := new(Adapter).validateLegacyOmniRequest(r)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatalf("accepted invalid conditional request: %v", err)
			}
		})
	}
	for _, id := range []string{OmniVideo3, OmniVideoO1} {
		for _, tc := range []struct {
			name  string
			setup func(*core.MediaRequest)
		}{
			{"last frame alone", func(r *core.MediaRequest) { legacyOmniImages(r, "last_frame") }},
			{"duplicate first frame", func(r *core.MediaRequest) { legacyOmniImages(r, "first_frame", "first_frame") }},
			{"middle frame", func(r *core.MediaRequest) { legacyOmniImages(r, "middle_frame") }},
			{"raw element ID", func(r *core.MediaRequest) { r.Options.Parameters["element_id"] = "remote_id" }},
			{"voice resource", func(r *core.MediaRequest) {
				legacyOmniElements(r, "multi_image")
				r.Options.ResourceSnapshots[0].Kind = "voice"
			}},
			{"unresolved element", func(r *core.MediaRequest) { legacyOmniElements(r, "multi_image"); r.Options.ResourceSnapshots = nil }},
			{"raw voice", func(r *core.MediaRequest) { r.Options.Voice = "oversea_male1" }},
			{"audio file", func(r *core.MediaRequest) { r.Options.AudioURLs = []string{"https://cdn.example/a.wav"} }},
			{"generic audio switch", func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(false) }},
			{"original without video", func(r *core.MediaRequest) { r.Options.Parameters["audio"] = "original" }},
			{"private media URL", func(r *core.MediaRequest) { r.Options.ImageURLs = []string{"https://127.0.0.1/a.png"} }},
			{"nonfinite inspected duration", func(r *core.MediaRequest) { legacyOmniVideo(r, "reference", math.NaN()) }},
			{"two videos", func(r *core.MediaRequest) {
				legacyOmniVideo(r, "reference", 3)
				r.Options.VideoURLs = append(r.Options.VideoURLs, "https://cdn.example/second.mp4")
			}},
		} {
			t.Run(id+" "+tc.name, func(t *testing.T) {
				r := legacyOmniFixture(id)
				tc.setup(&r)
				calls := 0
				a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected call") }))
				if _, err := a.generateLegacyOmni(audioFixtureContext(), r); !errors.Is(err, core.ErrOptionsInvalid) || calls != 0 {
					t.Fatal(err, calls)
				}
			})
		}
	}
}

func TestLegacyOmniModelsAndPromptBoundaries(t *testing.T) {
	for _, m := range legacyOmniModels() {
		if !isLegacyOmniOperation(m.ID) || m.SupportsAudio || !m.RatioMayBeInferred || m.MaxReferenceInputs != 7 {
			t.Fatal(m)
		}
		r := legacyOmniFixture(m.ID)
		r.Prompt = strings.Repeat("雨", m.MaxPromptCharacters)
		normalized, err := core.NormalizeModelOptions(m, r.Options)
		if err != nil {
			t.Fatal(err)
		}
		r.Options = normalized
		if err := new(Adapter).validateLegacyOmniRequest(r); err != nil {
			t.Fatal(err)
		}
		r.Prompt += "雨"
		if !errors.Is(new(Adapter).validateLegacyOmniRequest(r), core.ErrOptionsInvalid) {
			t.Fatal("accepted overlong prompt")
		}
		if m.ReferenceMediaLimits.Video.MinFPS != 24 || m.ReferenceMediaLimits.Video.MaxFPS != 60 || m.ReferenceMediaLimits.Video.MaxBytes != 200<<20 || m.ReferenceMediaLimits.Image.MaxBytes != 50<<20 {
			t.Fatal(m.ReferenceMediaLimits)
		}
	}
}

func TestLegacyOmniOutputOnlyQuotes(t *testing.T) {
	tests := []struct {
		id, res, audio string
		video          bool
		rate           money.USD
	}{
		{OmniVideo3, "720p", "off", false, 84000}, {OmniVideo3, "1080p", "native", false, 140000}, {OmniVideo3, "4k", "native", false, 420000},
		{OmniVideo3, "720p", "off", true, 126000}, {OmniVideo3, "1080p", "off", true, 168000}, {OmniVideo3, "4k", "off", true, 420000},
		{OmniVideoO1, "720p", "off", false, 84000}, {OmniVideoO1, "1080p", "off", false, 112000}, {OmniVideoO1, "720p", "off", true, 126000}, {OmniVideoO1, "1080p", "original", true, 168000},
	}
	for i, tc := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			r := legacyOmniFixture(tc.id)
			r.Options.Resolution = tc.res
			r.Options.Parameters["audio"] = tc.audio
			r.Options.Duration = 7
			if tc.video {
				legacyOmniVideo(&r, "source", 9)
				delete(r.Options.ReferenceMediaEvidence, r.Options.VideoURLs[0])
			}
			cost, _, err := new(Adapter).quoteLegacyOmni(r)
			if err != nil {
				t.Fatal(err)
			}
			if cost.ExpectedUSD != tc.rate*7 || cost.EstimatedUnits != 7 || cost.Pricing.Price != tc.rate || cost.Pricing.BillInputVideo == nil || *cost.Pricing.BillInputVideo || cost.Pricing.DurationBillingRule != "" || cost.ActualUSD != nil {
				t.Fatal(cost)
			}
			if !cost.MeterUsage(*core.NormalizeProviderUsage(map[string]any{"output_video_duration": 7.0, "input_video_duration": 99.0})) || *cost.MeteredUSD != tc.rate*7 {
				t.Fatal("charged input seconds for output-only tariff", cost)
			}
		})
	}
	r := legacyOmniFixture(OmniVideo3)
	r.Options.Duration = 0
	cost, _, err := new(Adapter).quoteLegacyOmni(r)
	if err != nil || cost.EstimatedUnits != 5 {
		t.Fatal("documented default duration quote", cost, err)
	}
	legacyOmniElements(&r, "video_character")
	if _, _, err := new(Adapter).quoteLegacyOmni(r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("guessed video-character tariff", err)
	}
	r.Options.ResourceSnapshots[0].Sources = []core.ResourceSourceEvidence{{MediaType: "video", Duration: ptr(6.0)}}
	if _, _, err := new(Adapter).quoteLegacyOmni(r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("duration does not establish undocumented tariff", err)
	}
}

func TestLegacyOmniUnifiedTaskPolling(t *testing.T) {
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || req.URL.Path != "/tasks" || req.URL.Query().Get("task_ids") != "omni_fixture" {
			t.Fatal(req.URL)
		}
		return response(200, `{"code":0,"data":[{"id":"omni_fixture","status":"succeeded","outputs":[{"type":"video","id":"video_fixture","url":"https://cdn.example/result.mp4","duration":"5"}]}]}`), nil
	}))
	result, err := a.GetTask(context.Background(), "omni_fixture")
	if err != nil || result.Status != core.ProviderSucceeded || result.ResultURL != "https://cdn.example/result.mp4" {
		t.Fatal(result, err)
	}
}

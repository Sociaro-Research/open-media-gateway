package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func motionFixture(id string) core.MediaRequest {
	r := core.MediaRequest{Model: id, Prompt: "Synthetic motion fixture"}
	switch id {
	case ImageToVideo26:
		r.Options.ImageURLs = []string{"https://cdn.example/first.png"}
	case MotionControl3, MotionControl26:
		r.Options.ImageURLs = []string{"https://cdn.example/appearance.png"}
		r.Options.VideoURLs = []string{"https://cdn.example/motion.mp4"}
		r.Options.Parameters = map[string]any{"character_orientation": "video"}
		r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: 8.5}}
	}
	return r
}

func TestMotionOperationsUnifiedContracts(t *testing.T) {
	for _, id := range []string{TextToVideo26, ImageToVideo26, MotionControl3, MotionControl26} {
		t.Run(id, func(t *testing.T) {
			r := motionFixture(id)
			r.TaskID = "gateway_fixture"
			r.Options.Watermark = ptr(true)
			r.Options.Resolution = "1080p"
			isMotion := id == MotionControl3 || id == MotionControl26
			expectedPath := "/text-to-video/kling-2.6"
			if id == ImageToVideo26 {
				expectedPath = "/image-to-video/kling-2.6"
				r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/last.png")
			}
			if !isMotion {
				r.Options.GenerateAudio = ptr(true)
				r.Options.Duration = 10
			}
			if isMotion {
				expectedPath = "/motion-control/kling-3.0"
				if id == MotionControl26 {
					expectedPath = "/motion-control/kling-2.6"
				}
				r.Options.Parameters["audio"] = "off"
			}
			submissions := 0
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				submissions++
				if req.Method != http.MethodPost || req.URL.Path != expectedPath {
					t.Fatal(req.Method, req.URL)
				}
				var p map[string]any
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				settings := p["settings"].(map[string]any)
				if settings["resolution"] != "1080p" || settings["multi_shot"] != nil || p["model_name"] != nil {
					t.Fatal(p)
				}
				options := p["options"].(map[string]any)
				if options["external_task_id"] != r.TaskID || options["watermark_info"].(map[string]any)["enabled"] != true {
					t.Fatal(p)
				}
				if isMotion {
					if settings["character_orientation"] != "video" || settings["audio"] != "off" || settings["duration"] != nil || settings["aspect_ratio"] != nil {
						t.Fatal(p)
					}
					c := p["contents"].([]any)
					if len(c) != 3 || c[1].(map[string]any)["type"] != "image" || c[1].(map[string]any)["url"] != r.Options.ImageURLs[0] || c[2].(map[string]any)["type"] != "video" || c[2].(map[string]any)["url"] != r.Options.VideoURLs[0] {
						t.Fatal(p)
					}
				} else {
					if settings["audio"] != "native" || settings["duration"] != float64(10) {
						t.Fatal(p)
					}
					if id == TextToVideo26 {
						if p["prompt"] != r.Prompt || settings["aspect_ratio"] != "16:9" || p["contents"] != nil {
							t.Fatal(p)
						}
					} else {
						c := p["contents"].([]any)
						if len(c) != 3 || c[0].(map[string]any)["text"] != r.Prompt || c[1].(map[string]any)["type"] != "first_frame" || c[2].(map[string]any)["type"] != "last_frame" || settings["aspect_ratio"] != nil || p["prompt"] != nil {
							t.Fatal(p)
						}
					}
				}
				return response(200, `{"code":0,"data":{"id":"motion_fixture_task","status":"submitted"}}`), nil
			})
			got, err := a.generateMotion(context.Background(), r)
			if err != nil || got.ID != "motion_fixture_task" || submissions != 1 {
				t.Fatal(got, err, submissions)
			}
		})
	}
}

func TestMotionOperationsDefaultsAndCatalog(t *testing.T) {
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		var p motionPayload
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Options != nil || p.Settings.Resolution != "720p" {
			t.Fatal(p)
		}
		if strings.HasPrefix(req.URL.Path, "/motion-control/") {
			if p.Settings.Audio != "original" || p.Settings.CharacterOrientation != "video" || p.Settings.Duration != 0 {
				t.Fatal(p)
			}
		} else if p.Settings.Audio != "off" || p.Settings.Duration != 5 {
			t.Fatal(p)
		}
		return response(200, `{"code":0,"data":{"id":"default_task","status":"processing"}}`), nil
	})
	for _, m := range motionModels() {
		if !isMotionOperation(m.ID) || m.MaxPromptCharacters != 2500 || m.NoWatermark {
			t.Fatal(m)
		}
		r := motionFixture(m.ID)
		if _, err := core.NormalizeModelOptions(m, r.Options); err != nil {
			t.Fatal(m.ID, err)
		}
		if _, err := a.generateMotion(context.Background(), r); err != nil {
			t.Fatal(m.ID, err)
		}
		if m.ID == MotionControl3 || m.ID == MotionControl26 {
			v := m.ReferenceMediaLimits.Video
			if !m.DurationFromInput || !m.RequiresVideoInput || v.MaxBytes != 100<<20 || v.MinDuration != 3 || v.MaxDuration != 30 || v.MinDimension != 340 || v.MaxDimension != 3850 || m.SupportsAudio {
				t.Fatal(m)
			}
		}
	}
	if isMotionOperation("kling-2.6") {
		t.Fatal("bare upstream versions must not become operation aliases")
	}
}

func TestMotionOperationsRejectUnsupportedInputs(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("validation must be local"); return nil, nil })
	for _, id := range []string{TextToVideo26, ImageToVideo26, MotionControl3, MotionControl26} {
		cases := map[string]func(*core.MediaRequest){
			"overlong prompt":    func(r *core.MediaRequest) { r.Prompt = strings.Repeat("界", 2501) },
			"empty prompt":       func(r *core.MediaRequest) { r.Prompt = " " },
			"invalid resolution": func(r *core.MediaRequest) { r.Options.Resolution = "4k" },
			"negative prompt":    func(r *core.MediaRequest) { r.Options.NegativePrompt = "ignored" },
			"audio url":          func(r *core.MediaRequest) { r.Options.AudioURLs = []string{"https://cdn.example/voice.mp3"} },
			"unchecked voice":    func(r *core.MediaRequest) { r.Options.Voice = "remote_voice_id" },
			"raw parameter":      func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"voice_id": "remote_voice_id"} },
			"output count":       func(r *core.MediaRequest) { r.Options.OutputCount = 2 },
		}
		if id == MotionControl3 || id == MotionControl26 {
			cases["missing orientation"] = func(r *core.MediaRequest) { r.Options.Parameters = nil }
			cases["invalid orientation"] = func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"character_orientation": "auto"} }
			cases["native audio"] = func(r *core.MediaRequest) {
				r.Options.Parameters = map[string]any{"character_orientation": "video", "audio": "native"}
			}
			cases["audio switch"] = func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(false) }
			cases["fixed output duration"] = func(r *core.MediaRequest) { r.Options.Duration = 5 }
			cases["missing image"] = func(r *core.MediaRequest) { r.Options.ImageURLs = nil }
			cases["missing video"] = func(r *core.MediaRequest) { r.Options.VideoURLs = nil }
			cases["extra image"] = func(r *core.MediaRequest) {
				r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/extra.png")
			}
			cases["private video"] = func(r *core.MediaRequest) { r.Options.VideoURLs = []string{"https://127.0.0.1/motion.mp4"} }
		} else {
			cases["invalid duration"] = func(r *core.MediaRequest) { r.Options.Duration = 6 }
			cases["native audio at 720p"] = func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(true) }
			cases["video input"] = func(r *core.MediaRequest) { r.Options.VideoURLs = []string{"https://cdn.example/motion.mp4"} }
			if id == ImageToVideo26 {
				cases["first last at 720p"] = func(r *core.MediaRequest) {
					r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/last.png")
				}
				cases["last only"] = func(r *core.MediaRequest) { r.Options.ReferenceRoles = map[string]string{"image_1": "last_frame"} }
				cases["image ratio"] = func(r *core.MediaRequest) { r.Options.Ratio = "16:9" }
				cases["missing first frame"] = func(r *core.MediaRequest) { r.Options.ImageURLs = nil }
			} else {
				cases["image input"] = func(r *core.MediaRequest) { r.Options.ImageURLs = []string{"https://cdn.example/first.png"} }
			}
		}
		for name, mutate := range cases {
			t.Run(id+"/"+name, func(t *testing.T) {
				r := motionFixture(id)
				mutate(&r)
				if _, err := a.generateMotion(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal(err)
				}
				if _, _, err := a.quoteMotion(r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestMotionOperationsInspectedDuration(t *testing.T) {
	a := adapter(t, nil)
	for _, id := range []string{MotionControl3, MotionControl26} {
		for _, orientation := range []string{"image", "video"} {
			for _, d := range []float64{0, 2.9, 3, 10, 10.1, 30, 30.1, math.NaN(), math.Inf(1)} {
				r := motionFixture(id)
				r.Options.Parameters["character_orientation"] = orientation
				r.Options.ReferenceMediaEvidence[r.Options.VideoURLs[0]] = core.InputMediaEvidence{Type: "video", Duration: d}
				maximum := 30.0
				if orientation == "image" {
					maximum = 10
				}
				valid := d >= 3 && d <= maximum
				if err := a.validateMotionRequest(r); (err == nil) != valid {
					t.Fatal(id, orientation, d, err)
				}
			}
		}
		r := motionFixture(id)
		r.Options.ReferenceMediaEvidence = nil
		if err := a.validateMotionRequest(r); err != nil {
			t.Fatal("unknown duration is deferred to inspection/provider", err)
		}
		if _, _, err := a.quoteMotion(r); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("unknown duration must not produce a zero estimate", err)
		}
	}
}

func TestMotionOperationsPublishedPrices(t *testing.T) {
	a := adapter(t, nil)
	for _, tc := range []struct {
		id, res string
		audio   bool
		rate    money.USD
	}{
		{TextToVideo26, "720p", false, 42000}, {TextToVideo26, "1080p", false, 70000}, {TextToVideo26, "1080p", true, 140000},
		{ImageToVideo26, "720p", false, 42000}, {ImageToVideo26, "1080p", false, 70000}, {ImageToVideo26, "1080p", true, 140000},
		{MotionControl3, "720p", false, 126000}, {MotionControl3, "1080p", false, 168000}, {MotionControl26, "720p", false, 70000}, {MotionControl26, "1080p", false, 112000},
	} {
		r := motionFixture(tc.id)
		r.Options.Resolution = tc.res
		quantity := 5.0
		if tc.id == MotionControl3 || tc.id == MotionControl26 {
			quantity = 8.5
		} else {
			r.Options.GenerateAudio = ptr(tc.audio)
		}
		got, warning, err := a.quoteMotion(r)
		want, _ := tc.rate.Multiply(quantity, 1)
		if err != nil || warning != "" || got.ExpectedUSD != want || got.EstimatedUnits != quantity || got.Pricing.Price != tc.rate || got.Pricing.Unit != "second" || got.Pricing.AccountSpecific || got.Status != "estimated" {
			t.Fatal(tc, got, warning, err)
		}
	}
}

func attachMotionResource(r *core.MediaRequest, kind, alias, remote string) {
	binding := fmt.Sprintf("00000000-0000-4000-8000-%012d", len(r.Options.ResourceReferences)+1)
	r.Options.ResourceReferences = append(r.Options.ResourceReferences, core.ResourceReference{BindingID: binding, ReferenceID: alias})
	r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, core.ResourceSnapshot{BindingID: binding, ReferenceID: alias, Kind: kind, RemoteID: remote, ConnectionIdentity: "fixture_connection_revision", Subtype: "multi_image"})
}

func TestMotionOperationsManagedResources(t *testing.T) {
	for _, kind := range []string{"voice", "element"} {
		t.Run(kind, func(t *testing.T) {
			r := motionFixture(ImageToVideo26)
			r.Options.Resolution = "1080p"
			r.Options.GenerateAudio = ptr(true)
			if kind == "element" {
				r = motionFixture(MotionControl3)
				r.Options.ImageURLs = nil
				r.Options.ReferenceMode = "element_motion"
			}
			attachMotionResource(&r, kind, kind+"_1", "900719925474099312345")
			if kind == "voice" {
				attachMotionResource(&r, kind, "voice_2", "900719925474099312346")
			}
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				var p map[string]any
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Fatal(err)
				}
				contents := p["contents"].([]any)
				found := 0
				for _, raw := range contents {
					c := raw.(map[string]any)
					if c["type"] == kind {
						expected := r.Options.ResourceSnapshots[found]
						if c[kind+"_id"] != expected.RemoteID || c["id"] != expected.ReferenceID || c["url"] != nil {
							t.Fatal(c)
						}
						found++
					}
				}
				if found != len(r.Options.ResourceSnapshots) {
					t.Fatal(p)
				}
				return response(200, `{"code":0,"data":{"id":"managed_motion_task","status":"submitted"}}`), nil
			})
			if _, err := a.generateMotion(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			cost, _, err := a.quoteMotion(r)
			rate := money.USD(168000)
			quantity := 5.0
			if kind == "element" {
				rate = 126000
				quantity = 8.5
			}
			expected, _ := rate.Multiply(quantity, 1)
			if err != nil || cost.ExpectedUSD != expected || cost.Pricing.Price != rate {
				t.Fatal(cost, err)
			}
			for name, mutate := range map[string]func(*core.MediaRequest){
				"unresolved binding": func(b *core.MediaRequest) { b.Options.ResourceSnapshots = nil },
				"unbound snapshot":   func(b *core.MediaRequest) { b.Options.ResourceReferences = nil },
				"wrong kind":         func(b *core.MediaRequest) { b.Options.ResourceSnapshots[0].Kind = "unknown" },
				"no connection":      func(b *core.MediaRequest) { b.Options.ResourceSnapshots[0].ConnectionIdentity = "" },
				"mismatched alias":   func(b *core.MediaRequest) { b.Options.ResourceSnapshots[0].ReferenceID = "wrong_alias" },
			} {
				t.Run(name, func(t *testing.T) {
					b := r
					b.Options.ResourceReferences = append([]core.ResourceReference(nil), r.Options.ResourceReferences...)
					b.Options.ResourceSnapshots = append([]core.ResourceSnapshot(nil), r.Options.ResourceSnapshots...)
					mutate(&b)
					if err := a.validateMotionRequest(b); !errors.Is(err, core.ErrOptionsInvalid) {
						t.Fatal(err)
					}
				})
			}
			if kind == "voice" {
				r.Options.GenerateAudio = ptr(false)
				if err := a.validateMotionRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal(err)
				}
				r.Options.GenerateAudio = ptr(true)
				attachMotionResource(&r, kind, "voice_3", "900719925474099312347")
				if err := a.validateMotionRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal(err)
				}
			} else {
				r.Options.ImageURLs = []string{"https://cdn.example/appearance.png"}
				r.Options.ReferenceMode = "motion"
				if _, err := a.generateMotion(context.Background(), r); err != nil {
					t.Fatal(err)
				}
				r.Options.ImageURLs = nil
				r.Options.ReferenceMode = "element_motion"
				r.Options.Parameters["character_orientation"] = "image"
				if err := a.validateMotionRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal(err)
				}
				r.Options.Parameters["character_orientation"] = "video"
				r.Model = MotionControl26
				if err := a.validateMotionRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal(err)
				}
			}
		})
	}
}

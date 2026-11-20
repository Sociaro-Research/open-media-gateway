package kling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerkit"
)

func TestKling4DocumentedRoutesDefaultsAndKeyframes(t *testing.T) {
	for _, count := range []int{0, 1, 2, 3, 10} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			r := core.MediaRequest{Model: TextToVideo4, Prompt: "Synthetic fixture"}
			path := "/text-to-video/kling-4.0"
			if count > 0 {
				r.Model = ImageToVideo4
				r.Prompt = "Preserve @image_1, then end on @image_" + fmt.Sprint(count) + ". Literal <tag> stays text."
				path = "/image-to-video/kling-4.0"
				for i := 0; i < count; i++ {
					r.Options.ImageURLs = append(r.Options.ImageURLs, fmt.Sprintf("https://cdn.example/frame-%d.png", i))
				}
			}
			a := adapter(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.URL.Path != path || req.Header.Get("Authorization") != "Bearer fixture-secret" {
					t.Fatal(req.URL)
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				s := body["settings"].(map[string]any)
				if s["duration"] != float64(10) || s["resolution"] != "1080p" || s["audio"] != nil || s["multi_shot"] != nil || body["model_name"] != nil {
					t.Fatal(body)
				}
				if count == 0 {
					if body["prompt"] != r.Prompt || s["aspect_ratio"] != "16:9" {
						t.Fatal(body)
					}
				} else {
					contents := body["contents"].([]any)
					if contents[0].(map[string]any)["text"] != r.Prompt || len(contents) != count+1 || s["aspect_ratio"] != nil {
						t.Fatal(body)
					}
					for i := 0; i < count; i++ {
						frame := contents[i+1].(map[string]any)
						role := "middle_frame"
						if i == 0 {
							role = "first_frame"
						} else if i == count-1 {
							role = "last_frame"
						}
						if frame["type"] != role || frame["id"] != fmt.Sprintf("image_%d", i+1) || frame["url"] != r.Options.ImageURLs[i] {
							t.Fatal(frame)
						}
					}
				}
				return response(200, `{"code":0,"data":{"id":"kling4_task","status":"submitted"}}`), nil
			})
			task, err := a.Generate(context.Background(), r)
			if err != nil || task.ID != "kling4_task" {
				t.Fatal(task, err)
			}
			poller := adapter(t, func(req *http.Request) (*http.Response, error) {
				if req.Method != "GET" || req.URL.Path != "/tasks" || req.URL.Query().Get("task_ids") != task.ID {
					t.Fatal(req.URL)
				}
				return response(200, `{"code":0,"data":[{"id":"kling4_task","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/result.mp4","duration":"10"}]}]}`), nil
			})
			result, err := poller.GetTask(context.Background(), task.ID)
			if err != nil || result.Status != core.ProviderSucceeded {
				t.Fatal(result, err)
			}
		})
	}
}

func TestKling4ValidationPublishedPricesAndAccountOverrides(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("validation and quotes must not contact Kling")
		return nil, nil
	})
	r := core.MediaRequest{Model: TextToVideo4, Prompt: strings.Repeat("猫", 10000), Options: core.MediaOptions{Duration: 30, Resolution: "720p", Ratio: "21:9"}}
	if err := a.ValidateRequest(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if cost, _, err := a.Quote(context.Background(), core.MediaModel{}, r); err != nil || cost.ExpectedUSD != 4710000 || cost.Pricing.AccountSpecific {
		t.Fatal("published no-input-video tariff missing", cost, err)
	}
	for _, m := range a.models() {
		if isKling4(m.ID) && (!m.Pricing.Known || m.Pricing.ReferencePriceUSD != 2.35) {
			t.Fatal("published reference tariff missing")
		}
	}
	for name, mutate := range map[string]func(*core.MediaRequest){
		"unsupported resolution":           func(r *core.MediaRequest) { r.Options.Resolution = "8k" },
		"long duration":                    func(r *core.MediaRequest) { r.Options.Duration = 31 },
		"audio switch absent from API":     func(r *core.MediaRequest) { r.Options.GenerateAudio = ptr(false) },
		"multi shot field absent from API": func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"multi_shot": true} },
		"Gateway prompt cap":               func(r *core.MediaRequest) { r.Prompt = strings.Repeat("a", 10001) },
		"too many frames": func(r *core.MediaRequest) {
			r.Model = ImageToVideo4
			r.Options.Ratio = ""
			r.Options.ImageURLs = make([]string, 11)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := r
			mutate(&bad)
			if _, err := a.Generate(context.Background(), bad); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
	// Synthetic account overrides take precedence over public list prices.
	if err := a.setPrices(providerkit.Credentials{Extra: map[string]string{"kling4_720p_price": "0.200001", "kling4_1080p_price": "0.300001"}}); err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{TextToVideo4, ImageToVideo4} {
		r.Model = model
		r.Options.Ratio = ""
		if model == ImageToVideo4 {
			r.Options.ImageURLs = []string{"https://cdn.example/first.png"}
		}
		cost, _, err := a.Quote(context.Background(), core.MediaModel{}, r)
		if err != nil || cost.ExpectedUSD != 6000030 || cost.ActualUSD != nil || !cost.Pricing.AccountSpecific || !strings.Contains(cost.Pricing.Source, "Operator-confirmed") {
			t.Fatal(cost, err)
		}
		r.Options.Resolution = "1080p"
		cost, _, err = a.Quote(context.Background(), core.MediaModel{}, r)
		if err != nil || cost.ExpectedUSD != 9000030 {
			t.Fatal(cost, err)
		}
		r.Options.Resolution = "720p"
	}
	for _, raw := range []string{"0", "-0.1", "NaN", "secret-invalid-price"} {
		_, err := Definition().Factory(providerkit.Credentials{APIKey: "fixture-secret", Extra: map[string]string{"kling4_720p_price": raw}}, nil, nil)
		if err == nil || strings.Contains(err.Error(), raw) && raw == "secret-invalid-price" {
			t.Fatal("invalid or leaked tariff", err)
		}
	}
}

func TestReferencePromptMetadataMatchesReviewedKlingRoutes(t *testing.T) {
	for _, model := range catalog() {
		if model.ID == OmniVideo4 || model.ID == OmniVideo4Flash || model.ID == OmniVideo3 || model.ID == OmniVideoO1 {
			if model.ReferencePromptSyntax != "kling-id" || strings.Join(model.ReferencePromptTypes, ",") != "image,video" {
				t.Fatal(model)
			}
		} else if model.ID == ImageToVideo4 {
			if model.ReferencePromptSyntax != "kling-id" || len(model.ReferencePromptTypes) != 1 || model.ReferencePromptTypes[0] != "image" {
				t.Fatalf("Kling 4 frame IDs missing from descriptor: %+v", model)
			}
		} else if model.ReferencePromptSyntax != "" || len(model.ReferencePromptTypes) != 0 {
			t.Fatalf("unreviewed reference grammar enabled: %s", model.ID)
		}
	}
	if Definition().Profiles != nil {
		t.Fatal("Kling routes must not enable generic manual model overrides")
	}
}

func TestKling4DynamicRangeRequestAndBilling(t *testing.T) {
	for _, id := range []string{TextToVideo4, ImageToVideo4} {
		for _, tc := range []struct {
			res, dynamicRange string
			micros            int64
		}{
			{"", "", 235000}, {"720p", "sdr", 157000}, {"1080p", "hdr", 282000},
			{"4k", "", 549000}, {"4k", "sdr", 549000}, {"4k", "hdr", 627000},
		} {
			t.Run(id+"/"+tc.res+"/"+tc.dynamicRange, func(t *testing.T) {
				r := core.MediaRequest{Model: id, Prompt: "Test video", Options: core.MediaOptions{Resolution: tc.res, Duration: 5}}
				path := "/text-to-video/kling-4.0"
				if id == ImageToVideo4 {
					path = "/image-to-video/kling-4.0"
					r.Options.ImageURLs = []string{"https://cdn.example/first.png"}
				}
				if tc.dynamicRange != "" {
					r.Options.Parameters = map[string]any{"dynamic_range": tc.dynamicRange}
				}
				a := adapter(t, func(req *http.Request) (*http.Response, error) {
					if req.Method == "GET" {
						return response(200, `{"code":0,"data":[{"id":"video4","status":"succeeded","usage":{"output_duration":"4","input_duration":"99.50"},"outputs":[{"type":"video","url":"https://cdn.example/result.mp4","duration":"4.7"}]}]}`), nil
					}
					var body struct{ Settings map[string]any }
					if req.URL.Path != path || json.NewDecoder(req.Body).Decode(&body) != nil {
						t.Fatal(req.URL.Path, body)
					}
					res := tc.res
					if res == "" {
						res = "1080p"
					}
					value, present := body.Settings["dynamic_range"]
					if body.Settings["resolution"] != res || present != (tc.dynamicRange != "") || present && value != tc.dynamicRange {
						t.Fatal(body)
					}
					return response(200, `{"code":0,"data":{"id":"video4","status":"submitted"}}`), nil
				})
				ctx := context.Background()
				cost, _, err := a.Quote(ctx, core.MediaModel{}, r)
				if err != nil || int64(cost.ExpectedUSD) != 5*tc.micros {
					t.Fatal(cost, err)
				}
				task, err := a.Generate(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				result, err := a.GetTask(core.WithProviderPollRequest(ctx, r), task.ID)
				if err != nil || len(result.Artifacts) != 1 || result.Artifacts[0].Duration != 4.7 || result.Usage == nil || !cost.MeterUsage(*result.Usage) || *cost.BilledUnits != 4 || int64(*cost.MeteredUSD) != 4*tc.micros || cost.ActualUSD != nil {
					t.Fatal(result, cost, err)
				}
			})
		}
	}
}

func TestKling4MissingBillableOutputPreservesMedia(t *testing.T) {
	for _, usage := range []string{`{}`, `{"output_duration":"bad"}`, `{"output_duration":"0"}`} {
		a := adapter(t, func(*http.Request) (*http.Response, error) {
			return response(200, fmt.Sprintf(`{"code":0,"data":[{"id":"video4","status":"succeeded","usage":%s,"outputs":[{"type":"video","url":"https://cdn.example/result.mp4","duration":"5"}]}]}`, usage)), nil
		})
		r := core.MediaRequest{Model: TextToVideo4, Prompt: "Test", Options: core.MediaOptions{Parameters: map[string]any{"dynamic_range": "hdr"}}}
		cost, _, err := a.Quote(context.Background(), core.MediaModel{}, r)
		if err != nil {
			t.Fatal(err)
		}
		result, err := a.GetTask(core.WithProviderPollRequest(context.Background(), r), "video4")
		if err != nil || result.Status != core.ProviderSucceeded || result.Artifacts[0].Duration != 5 || result.Usage != nil && cost.MeterUsage(*result.Usage) || cost.MeteredUSD != nil {
			t.Fatal(result, cost, err)
		}
	}
}

func TestKling4RejectsUnsupportedRangeBeforeDispatch(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid settings reached provider")
		return nil, nil
	})
	for _, tc := range []struct{ id, res, dr string }{
		{TextToVideo4, "720p", "hdr"}, {ImageToVideo4, "720p", "hdr"}, {TextToVideo4, "4k", "auto"},
		{TextToVideo4Flash, "720p", "hdr"}, {ImageToVideo4Flash, "4k", ""}, {OmniVideo4Flash, "720p", "hdr"},
		{OmniVideo4, "720p", "hdr"}, {OmniVideo4, "4k", "invalid"},
	} {
		r := core.MediaRequest{Model: tc.id, Prompt: "Test", Options: core.MediaOptions{Resolution: tc.res, Duration: 5}}
		if tc.id == ImageToVideo4 || tc.id == ImageToVideo4Flash {
			r.Options.ImageURLs = []string{"https://cdn.example/first.png"}
		}
		if tc.dr != "" {
			r.Options.Parameters = map[string]any{"dynamic_range": tc.dr}
		}
		if _, err := a.Generate(pricingProbeContext(), r); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal(tc, err)
		}
		if _, _, err := a.Quote(pricingProbeContext(), core.MediaModel{}, r); !errors.Is(err, core.ErrOptionsInvalid) {
			t.Fatal(tc, err)
		}
	}
}

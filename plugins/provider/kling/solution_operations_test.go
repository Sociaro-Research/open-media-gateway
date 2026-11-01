package kling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func solutionFixtureContext() context.Context {
	return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "fixture-owner"), core.ProviderConnectionScope{ID: "kling--fixture", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
}
func solutionFixtureRequest(id string) core.MediaRequest {
	r := core.MediaRequest{Model: id, TaskID: "synthetic-local-task", Options: core.MediaOptions{}}
	switch id {
	case KlingEffects:
		r.Options.ImageURLs = []string{"https://cdn.example/effect.png"}
		r.Options.Parameters = map[string]any{"effect_scene": "net_the_fish"}
	case ApparelReplicator:
		r.Prompt = "Synthetic apparel description"
		r.Options.ImageURLs = []string{"https://cdn.example/product.webp"}
		r.Options.VideoURLs = []string{"https://cdn.example/source.mp4"}
	case GoodsStudio:
		r.Prompt = "Synthetic product title"
		r.Options.ImageURLs = []string{"https://cdn.example/product.webp"}
		r.Options.Resolution = "720p"
		r.Options.Ratio = "9:16"
		r.Options.Parameters = map[string]any{"duration": "15"}
	case VideoCommerce:
		r.Prompt = "Synthetic speech script"
		r.Options.Parameters = map[string]any{"avatar_id": "avatar_anna_female"}
	case VirtualTryOn:
		r.Options.ImageURLs = []string{"https://cdn.example/product.webp", "https://cdn.example/person.webp"}
		r.Options.ReferenceRoles = map[string]string{"image_2": "person_image"}
	}
	return r
}
func TestSolutionCatalogAndReviewedEffects(t *testing.T) {
	if len(solutionModels()) != 5 || len(effectScenes) != 219 || len(dualImageEffects) != 15 {
		t.Fatal(len(solutionModels()), len(effectScenes), len(dualImageEffects))
	}
	seen := map[string]bool{}
	for _, scene := range effectScenes {
		if seen[scene] {
			t.Fatal(scene)
		}
		seen[scene] = true
		r := solutionFixtureRequest(KlingEffects)
		r.Options.Parameters["effect_scene"] = scene
		if slicesDual := strings.Join(dualImageEffects, ","); strings.Contains(","+slicesDual+",", ","+scene+",") {
			r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/right.png")
		}
		if err := validateSolutionRequest(r); err != nil {
			t.Fatal(scene, err)
		}
	}
	for _, m := range solutionModels() {
		if !isSolutionOperation(m.ID) || solutionPath(m.ID) == "" || m.ReferenceMediaLimits.Image.MinDimension < 300 {
			t.Fatal(m)
		}
		if len(m.ResourceInputSchemas) != 0 {
			t.Fatal("custom resource libraries are not supported by the reviewed solution contracts")
		}
	}
	if isSolutionOperation("clothing_dupe") || isSolutionOperation("invented-solution") {
		t.Fatal("invented aliases")
	}
	apparel := solutionModel(ApparelReplicator)
	v := apparel.ReferenceMediaLimits.Video
	if v.MaxBytes != 100<<20 || v.MinDuration != 5 || v.MaxDuration != 180 || v.MinDimension != 576 || v.MaxDimension != 4553 || v.MaxPixels != 8294400 || v.MinFPS != 24 || v.MaxFPS != 60 || v.MinRatio != .4 || v.MaxRatio != 2 {
		t.Fatal(v)
	}
	limits := solutionModel(VirtualTryOn).ReferenceMediaLimits.Image
	if limits.MinDimension != 301 || limits.MaxDimension != 2048 || limits.MaxBytes != 10<<20 {
		t.Fatal(limits)
	}
}
func TestSolutionNativeSubmissionSchemas(t *testing.T) {
	for _, id := range []string{KlingEffects, ApparelReplicator, GoodsStudio, VideoCommerce, VirtualTryOn} {
		t.Run(id, func(t *testing.T) {
			r := solutionFixtureRequest(id)
			if id != KlingEffects {
				r.Options.Watermark = ptr(false)
			}
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.URL.Path != solutionPath(id) {
					t.Fatalf("%s %s", req.Method, req.URL.Path)
				}
				raw, _ := io.ReadAll(req.Body)
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				if body["parameters"] != nil || body["model_name"] != nil || body["callback_url"] != nil {
					t.Fatal(body)
				}
				if id == KlingEffects {
					if body["effect_scene"] != "net_the_fish" || body["input"].(map[string]any)["image"] != r.Options.ImageURLs[0] || body["contents"] != nil || body["external_task_id"] != r.TaskID {
						t.Fatal(body)
					}
					return response(200, `{"code":0,"data":{"task_id":9007199254740993,"task_status":"submitted"}}`), nil
				}
				options := body["options"].(map[string]any)
				if options["external_task_id"] != r.TaskID || options["callback_url"] != nil {
					t.Fatal(options)
				}
				wm := "watermark"
				if id == ApparelReplicator {
					wm = "watermark_info"
				}
				if options[wm].(map[string]any)["enabled"] != false {
					t.Fatal(options)
				}
				if body["contents"] == nil || body["settings"] == nil {
					t.Fatal(body)
				}
				if id == VirtualTryOn {
					settings := body["settings"].(map[string]any)
					if settings["keep_face"] != true || settings["keep_pose"] != true || settings["keep_background"] != true || settings["resolution"] != nil {
						t.Fatal(settings)
					}
				}
				return response(200, `{"code":0,"data":{"id":9007199254740993,"status":"submitted"}}`), nil
			}))
			if err := validateSolutionRequest(r); err != nil {
				t.Fatal(err)
			}
			task, err := a.generateSolution(solutionFixtureContext(), r)
			if err != nil {
				t.Fatal(err)
			}
			handle, err := a.parseLegacy(solutionFixtureContext(), task.ID)
			if err != nil || handle.Operation != id || handle.ID != "9007199254740993" {
				t.Fatalf("%+v %v", handle, err)
			}
		})
	}
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected call") }))
	if _, err := a.generateSolution(context.Background(), solutionFixtureRequest(GoodsStudio)); err == nil || calls != 0 {
		t.Fatalf("scope check: %d %v", calls, err)
	}
}
func TestSolutionOrderedMediaAndTypedSettings(t *testing.T) {
	r := solutionFixtureRequest(ApparelReplicator)
	r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/model.webp")
	r.Options.ReferenceRoles = map[string]string{"image_2": "model_image"}
	r.Options.AudioURLs = []string{"https://cdn.example/bgm.m4a"}
	r.Options.Voice = "calm_sales_woman"
	r.Options.Resolution = "1080p"
	r.Options.Ratio = "16:9"
	if err := validateSolutionRequest(r); err != nil {
		t.Fatal(err)
	}
	body := solutionNativePayload(r).(solutionPayload)
	types := []string{}
	for _, content := range body.Contents {
		types = append(types, content.Type)
	}
	if !reflect.DeepEqual(types, []string{"product_info", "product_image", "model_image", "source_video", "bgm", "voice"}) || body.Contents[5].VoiceID != "calm_sales_woman" {
		t.Fatal(body)
	}
	r = solutionFixtureRequest(KlingEffects)
	r.Options.Parameters["effect_scene"] = "hug_pro"
	r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/right.png")
	r.Options.ReferenceRoles = map[string]string{"image_1": "left", "image_2": "right"}
	r.Options.ReferenceMode = "dual_image"
	if err := validateSolutionRequest(r); err != nil {
		t.Fatal(err)
	}
	effects := solutionNativePayload(r).(effectsPayload)
	if effects.Input.Image != "" || !reflect.DeepEqual(effects.Input.Images, r.Options.ImageURLs) {
		t.Fatal(effects)
	}
	r = solutionFixtureRequest(VideoCommerce)
	r.Options.Parameters = map[string]any{"goods_title": "Synthetic title", "goods_price": "399 CNY", "goods_target_audience": []any{"Campers", "Walkers"}, "goods_selling_point": []string{"Water resistant", "Lightweight"}, "action_prompt": "Wave", "allow_polish": false, "bgm_enabled": true}
	r.Options.ImageURLs = []string{"https://cdn.example/avatar.png", "https://cdn.example/product.png"}
	r.Options.ReferenceRoles = map[string]string{"image_2": "ref_image"}
	r.Options.Voice = "female_clear_rational"
	if err := validateSolutionRequest(r); err != nil {
		t.Fatal(err)
	}
	body = solutionNativePayload(r).(solutionPayload)
	counts := map[string]int{}
	for _, content := range body.Contents {
		counts[content.Type]++
	}
	if counts["goods_target_audience"] != 2 || counts["goods_selling_point"] != 2 || counts["speech_script"] != 1 || body.Settings.VoiceID != r.Options.Voice || body.Settings.AllowPolish == nil || *body.Settings.AllowPolish || body.Settings.BGMEnabled == nil || !*body.Settings.BGMEnabled || body.Settings.SpeechRate != nil {
		t.Fatal(body)
	}
	r = solutionFixtureRequest(VideoCommerce)
	r.Options.Parameters["speech_rate"] = "0.8"
	if err := validateSolutionRequest(r); err != nil {
		t.Fatal(err)
	}
	body = solutionNativePayload(r).(solutionPayload)
	if body.Settings.SpeechRate == nil || *body.Settings.SpeechRate != .8 {
		t.Fatal(body)
	}
	r = solutionFixtureRequest(VirtualTryOn)
	r.Options.Parameters = map[string]any{"keep_face": false, "keep_pose": false, "keep_background": false}
	body = solutionNativePayload(r).(solutionPayload)
	if *body.Settings.KeepFace || *body.Settings.KeepPose || *body.Settings.KeepBackground {
		t.Fatal(body)
	}
}
func TestSolutionValidationRejectsUnsupportedInputs(t *testing.T) {
	cases := []struct {
		name, id string
		change   func(*core.MediaRequest)
	}{
		{"missing effect", KlingEffects, func(r *core.MediaRequest) { r.Options.Parameters = nil }},
		{"unknown effect", KlingEffects, func(r *core.MediaRequest) { r.Options.Parameters["effect_scene"] = "invented_effect" }},
		{"dual missing second", KlingEffects, func(r *core.MediaRequest) { r.Options.Parameters["effect_scene"] = "hug_pro" }},
		{"single extra image", KlingEffects, func(r *core.MediaRequest) {
			r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/b.png")
		}},
		{"effect watermark undocumented", KlingEffects, func(r *core.MediaRequest) { r.Options.Watermark = ptr(true) }},
		{"effect prompt", KlingEffects, func(r *core.MediaRequest) { r.Prompt = "ignored" }},
		{"effect settings undocumented", KlingEffects, func(r *core.MediaRequest) { r.Options.Duration = 5 }},
		{"private image", VirtualTryOn, func(r *core.MediaRequest) { r.Options.ImageURLs[0] = "https://127.0.0.1/a.png" }},
		{"raw JSON", GoodsStudio, func(r *core.MediaRequest) { r.Options.Parameters["raw"] = map[string]any{"contents": true} }},
		{"goods duration", GoodsStudio, func(r *core.MediaRequest) { r.Options.Parameters["duration"] = "45" }},
		{"goods title length", GoodsStudio, func(r *core.MediaRequest) { r.Prompt = strings.Repeat("界", 201) }},
		{"goods description length", GoodsStudio, func(r *core.MediaRequest) { r.Options.Parameters["goods_description"] = strings.Repeat("界", 2001) }},
		{"goods no resolution", GoodsStudio, func(r *core.MediaRequest) { r.Options.Resolution = "" }},
		{"goods no ratio", GoodsStudio, func(r *core.MediaRequest) { r.Options.Ratio = "" }},
		{"goods unsupported ratio", GoodsStudio, func(r *core.MediaRequest) { r.Options.Ratio = "21:9" }},
		{"goods unsupported seed", GoodsStudio, func(r *core.MediaRequest) { r.Options.Seed = ptr(int64(5)) }},
		{"apparel missing product", ApparelReplicator, func(r *core.MediaRequest) { r.Options.ReferenceRoles = map[string]string{"image_1": "model_image"} }},
		{"apparel source duration", ApparelReplicator, func(r *core.MediaRequest) {
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: 181}}
		}},
		{"apparel custom voice", ApparelReplicator, func(r *core.MediaRequest) { r.Options.Voice = "arbitrary_external_voice" }},
		{"apparel product length", ApparelReplicator, func(r *core.MediaRequest) { r.Prompt = strings.Repeat("a", 2501) }},
		{"commerce custom avatar ID", VideoCommerce, func(r *core.MediaRequest) { r.Options.Parameters["avatar_id"] = "arbitrary_external_id" }},
		{"commerce both avatars", VideoCommerce, func(r *core.MediaRequest) { r.Options.ImageURLs = []string{"https://cdn.example/avatar.png"} }},
		{"commerce missing avatars", VideoCommerce, func(r *core.MediaRequest) { r.Options.Parameters = nil }},
		{"commerce uploaded avatar no voice", VideoCommerce, func(r *core.MediaRequest) {
			r.Options.Parameters = nil
			r.Options.ImageURLs = []string{"https://cdn.example/avatar.png"}
		}},
		{"commerce library voice", VideoCommerce, func(r *core.MediaRequest) { r.Options.Voice = "female_clear_rational" }},
		{"commerce requested duration", VideoCommerce, func(r *core.MediaRequest) { r.Options.Duration = 15 }},
		{"commerce product no image", VideoCommerce, func(r *core.MediaRequest) { r.Options.Parameters["goods_title"] = "product" }},
		{"commerce product no title", VideoCommerce, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/product.png"}
			r.Options.ReferenceRoles = map[string]string{"image_1": "ref_image"}
		}},
		{"commerce product rate", VideoCommerce, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/product.png"}
			r.Options.ReferenceRoles = map[string]string{"image_1": "ref_image"}
			r.Options.Parameters["goods_title"] = "product"
			r.Options.Parameters["speech_rate"] = "1.0"
		}},
		{"commerce twenty one audiences", VideoCommerce, func(r *core.MediaRequest) { r.Options.Parameters["goods_target_audience"] = make([]string, 21) }},
		{"commerce audience unicode length", VideoCommerce, func(r *core.MediaRequest) {
			r.Options.Parameters["goods_target_audience"] = []string{strings.Repeat("界", 201)}
		}},
		{"commerce untyped list", VideoCommerce, func(r *core.MediaRequest) { r.Options.Parameters["goods_target_audience"] = []any{true} }},
		{"commerce selling length", VideoCommerce, func(r *core.MediaRequest) {
			r.Options.Parameters["goods_selling_point"] = []string{strings.Repeat("界", 501)}
		}},
		{"commerce script length", VideoCommerce, func(r *core.MediaRequest) { r.Prompt = strings.Repeat("a", 2001) }},
		{"tryon role duplicate", VirtualTryOn, func(r *core.MediaRequest) { r.Options.ReferenceRoles = nil }},
		{"tryon outputcount", VirtualTryOn, func(r *core.MediaRequest) { r.Options.OutputCount = 2 }},
		{"tryon resolution", VirtualTryOn, func(r *core.MediaRequest) { r.Options.Resolution = "1k" }},
		{"tryon nonboolean preservation", VirtualTryOn, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"keep_face": "true"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := solutionFixtureRequest(tc.id)
			tc.change(&r)
			if err := validateSolutionRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
}
func TestSolutionExactTariffBranchesAndUnknownQuantity(t *testing.T) {
	for _, tc := range []struct {
		id, voice, res string
		product        bool
		rate           money.USD
	}{
		{ApparelReplicator, "", "720p", false, 182000}, {ApparelReplicator, "", "1080p", false, 238000}, {ApparelReplicator, "calm_sales_woman", "720p", false, 238000}, {ApparelReplicator, "calm_sales_woman", "1080p", false, 280000},
		{GoodsStudio, "", "720p", false, 238000}, {GoodsStudio, "", "1080p", false, 280000},
		{VideoCommerce, "", "720p", false, 140000}, {VideoCommerce, "", "1080p", false, 168000}, {VideoCommerce, "", "720p", true, 196000}, {VideoCommerce, "", "1080p", true, 224000},
	} {
		r := solutionFixtureRequest(tc.id)
		r.Options.Voice = tc.voice
		r.Options.Resolution = tc.res
		if tc.product {
			r.Options.ImageURLs = []string{"https://cdn.example/product.png"}
			r.Options.ReferenceRoles = map[string]string{"image_1": "ref_image"}
			r.Options.Parameters["goods_title"] = "Product"
		}
		rate, unit, err := solutionRate(r)
		if err != nil || rate != tc.rate || unit != "second" {
			t.Fatalf("%+v %v %s %v", tc, rate, unit, err)
		}
		if tc.id == ApparelReplicator || tc.id == VideoCommerce {
			if cost, _, err := quoteSolution(r); err != nil || !cost.QuantityUnknown || cost.Pricing.Price != tc.rate || cost.ExpectedUSD != 0 || cost.Status != "quantity_unknown" {
				t.Fatal(tc, cost, err)
			}
		}
	}
	for _, tc := range []struct {
		duration string
		res      string
		expected money.USD
	}{{"15", "720p", 3570000}, {"30", "1080p", 8400000}, {"60", "1080p", 16800000}} {
		r := solutionFixtureRequest(GoodsStudio)
		r.Options.Resolution = tc.res
		r.Options.Parameters["duration"] = tc.duration
		cost, _, err := quoteSolution(r)
		if err != nil || cost.ExpectedUSD != tc.expected || cost.Status != "estimated" || cost.Pricing.Unit != "second" {
			t.Fatalf("%+v %v", cost, err)
		}
	}
	cost, _, err := quoteSolution(solutionFixtureRequest(VirtualTryOn))
	if err != nil || cost.ExpectedUSD != 112000 || cost.EstimatedUnits != 1 || cost.Pricing.Unit != "image" {
		t.Fatalf("%+v %v", cost, err)
	}
	if _, _, err := quoteSolution(solutionFixtureRequest(KlingEffects)); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal(err)
	}
}
func TestSolutionUnifiedPollingRetainsEveryArtifact(t *testing.T) {
	cases := []struct {
		id, status, outputs string
		count               int
		primary             string
		duration            float64
	}{
		{ApparelReplicator, "succeeded", `[{"type":"product_image","url":"https://cdn.example/product.png"},{"type":"model_images","url":"https://cdn.example/model.png"},{"type":"product_video","url":"https://cdn.example/video.mp4"}]`, 3, "https://cdn.example/video.mp4", 0},
		{GoodsStudio, "succeeded", `[{"type":"video","url":"https://cdn.example/video.mp4","duration":"15.5"}]`, 1, "https://cdn.example/video.mp4", 15.5},
		{VideoCommerce, "succeeded", `[{"type":"video","url":"https://cdn.example/video.mp4","duration":"22.75"}]`, 1, "https://cdn.example/video.mp4", 22.75},
		{VirtualTryOn, "succeed", `[{"type":"image","url":"https://cdn.example/image.webp"}]`, 1, "https://cdn.example/image.webp", 0},
	}
	for _, tc := range cases {
		a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
			if req.Method != "GET" || req.URL.Path != "/solutions" || req.URL.Query().Get("task_ids") != "9007199254740993" || req.URL.Query().Get("external_task_ids") != "" {
				t.Fatal(req.URL)
			}
			return response(200, `{"code":0,"data":[{"id":9007199254740993,"status":"`+tc.status+`","outputs":`+tc.outputs+`,"billing":[{"charge_type":"cash","amount":"0.07000","currency":"CNY"}]}]}`), nil
		}))
		result, err := a.getSolutionTask(context.Background(), tc.id, "9007199254740993")
		if err != nil || result.Status != core.ProviderSucceeded || len(result.Artifacts) != tc.count || result.ResultURL != tc.primary || result.Artifacts[0].URL != tc.primary || result.Artifacts[0].Duration != tc.duration {
			t.Fatalf("%s %+v %v", tc.id, result, err)
		}
		if result.Usage.EstimatedCost != nil || !strings.Contains(string(result.Usage.Raw), "0.07000") {
			t.Fatal(result.Usage)
		}
		if tc.id == ApparelReplicator {
			if result.Artifacts[1].MediaType != "image" || result.Artifacts[2].MediaType != "image" {
				t.Fatal(result.Artifacts)
			}
			for _, metric := range result.Usage.Metrics {
				if metric.Name == "output_video_duration" {
					t.Fatal("undocumented apparel duration was invented")
				}
			}
		}
	}
}
func TestSolutionLegacyEffectsPolling(t *testing.T) {
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/videos/effects/fixture" {
			t.Fatal(req.URL)
		}
		return response(200, `{"code":0,"data":{"task_id":"fixture","task_status":"succeed","task_result":{"videos":[{"id":9007199254740993,"url":"https://cdn.example/a.mp4"},{"id":"second","url":"https://cdn.example/b.mp4"}]},"final_unit_deduction":"8.0000"}}`), nil
	}))
	result, err := a.getSolutionTask(context.Background(), KlingEffects, "fixture")
	if err != nil || len(result.Artifacts) != 2 || result.Artifacts[0].ID != "9007199254740993" || result.Artifacts[1].MediaType != "video" || !strings.Contains(string(result.Usage.Raw), "8.0000") {
		t.Fatalf("%+v %v", result, err)
	}
}
func TestSolutionPollingStatesAndMalformedResults(t *testing.T) {
	for _, tc := range []struct {
		state  string
		status core.ProviderStatus
	}{{"submitted", core.ProviderQueued}, {"processing", core.ProviderRunning}, {"failed", core.ProviderFailed}} {
		a := adapter(t, transport(func(*http.Request) (*http.Response, error) {
			return response(200, `{"code":0,"data":[{"id":"fixture","status":"`+tc.state+`","message":"private_provider_detail"}]}`), nil
		}))
		result, err := a.getSolutionTask(context.Background(), GoodsStudio, "fixture")
		if err != nil || result.Status != tc.status || strings.Contains(result.Error, "private_provider_detail") {
			t.Fatalf("%+v %v", result, err)
		}
	}
	bodies := []string{
		`[{"id":"other","status":"succeeded"}]`,
		`[{"id":"fixture","status":"succeed"}]`,
		`[{"id":"fixture","status":"unknown"}]`,
		`[{"id":"fixture","status":"succeeded","outputs":[]}]`,
		`[{"id":"fixture","status":"succeeded","outputs":[{"type":"image","url":"https://cdn.example/a.png"}]}]`,
		`[{"id":"fixture","status":"succeeded","outputs":[{"type":"video","url":"https://127.0.0.1/a.mp4","duration":"15"}]}]`,
		`[{"id":"fixture","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/a.mp4","duration":"NaN"}]}]`,
		`[{"id":"fixture","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/a.mp4"}]}]`,
	}
	for _, body := range bodies {
		a := adapter(t, transport(func(*http.Request) (*http.Response, error) { return response(200, `{"code":0,"data":`+body+`}`), nil }))
		if _, err := a.getSolutionTask(context.Background(), GoodsStudio, "fixture"); err == nil {
			t.Fatal(body)
		}
	}
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") }))
	if _, err := a.getSolutionTask(context.Background(), GoodsStudio, "../escape"); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
}

func TestSolutionEmptyOptionalProductListsKeepCreatorMode(t *testing.T) {
	r := solutionFixtureRequest(VideoCommerce)
	r.Options.Parameters["goods_target_audience"] = []string{}
	r.Options.Parameters["goods_selling_point"] = []any{}
	r.Options.Parameters["goods_price"] = ""
	if err := validateSolutionRequest(r); err != nil {
		t.Fatal(err)
	}
	body := solutionNativePayload(r).(solutionPayload)
	for _, c := range body.Contents {
		if strings.HasPrefix(c.Type, "goods_") {
			t.Fatal(body)
		}
	}
	rate, _, err := solutionRate(r)
	if err != nil || rate != 140000 {
		t.Fatal(rate, err)
	}
	r.Options.Parameters["goods_target_audience"] = []string{" "}
	if err := validateSolutionRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal(err)
	}
}

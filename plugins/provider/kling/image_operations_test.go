package kling

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func imageFixtureRequest(id string) core.MediaRequest {
	r := core.MediaRequest{Model: id, Prompt: "Synthetic image fixture"}
	switch id {
	case MultiImage21, ImageOutpainting, ImageMultiShot:
		r.Options.ImageURLs = []string{"https://cdn.example/fixture.png"}
	}
	if id == ImageMultiShot {
		r.Prompt = ""
	}
	if id == ImageOutpainting {
		r.Options.Parameters = map[string]any{"up_expansion_ratio": 0, "down_expansion_ratio": 0, "left_expansion_ratio": .5, "right_expansion_ratio": .5}
	}
	return r
}
func TestImageOperationCatalogAndNativeSerializers(t *testing.T) {
	expected := map[string]string{ImageGeneration3: "kling-v3", ImageGeneration21: "kling-v2-1", ImageOmni3: "kling-v3-omni", ImageO1: "kling-image-o1", MultiImage21: "kling-v2-1", ImageOutpainting: "", ImageMultiShot: ""}
	if len(imageModels()) != len(expected) {
		t.Fatal(imageModels())
	}
	for _, m := range imageModels() {
		t.Run(m.ID, func(t *testing.T) {
			op, ok := findImageOperation(m.ID)
			if !ok || !isImageOperation(m.ID) || op.modelName != expected[m.ID] {
				t.Fatal(op)
			}
			if m.MediaType != "image" || m.ReferenceMediaLimits.Image.MaxBytes != 10<<20 || m.ReferenceMediaLimits.Image.MinDimension != 300 || m.ReferenceMediaLimits.Image.MinRatio != .4 || m.ReferenceMediaLimits.Image.MaxRatio != 2.5 {
				t.Fatal(m)
			}
			for _, p := range m.Parameters {
				if p.Key == "element_list" || p.Key == "callback_url" {
					t.Fatalf("unbound control %s", p.Key)
				}
			}
			r := imageFixtureRequest(m.ID)
			if !op.perCall {
				r.Options.OutputCount = 9
			}
			r.TaskID = "fixture-local-task"
			if !op.noPrompt {
				r.Options.Watermark = ptr(false)
			}
			a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.URL.Path != op.path {
					t.Fatalf("%s %s", req.Method, req.URL.Path)
				}
				raw, _ := io.ReadAll(req.Body)
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				if op.modelName != "" && body["model_name"] != expected[m.ID] {
					t.Fatal(body)
				}
				if op.modelName == "" && body["model_name"] != nil {
					t.Fatal(body)
				}
				if body["external_task_id"] != r.TaskID || body["callback_url"] != nil || body["settings"] != nil || body["parameters"] != nil {
					t.Fatal(body)
				}
				if op.perCall {
					if body["n"] != nil || body["element_frontal_image"] != r.Options.ImageURLs[0] || body["watermark_info"] != nil {
						t.Fatal(body)
					}
				} else if body["n"] != float64(9) || body["watermark_info"].(map[string]any)["enabled"] != false {
					t.Fatal(body)
				}
				if m.ID == ImageOutpainting && body["up_expansion_ratio"] != float64(0) {
					t.Fatal(body)
				}
				return response(200, `{"code":0,"data":{"task_id":"image-fixture-task","task_status":"submitted"}}`), nil
			}))
			if err := a.validateImageRequest(r); err != nil {
				t.Fatal(err)
			}
			task, err := a.generateImage(imageFixtureContext(), r)
			if err != nil || task.ID == "" {
				t.Fatalf("%+v %v", task, err)
			}
		})
	}
	if isImageOperation("kling-v3") || isImageOperation("invented-image") {
		t.Fatal("unreviewed image alias")
	}
}

func TestImageOperationValidation(t *testing.T) {
	cases := []struct {
		name, id string
		change   func(*core.MediaRequest)
	}{
		{"missing prompt", ImageGeneration3, func(r *core.MediaRequest) { r.Prompt = "" }},
		{"prompt limit", ImageGeneration3, func(r *core.MediaRequest) { r.Prompt = strings.Repeat("界", 2501) }},
		{"negative count", ImageGeneration3, func(r *core.MediaRequest) { r.Options.OutputCount = -1 }},
		{"count ten", ImageGeneration3, func(r *core.MediaRequest) { r.Options.OutputCount = 10 }},
		{"o1 4k", ImageO1, func(r *core.MediaRequest) { r.Options.Resolution = "4k" }},
		{"v3 auto", ImageGeneration3, func(r *core.MediaRequest) { r.Options.Ratio = "auto" }},
		{"omni text auto", ImageOmni3, func(r *core.MediaRequest) { r.Options.Ratio = "auto" }},
		{"unsupported seed", ImageO1, func(r *core.MediaRequest) { r.Options.Seed = ptr(int64(1)) }},
		{"unsupported audio", ImageO1, func(r *core.MediaRequest) { r.Options.AudioURLs = []string{"https://cdn.example/audio.mp3"} }},
		{"unsupported video", ImageO1, func(r *core.MediaRequest) { r.Options.VideoURL = "https://cdn.example/video.mp4" }},
		{"unknown scalar", ImageO1, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"native_json": "{}"} }},
		{"element scalar rejected", ImageOmni3, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"element_id": "829836802793406551"} }},
		{"nested passthrough", ImageGeneration21, func(r *core.MediaRequest) {
			r.Options.Parameters = map[string]any{"image_reference": map[string]any{"subject": true}}
		}},
		{"negative image", ImageGeneration21, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
			r.Options.NegativePrompt = "blur"
		}},
		{"negative omni", ImageO1, func(r *core.MediaRequest) { r.Options.NegativePrompt = "blur" }},
		{"negative length", ImageGeneration3, func(r *core.MediaRequest) { r.Options.NegativePrompt = strings.Repeat("a", 2501) }},
		{"face no image", ImageGeneration21, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"image_reference": "face"} }},
		{"human face", ImageGeneration21, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
			r.Options.Parameters = map[string]any{"image_reference": "face", "human_fidelity": .5}
		}},
		{"v3 fidelity", ImageGeneration3, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"image_fidelity": .5} }},
		{"fidelity nan", ImageGeneration21, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
			r.Options.Parameters = map[string]any{"image_fidelity": math.NaN()}
		}},
		{"fidelity range", ImageGeneration21, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
			r.Options.Parameters = map[string]any{"image_fidelity": 1.1}
		}},
		{"private input", ImageO1, func(r *core.MediaRequest) { r.Options.ImageURLs = []string{"https://127.0.0.1/a.png"} }},
		{"too many inputs", ImageO1, func(r *core.MediaRequest) { r.Options.ImageURLs = make([]string, 11) }},
		{"unknown role", ImageO1, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
			r.Options.ReferenceRoles = map[string]string{"image_1": "scene"}
		}},
		{"five subjects", MultiImage21, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png", "https://cdn.example/b.png", "https://cdn.example/c.png", "https://cdn.example/d.png", "https://cdn.example/e.png"}
		}},
		{"no subjects", MultiImage21, func(r *core.MediaRequest) { r.Options.ReferenceRoles = map[string]string{"image_1": "scene"} }},
		{"multi resolution not documented", MultiImage21, func(r *core.MediaRequest) { r.Options.Resolution = "2k" }},
		{"expand missing ratios", ImageOutpainting, func(r *core.MediaRequest) { r.Options.Parameters = nil }},
		{"expand area", ImageOutpainting, func(r *core.MediaRequest) {
			r.Options.Parameters["up_expansion_ratio"] = 1
			r.Options.Parameters["down_expansion_ratio"] = 1
		}},
		{"expand negative", ImageOutpainting, func(r *core.MediaRequest) { r.Options.Parameters["left_expansion_ratio"] = -.1 }},
		{"multishot prompt", ImageMultiShot, func(r *core.MediaRequest) { r.Prompt = "ignored prompt" }},
		{"multishot count", ImageMultiShot, func(r *core.MediaRequest) { r.Options.OutputCount = 2 }},
		{"multishot watermark", ImageMultiShot, func(r *core.MediaRequest) { r.Options.Watermark = ptr(false) }},
		{"o1 series", ImageO1, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"result_type": "series"} }},
		{"series without input", ImageOmni3, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"result_type": "series"} }},
		{"single with series amount", ImageOmni3, func(r *core.MediaRequest) { r.Options.Parameters = map[string]any{"series_amount": "4"} }},
		{"series and n", ImageOmni3, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
			r.Options.OutputCount = 1
			r.Options.Parameters = map[string]any{"result_type": "series"}
		}},
	}
	a := &Adapter{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := imageFixtureRequest(tc.id)
			tc.change(&r)
			if err := a.validateImageRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatalf("expected invalid options: %v", err)
			}
		})
	}
	r := imageFixtureRequest(ImageGeneration3)
	r.Prompt = strings.Repeat("界", 2500)
	if err := a.validateImageRequest(r); err != nil {
		t.Fatal(err)
	}
	r = imageFixtureRequest(ImageGeneration21)
	r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
	r.Options.Parameters = map[string]any{"image_reference": "subject", "image_fidelity": 0, "human_fidelity": 1}
	if err := a.validateImageRequest(r); err != nil {
		t.Fatal(err)
	}
	op, _ := findImageOperation(r.Model)
	b := imagePayload(op, r)
	if b.ImageFidelity == nil || *b.ImageFidelity != 0 || b.HumanFidelity == nil || *b.HumanFidelity != 1 {
		t.Fatal(b)
	}
}
func TestImageMultiReferenceAndSeriesPayloads(t *testing.T) {
	r := imageFixtureRequest(MultiImage21)
	r.Options.ImageURLs = []string{"https://cdn.example/subject.png", "https://cdn.example/style.png", "https://cdn.example/scene.png"}
	r.Options.ReferenceRoles = map[string]string{"image_2": "style", "image_3": "scene"}
	if err := (&Adapter{}).validateImageRequest(r); err != nil {
		t.Fatal(err)
	}
	op, _ := findImageOperation(r.Model)
	b := imagePayload(op, r)
	if len(b.SubjectImages) != 1 || b.SubjectImages[0].Image != r.Options.ImageURLs[0] || b.StyleImage != r.Options.ImageURLs[1] || b.SceneImage != r.Options.ImageURLs[2] || b.Resolution != "" {
		t.Fatal(b)
	}
	for _, amount := range []string{"2", "9", "auto", ""} {
		r = imageFixtureRequest(ImageOmni3)
		r.Options.ImageURLs = []string{"https://cdn.example/a.png", "https://cdn.example/b.png"}
		r.Options.Parameters = map[string]any{"result_type": "series"}
		if amount != "" {
			r.Options.Parameters["series_amount"] = amount
		}
		if err := (&Adapter{}).validateImageRequest(r); err != nil {
			t.Fatal(err)
		}
		op, _ = findImageOperation(r.Model)
		b = imagePayload(op, r)
		raw, _ := json.Marshal(b)
		var p map[string]any
		_ = json.Unmarshal(raw, &p)
		if p["n"] != nil || len(b.ImageList) != 2 || b.ResultType != "series" {
			t.Fatal(p)
		}
		if amount == "auto" {
			if b.SeriesAmount != "auto" {
				t.Fatal(b)
			}
		} else {
			expected := 4
			if amount == "2" {
				expected = 2
			}
			if amount == "9" {
				expected = 9
			}
			if b.SeriesAmount != expected {
				t.Fatal(b)
			}
		}
	}
}
func TestImagePublicPriceRows(t *testing.T) {
	cases := []struct {
		id, res   string
		images, n int
		series    string
		price     money.USD
		unit      string
		quantity  float64
	}{
		{ImageGeneration3, "1k", 0, 1, "", 28000, "image", 1}, {ImageGeneration3, "2k", 1, 9, "", 252000, "image", 9},
		{ImageOmni3, "1k", 0, 1, "", 28000, "image", 1}, {ImageOmni3, "2k", 1, 2, "", 56000, "image", 2}, {ImageOmni3, "4k", 1, 2, "", 112000, "image", 2},
		{ImageO1, "1k", 0, 1, "", 28000, "image", 1}, {ImageO1, "2k", 1, 2, "", 56000, "image", 2},
		{ImageGeneration21, "1k", 0, 2, "", 28000, "image", 2}, {ImageGeneration21, "2k", 1, 2, "", 56000, "image", 2},
		{MultiImage21, "", 1, 2, "", 112000, "image", 2}, {ImageMultiShot, "", 1, 0, "", 70000, "request", 1}, {ImageOutpainting, "", 1, 2, "", 56000, "image", 2},
		{ImageOmni3, "4k", 1, 0, "9", 504000, "image", 9},
	}
	for _, tc := range cases {
		r := imageFixtureRequest(tc.id)
		r.Options.Resolution = tc.res
		r.Options.OutputCount = tc.n
		if tc.images > 0 {
			r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
		}
		if tc.series != "" {
			r.Options.Parameters = map[string]any{"result_type": "series", "series_amount": tc.series}
		}
		cost, _, err := (&Adapter{}).quoteImage(r)
		if err != nil || cost.ExpectedUSD != tc.price || cost.Pricing.Unit != tc.unit || cost.EstimatedUnits != tc.quantity || cost.Status != "estimated" || cost.Pricing.AccountSpecific {
			t.Fatalf("%+v: %+v %v", tc, cost, err)
		}
	}
	r := imageFixtureRequest(ImageOmni3)
	r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
	r.Options.Parameters = map[string]any{"result_type": "series", "series_amount": "auto"}
	if _, _, err := (&Adapter{}).quoteImage(r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal(err)
	}
}
func TestImagePollingLegacyStatesArtifactsAndBilling(t *testing.T) {
	for _, tc := range []struct {
		status   string
		expected core.ProviderStatus
	}{{"submitted", core.ProviderQueued}, {"processing", core.ProviderRunning}, {"failed", core.ProviderFailed}} {
		a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
			return response(200, `{"code":0,"data":{"task_id":"fixture-id","task_status":"`+tc.status+`","task_status_msg":"private provider detail"}}`), nil
		}))
		result, err := a.getImageTask(context.Background(), ImageGeneration3, "fixture-id")
		if err != nil || result.Status != tc.expected || strings.Contains(result.Error, "private") {
			t.Fatalf("%+v %v", result, err)
		}
	}
	cases := []struct {
		id, body string
		count    int
		group    string
	}{
		{ImageGeneration21, `"images":[{"index":0,"url":"https://cdn.example/a.png"},{"index":1,"url":"https://cdn.example/b.png"}]`, 2, "images"},
		{ImageOmni3, `"result_type":"series","series_images":[{"index":9007199254740993,"url":"https://cdn.example/a.png"},{"index":1,"url":"https://cdn.example/b.png"}]`, 2, "series"},
		{ImageMultiShot, `"images":[{"index":0,"url_1":"https://cdn.example/a.png","url_2":"https://cdn.example/b.png","url_3":"https://cdn.example/c.png"}]`, 3, "images:0"},
	}
	for _, tc := range cases {
		a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
			op, _ := findImageOperation(tc.id)
			if req.Method != "GET" || req.URL.Path != op.path+"/9007199254740993" {
				t.Fatal(req.URL)
			}
			return response(200, `{"code":0,"data":{"task_id":9007199254740993,"task_status":"succeed","final_unit_deduction":"16.0000","final_balance_deduction":{"quota":"0.0280","list_price":"0.0560"},"task_result":{`+tc.body+`}}}`), nil
		}))
		result, err := a.getImageTask(context.Background(), tc.id, "9007199254740993")
		if err != nil || result.Status != core.ProviderSucceeded || len(result.Artifacts) != tc.count || result.ResultURL != result.Artifacts[0].URL {
			t.Fatalf("%+v %v", result, err)
		}
		for _, artifact := range result.Artifacts {
			if artifact.MediaType != "image" || artifact.GroupID != tc.group {
				t.Fatal(artifact)
			}
		}
		if tc.group == "series" && !strings.Contains(result.Artifacts[0].ID, "9007199254740993") {
			t.Fatal(result.Artifacts)
		}
		if !reflect.DeepEqual(result.Usage.Metrics, []core.UsageMetric{{Name: "generated_images", Unit: "image", Quantity: float64(tc.count)}}) || !strings.Contains(string(result.Usage.Raw), "16.0000") || result.Usage.EstimatedCost != nil {
			t.Fatal(result.Usage)
		}
	}
}
func TestImagePollingRejectsMalformedResults(t *testing.T) {
	bodies := []string{
		`{"task_id":"other","task_status":"succeed"}`,
		`{"task_id":"fixture-id","task_status":"succeeded","outputs":[{"type":"image","url":"https://cdn.example/a.png"}]}`,
		`{"task_id":"fixture-id","task_status":"succeed","task_result":{"images":[]}}`,
		`{"task_id":"fixture-id","task_status":"succeed","task_result":{"images":[{"index":0,"url":"https://127.0.0.1/a.png"}]}}`,
		`{"task_id":"fixture-id","task_status":"succeed","task_result":{"images":[{"index":0,"url":"https://cdn.example/a.png"},{"index":0,"url":"https://cdn.example/b.png"}]}}`,
		`{"task_id":"fixture-id","task_status":"succeed","task_result":{"result_type":"unexpected","images":[{"url":"https://cdn.example/a.png"}]}}`,
	}
	for _, body := range bodies {
		a := adapter(t, transport(func(*http.Request) (*http.Response, error) { return response(200, `{"code":0,"data":`+body+`}`), nil }))
		if _, err := a.getImageTask(context.Background(), ImageGeneration3, "fixture-id"); err == nil {
			t.Fatal(body)
		}
	}
	calls := 0
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("must not call") }))
	if _, err := a.getImageTask(context.Background(), ImageGeneration3, "../escape"); err == nil || calls != 0 {
		t.Fatalf("%d %v", calls, err)
	}
}

func TestImageOmniPromptAddresses(t *testing.T) {
	op, _ := findImageOperation(ImageOmni3)
	prompt := "Use @Image1 and @image_2 with <<<image_3>>>; keep @named and embedded@Image4"
	expected := "Use <<<image_1>>> and <<<image_2>>> with <<<image_3>>>; keep @named and embedded@Image4"
	if got := imagePrompt(op, prompt); got != expected {
		t.Fatal(got)
	}
	r := imageFixtureRequest(ImageOmni3)
	r.Options.ImageURLs = []string{"https://cdn.example/a.png"}
	r.Prompt = "Use @Image1"
	if imagePayload(op, r).Prompt != "Use <<<image_1>>>" || r.Prompt != "Use @Image1" {
		t.Fatal(r)
	}
	basic, _ := findImageOperation(ImageGeneration3)
	if imagePrompt(basic, prompt) != prompt {
		t.Fatal("basic prompt was rewritten")
	}
	r.Prompt = strings.Repeat("a", 2490) + " @Image1"
	if err := (&Adapter{}).validateImageRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("wire prompt limit", err)
	}
}

func imageFixtureContext() context.Context {
	return core.WithProviderConnectionScope(identity.WithOwnerID(context.Background(), "fixture-owner"), core.ProviderConnectionScope{ID: "kling--fixture", OwnerID: "fixture-provider-owner", Revision: "fixture-revision"})
}
func imageFixtureResource(r *core.MediaRequest) {
	const binding = "f6d4f4a7-0c17-4edb-8cdd-8934339a2222"
	r.Options.ResourceReferences = []core.ResourceReference{{BindingID: binding, ReferenceID: "element_1"}}
	r.Options.ResourceSnapshots = []core.ResourceSnapshot{{BindingID: binding, ReferenceID: "element_1", Kind: "element", Subtype: "multi_image", RemoteID: "829836802793406551", ConnectionIdentity: "fixture_connection_revision"}}
}
func TestImageResolvedElements(t *testing.T) {
	for _, id := range []string{ImageGeneration3, ImageOmni3, ImageO1} {
		r := imageFixtureRequest(id)
		imageFixtureResource(&r)
		if err := (&Adapter{}).validateImageRequest(r); err != nil {
			t.Fatal(id, err)
		}
		op, _ := findImageOperation(id)
		raw, err := json.Marshal(imagePayload(op, r))
		if err != nil || !strings.Contains(string(raw), `"element_id":829836802793406551`) || strings.Contains(string(raw), `"element_id":"`) {
			t.Fatalf("%s %v", raw, err)
		}
	}
	cases := []struct {
		name   string
		change func(*core.MediaRequest)
	}{
		{"unresolved", func(r *core.MediaRequest) { r.Options.ResourceSnapshots = nil }},
		{"unbound", func(r *core.MediaRequest) { r.Options.ResourceReferences = nil }},
		{"video element", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Subtype = "video_character" }},
		{"wrong kind", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Kind = "voice" }},
		{"missing connection", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].ConnectionIdentity = "" }},
		{"alias mismatch", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].ReferenceID = "other" }},
		{"raw uuid ID", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].RemoteID = "invalid-native-id" }},
		{"float ID", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].RemoteID = "829836802793406551.0" }},
		{"negative ID", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].RemoteID = "-1" }},
		{"native long overflow", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].RemoteID = "9223372036854775808" }},
		{"combined count", func(r *core.MediaRequest) {
			for i := 0; i < 10; i++ {
				r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/a.png")
			}
		}},
		{"unsupported endpoint", func(r *core.MediaRequest) { r.Model = ImageGeneration21 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := imageFixtureRequest(ImageOmni3)
			imageFixtureResource(&r)
			tc.change(&r)
			if err := (&Adapter{}).validateImageRequest(r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
	r := imageFixtureRequest(ImageOmni3)
	imageFixtureResource(&r)
	r.Options.Parameters = map[string]any{"result_type": "series", "series_amount": "2"}
	if err := (&Adapter{}).validateImageRequest(r); err != nil {
		t.Fatal(err)
	}
}

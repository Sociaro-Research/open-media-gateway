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
)

func TestOmni4RealVideoReferencesRolesAndOmission(t *testing.T) {
	r := core.MediaRequest{Model: OmniVideo4, Prompt: "@Image1 follows @Video1; keep native @image_2. person@Image1.example stays text.", Options: core.MediaOptions{ReferenceMode: "references", ImageURLs: []string{"https://cdn.example/a.png", "https://cdn.example/b.png"}, VideoURLs: []string{"https://cdn.example/v.mp4"}, ReferenceRoles: map[string]string{"image_1": "first_frame", "video_1": "source"}}}
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != "POST" || req.URL.Path != "/omni-video/kling-4.0" {
			t.Fatal(req.URL)
		}
		var body struct {
			Contents []map[string]string `json:"contents"`
			Settings map[string]any      `json:"settings"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Settings["duration"] != nil || body.Settings["aspect_ratio"] != nil || body.Settings["audio"] != nil || body.Settings["multi_shot"] != nil || body.Settings["resolution"] != "1080p" {
			t.Fatal(body)
		}
		want := "@image_1 follows @video_1; keep native @image_2. person@Image1.example stays text."
		if len(body.Contents) != 4 || body.Contents[0]["text"] != want || body.Contents[1]["type"] != "first_frame" || body.Contents[2]["type"] != "refer_image" || body.Contents[3]["type"] != "base_video" || body.Contents[3]["id"] != "video_1" {
			t.Fatal(body)
		}
		return response(200, `{"code":0,"data":{"id":"omni_fixture","status":"submitted"}}`), nil
	})
	if _, err := a.Generate(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	m, _ := modelFor(OmniVideo4)
	normalized, err := core.NormalizeModelOptions(m, r.Options)
	if err != nil || normalized.Duration != 0 || normalized.Ratio != "" {
		t.Fatal("omitted native intent overwritten", normalized, err)
	}
	if _, _, err := a.Quote(context.Background(), m, r); !errors.Is(err, core.ErrPricingUnavailable) {
		t.Fatal("inferred duration guessed", err)
	}
}

func TestOmni4InputVideoQuantityAndUnknownEvidence(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) { t.Fatal("quote must not call Kling"); return nil, nil })
	for _, tc := range []struct {
		input      float64
		resolution string
		want       int64
	}{{0, "720p", 1570000}, {0, "1080p", 2350000}, {4, "720p", 1880000}, {4, "1080p", 2820000}, {17, "1080p", 3807000}} {
		r := core.MediaRequest{Model: OmniVideo4, Prompt: "fixture", Options: core.MediaOptions{Duration: 10, Resolution: tc.resolution}}
		if tc.input > 0 {
			r.Options.VideoURLs = []string{"https://cdn.example/v.mp4"}
			r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{r.Options.VideoURLs[0]: {Type: "video", Duration: tc.input}}
		}
		cost, _, err := a.Quote(context.Background(), core.MediaModel{}, r)
		if err != nil || int64(cost.ExpectedUSD) != tc.want || cost.ActualUSD != nil {
			t.Fatal(tc, cost, err)
		}
		if tc.input > 0 {
			if cost.Pricing.DurationBillingRule != "output_plus_max_input_output" {
				t.Fatal(cost)
			}
			r.Options.ReferenceMediaEvidence = nil
			if _, _, err := a.Quote(context.Background(), core.MediaModel{}, r); !errors.Is(err, core.ErrPricingUnavailable) {
				t.Fatal("unknown duration treated as zero", err)
			}
		}
	}
}

func TestOmni4LimitsAndMixedBudget(t *testing.T) {
	a := adapter(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached network")
		return nil, nil
	})
	r := core.MediaRequest{Model: OmniVideo4, Prompt: strings.Repeat("E", 13000) + strings.Repeat("界", 5500), Options: core.MediaOptions{Duration: 10}}
	if err := a.ValidateRequest(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	r.Prompt += "E"
	if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrPromptTooLong) {
		t.Fatal(err)
	}
	r.Prompt = "fixture"
	for i := 0; i < 10; i++ {
		r.Options.ImageURLs = append(r.Options.ImageURLs, fmt.Sprintf("https://cdn.example/%d.png", i))
	}
	for i := 0; i < 5; i++ {
		r.Options.VideoURLs = append(r.Options.VideoURLs, fmt.Sprintf("https://cdn.example/%d.mp4", i))
	}
	r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{}
	for _, u := range r.Options.VideoURLs {
		r.Options.ReferenceMediaEvidence[u] = core.InputMediaEvidence{Type: "video", Duration: 3}
	}
	if err := a.ValidateRequest(context.Background(), r); err != nil {
		t.Fatal("15 documented visual references rejected", err)
	}
	for _, u := range r.Options.VideoURLs {
		r.Options.ReferenceMediaEvidence[u] = core.InputMediaEvidence{Type: "video", Duration: 7}
	}
	if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("35 input seconds accepted", err)
	}
	for _, u := range r.Options.VideoURLs {
		r.Options.ReferenceMediaEvidence[u] = core.InputMediaEvidence{Type: "video", Duration: 3}
	}
	r.Options.ReferenceRoles = map[string]string{"video_1": "source", "video_2": "source"}
	if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("two base videos accepted", err)
	}
	r.Options.ReferenceRoles = nil
	r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/extra.png")
	if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
		t.Fatal("eleven images accepted", err)
	}
}

func TestOmni4PollingInputUsageAndBillingDenominations(t *testing.T) {
	a := adapter(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || req.URL.Path != "/tasks" || req.URL.Query().Get("task_ids") != "omni_fixture" {
			t.Fatal(req.URL)
		}
		return response(200, `{"code":0,"data":[{"id":"omni_fixture","status":"succeeded","outputs":[{"type":"video","url":"https://cdn.example/out.mp4","duration":"10"}],"usage":{"input_duration":"17.00"},"billing":[{"charge_type":"cash","cash_type":"balance","currency":"USD","amount":"3.00","list_price":"3.807"},{"charge_type":"unit","package_type":"video","amount":"24.3"}]}]}`), nil
	})
	result, err := a.GetTask(context.Background(), "omni_fixture")
	if err != nil || result.Status != core.ProviderSucceeded {
		t.Fatal(result, err)
	}
	if !strings.Contains(string(result.Usage.Raw), `"input_video_duration":17`) || !strings.Contains(string(result.Usage.Raw), `"list_price":"3.807"`) || !strings.Contains(string(result.Usage.Raw), `"amount":"24.3"`) {
		t.Fatal(string(result.Usage.Raw))
	}
	if result.Usage.EstimatedCost != nil {
		t.Fatal("raw cash or units became confirmed USD")
	}
}

func omni4AddResource(r *core.MediaRequest, kind, subtype string, seconds *float64) {
	i := len(r.Options.ResourceReferences) + 1
	binding := fmt.Sprintf("a6dd8e89-3b7c-40cf-8dc8-%012d", i)
	alias := fmt.Sprintf("managed%d", i)
	r.Options.ResourceReferences = append(r.Options.ResourceReferences, core.ResourceReference{BindingID: binding, ReferenceID: alias})
	s := core.ResourceSnapshot{BindingID: binding, ReferenceID: alias, Kind: kind, Subtype: subtype, RemoteID: fmt.Sprintf("9007199254740993%d", i), ConnectionIdentity: "fixture-connection-revision"}
	if subtype == "video_character" && seconds != nil {
		s.Sources = []core.ResourceSourceEvidence{{ReferenceID: "a6dd8e89-3b7c-40cf-8dc8-e7534c42371d", MediaType: "video", SHA256: strings.Repeat("a", 64), Duration: seconds}}
	}
	r.Options.ResourceSnapshots = append(r.Options.ResourceSnapshots, s)
}
func omni4ResourceFixture() core.MediaRequest {
	return core.MediaRequest{Model: OmniVideo4, TaskID: "gateway_fixture", Prompt: "Synthetic resource fixture", Options: core.MediaOptions{Duration: 10, Resolution: "1080p"}}
}
func omni4AddVideos(r *core.MediaRequest, count int, seconds float64) {
	if r.Options.ReferenceMediaEvidence == nil {
		r.Options.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{}
	}
	for i := 0; i < count; i++ {
		raw := fmt.Sprintf("https://cdn.example/video%d.mp4", i+1)
		r.Options.VideoURLs = append(r.Options.VideoURLs, raw)
		r.Options.ReferenceMediaEvidence[raw] = core.InputMediaEvidence{Type: "video", Duration: seconds}
	}
}

func TestOmni4ManagedResourcesNativeMapping(t *testing.T) {
	r := omni4ResourceFixture()
	r.Options.Duration = 0
	r.Prompt = "@image1 follows @managed1 and @managed2 with @speaker."
	r.Options.ImageURLs = []string{"https://cdn.example/i.png"}
	r.Options.ReferenceRoles = map[string]string{"image_1": "middle_frame"}
	omni4AddResource(&r, "element", "multi_image", nil)
	omni4AddResource(&r, "element", "video_character", ptr(6.0))
	omni4AddResource(&r, "voice", "", nil)
	r.Options.ResourceReferences[2].ReferenceID = "speaker"
	r.Options.ResourceSnapshots[2].ReferenceID = "speaker"
	r.Options.ResourceSnapshots[2].IsPreset = true
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/omni-video/kling-4.0" || req.Method != "POST" {
			t.Fatal(req.URL)
		}
		var p struct {
			Contents []map[string]string `json:"contents"`
			Settings map[string]any      `json:"settings"`
			Options  map[string]any      `json:"options"`
		}
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if len(p.Contents) != 5 || p.Contents[0]["text"] != "@image_1 follows @managed1 and @managed2 with @speaker." || p.Contents[1]["type"] != "middle_frame" || p.Contents[2]["element_id"] != "90071992547409931" || p.Contents[3]["element_id"] != "90071992547409932" || p.Contents[4]["voice_id"] != "90071992547409933" || p.Contents[4]["id"] != "speaker" {
			t.Fatal(p)
		}
		for _, k := range []string{"duration", "aspect_ratio", "audio", "multi_shot"} {
			if p.Settings[k] != nil {
				t.Fatal("inferred/unsupported control materialized", p.Settings)
			}
		}
		if p.Options["external_task_id"] != "gateway_fixture" {
			t.Fatal(p.Options)
		}
		return response(200, `{"code":0,"data":{"id":"resource_fixture","status":"submitted"}}`), nil
	}))
	m := omni4Model()
	if err := validateOmni4(m, r); err != nil {
		t.Fatal(err)
	}
	if _, err := a.generateOmni4(audioFixtureContext(), m, r); err != nil {
		t.Fatal(err)
	}
}

func TestOmni4CombinedResourceQuotas(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
		setup func(*core.MediaRequest)
	}{
		{"ten images five elements fifteen total", true, func(r *core.MediaRequest) {
			for i := 0; i < 10; i++ {
				r.Options.ImageURLs = append(r.Options.ImageURLs, fmt.Sprintf("https://cdn.example/%d.png", i))
			}
			for i := 0; i < 5; i++ {
				omni4AddResource(r, "element", "multi_image", nil)
			}
		}},
		{"voice exceeds fifteen total", false, func(r *core.MediaRequest) {
			for i := 0; i < 10; i++ {
				r.Options.ImageURLs = append(r.Options.ImageURLs, fmt.Sprintf("https://cdn.example/%d.png", i))
			}
			for i := 0; i < 5; i++ {
				omni4AddResource(r, "element", "multi_image", nil)
			}
			omni4AddResource(r, "voice", "", nil)
		}},
		{"seven elements", true, func(r *core.MediaRequest) {
			for i := 0; i < 7; i++ {
				omni4AddResource(r, "element", "multi_image", nil)
			}
		}},
		{"eight elements", false, func(r *core.MediaRequest) {
			for i := 0; i < 8; i++ {
				omni4AddResource(r, "element", "multi_image", nil)
			}
		}},
		{"three video elements two videos", true, func(r *core.MediaRequest) {
			for i := 0; i < 3; i++ {
				omni4AddResource(r, "element", "video_character", ptr(3.0))
			}
			omni4AddVideos(r, 2, 3)
		}},
		{"three video elements three videos", false, func(r *core.MediaRequest) {
			for i := 0; i < 3; i++ {
				omni4AddResource(r, "element", "video_character", ptr(3.0))
			}
			omni4AddVideos(r, 3, 3)
		}},
		{"four video elements", false, func(r *core.MediaRequest) {
			for i := 0; i < 4; i++ {
				omni4AddResource(r, "element", "video_character", ptr(3.0))
			}
		}},
		{"three voices", true, func(r *core.MediaRequest) {
			for i := 0; i < 3; i++ {
				omni4AddResource(r, "voice", "", nil)
			}
		}},
		{"five voices", false, func(r *core.MediaRequest) {
			for i := 0; i < 5; i++ {
				omni4AddResource(r, "voice", "", nil)
			}
		}},
		{"aggregate video and element thirty seconds", true, func(r *core.MediaRequest) {
			omni4AddResource(r, "element", "video_character", ptr(3.0))
			omni4AddVideos(r, 1, 27)
		}},
		{"aggregate video and element thirty one seconds", false, func(r *core.MediaRequest) {
			omni4AddResource(r, "element", "video_character", ptr(4.0))
			omni4AddVideos(r, 1, 27)
		}},
		{"five seven-second videos", false, func(r *core.MediaRequest) { omni4AddVideos(r, 5, 7) }},
		{"two base videos", false, func(r *core.MediaRequest) {
			omni4AddVideos(r, 2, 3)
			r.Options.ReferenceRoles = map[string]string{"video_1": "source", "video_2": "source"}
		}},
		{"element individual too short", false, func(r *core.MediaRequest) { omni4AddResource(r, "element", "video_character", ptr(2.9)) }},
		{"element individual too long", false, func(r *core.MediaRequest) { omni4AddResource(r, "element", "video_character", ptr(30.1)) }},
		{"alias collision with image", false, func(r *core.MediaRequest) {
			r.Options.ImageURLs = []string{"https://cdn.example/i.png"}
			omni4AddResource(r, "voice", "", nil)
			r.Options.ResourceReferences[0].ReferenceID = "image_1"
			r.Options.ResourceSnapshots[0].ReferenceID = "image_1"
		}},
		{"duplicate resource alias", false, func(r *core.MediaRequest) {
			omni4AddResource(r, "element", "multi_image", nil)
			omni4AddResource(r, "voice", "", nil)
			r.Options.ResourceReferences[1].ReferenceID = "managed1"
			r.Options.ResourceSnapshots[1].ReferenceID = "managed1"
		}},
		{"missing connection identity", false, func(r *core.MediaRequest) {
			omni4AddResource(r, "element", "multi_image", nil)
			r.Options.ResourceSnapshots[0].ConnectionIdentity = ""
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := omni4ResourceFixture()
			tc.setup(&r)
			err := validateOmni4(omni4Model(), r)
			if tc.valid && err != nil {
				t.Fatal(err)
			}
			if !tc.valid && !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatalf("accepted invalid references: %v", err)
			}
		})
	}
}

func TestOmni4ResourceInputBillingEvidence(t *testing.T) {
	a := adapter(t, transport(func(*http.Request) (*http.Response, error) { t.Fatal("quote called provider"); return nil, nil }))
	for _, tc := range []struct {
		resolution string
		seconds    float64
		want       int64
	}{{"720p", 4, 1880000}, {"1080p", 4, 2820000}, {"1080p", 17, 3807000}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			r := omni4ResourceFixture()
			r.Options.Resolution = tc.resolution
			omni4AddResource(&r, "element", "video_character", ptr(tc.seconds))
			cost, _, err := a.quoteOmni4(omni4Model(), r)
			if err != nil || int64(cost.ExpectedUSD) != tc.want || cost.Pricing.DurationBillingRule != "output_plus_max_input_output" {
				t.Fatal(cost, err)
			}
		})
	}
	r := omni4ResourceFixture()
	omni4AddResource(&r, "element", "video_character", ptr(5.0))
	omni4AddVideos(&r, 1, 12)
	cost, _, err := a.quoteOmni4(omni4Model(), r)
	if err != nil || int64(cost.ExpectedUSD) != 3807000 {
		t.Fatal("mixed input seconds not summed", cost, err)
	}
	for _, edit := range []func(*core.MediaRequest){
		func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Sources = nil },
		func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Sources[0].Duration = nil },
		func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Sources[0].SHA256 = "" },
		func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Sources[0].ReferenceID = "" },
		func(r *core.MediaRequest) {
			r.Options.ResourceSnapshots[0].IsPreset = true
			r.Options.ResourceSnapshots[0].Sources = nil
		},
	} {
		r := omni4ResourceFixture()
		omni4AddResource(&r, "element", "video_character", ptr(5.0))
		edit(&r)
		if _, _, err := a.quoteOmni4(omni4Model(), r); !errors.Is(err, core.ErrPricingUnavailable) {
			t.Fatal("unknown element duration treated as zero", err)
		}
	}
	a.kling4Prices["1080p"] = 300000
	r = omni4ResourceFixture()
	omni4AddResource(&r, "element", "multi_image", nil)
	cost, _, err = a.quoteOmni4(omni4Model(), r)
	if err != nil || int64(cost.ExpectedUSD) != 3000000 || !cost.Pricing.AccountSpecific {
		t.Fatal("output-only override lost", cost, err)
	}
	omni4AddResource(&r, "element", "video_character", ptr(4.0))
	cost, _, err = a.quoteOmni4(omni4Model(), r)
	if err != nil || int64(cost.ExpectedUSD) != 2820000 || cost.Pricing.AccountSpecific {
		t.Fatal("output-only override leaked into input-video tariff", cost, err)
	}
}

func TestOmni4ManagedAliasesPreserveNativeIdentity(t *testing.T) {
	r := omni4ResourceFixture()
	r.Prompt = "Use @image1 and @managed2."
	omni4AddResource(&r, "element", "multi_image", nil)
	r.Options.ResourceReferences[0].ReferenceID = "image1"
	r.Options.ResourceSnapshots[0].ReferenceID = "image1"
	a := adapter(t, transport(func(req *http.Request) (*http.Response, error) {
		var p struct {
			Contents []map[string]string `json:"contents"`
		}
		if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		if p.Contents[0]["text"] != r.Prompt || p.Contents[1]["id"] != "image1" {
			t.Fatal("managed alias was rewritten", p)
		}
		return response(200, `{"code":0,"data":{"id":"alias_fixture","status":"submitted"}}`), nil
	}))
	if _, err := a.generateOmni4(audioFixtureContext(), omni4Model(), r); err != nil {
		t.Fatal(err)
	}
}

func TestOmni4ResourceMetadataRejectsKnownContradictions(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*core.MediaRequest)
	}{
		{"two video sources", func(r *core.MediaRequest) {
			s := r.Options.ResourceSnapshots[0].Sources[0]
			r.Options.ResourceSnapshots[0].Sources = append(r.Options.ResourceSnapshots[0].Sources, s)
		}},
		{"NaN duration", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Sources[0].Duration = ptr(math.NaN()) }},
		{"infinite duration", func(r *core.MediaRequest) { r.Options.ResourceSnapshots[0].Sources[0].Duration = ptr(math.Inf(1)) }},
		{"URL evidence wrong type", func(r *core.MediaRequest) {
			omni4AddVideos(r, 1, 3)
			r.Options.ReferenceMediaEvidence[r.Options.VideoURLs[0]] = core.InputMediaEvidence{Type: "image", Duration: 3}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := omni4ResourceFixture()
			omni4AddResource(&r, "element", "video_character", ptr(4.0))
			tc.edit(&r)
			if err := validateOmni4(omni4Model(), r); !errors.Is(err, core.ErrOptionsInvalid) {
				t.Fatal(err)
			}
		})
	}
}

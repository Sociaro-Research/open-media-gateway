package minimax

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/google/uuid"
	"math"
	"net/http"
	"strings"
	"testing"
)

func baseRequest() core.MediaRequest {
	yes := 1
	u := "https://media.example/owned-source.mp4"
	return core.MediaRequest{Model: RegenerationVideo, Prompt: "The exact final original prompt", Options: core.MediaOptions{ReferenceMode: "references", VideoURLs: []string{u}, ReferenceRoles: map[string]string{"video_1": "base_video"}, ReferenceMediaEvidence: map[string]core.InputMediaEvidence{u: {Type: "video", ReferenceID: uuid.NewString(), ContentType: "video/mp4", SizeBytes: 12345, SHA256: strings.Repeat("a", 64), Width: 768, Height: 1024, FPS: 24, FrameCount: 124, Duration: 124.0 / 24, AudioStreams: &yes, VideoStreams: &yes}}}}
}
func TestStandaloneBaseVideoProtocolAndRechargeQuote(t *testing.T) {
	calls := 0
	a := fixture(t, func(h *http.Request) (*http.Response, error) {
		calls++
		p := body(t, h)
		if h.URL.Path != "/v2/video_regeneration" || p["source_task_id"] != nil || p["resolution"] != "2K" || p["model"] != "MiniMax-H3" {
			t.Fatal(p)
		}
		content := p["content"].([]any)
		if content[len(content)-1].(map[string]any)["role"] != "base_video" || content[0].(map[string]any)["text"] != "The exact final original prompt" {
			t.Fatal(content)
		}
		return response(200, map[string]string{"task_id": "456"}), nil
	})
	r := baseRequest()
	r.Options.VideoURLs = append(r.Options.VideoURLs, "https://media.example/original-reference.mp4")
	r.Options.ReferenceMediaEvidence[r.Options.VideoURLs[1]] = core.InputMediaEvidence{Type: "video", Duration: 4, AudioStreams: new(int)}
	quote, _, err := a.Quote(owned("alice"), baseRegenerationModel(), r)
	if err != nil || quote.ExpectedUSD <= 0 || len(quote.Pricing.Components) != 3 {
		t.Fatal(quote, err)
	}
	if quote.Pricing.Components[0].EstimatedUnits != 124.0/24 || quote.Pricing.Components[1].EstimatedUnits != 4 {
		t.Fatal(quote)
	}
	task, err := a.Generate(owned("alice"), r)
	if err != nil || task.ID == "" || calls != 1 {
		t.Fatal(task, err, calls)
	}
	h, err := a.open(owned("alice"), task.ID)
	if err != nil || h.Kind != "regeneration" || h.Model != "MiniMax-H3" {
		t.Fatal(h, err)
	}
}
func TestBaseVideoInspectionBoundariesAndUnknownEvidence(t *testing.T) {
	a := fixture(t, func(*http.Request) (*http.Response, error) { t.Fatal("invalid base reached HTTP"); return nil, nil })
	cases := map[string]func(*core.InputMediaEvidence){"gateway bytes": func(e *core.InputMediaEvidence) { e.SizeBytes = maxBaseVideoBytes }, "above gateway bytes": func(e *core.InputMediaEvidence) { e.SizeBytes = maxBaseVideoBytes + 1 }, "unknown fps": func(e *core.InputMediaEvidence) { e.FPS = math.NaN() }, "infinite fps": func(e *core.InputMediaEvidence) { e.FPS = math.Inf(1) }, "unknown audio": func(e *core.InputMediaEvidence) { e.AudioStreams = nil }, "silent": func(e *core.InputMediaEvidence) { n := 0; e.AudioStreams = &n }, "unknownvideo": func(e *core.InputMediaEvidence) { e.VideoStreams = nil }, "fps": func(e *core.InputMediaEvidence) { e.FPS = 23.976 }, "modulus": func(e *core.InputMediaEvidence) { e.Width = 769 }, "smallarea": func(e *core.InputMediaEvidence) { e.Width = 512; e.Height = 512 }, "largearea": func(e *core.InputMediaEvidence) { e.Width = 1344; e.Height = 1344 }, "framestep": func(e *core.InputMediaEvidence) { e.FrameCount = 125 }, "frameslow": func(e *core.InputMediaEvidence) { e.FrameCount = 106 }, "frameshigh": func(e *core.InputMediaEvidence) { e.FrameCount = 363 }, "unknownhash": func(e *core.InputMediaEvidence) { e.SHA256 = "" }, "unknownreference": func(e *core.InputMediaEvidence) { e.ReferenceID = "" }, "duration": func(e *core.InputMediaEvidence) { e.Duration = math.NaN() }, "tiny duration": func(e *core.InputMediaEvidence) { e.Duration = 0.01 }, "huge duration": func(e *core.InputMediaEvidence) { e.Duration = 1000 }, "wrongtype": func(e *core.InputMediaEvidence) { e.ContentType = "video/quicktime" }}
	for label, change := range cases {
		t.Run(label, func(t *testing.T) {
			r := baseRequest()
			u := r.Options.Videos()[0]
			e := r.Options.ReferenceMediaEvidence[u]
			change(&e)
			r.Options.ReferenceMediaEvidence[u] = e
			if err := a.ValidateRequest(owned("alice"), r); err == nil {
				t.Fatal("invalid evidence accepted")
			}
			if _, err := a.Generate(owned("alice"), r); err == nil {
				t.Fatal("invalid evidence submitted")
			}
		})
	}
	for _, frames := range []int64{107, 362} {
		r := baseRequest()
		u := r.Options.Videos()[0]
		e := r.Options.ReferenceMediaEvidence[u]
		e.FrameCount = frames
		e.Duration = float64(frames) / 24
		r.Options.ReferenceMediaEvidence[u] = e
		if err := a.ValidateRequest(owned("alice"), r); err != nil {
			t.Fatal(frames, err)
		}
	}
	r := baseRequest()
	u := r.Options.Videos()[0]
	e := r.Options.ReferenceMediaEvidence[u]
	e.SizeBytes = maxBaseVideoBytes - 1
	r.Options.ReferenceMediaEvidence[u] = e
	if err := a.ValidateRequest(owned("alice"), r); err != nil {
		t.Fatal("below Gateway byte ceiling rejected", err)
	}
	r = baseRequest()
	r.Options.ReferenceRoles = nil
	if err := a.ValidateRequest(owned("alice"), r); err == nil {
		t.Fatal("missing base role accepted")
	}
}
func TestBaseVideoOriginalRoleAndPromptContracts(t *testing.T) {
	a := fixture(t, nil)
	r := baseRequest()
	r.Prompt = strings.Repeat("x", 40000)
	if err := a.ValidateRequest(owned("alice"), r); err != nil {
		t.Fatal(err)
	}
	expanded := r
	expanded.Prompt = strings.Repeat("@image1 ", 5000)
	if err := a.ValidateRequest(owned("alice"), expanded); err == nil {
		t.Fatal("normalized prompt exceeds native character limit")
	}
	r.Prompt += "x"
	if err := a.ValidateRequest(owned("alice"), r); err == nil {
		t.Fatal("overlong prompt")
	}
	r = baseRequest()
	r.Options.ImageURLs = []string{"https://media.example/first.png"}
	r.Options.ReferenceRoles["image_1"] = "first_frame"
	if err := a.ValidateRequest(owned("alice"), r); err != nil {
		t.Fatal(err)
	}
	r.Options.ReferenceRoles["image_1"] = "last_frame"
	if err := a.ValidateRequest(owned("alice"), r); err == nil {
		t.Fatal("last frame without original first frame accepted")
	}
	r.Options.ReferenceRoles["image_1"] = "first_frame"
	r.Options.AudioURLs = []string{"https://media.example/ref.mp3"}
	if err := a.ValidateRequest(owned("alice"), r); err == nil {
		t.Fatal("mixed original frame/reference modes")
	}
	// PromptPolicy must expose the same reviewed ceiling as native validation.
	m := baseRegenerationModel()
	if m.EffectivePromptCharacterLimit() != 40000 {
		t.Fatalf("shared reviewed prompt ceiling still blocks documented40000characters: effective=%d", m.EffectivePromptCharacterLimit())
	}
	if err := core.ValidatePromptPolicy(m, strings.Repeat("x", 40000)); err != nil {
		t.Fatal(err)
	}
}

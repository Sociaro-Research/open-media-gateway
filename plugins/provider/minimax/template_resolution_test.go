package minimax

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func TestH3TemplateNearestResolutionNativeRequestAndTariff(t *testing.T) {
	m, _ := modelFor("MiniMax-H3")
	d := templates.Definition{MediaType: "video", Prompt: "@Image1 follows @Video1", Syntax: templates.UIReferenceSyntax, Options: core.MediaOptions{ReferenceMode: "references", Resolution: "720p", Duration: 10, Ratio: "9:16"}}
	d.Slots, _ = d.InferSlots(m)
	if err := d.Compatible(m); err != nil {
		t.Fatal(err)
	}
	o, err := d.OptionsFor(m)
	if err != nil || o.Resolution != "768p" || d.Options.Resolution != "720p" {
		t.Fatal("wrong effective/persisted resolution", err)
	}
	u := "https://media.example/video.mp4"
	o.ImageURLs = []string{"https://media.example/image.png"}
	o.VideoURLs = []string{u}
	o.ReferenceMediaEvidence = map[string]core.InputMediaEvidence{u: {Type: "video", Duration: 4, AudioStreams: new(int)}}
	calls := 0
	a := fixture(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v2/video_generation" {
			t.Fatal(r.Method, r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["resolution"] != "768P" || payload["ratio"] != "9:16" || payload["duration"] != float64(10) {
			t.Fatal(payload)
		}
		return response(200, success("12345678")), nil
	})
	r := core.MediaRequest{Model: m.ID, Prompt: d.Prompt, Options: o}
	quote, _, err := a.Quote(context.Background(), m, r)
	if err != nil || quote.ExpectedUSD != 1120000 || calls != 0 {
		t.Fatal("wrong 768p quote", quote, err, calls)
	}
	if _, err := a.Generate(owned("alice"), r); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	r.Options.Resolution = "2k"
	higher, _, err := a.Quote(context.Background(), m, r)
	if err != nil || higher.ExpectedUSD != 1820000 || higher.ExpectedUSD <= quote.ExpectedUSD {
		t.Fatal("2k tariff confused with 768p", higher, err)
	}
	r.Options.Resolution = "720p"
	if err := a.ValidateRequest(context.Background(), r); err == nil {
		t.Fatal("direct unsupported resolution accepted")
	}
	if calls != 1 {
		t.Fatal("quote/validation called provider")
	}
}

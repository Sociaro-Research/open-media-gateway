package kling

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func TestOmniTemplateOptionalAudioSerialization(t *testing.T) {
	for _, state := range []string{"absent", "off", "on"} {
		for _, replacement := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/replacement=%v", state, replacement), func(t *testing.T) {
				m := omni4Model()
				d := templates.Definition{Title: "Synthetic", MediaType: "video", Prompt: "@Image1 follows @Video1", Syntax: templates.UIReferenceSyntax, Options: core.MediaOptions{ReferenceMode: "references", Duration: 10, Ratio: "9:16", Resolution: "720p"}}
				if state != "absent" {
					d.Options.GenerateAudio = ptr(state == "on")
				}
				if replacement {
					d.OverrideSoundID = "resolved-by-host"
				}
				d.Slots, _ = d.InferSlots(m)
				if err := d.Compatible(m); err != nil {
					t.Fatal(err)
				}
				o, err := d.OptionsFor(m)
				if err != nil || o.GenerateAudio != nil {
					t.Fatal("template preference not omitted", err)
				}
				o.ImageURLs = []string{"https://media.example/image.png"}
				o.VideoURLs = []string{"https://media.example/video.mp4"}
				calls := 0
				a := adapter(t, func(r *http.Request) (*http.Response, error) {
					calls++
					if r.Method != "POST" || r.URL.Path != "/omni-video/kling-4.0" {
						t.Fatal(r.Method, r.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					settings := body["settings"].(map[string]any)
					for _, key := range []string{"generate_audio", "audio", "sound", "override_sound_id"} {
						if body[key] != nil || settings[key] != nil {
							t.Fatal("unsupported audio field serialized", key)
						}
					}
					return response(200, `{"code":0,"data":{"id":"synthetic-omni","status":"submitted"}}`), nil
				})
				r := core.MediaRequest{Model: m.ID, Prompt: d.Prompt, Options: o}
				if _, err := a.Generate(context.Background(), r); err != nil || calls != 1 {
					t.Fatal("template native request", err, calls)
				}
				for _, value := range []bool{false, true} {
					r.Options.GenerateAudio = &value
					if err := a.ValidateRequest(context.Background(), r); err == nil {
						t.Fatal("direct unsupported audio flag accepted", value)
					}
				}
				if calls != 1 {
					t.Fatal("direct invalid request invoked provider")
				}
			})
		}
	}
}

package templates

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestTemplateAudioPreferenceIsOptionalForTarget(t *testing.T) {
	for _, target := range []string{"toggle", "unsupported", "always-on"} {
		for _, state := range []string{"absent", "off", "on"} {
			for _, override := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/override=%v", target, state, override), func(t *testing.T) {
					m := danceModel()
					m.SupportsAudio = target != "unsupported"
					m.AudioAlwaysOn = target == "always-on"
					d := Definition{Title: "Optional audio", MediaType: "video", Prompt: "Image 1 follows Video 1", Syntax: "numbered", Options: core.MediaOptions{ReferenceMode: "references"}}
					if state != "absent" {
						value := state == "on"
						d.Options.GenerateAudio = &value
					}
					if override {
						d.OverrideSoundID = "replacement"
					}
					d.Slots, _ = d.InferSlots(m)
					before, _ := json.Marshal(d)
					if err := d.Compatible(m); err != nil {
						t.Fatal("audio preference filtered target", err)
					}
					o, err := d.OptionsFor(m)
					if err != nil {
						t.Fatal(err)
					}
					var want *bool
					if target == "toggle" {
						want = d.Options.GenerateAudio
					}
					if !reflect.DeepEqual(o.GenerateAudio, want) {
						t.Fatalf("effective audio=%v want %v", o.GenerateAudio, want)
					}
					after, _ := json.Marshal(d)
					if string(before) != string(after) {
						t.Fatal("target adaptation changed saved preference")
					}
					d.Options.Ratio = "unsupported-ratio"
					if d.Compatible(m) == nil {
						t.Fatal("audio adaptation bypassed other options")
					}
				})
			}
		}
	}
}

package kling

import (
	"context"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
	"testing"
)

func TestPromptCharacterLimitsNativeModelsAndModes(t *testing.T) {
	for _, m := range catalog() {
		if isSessionOperation(m.ID) || isLegacyOmniOperation(m.ID) || isResourceGenerationOperation(m.ID) || isAuxiliaryOperation(m.ID) || isImageOperation(m.ID) || isAudioOperation(m.ID) || isMotionOperation(m.ID) || isSolutionOperation(m.ID) {
			continue
		} // These families have dedicated input/parameter boundary fixtures.
		limit := 3072
		if isKling4(m.ID) {
			limit = 10000
		}
		if m.ID == ImageToVideo3Turbo || m.ID == TextToVideo25Turbo || m.ID == ImageToVideo25Turbo {
			limit = 2500
		}
		if m.ID == OmniVideo4 {
			limit = 26000
		}
		if m.MaxPromptCharacters != limit {
			t.Fatal(m.ID, m.MaxPromptCharacters)
		}
		for _, mode := range m.ReferenceModes {
			t.Run(m.ID+"/"+mode.ID, func(t *testing.T) {
				unicodeLimit := limit
				if m.ID == OmniVideo4 {
					unicodeLimit = 11000
				}
				r := core.MediaRequest{Model: m.ID, Prompt: strings.Repeat("😀", unicodeLimit), Options: core.MediaOptions{ReferenceMode: mode.ID}}
				if m.ID == ImageToVideo25Turbo && mode.ID == "first_last_frame" {
					r.Options.Resolution = "1080p"
				}
				for range mode.MinImages {
					r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/frame.png")
				}
				a := &Adapter{}
				if err := a.ValidateRequest(context.Background(), r); err != nil {
					t.Fatal("Unicode boundary rejected", err)
				}
				r.Prompt += "界"
				if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) && !errors.Is(err, core.ErrPromptTooLong) {
					t.Fatal("excess prompt accepted", err)
				}
			})
		}
	}
}

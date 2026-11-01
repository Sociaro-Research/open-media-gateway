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
		limit := 3072
		if isKling4(m.ID) {
			limit = 10000
		}
		if m.MaxPromptCharacters != limit {
			t.Fatal(m.ID, m.MaxPromptCharacters)
		}
		for _, mode := range m.ReferenceModes {
			t.Run(m.ID+"/"+mode.ID, func(t *testing.T) {
				r := core.MediaRequest{Model: m.ID, Prompt: strings.Repeat("😀", limit), Options: core.MediaOptions{ReferenceMode: mode.ID}}
				for range mode.MinImages {
					r.Options.ImageURLs = append(r.Options.ImageURLs, "https://cdn.example/frame.png")
				}
				a := &Adapter{}
				if err := a.ValidateRequest(context.Background(), r); err != nil {
					t.Fatal("Unicode boundary rejected", err)
				}
				r.Prompt += "界"
				if err := a.ValidateRequest(context.Background(), r); !errors.Is(err, core.ErrOptionsInvalid) {
					t.Fatal("excess prompt accepted", err)
				}
			})
		}
	}
}

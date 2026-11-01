package agnes

import (
	"context"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
)

func TestZeroReferenceTemplatesRejectWithoutChangingOrdinaryTextWorkflow(t *testing.T) {
	for _, id := range []string{"agnes-video-2.5", "agnes-video-2.5-flash"} {
		t.Run(id, func(t *testing.T) {
			model := describeModel(id)
			// Templates no longer map an empty recipe to a different native workflow.
			// Ordinary text generation retains its existing adapter validation.
			d := templates.Definition{Title: "Landscape", MediaType: "video", Prompt: "A calm landscape", Syntax: "numbered", Slots: []templates.Slot{}, Options: core.MediaOptions{ReferenceMode: "references"}}
			if err := d.Compatible(model); err == nil {
				t.Fatal("empty template accepted")
			}
			if _, err := d.OptionsFor(model); err == nil {
				t.Fatal("empty template mapped to another mode")
			}
			options := d.Options
			options.ReferenceMode = "none"
			if err := (&Adapter{}).ValidateRequest(context.Background(), core.MediaRequest{Model: id, Prompt: d.Prompt, Options: options}); err != nil {
				t.Fatal("ordinary text generation failed native admission", err)
			}
		})
	}
}

package templates

import (
	"fmt"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// AuthoringMode limits Templates to the explicit references workflow. Frame,
// continuation and text workflows retain their ordinary generation contracts.
// Model identity and display labels never determine eligibility.
func AuthoringMode(model core.MediaModel, id string) (core.ReferenceMode, error) {
	if id == "references" && !model.NoPrompt && (model.MediaType == "image" || model.MediaType == "video") {
		for _, mode := range core.ReferenceModesFor(model) {
			if mode.ID == id && mode.MaxImages+mode.MaxVideos+mode.MaxAudios > 0 {
				return mode, nil
			}
		}
	}
	return core.ReferenceMode{}, fmt.Errorf("%w: templates require a model with the References workflow", ErrInvalid)
}

// WorkflowError also gates legacy/imported recipes without rewriting them.
func (d Definition) WorkflowError() error {
	if d.Options.ReferenceMode != "references" || len(d.Slots) == 0 {
		return fmt.Errorf("%w: templates require References mode and at least one reviewed reference slot; an Administrator must explicitly revise and republish this recipe", ErrInvalid)
	}
	return nil
}

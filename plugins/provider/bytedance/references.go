package bytedance

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
)

func referenceModes(m core.MediaModel) []core.ReferenceMode {
	modes := core.ReferenceModesFor(m)
	if len(modes) == 0 || m.MediaType != "video" {
		return modes
	}
	base := modes[0]
	id := m.ID
	frame := core.ReferenceMode{ID: "first_frame", Label: "First frame", MinImages: 1, MaxImages: 1, MaxAudios: base.MaxAudios, ImageRoles: []string{"First frame"}}
	frames := frame
	frames.ID = "first_last_frames"
	frames.Label = "First and last frames"
	frames.MinImages = 2
	frames.MaxImages = 2
	frames.ImageRoles = []string{"First frame", "Last frame"}
	modes = nil
	switch {
	case strings.HasPrefix(id, "seedance-1-"):
		if !m.RequiresImageInput {
			modes = append(modes, core.ReferenceMode{ID: "none", Label: "Text only"})
		}
		modes = append(modes, frame)
		if m.MaxImageInputs > 1 {
			modes = append(modes, frames)
		}
		if strings.Contains(id, "lite-i2v") {
			base.MinImages = 1
			base.MaxImages = 4
			modes = append(modes, base)
		}
	case strings.HasPrefix(id, "dreamina-seedance-2-"):
		frame.MaxAudios = 0
		frames.MaxAudios = 0
		base.MaxVideos = 3
		base.MaxAudios = 3
		base.RequiresVisual = !strings.Contains(id, "2-5")
		if strings.Contains(id, "2-5") {
			base.MaxAudios = 10
			base.MaxVideos = 10
		}
		modes = []core.ReferenceMode{{ID: "none", Label: "Text only"}, frame, frames, base}
	default:
		modes = []core.ReferenceMode{base}
	}
	return modes
}

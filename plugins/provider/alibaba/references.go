package alibaba

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
	case strings.HasPrefix(id, "wan3.0-"):
		frame.MaxAudios = 0
		frames.MaxAudios = 0
		base.MaxVideos = 5
		base.MaxAudios = 5
		base.MaxImages = 10
		modes = []core.ReferenceMode{{ID: "none", Label: "Text only"}, frame, frames, base}
	case strings.HasPrefix(id, "wan2.7-i2v"):
		clip := core.ReferenceMode{ID: "continue_video", Label: "Continue video", MinVideos: 1, MaxVideos: 1, MaxAudios: 1}
		end := clip
		end.ID = "continue_video_last_frame"
		end.Label = "Continue video to last frame"
		end.MinImages = 1
		end.MaxImages = 1
		end.ImageRoles = []string{"Last frame"}
		modes = []core.ReferenceMode{frame, frames, clip, end}
	case strings.Contains(id, "-kf2v"):
		modes = []core.ReferenceMode{frames}
	case strings.Contains(id, "-i2v"), strings.Contains(id, "img2video"):
		modes = []core.ReferenceMode{frame}
	case strings.Contains(id, "-r2v"):
		base.RequiresVisual = true
		base.MinVisuals = 1
		if strings.HasPrefix(id, "wan2.") {
			base.MaxVideos = 5
			base.MaxVisuals = 5
			base.MaxAudios = 0
		}
		modes = []core.ReferenceMode{base}
		if strings.HasPrefix(id, "wan2.7-") {
			base.SupportsReferenceVoice = true
			base.MaxDurationWithVideo = 10
			modes[0] = base
			start := base
			start.ID = "first_frame_references"
			start.Label = "First frame and references"
			start.MinImages = 1
			start.MinVisuals = 2
			start.VoiceImageStart = 1
			start.MaxImages = 6
			start.MaxVisuals = 6
			start.ImageRoles = []string{"First frame"}
			modes = append(modes, start)
		}
	case strings.Contains(id, "animate"):
		base.Label = "Character image and driving video"
		base.ImageRoles = []string{"Character image"}
		modes = []core.ReferenceMode{base}
	case strings.Contains(id, "videoedit") || strings.Contains(id, "video-edit"):
		base.Label = "Source video and reference images"
		modes = []core.ReferenceMode{base}
	default:
		modes = []core.ReferenceMode{base}
	}
	return modes
}

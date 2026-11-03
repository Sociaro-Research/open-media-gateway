package templates

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"strings"
	"testing"
)

func TestReferenceCountWarningUsesSelectedLimitsAndAllSlots(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		images, videos, audios int
		mode                   core.ReferenceMode
		total                  int
		want                   []string
	}{
		{"fixed and unfilled images", 3, 0, 0, core.ReferenceMode{MaxImages: 2}, 0, []string{"3 image slots", "maximum 2"}},
		{"video", 0, 3, 0, core.ReferenceMode{MaxVideos: 2}, 0, []string{"3 video slots", "maximum 2"}},
		{"audio", 0, 0, 2, core.ReferenceMode{MaxAudios: 1}, 0, []string{"2 audio slots", "maximum 1"}},
		{"combined visuals", 2, 2, 0, core.ReferenceMode{MaxImages: 3, MaxVideos: 3, MaxVisuals: 3}, 0, []string{"4 image and video slots combined", "maximum 3"}},
		{"total files", 2, 1, 1, core.ReferenceMode{MaxImages: 2, MaxVideos: 1, MaxAudios: 1}, 3, []string{"4 reference slots in total", "maximum 3"}},
		{"exact limits", 2, 1, 1, core.ReferenceMode{MaxImages: 2, MaxVideos: 1, MaxAudios: 1, MaxVisuals: 3}, 4, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.mode.ID = "references"
			model := core.MediaModel{ReferenceModes: []core.ReferenceMode{tc.mode}}
			if tc.total > 0 {
				model.ReferenceMediaLimits = &core.ReferenceMediaLimits{MaxFiles: tc.total}
			}
			d := Definition{Options: core.MediaOptions{ReferenceMode: "references"}}
			for _, kind := range []struct {
				name string
				n    int
			}{{"image", tc.images}, {"video", tc.videos}, {"audio", tc.audios}} {
				for i := 0; i < kind.n; i++ {
					s := Slot{Type: kind.name, Index: i + 1}
					if i == 0 {
						s.AssetID = "fixed"
					}
					d.Slots = append(d.Slots, s)
				}
			}
			warning := d.ReferenceCountWarning(model)
			if tc.want == nil && warning != "" {
				t.Fatal(warning)
			}
			for _, text := range tc.want {
				if !strings.Contains(warning, text) {
					t.Fatal(warning, text)
				}
			}
		})
	}
}

package api

import (
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestVideoSizeMapsUHDToTheModel4K(t *testing.T) {
	model := core.MediaModel{Resolutions: []string{"1080p", "720p", "4k"}, Ratios: []string{"16:9", "9:16", "1:1"}}
	for size, want := range map[string][2]string{
		"3840x2160": {"4k", "16:9"}, "2160x3840": {"4k", "9:16"}, "1920x1080": {"1080p", "16:9"}, "4k": {"4k", ""},
	} {
		resolution, ratio, err := modelSize(model, size, "video")
		if err != nil || resolution != want[0] || ratio != want[1] {
			t.Errorf("%s: got %q %q %v, want %q %q", size, resolution, ratio, err, want[0], want[1])
		}
	}
	// Without a 4k choice the request is still refused instead of picking another resolution.
	if _, _, err := modelSize(core.MediaModel{Resolutions: []string{"1080p"}, Ratios: []string{"16:9"}}, "3840x2160", "video"); err == nil {
		t.Error("3840x2160 accepted by a 1080p-only model")
	}
}

package templates_test

import (
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
)

func TestWan3InspectedVideoAndOutputDuration(t *testing.T) {
	count := 0
	for _, model := range alibaba.Definition().Profiles() {
		if model.ID != "wan3.0-video" && model.ID != "wan3.0-video-prime" {
			continue
		}
		count++
		t.Run(model.ID, func(t *testing.T) {
			if model.ReferenceMediaLimits.MaxVideoAndOutputDuration != 30 {
				t.Fatal("combined native limit missing")
			}
			for _, tc := range []struct {
				name              string
				output            int
				videos            []float64
				complete, invalid bool
			}{
				{"boundary", 15, []float64{15}, true, false},
				{"over", 16, []float64{15}, true, true},
				{"multi-boundary", 20, []float64{4, 6}, true, false},
				{"multi-over", 21, []float64{4, 6}, true, true},
				{"fractional-over", 25, []float64{5.01}, true, true},
				{"partial-known-over", 26, []float64{5}, false, true},
				{"partial-known-allowed", 25, []float64{5}, false, false},
				{"default", 0, []float64{15}, true, false},
				{"no-video", 30, nil, true, false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var files []templates.InputMedia
					for _, duration := range tc.videos {
						files = append(files, templates.InputMedia{Type: "video", Info: mediafile.Info{ContentType: "video/mp4", Size: 100, Width: 640, Height: 480, FPS: 30, Duration: duration}})
					}
					err := templates.ValidateRequestFiles(model, core.MediaOptions{Duration: tc.output}, files, tc.complete)
					if (err != nil) != tc.invalid {
						t.Fatalf("output=%d videos=%v err=%v", tc.output, tc.videos, err)
					}
				})
			}
			clone := model
			clone.ReferenceMediaLimits = model.ReferenceMediaLimits.Clone()
			clone.ReferenceMediaLimits.MaxVideoAndOutputDuration = 0
			files := []templates.InputMedia{{Type: "video", Info: mediafile.Info{ContentType: "video/mp4", Size: 100, Width: 640, Height: 480, FPS: 30, Duration: 15}}}
			if err := templates.ValidateRequestFiles(clone, core.MediaOptions{Duration: 30}, files, true); err != nil {
				t.Fatal("unspecified combined constraint changed", err)
			}
			clone.ReferenceMediaLimits = nil
			if err := templates.ValidateRequestFiles(clone, core.MediaOptions{Duration: 30}, files, true); err != nil {
				t.Fatal("optional missing metadata excluded", err)
			}
			clone = model
			clone.DefaultDuration = 20
			if err := templates.ValidateRequestFiles(clone, core.MediaOptions{}, files, true); err == nil {
				t.Fatal("default output duration ignored")
			}
		})
	}
	if count != 2 {
		t.Fatalf("expected both Wan 3 variants, got %d", count)
	}
}

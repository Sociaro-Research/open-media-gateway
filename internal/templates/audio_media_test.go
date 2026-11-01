package templates_test

import (
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/templates"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/alibaba"
)

func TestWAVAliasesPreserveModelAudioLimits(t *testing.T) {
	models := 0
	for _, model := range alibaba.Definition().Profiles() {
		if model.ID != "wan3.0-video" && model.ID != "wan3.0-video-prime" {
			continue
		}
		models++
		for _, kind := range []string{"audio/wave", "audio/wav", "audio/x-wav"} {
			t.Run(model.ID+"/"+kind, func(t *testing.T) {
				for _, tc := range []struct {
					name      string
					size      int64
					durations []float64
					valid     bool
				}{
					{"minimum", 100, []float64{1}, true},
					{"maximum", 15_000_000, []float64{15}, true},
					{"oversized", 15_000_001, []float64{2}, false},
					{"short", 100, []float64{.99}, false},
					{"long", 100, []float64{15.01}, false},
					{"total-boundary", 100, []float64{7, 8}, true},
					{"total-exceeded", 100, []float64{8, 8}, false},
				} {
					t.Run(tc.name, func(t *testing.T) {
						files := []templates.InputMedia{}
						for _, duration := range tc.durations {
							files = append(files, templates.InputMedia{Type: "audio", Info: mediafile.Info{ContentType: kind, Size: tc.size, Duration: duration}})
						}
						if err := templates.ValidateRequestFiles(model, core.MediaOptions{Duration: 5}, files, true); (err == nil) != tc.valid {
							t.Fatal(err)
						}
					})
				}
			})
		}
	}
	if models != 2 {
		t.Fatal("missing native Wan3 profiles")
	}
}

func TestAudioMIMEEquivalenceDoesNotEnableOtherFormats(t *testing.T) {
	for _, allowed := range []string{"audio/wave", "audio/wav", "audio/x-wav", "audio/mpeg"} {
		model := core.MediaModel{ReferenceMediaLimits: &core.ReferenceMediaLimits{Audio: core.MediaFileLimits{ContentTypes: []string{allowed}}}}
		for _, kind := range []string{"audio/wave", "audio/wav", "audio/x-wav", "audio/mpeg", "audio/ogg", "audio/flac", "audio/x-pn-wav", "audio/wav; codecs=pcm"} {
			files := []templates.InputMedia{{Type: "audio", Info: mediafile.Info{ContentType: kind, Size: 100, Duration: 2}}}
			want := kind == allowed || allowed != "audio/mpeg" && (kind == "audio/wave" || kind == "audio/wav" || kind == "audio/x-wav")
			if err := templates.ValidateFiles(model, files, true); (err == nil) != want {
				t.Fatalf("allow=%s input=%s: %v", allowed, kind, err)
			}
		}
	}
}

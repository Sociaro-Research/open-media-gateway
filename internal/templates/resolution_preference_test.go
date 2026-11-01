package templates

import (
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func TestTemplateNearestResolutionPreference(t *testing.T) {
	for _, tc := range []struct {
		name, saved, want string
		choices           []string
	}{
		{"H3", "720p", "768p", []string{"768p", "2k"}},
		{"mixed order reversed", "720p", "768p", []string{"2k", "768p"}},
		{"mixed exact", "2k", "2k", []string{"768p", "2k"}},
		{"K order", "1.5K", "1K", []string{"1K", "2K"}},
		{"exact", "1080p", "1080p", []string{"720p", "1080p"}},
		{"case", "720P", "720p", []string{"1080p", "720p"}},
		{"numeric tie", "720p", "480p", []string{"960p", "480p"}},
		{"decimal K", "1.5K", "1K", []string{"2K", "1K"}},
		{"incomparable K", "720p", "2K", []string{"2K", "1K"}},
		{"unknown", "high", "768p", []string{"768p", "2k"}},
		{"absent", "", "1080p", []string{"1080p", "720p"}},
		{"no choices", "720p", "", nil},
		{"named exact", "auto", "auto", []string{"720p", "auto"}},
		{"dimensions", "1600x900", "1920x1080", []string{"1536x1024", "1920x1080", "1024x1536"}},
		{"portrait", "1000x1500", "1024x1536", []string{"1536x1024", "1024x1536"}},
		{"dimension alias", "1024x1024", "1024*1024", []string{"1024*1024", "2048x2048"}},
		{"dimension tie", "1500x1500", "1000x1000", []string{"2000x2000", "1000x1000"}},
		{"unknown candidate", "720p", "768p", []string{"native", "768p"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := danceModel()
			model.Resolutions = tc.choices
			d := Definition{MediaType: "video", Slots: []Slot{{ID: "image:1", Type: "image", Index: 1, Role: "reference"}}, Options: core.MediaOptions{ReferenceMode: "references", Resolution: tc.saved}}
			o, err := d.OptionsFor(model)
			if err != nil {
				t.Fatal("resolution filtered target", err)
			}
			if o.Resolution != tc.want {
				t.Fatalf("resolution=%q want %q", o.Resolution, tc.want)
			}
			if d.Options.Resolution != tc.saved {
				t.Fatal("saved preference mutated")
			}
		})
	}
}

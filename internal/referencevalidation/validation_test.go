package referencevalidation

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"strings"
	"testing"
)

func TestPreciseFileConstraintsAndUnspecifiedLimits(t *testing.T) {
	model := core.MediaModel{ID: "fixture", ReferenceMediaLimits: &core.ReferenceMediaLimits{Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4"}, Codecs: []string{"avc1"}, MinDuration: 2, MaxDuration: 30, MinFPS: 24, MaxFPS: 60, MinDimension: 300, MaxDimension: 6000, MaxBytes: 1000}}}
	original := File{Type: "video", Name: "motion.mp4", Info: mediafile.Info{ContentType: "video/mp4", Codec: "avc1", Size: 500, Width: 540, Height: 960, Duration: 10, FPS: 30}}
	for _, tc := range []struct {
		name, actual, allowed string
		change                func(*File)
	}{
		{"low FPS", "23.8041220744", "24–60 FPS", func(f *File) { f.Info.FPS = 23.804122074375176 }},
		{"high FPS", "61", "24–60 FPS", func(f *File) { f.Info.FPS = 61 }},
		{"short duration", "1.99", "2–30 seconds", func(f *File) { f.Info.Duration = 1.99 }},
		{"long duration", "30.01", "2–30 seconds", func(f *File) { f.Info.Duration = 30.01 }},
		{"size", "1000 bytes", "less than 1000", func(f *File) { f.Info.Size = 1000 }},
		{"dimensions", "200×960", "300–6000", func(f *File) { f.Info.Width = 200 }},
		{"codec", "vp09", "avc1", func(f *File) { f.Info.Codec = "vp09" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := original
			tc.change(&f)
			err := ValidateFiles(model, []File{f}, false)
			if err == nil || !strings.Contains(err.Error(), f.Name) || !strings.Contains(err.Error(), tc.actual) || !strings.Contains(err.Error(), tc.allowed) {
				t.Fatal(err)
			}
		})
	}
	original.Info.FPS = 10
	if err := ValidateFiles(core.MediaModel{}, []File{original}, true); err != nil {
		t.Fatal("universal FPS bound invented", err)
	}
}
func TestPartialModesDeferMinimaButNotMaxima(t *testing.T) {
	m := core.MediaModel{ID: "image", SupportsImageInput: true, ReferenceModes: []core.ReferenceMode{{ID: "keyframes", Label: "Keyframes", MinImages: 2, MaxImages: 2}}}
	f := File{Type: "image"}
	if err := ValidateMode(m, core.MediaOptions{ReferenceMode: "keyframes"}, []File{f}, false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMode(m, core.MediaOptions{ReferenceMode: "keyframes"}, []File{f}, true); err == nil {
		t.Fatal("minimum skipped for complete input")
	}
	if err := ValidateMode(m, core.MediaOptions{}, []File{f, f, f}, false); err == nil {
		t.Fatal("omitted mode bypassed maximum")
	}
}

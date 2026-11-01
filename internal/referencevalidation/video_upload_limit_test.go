package referencevalidation_test

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/referencevalidation"
	"github.com/Sociaro-Research/open-media-gateway/plugins/provider/kling"
	"testing"
)

func TestVideoUploadAdmissionBoundaries(t *testing.T) {
	var native core.MediaModel
	adapter, err := kling.New("fixture", "https://api-singapore.klingai.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range adapter.ReviewedCatalogModels() {
		if model.ID == "kling-4.0-omni-video" {
			native = model
		}
	}
	if native.ID == "" {
		t.Fatal("reviewed Kling 4 model missing")
	}
	manual := native
	manual.Manual = true
	cases := []struct {
		name    string
		model   core.MediaModel
		size    int64
		allowed bool
	}{
		{"ordinary default exact", core.MediaModel{}, 200 << 20, true},
		{"ordinary stored extended", core.MediaModel{}, 200<<20 + 1, false},
		{"manual stored extended", manual, 200<<20 + 1, false},
		{"reviewed exact decimal", native, 300000000, true},
		{"reviewed over decimal", native, 300000001, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := referencevalidation.ValidateFiles(tc.model, []referencevalidation.File{{Type: "video", Name: "synthetic.mp4", Info: mediafile.Info{ContentType: "video/mp4", Size: tc.size, Width: 1280, Height: 720, Duration: 5, FPS: 30}}}, false)
			if (err == nil) != tc.allowed {
				t.Fatalf("size=%d allowed=%t err=%v", tc.size, tc.allowed, err)
			}
		})
	}
}

func TestReviewedByteLimitOptInAndHardCeiling(t *testing.T) {
	for _, tc := range []struct {
		name      string
		manual    bool
		max       int64
		inclusive bool
		size      int64
		allowed   bool
	}{
		{"legacy exclusive unchanged", false, 1000, false, 1000, false},
		{"inclusive exact", false, 1000, true, 1000, true},
		{"inclusive over", false, 1000, true, 1001, false},
		{"reviewed extension exclusive", false, 300000000, false, 299999999, true},
		{"reviewed extension exclusive exact", false, 300000000, false, 300000000, false},
		{"native metadata cannot raise hard cap", false, 900000000, true, 300000001, false},
		{"manual cannot opt in", true, 300000000, true, 200<<20 + 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := core.MediaModel{Manual: tc.manual, ReferenceMediaLimits: &core.ReferenceMediaLimits{Video: core.MediaFileLimits{MaxBytes: tc.max, MaxBytesInclusive: tc.inclusive}}}
			err := referencevalidation.ValidateRequestFiles(model, core.MediaOptions{}, []referencevalidation.File{{Type: "video", Info: mediafile.Info{Size: tc.size}}}, false)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%t err=%v", tc.allowed, err)
			}
		})
	}
}

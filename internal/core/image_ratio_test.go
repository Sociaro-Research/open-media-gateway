package core_test

import (
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// An image ratio is checked like a video ratio, so it is never silently dropped.
func TestImageRatioIsCheckedAgainstTheModel(t *testing.T) {
	plain := core.MediaModel{ID: "plain-image", MediaType: "image"}
	ratios := core.MediaModel{ID: "ratio-image", MediaType: "image", Ratios: []string{"1:1", "16:9"}}
	for _, tc := range []struct {
		name  string
		model core.MediaModel
		ratio string
		hint  string
	}{
		{"model without ratios", plain, "16:9", "is not supported by this model; omit it"},
		{"ratio not advertised", ratios, "4:3", "must be one of: 1:1, 16:9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := core.NormalizeModelOptions(tc.model, core.MediaOptions{Ratio: tc.ratio})
			var option *core.OptionValidationError
			if !errors.As(err, &option) || !errors.Is(err, core.ErrOptionsInvalid) || option.Field != "ratio" || option.Hint != tc.hint {
				t.Fatalf("got %v", err)
			}
		})
	}
	// A reference mode with its own ratios overrides the model's, as for video.
	moded := core.MediaModel{ID: "mode-image", MediaType: "image", Ratios: []string{"1:1"}, SupportsImageInput: true,
		ReferenceModes: []core.ReferenceMode{{ID: "wide", Label: "Wide", MaxImages: 1, Ratios: []string{"21:9"}}}}
	if got, err := core.NormalizeModelOptions(moded, core.MediaOptions{ReferenceMode: "wide", Ratio: "21:9"}); err != nil || got.Ratio != "21:9" {
		t.Fatalf("mode ratio: %v %q", err, got.Ratio)
	}
	_, err := core.NormalizeModelOptions(moded, core.MediaOptions{ReferenceMode: "wide", Ratio: "1:1"})
	if option := (*core.OptionValidationError)(nil); !errors.As(err, &option) || option.Hint != "must be one of: 21:9" {
		t.Fatalf("base ratio in a mode with its own ratios: %v", err)
	}
	for _, ok := range []struct {
		model core.MediaModel
		ratio string
	}{{plain, ""}, {ratios, ""}, {ratios, "16:9"}} {
		if got, err := core.NormalizeModelOptions(ok.model, core.MediaOptions{Ratio: ok.ratio}); err != nil || got.Ratio != ok.ratio {
			t.Fatalf("%s %q: %v %q", ok.model.ID, ok.ratio, err, got.Ratio)
		}
	}
}

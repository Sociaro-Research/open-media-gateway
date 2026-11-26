package core

import (
	"errors"
	"testing"
)

// A music or audio model without a duration range has no duration to set, so a
// duration is rejected instead of being dropped by the provider (Alibaba Fun-Music).
func TestAudioDurationWithoutRangeIsRejected(t *testing.T) {
	s := &TaskService{}
	for _, kind := range []string{"music", "audio"} {
		m := MediaModel{MediaType: kind}
		if o, err := s.normalizeOptions(m, MediaOptions{}); err != nil || o.Duration != 0 {
			t.Fatalf("%s without duration: %+v, %v", kind, o, err)
		}
		for _, d := range []int{30, -1} {
			_, err := s.normalizeOptions(m, MediaOptions{Duration: d})
			var option *OptionValidationError
			if !errors.As(err, &option) || option.Field != "duration" || option.Hint != "is not supported by this model; omit it" {
				t.Fatalf("%s accepted duration %d without a range: %v", kind, d, err)
			}
		}
	}
}

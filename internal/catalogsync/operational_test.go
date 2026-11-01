package catalogsync

import (
	"math"
	"testing"
)

func TestVideoDurationSumBoundary(t *testing.T) {
	limit := VideoDurationSumLimit{MaxSeconds: 30}
	for _, tc := range []struct {
		input, output float64
		allowed       bool
	}{
		{15, 15, true}, {15, 15.001, false}, {0, 30, true}, {0, 30.001, false},
		{-1, 1, false}, {1, 0, false}, {math.NaN(), 1, false}, {1, math.Inf(1), false},
	} {
		if got := limit.Allows(tc.input, tc.output); got != tc.allowed {
			t.Errorf("%v+%v: %v, want %v", tc.input, tc.output, got, tc.allowed)
		}
	}
	for _, maximum := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if (VideoDurationSumLimit{MaxSeconds: maximum}).Allows(1, 1) {
			t.Fatal("invalid maximum accepted", maximum)
		}
	}
	field := "workflow.references.video.input_output_duration"
	if !compatibleOperationalField(field, encoded(videoDurationEnvelope{15, 15}), encoded(limit)) {
		t.Fatal("exact safe envelope rejected")
	}
	if compatibleOperationalField(field, encoded(videoDurationEnvelope{15, 16}), encoded(limit)) {
		t.Fatal("independent maxima exceed documented sum")
	}
	if compatibleOperationalField(field, encoded(videoDurationEnvelope{0, 15}), encoded(limit)) {
		t.Fatal("unknown input maximum verified")
	}
}

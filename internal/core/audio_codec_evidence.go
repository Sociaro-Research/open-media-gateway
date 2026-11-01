package core

import (
	"reflect"
	"slices"
)

// CloneAudioCodecs preserves stream order and explicit unknown entries. The
// AudioStreams count distinguishes unknown inventory from established silence.
func CloneAudioCodecs(in []string) []string { return slices.Clone(in) }

// AudioCodecsAllowed is opt-in: undeclared constraints retain legacy behavior.
// A positive stream count with missing/unknown codec evidence never proves that
// embedded audio uses an allowed codec. Zero streams is established silence.
func AudioCodecsAllowed(streams *int, codecs, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	if streams == nil || *streams < 0 || len(codecs) != *streams {
		return false
	}
	for _, codec := range codecs {
		if codec == "" || !slices.Contains(allowed, codec) {
			return false
		}
	}
	return true
}

// EqualInputMediaEvidence compares values, not the addresses of optional count
// pointers. Empty codec arrays and nil are equivalent; the count retains the
// unknown-versus-zero distinction through JSON's omitted empty arrays.
func EqualInputMediaEvidence(a, b InputMediaEvidence) bool {
	codecsEqual := slices.Equal(a.AudioCodecs, b.AudioCodecs)
	a.AudioCodecs = nil
	b.AudioCodecs = nil
	return codecsEqual && reflect.DeepEqual(a, b)
}

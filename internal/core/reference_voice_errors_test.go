package core_test

import (
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

// Each reference_voices rejection names the field instead of the bare options error.
func TestReferenceVoiceErrorsNameTheField(t *testing.T) {
	model := func(voices bool) core.MediaModel {
		return core.MediaModel{ID: "voices", MediaType: "video", SupportsImageInput: true, MaxImageInputs: 2, ReferenceModes: []core.ReferenceMode{{ID: "references", Label: "References", MinImages: 1, MaxImages: 2, SupportsReferenceVoice: voices}}}
	}
	image := "https://example.test/subject.png"
	for _, tc := range []struct {
		name   string
		voices bool
		input  map[string]string
		hint   string
	}{
		{"mode without voices", false, map[string]string{image: "https://example.test/voice.wav"}, "is not supported by the References reference mode"},
		{"voice for a reference not sent", true, map[string]string{"https://example.test/other.png": "https://example.test/voice.wav"}, "keys must be image or video references sent in this request, with a voice URL for each"},
		{"voice URL without scheme", true, map[string]string{image: "www.example.test/voice.wav"}, "values must be absolute http or https URLs without credentials"},
		{"voice URL with credentials", true, map[string]string{image: "https://user:secret@example.test/voice.wav"}, "values must be absolute http or https URLs without credentials"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := core.ValidateReferenceMode(model(tc.voices), core.MediaOptions{ReferenceMode: "references", ImageURLs: []string{image}, ReferenceVoices: tc.input})
			var option *core.OptionValidationError
			if !errors.As(err, &option) || !errors.Is(err, core.ErrOptionsInvalid) || option.Field != "reference_voices" || option.Hint != tc.hint {
				t.Fatalf("got %v", err)
			}
		})
	}
	if err := core.ValidateReferenceMode(model(true), core.MediaOptions{ReferenceMode: "references", ImageURLs: []string{image}, ReferenceVoices: map[string]string{image: "https://example.test/voice.wav"}}); err != nil {
		t.Fatal("valid voice rejected", err)
	}
}

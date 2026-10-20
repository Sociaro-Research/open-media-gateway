package alibaba

import (
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"testing"
)

func TestReferenceModeContracts(t *testing.T) {
	for _, tc := range []struct {
		name, id, mode         string
		images, videos, audios int
		ok                     bool
	}{
		{"wan3 frames", "wan3.0-video-prime", "first_last_frames", 2, 0, 0, true},
		{"wan3 rejects frame plus audio", "wan3.0-video-prime", "first_frame", 1, 0, 1, false},
		{"wan27 continuation", "wan2.7-i2v", "continue_video_last_frame", 1, 1, 1, true},
		{"wan26 one frame only", "wan2.6-i2v", "first_last_frames", 2, 0, 0, false},
		{"wan27 first plus references", "wan2.7-r2v", "first_frame_references", 1, 2, 0, true},
		{"wan27 missing references", "wan2.7-r2v", "first_frame_references", 1, 0, 0, false},
		{"wan26 combined cap", "wan2.6-r2v", "references", 3, 3, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := core.MediaModel{ID: tc.id, MediaType: "video", SupportsImageInput: true, MaxImageInputs: 30, SupportsVideoInput: true, SupportsAudioInput: true}
			m.ReferenceModes = referenceModes(m)
			o := core.MediaOptions{ReferenceMode: tc.mode, ImageURLs: make([]string, tc.images), VideoURLs: make([]string, tc.videos), AudioURLs: make([]string, tc.audios)}
			err := core.ValidateReferenceMode(m, o)
			if (err == nil) != tc.ok {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestNumberedImagePromptCapability(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{{"wan3.0-plus", true}, {"wan2.7-r2v", true}, {"wan2.7-i2v", false}, {"wan2.6-r2v", false}, {"happyhorse-1.0-r2v", false}} {
		m := core.MediaModel{ID: tc.id, MediaType: "video"}
		applyModelContract(&m)
		if got := m.ReferencePromptSyntax == "numbered"; got != tc.want {
			t.Fatalf("%s: %v", tc.id, got)
		}
	}
}

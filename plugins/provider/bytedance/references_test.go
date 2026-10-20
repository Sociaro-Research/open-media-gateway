package bytedance

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
		{"seedance15 first and last", "seedance-1-5-pro-251215", "first_last_frames", 2, 0, 0, true},
		{"seedance15 missing last", "seedance-1-5-pro-251215", "first_last_frames", 1, 0, 0, false},
		{"seedance15 rejects ordinary refs", "seedance-1-5-pro-251215", "references", 2, 0, 0, false},
		{"lite ordinary references", "seedance-1-0-lite-i2v-250428", "references", 4, 0, 0, true},
		{"seedance2 frames", "dreamina-seedance-2-0-260128", "first_last_frames", 2, 0, 0, true},
		{"seedance2 rejects mixed modes", "dreamina-seedance-2-0-260128", "first_frame", 1, 1, 0, false},
		{"seedance2 multiple clips", "dreamina-seedance-2-0-260128", "references", 2, 3, 3, true},
		{"seedance2 audio needs visuals", "dreamina-seedance-2-0-260128", "references", 0, 0, 1, false},
		{"seedance25 audio alone", "dreamina-seedance-2-5-260628", "references", 0, 0, 1, true},
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

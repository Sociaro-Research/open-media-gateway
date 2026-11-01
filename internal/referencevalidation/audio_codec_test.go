package referencevalidation

import (
	"encoding/binary"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
)

func TestEmbeddedAudioCodecModelConstraintIsOptInAndPerStream(t *testing.T) {
	model := core.MediaModel{ID: "reviewed-audio-constraint", ReferenceMediaLimits: &core.ReferenceMediaLimits{Video: core.MediaFileLimits{AllowedAudioCodecs: []string{"aac", "mp3"}}}}
	for _, tc := range []struct {
		n        int
		known    bool
		codecs   []string
		accepted bool
	}{{0, false, nil, false}, {0, true, nil, true}, {1, true, []string{"aac"}, true}, {1, true, []string{"mp3"}, true}, {2, true, []string{"aac", "mp3"}, true}, {1, true, nil, false}, {1, true, []string{""}, false}, {1, true, []string{"opus"}, false}, {2, true, []string{"aac"}, false}} {
		var streams *int
		if tc.known {
			v := tc.n
			streams = &v
		}
		file := File{Type: "video", Name: "local.mp4", Info: mediafile.Info{ContentType: "video/mp4", AudioStreams: streams, AudioCodecs: tc.codecs}}
		if err := ValidateFiles(model, []File{file}, false); (err == nil) != tc.accepted {
			t.Fatal(tc, err)
		}
		generic := model
		generic.ReferenceMediaLimits = nil
		if err := ValidateFiles(generic, []File{file}, false); err != nil {
			t.Fatal("generic support changed", err)
		}
	}
}

// Actual container bytes reach the same pure model validator used before native
// submission. These fixtures have no resolver/client/network dependency.
func TestAmbiguousMovieInventoryRejectsOnlyCodecConstrainedInputs(t *testing.T) {
	silent := testmedia.MP4(5000, 1000, 150)
	ftypSize := int(binary.BigEndian.Uint32(silent[:4]))
	ftyp := silent[:ftypSize]
	movie := silent[ftypSize+8:]
	constrained := core.MediaModel{ReferenceMediaLimits: &core.ReferenceMediaLimits{Video: core.MediaFileLimits{AllowedAudioCodecs: []string{"aac", "mp3"}}}}
	for _, variant := range []string{"silent-control", "duplicate-moov", "duplicate-mdia", "duplicate-hdlr", "truncated-hdlr", "missing-hdlr", "unknown-hdlr", "unsupported-hdlr-version", "mvex", "moof", "malformed-later-track"} {
		t.Run(variant, func(t *testing.T) {
			body := append([]byte(nil), movie...)
			switch variant {
			case "duplicate-mdia":
				body = testmedia.Atom("trak", append(append([]byte(nil), movie[8:]...), testmedia.Atom("mdia", nil)...))
			case "mvex":
				body = append(body, testmedia.Atom("mvex", nil)...)
			case "duplicate-hdlr", "truncated-hdlr", "missing-hdlr", "unknown-hdlr", "unsupported-hdlr-version":
				handler := make([]byte, 12)
				copy(handler[8:], "soun")
				metadata := testmedia.Atom("hdlr", handler)
				switch variant {
				case "duplicate-hdlr":
					metadata = append(metadata, testmedia.Atom("hdlr", handler)...)
				case "truncated-hdlr":
					metadata = testmedia.Atom("hdlr", []byte{0})
				case "missing-hdlr":
					metadata = nil
				case "unknown-hdlr":
					copy(handler[8:], "????")
					metadata = testmedia.Atom("hdlr", handler)
				case "unsupported-hdlr-version":
					handler[0] = 1
					metadata = testmedia.Atom("hdlr", handler)
				}
				body = append(body, testmedia.Atom("trak", testmedia.Atom("mdia", metadata))...)
			case "malformed-later-track":
				body = append(body, testmedia.Atom("trak", []byte("bad"))...)
			}
			data := append(append([]byte(nil), ftyp...), testmedia.Atom("moov", body)...)
			if variant == "duplicate-moov" {
				handler := make([]byte, 12)
				copy(handler[8:], "soun")
				earlier := append(append([]byte(nil), movie...), testmedia.Atom("trak", testmedia.Atom("mdia", testmedia.Atom("hdlr", handler)))...)
				data = append(append(append([]byte(nil), ftyp...), testmedia.Atom("moov", earlier)...), testmedia.Atom("moov", movie)...)
			}
			if variant == "moof" {
				data = append(data, testmedia.Atom("moof", nil)...)
			}
			info, err := mediafile.Inspect(data, "video/mp4")
			if err != nil {
				t.Fatal("generic inspection changed", err)
			}
			file := File{Type: "video", Name: "inventory.mp4", Info: info}
			err = ValidateFiles(constrained, []File{file}, true)
			if variant == "silent-control" {
				if err != nil || info.AudioStreams == nil || *info.AudioStreams != 0 {
					t.Fatal("unambiguous silence rejected", info, err)
				}
			} else if err == nil || info.AudioStreams != nil {
				t.Fatal("ambiguous inventory passed before native submission", variant, info, err)
			}
			if err = ValidateFiles(core.MediaModel{}, []File{file}, true); err != nil {
				t.Fatal("generic model acquired a codec restriction", variant, err)
			}
		})
	}
}

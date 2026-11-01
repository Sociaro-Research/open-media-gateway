package providerresources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/mediafile"
	"github.com/Sociaro-Research/open-media-gateway/internal/testmedia"
	"github.com/google/uuid"
)

func TestGeneratedVideoInspectionKeepsNativeAndContainerDurationIndependent(t *testing.T) {
	data := testmedia.MP4(2000, 1000, 60)
	info, err := mediafile.Inspect(data, "video/mp4")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	id := uuid.NewString()
	for _, native := range []*float64{nil, func() *float64 { v := 2.; return &v }(), func() *float64 { v := 4.; return &v }()} {
		proof := core.InputMediaEvidence{ReferenceID: id, Type: "video", SHA256: hex.EncodeToString(sum[:]), ContentType: info.ContentType, SizeBytes: info.Size, Duration: info.Duration, Width: info.Width, Height: info.Height, FPS: info.FPS, FrameCount: info.FrameCount, AudioStreams: info.AudioStreams, VideoStreams: info.VideoStreams, AudioCodecs: core.CloneAudioCodecs(info.AudioCodecs)}
		source := core.ResourceSourceEvidence{ReferenceID: id, MediaType: "video", SHA256: proof.SHA256, Duration: native, Inspection: &proof}
		generated := Intent{Kind: "file_resource", Subtype: "video", Name: "Owned generated video", GeneratedTaskID: id, Sources: []core.ResourceSourceEvidence{source}}
		if err := ValidateIntent(generated); err != nil {
			t.Fatal("valid independent generated timelines rejected", native, err)
		}
		ordinary := CloneIntent(generated)
		ordinary.GeneratedTaskID = ""
		expectOrdinary := native != nil && *native == info.Duration
		if err := ValidateIntent(ordinary); (err == nil) != expectOrdinary {
			t.Fatal("ordinary uploaded source duration equality changed", native, err)
		}
		wrongTask := CloneIntent(generated)
		wrongTask.GeneratedTaskID = uuid.NewString()
		if err := ValidateIntent(wrongTask); (err == nil) != expectOrdinary {
			t.Fatal("other generated task gained timeline exemption", err)
		}
		if native != nil && *native != info.Duration {
			audio := CloneIntent(generated)
			audio.Sources[0].MediaType = "audio"
			audio.Sources[0].Inspection.Type = "audio"
			if !errors.Is(ValidateIntent(audio), ErrInvalid) {
				t.Fatal("non-video source gained timeline exemption")
			}
		}
		clone := CloneIntent(generated)
		*clone.Sources[0].Inspection.VideoStreams = 9
		if *generated.Sources[0].Inspection.VideoStreams != 1 {
			t.Fatal("cloned intent shared private count pointers")
		}
		if generated.Sources[0].Duration != native || generated.Sources[0].Inspection.Duration != 2 {
			t.Fatal("validation fabricated native duration")
		}
	}
}

package core

import (
	"strings"
	"testing"
)

func TestResourceSourceInspectionIsImmutableAndUnknownAware(t *testing.T) {
	duration := 4.0
	audio, video := 0, 1
	source := ResourceSourceInput{ReferenceID: "owned-reference", MediaType: "video", SHA256: strings.Repeat("a", 64), Duration: &duration, ContentType: "video/mp4", SizeBytes: 4096, Width: 1280, Height: 720, FPS: 30, FrameCount: 120, AudioStreams: &audio, VideoStreams: &video}
	proof := InspectedResourceSource(source)
	if proof.Inspection == nil || proof.Inspection.Width != 1280 || proof.Inspection.FPS != 30 || proof.Inspection.FrameCount != 120 || proof.Inspection.AudioStreams == nil || *proof.Inspection.AudioStreams != 0 {
		t.Fatal("inspected source facts lost", proof)
	}
	cloned := CloneResourceSources([]ResourceSourceEvidence{proof})
	cloned[0].Inspection.Width = 640
	*cloned[0].Inspection.AudioStreams = 2
	*cloned[0].Duration = 10
	if proof.Inspection.Width != 1280 || *proof.Inspection.AudioStreams != 0 || *proof.Duration != 4 || audio != 0 {
		t.Fatal("source proof aliased")
	}
	source.Duration = nil
	source.AudioStreams = nil
	source.FrameCount = 0
	source.FPS = 0
	unknown := InspectedResourceSource(source)
	if unknown.Duration != nil || unknown.Inspection.Duration != 0 || unknown.Inspection.AudioStreams != nil || unknown.Inspection.FrameCount != 0 {
		t.Fatal("unknown evidence fabricated", unknown)
	}
}

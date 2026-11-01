package core

import "slices"

// InspectedResourceSource retains host-inspected metadata with the exact source
// hash. It never upgrades unknown duration, frame or stream counts to zero facts.
func InspectedResourceSource(source ResourceSourceInput) ResourceSourceEvidence {
	evidence := InputMediaEvidence{AudioCodecs: CloneAudioCodecs(source.AudioCodecs), ReferenceID: source.ReferenceID, Type: source.MediaType, SHA256: source.SHA256, ContentType: source.ContentType, SizeBytes: source.SizeBytes, Width: source.Width, Height: source.Height, FPS: source.FPS, FrameCount: source.FrameCount, AudioStreams: source.AudioStreams, VideoStreams: source.VideoStreams}
	out := ResourceSourceEvidence{ReferenceID: source.ReferenceID, MediaType: source.MediaType, SHA256: source.SHA256}
	if source.Duration != nil {
		d := *source.Duration
		out.Duration = &d
		evidence.Duration = d
	}
	out.Inspection = &evidence
	return CloneResourceSources([]ResourceSourceEvidence{out})[0]
}
func CloneResourceSources(in []ResourceSourceEvidence) []ResourceSourceEvidence {
	out := slices.Clone(in)
	for i := range out {
		if out[i].Duration != nil {
			d := *out[i].Duration
			out[i].Duration = &d
		}
		if out[i].Inspection != nil {
			e := CloneInputMediaEvidence(map[string]InputMediaEvidence{"source": *out[i].Inspection})["source"]
			out[i].Inspection = &e
		}
	}
	return out
}

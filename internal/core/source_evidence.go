package core

import "math"

// InputMediaEvidence is populated only from inspected authorized source bytes.
type InputMediaEvidence struct {
	FrameCount   int64   `json:"frame_count,omitempty"`
	AudioStreams *int    `json:"audio_streams,omitempty"`
	VideoStreams *int    `json:"video_streams,omitempty"`
	ReferenceID  string  `json:"reference_id,omitempty"`
	ContentType  string  `json:"content_type,omitempty"`
	SizeBytes    int64   `json:"size_bytes,omitempty"`
	Width        int     `json:"width,omitempty"`
	Height       int     `json:"height,omitempty"`
	FPS          float64 `json:"fps,omitempty"`
	SHA256       string  `json:"sha256,omitempty"`
	Type         string  `json:"type"`
	Duration     float64 `json:"duration,omitempty"`
}

// InputVideoDuration requires evidence for every input; an absent duration is
// unknown, never zero. External URLs are not fetched as a side effect of quoting.
func (o MediaOptions) InputVideoDuration() (float64, bool) {
	total := 0.0
	for _, raw := range o.Videos() {
		evidence, ok := o.ReferenceMediaEvidence[raw]
		if !ok || evidence.Type != "video" || evidence.Duration <= 0 || math.IsNaN(evidence.Duration) || math.IsInf(evidence.Duration, 0) {
			return 0, false
		}
		total += evidence.Duration
	}
	return total, true
}

func CloneInputMediaEvidence(in map[string]InputMediaEvidence) map[string]InputMediaEvidence {
	if in == nil {
		return nil
	}
	out := make(map[string]InputMediaEvidence, len(in))
	for key, value := range in {
		if value.AudioStreams != nil {
			n := *value.AudioStreams
			value.AudioStreams = &n
		}
		if value.VideoStreams != nil {
			n := *value.VideoStreams
			value.VideoStreams = &n
		}
		out[key] = value
	}
	return out
}

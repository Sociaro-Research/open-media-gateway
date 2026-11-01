package core

import "slices"

// ReferenceMediaLimits describes reviewed input constraints, independently of
// provider names. Missing optional metadata retains shared upload/inspection
// limits and native request validation; it does not declare unsupported media.
type ReferenceMediaLimits struct {
	// MaxVideoAndOutputDuration caps inspected input-video seconds plus output
	// seconds when at least one input video is present. Zero means unspecified.
	MaxVideoAndOutputDuration float64          `json:"max_video_and_output_duration,omitempty"`
	MaxFiles                  int              `json:"max_files"`
	Image                     MediaFileLimits  `json:"image"`
	Video                     MediaFileLimits  `json:"video"`
	Audio                     MediaFileLimits  `json:"audio"`
	ReferenceVoice            *MediaFileLimits `json:"reference_voice,omitempty"`
}
type MediaFileLimits struct {
	MinFrames        int64    `json:"min_frames,omitempty"`
	MaxFrames        int64    `json:"max_frames,omitempty"`
	RequireAudio     *bool    `json:"require_audio,omitempty"`
	NoAlpha          bool     `json:"no_alpha,omitempty"`
	ContentTypes     []string `json:"content_types,omitempty"`
	Codecs           []string `json:"codecs,omitempty"`
	MaxBytes         int64    `json:"max_bytes,omitempty"`
	MaxTotalBytes    int64    `json:"max_total_bytes,omitempty"`
	MinDimension     int      `json:"min_dimension,omitempty"`
	MaxDimension     int      `json:"max_dimension,omitempty"`
	MinPixels        int64    `json:"min_pixels,omitempty"`
	MaxPixels        int64    `json:"max_pixels,omitempty"`
	MinRatio         float64  `json:"min_ratio,omitempty"`
	MaxRatio         float64  `json:"max_ratio,omitempty"`
	MinDuration      float64  `json:"min_duration,omitempty"`
	MaxDuration      float64  `json:"max_duration,omitempty"`
	MinTotalDuration float64  `json:"min_total_duration,omitempty"`
	MaxTotalDuration float64  `json:"max_total_duration,omitempty"`
	MinFPS           float64  `json:"min_fps,omitempty"`
	MaxFPS           float64  `json:"max_fps,omitempty"`
}

// Clone keeps read-time capability inheritance independent of saved configuration.
func (limits *ReferenceMediaLimits) Clone() *ReferenceMediaLimits {
	if limits == nil {
		return nil
	}
	result := *limits
	if limits.ReferenceVoice != nil {
		voice := *limits.ReferenceVoice
		voice.ContentTypes = slices.Clone(voice.ContentTypes)
		voice.Codecs = slices.Clone(voice.Codecs)
		result.ReferenceVoice = &voice
	}
	for _, rule := range []*MediaFileLimits{&result.Image, &result.Video, &result.Audio} {
		rule.ContentTypes = slices.Clone(rule.ContentTypes)
		rule.Codecs = slices.Clone(rule.Codecs)
	}
	return &result
}

type MediaRoleLimits struct {
	Kind   string          `json:"kind"`
	Role   string          `json:"role"`
	Limits MediaFileLimits `json:"limits"`
}

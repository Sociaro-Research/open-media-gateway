package core

import "time"

// MediaSessionEvidence is adapter-normalized, server-persisted state. Public
// callers select local face/frame indices, never assert remote session IDs,
// duration, expiry, face intervals or selection state.
type MediaSessionEvidence struct {
	ExpiresAt           time.Time        `json:"expires_at"`
	SourceVideoDuration float64          `json:"source_video_duration"`
	SourceSHA256        string           `json:"source_sha256"`
	Faces               []SessionFace    `json:"faces,omitempty"`
	FrameCount          int              `json:"frame_count,omitempty"`
	FPS                 float64          `json:"fps,omitempty"`
	SelectionRevision   int64            `json:"selection_revision,omitempty"`
	Selections          []FrameSelection `json:"selections,omitempty"`
	HasSelection        bool             `json:"has_selection,omitempty"`
}
type SessionFace struct {
	ID                string `json:"id"`
	StartMS           int64  `json:"start_ms"`
	EndMS             int64  `json:"end_ms"`
	PreviewArtifactID string `json:"preview_artifact_id,omitempty"`
}

// SelectionPoint is a normalized coordinate; schema validation must check [0,1]
// and finite values. Frame selections are bounded by a model-specific contract.
type SelectionPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type FrameSelection struct {
	FrameIndex int              `json:"frame_index"`
	Points     []SelectionPoint `json:"points"`
}

func CloneMediaSession(in *MediaSessionEvidence) *MediaSessionEvidence {
	if in == nil {
		return nil
	}
	out := *in
	out.Faces = append([]SessionFace(nil), in.Faces...)
	out.Selections = make([]FrameSelection, len(in.Selections))
	for i, selection := range in.Selections {
		out.Selections[i] = FrameSelection{FrameIndex: selection.FrameIndex, Points: append([]SelectionPoint(nil), selection.Points...)}
	}
	return &out
}

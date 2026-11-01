package core

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// SessionActionIntent contains only local resource identity and explicit user
// selection intent. Native session IDs and source evidence remain server-only.
type SessionActionIntent struct {
	BindingID        string          `json:"binding_id"`
	Action           string          `json:"action"`
	ExpectedRevision int64           `json:"expected_revision"`
	Frame            *FrameSelection `json:"frame,omitempty"`
	Recover          bool            `json:"recover,omitempty"`
}

type SessionOperationSchema struct {
	Kind           string `json:"kind"`
	Action         string `json:"action"`
	Mutates        bool   `json:"mutates"`
	MaxFrames      int    `json:"max_frames,omitempty"`
	MaxPoints      int    `json:"max_points,omitempty"`
	FrameIndexBase int    `json:"frame_index_base"`
}

// SessionActionResolver permits an explicit recovery clear of a failed session.
// All other actions retain normal ready-state and owner/connection checks.
type SessionActionResolver interface {
	ResolveSessionAction(context.Context, string, *SessionActionIntent, []ResourceReference) ([]ResourceSnapshot, error)
}

func CloneSessionAction(in *SessionActionIntent) *SessionActionIntent {
	if in == nil {
		return nil
	}
	out := *in
	if in.Frame != nil {
		f := *in.Frame
		f.Points = append([]SelectionPoint(nil), in.Frame.Points...)
		out.Frame = &f
	}
	return &out
}

func ValidateSessionAction(model MediaModel, options MediaOptions, deferResolution bool) error {
	invalid := func() error { return fmt.Errorf("%w: invalid or stale session action", ErrOptionsInvalid) }
	action, schema := options.SessionAction, model.SessionOperation
	if schema == nil {
		if action != nil {
			return invalid()
		}
		return nil
	}
	if action == nil || action.Action != schema.Action || action.ExpectedRevision < 0 || action.Recover && (action.Action != "clear" || !schema.Mutates) {
		return invalid()
	}
	if _, err := uuid.Parse(action.BindingID); err != nil {
		return invalid()
	}
	if len(options.ResourceReferences) != 1 || options.ResourceReferences[0].BindingID != action.BindingID {
		return invalid()
	}
	switch action.Action {
	case "add", "delete":
		if action.Frame == nil || action.Frame.FrameIndex < schema.FrameIndexBase || len(action.Frame.Points) == 0 || schema.MaxPoints <= 0 || len(action.Frame.Points) > schema.MaxPoints {
			return invalid()
		}
		seen := map[SelectionPoint]bool{}
		for _, p := range action.Frame.Points {
			if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) || p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 || seen[p] {
				return invalid()
			}
			seen[p] = true
		}
	case "clear", "preview":
		if action.Frame != nil {
			return invalid()
		}
	default:
		return invalid()
	}
	if deferResolution {
		return nil
	}
	if len(options.ResourceSnapshots) != 1 {
		return invalid()
	}
	snapshot := options.ResourceSnapshots[0]
	if snapshot.BindingID != action.BindingID || snapshot.Kind != schema.Kind || snapshot.Session == nil || snapshot.Session.SelectionRevision != action.ExpectedRevision {
		return invalid()
	}
	if action.Frame != nil && (snapshot.Session.FrameCount <= 0 || action.Frame.FrameIndex-schema.FrameIndexBase >= snapshot.Session.FrameCount) {
		return invalid()
	}
	return nil
}

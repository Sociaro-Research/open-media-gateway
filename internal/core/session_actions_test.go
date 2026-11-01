package core

import (
	"math"
	"testing"

	"github.com/google/uuid"
)

func TestSessionActionTypedRevisionAndCoordinateAdmission(t *testing.T) {
	id := uuid.NewString()
	model := MediaModel{SessionOperation: &SessionOperationSchema{Kind: "editing_session", Action: "add", Mutates: true, MaxFrames: 10, MaxPoints: 10}}
	baseline := MediaOptions{SessionAction: &SessionActionIntent{BindingID: id, Action: "add", ExpectedRevision: 0, Frame: &FrameSelection{FrameIndex: 0, Points: []SelectionPoint{{X: .123456789012345, Y: .75}}}}, ResourceReferences: []ResourceReference{{BindingID: id, ReferenceID: "editing_session_1"}}, ResourceSnapshots: []ResourceSnapshot{{BindingID: id, Kind: "editing_session", Session: &MediaSessionEvidence{FrameCount: 100, SelectionRevision: 0}}}}
	if err := ValidateSessionAction(model, baseline, false); err != nil {
		t.Fatal(err)
	}
	clone := CloneSessionAction(baseline.SessionAction)
	clone.Frame.Points[0].X = .5
	if baseline.SessionAction.Frame.Points[0].X != .123456789012345 {
		t.Fatal("mutated accepted coordinates")
	}
	cases := map[string]func(*MediaOptions){
		"negative revision": func(o *MediaOptions) { o.SessionAction.ExpectedRevision = -1 },
		"stale revision":    func(o *MediaOptions) { o.SessionAction.ExpectedRevision = 1 },
		"foreign binding":   func(o *MediaOptions) { o.SessionAction.BindingID = uuid.NewString() },
		"negative frame":    func(o *MediaOptions) { o.SessionAction.Frame.FrameIndex = -1 },
		"past last frame":   func(o *MediaOptions) { o.SessionAction.Frame.FrameIndex = 100 },
		"infinite":          func(o *MediaOptions) { o.SessionAction.Frame.Points[0].X = math.Inf(1) },
		"nan":               func(o *MediaOptions) { o.SessionAction.Frame.Points[0].Y = math.NaN() },
		"outside range":     func(o *MediaOptions) { o.SessionAction.Frame.Points[0].Y = 1.001 },
		"duplicate": func(o *MediaOptions) {
			o.SessionAction.Frame.Points = append(o.SessionAction.Frame.Points, o.SessionAction.Frame.Points[0])
		},
		"eleven points": func(o *MediaOptions) {
			for i := 0; i < 11; i++ {
				o.SessionAction.Frame.Points = append(o.SessionAction.Frame.Points, SelectionPoint{X: float64(i) / 11})
			}
		},
		"recovery add": func(o *MediaOptions) { o.SessionAction.Recover = true },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			o := baseline
			o.SessionAction = CloneSessionAction(baseline.SessionAction)
			change(&o)
			if ValidateSessionAction(model, o, false) == nil {
				t.Fatal("invalid intent accepted")
			}
		})
	}
	deferred := baseline
	deferred.ResourceSnapshots = nil
	if ValidateSessionAction(model, deferred, true) != nil || ValidateSessionAction(model, deferred, false) == nil {
		t.Fatal("mutable resolution must defer only for idempotency")
	}
	if ValidateSessionAction(MediaModel{}, baseline, false) == nil {
		t.Fatal("unsupported action accepted")
	}
}

package kling

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

const (
	SessionInit    = "kling-selection-init"
	SessionAdd     = "kling-selection-add"
	SessionDelete  = "kling-selection-delete"
	SessionClear   = "kling-selection-clear"
	SessionPreview = "kling-selection-preview"
)

func sessionModels() []core.MediaModel {
	result := make([]core.MediaModel, 0, 5)
	for _, item := range []struct{ id, action, label string }{{SessionInit, "init", "Initialize editing session"}, {SessionAdd, "add", "Add selection points"}, {SessionDelete, "delete", "Delete selection points"}, {SessionClear, "clear", "Clear selection"}, {SessionPreview, "preview", "Preview selection"}} {
		m := core.MediaModel{ID: item.id, DisplayName: "Kling · " + item.label, MediaType: "json", NoPrompt: true, NoSeed: true, NoWatermark: true, NoLanguage: true, NoInstructions: true,
			ReferenceModes: []core.ReferenceMode{{ID: "none", Label: "Managed editing session"}}, Pricing: core.MediaModelPricing{Known: false, ReferenceDescription: "No published numeric tariff is verified for this session subcall; an explicit operator-confirmed USD/request rate is required."}}
		if item.id == SessionInit {
			m.MediaType = "resource"
			m.ResourceOperation = &core.ResourceOperationSchema{Kind: "editing_session", Subtypes: []string{"selection"}, NameMaxCharacters: 256, Variants: []core.ResourceVariantSchema{{Subtype: "selection", MinSources: 1, MaxSources: 1, SourceKinds: []string{"video"}, FirstSourceLabel: "Video to initialize", Help: "Inspected MP4/MOV, 2–5 or 7–10 seconds, 720–2160px on both sides, exactly 24/30/60fps. Session expires after 24 hours.", Limits: &core.ReferenceMediaLimits{MaxFiles: 1, Video: core.MediaFileLimits{ContentTypes: []string{"video/mp4", "video/quicktime"}, MinDuration: 2, MaxDuration: 10, MinDimension: 720, MaxDimension: 2160, MinFPS: 24, MaxFPS: 60}}}}}
			m.ResourceOutputs = []core.ResourceOutputSchema{{Kind: "editing_session", GatewayRetentionSeconds: 86400, RequiresInspectedInput: true, ProviderExpiryKnown: true}}
		} else {
			m.SessionOperation = &core.SessionOperationSchema{Kind: "editing_session", Action: item.action, Mutates: item.action != "preview", MaxFrames: 10, MaxPoints: 10, FrameIndexBase: 0}
			m.MaxReferenceInputs = 1
			m.ResourceInputSchemas = []core.ResourceInputSchema{{Kind: "editing_session", MaxCount: 1}}
			if item.id == SessionPreview {
				m.MediaType = "video"
				m.DurationFromInput = true
				m.RatioMayBeInferred = true
			}
		}
		result = append(result, m)
	}
	return result
}
func isSessionOperation(id string) bool {
	return id == SessionInit || id == SessionAdd || id == SessionDelete || id == SessionClear || id == SessionPreview
}
func sessionModel(id string) (core.MediaModel, bool) {
	for _, m := range sessionModels() {
		if m.ID == id {
			return m, true
		}
	}
	return core.MediaModel{}, false
}

func sessionOptions(m core.MediaModel, o core.MediaOptions) error {
	if !isSessionOperation(m.ID) {
		return auxiliaryInvalid("unknown Kling selection operation")
	}
	if o.OutputCount != 0 || o.OverrideSoundID != "" || o.OverrideSound != nil || len(o.Parameters) > 0 || o.Quality != "" || o.Background != "" || len(o.ReferenceVoices) > 0 || len(o.ReferenceRoles) > 0 || len(o.ReferenceMediaEvidence) > 0 || len(o.ImageURLs) > 0 || len(o.Videos()) > 0 || len(o.Audios()) > 0 || o.Resolution != "" || o.Ratio != "" || o.Duration != 0 || o.GenerateAudio != nil || o.Watermark != nil || o.CameraFixed != nil || o.Draft != nil || o.Seed != nil || o.ReturnLastFrame != nil || o.NegativePrompt != "" || o.Voice != "" || o.Language != "" || o.Instructions != "" || o.Lyrics != "" || o.Gender != "" || o.Format != "" {
		return auxiliaryInvalid("selection operations accept only typed local source or session action intent")
	}
	if m.ID == SessionInit {
		if o.SessionAction != nil || len(o.ResourceReferences) > 0 || len(o.ResourceSnapshots) > 0 {
			return auxiliaryInvalid("initialization creates a session and does not accept an existing session action")
		}
		if err := core.ValidateResourceCreationIntent(m, o, false); err != nil {
			return err
		}
		r := o.ResourceCreation
		if r == nil || len(r.Videos) != 1 || len(r.Images) > 0 || len(r.VoiceSources) > 0 || len(r.FileSources) > 0 || r.GeneratedVideo != nil || r.BoundVoice != nil || r.Description != "" || len(r.Tags) > 0 || r.ExternalTaskID != "" {
			return auxiliaryInvalid("selection initialization requires exactly one inspected local video")
		}
		if err := validAuxiliarySource(r.Videos[0], "editing_session"); err != nil {
			return err
		}
		if !slices.Contains([]float64{24, 30, 60}, r.Videos[0].FPS) || r.Videos[0].FrameCount <= 0 || r.Videos[0].FrameCount > 600 {
			return auxiliaryInvalid("selection source requires inspected 24/30/60fps and a positive bounded frame count")
		}
		return nil
	}
	if o.ResourceIntent != nil || o.ResourceCreation != nil {
		return auxiliaryInvalid("selection actions do not create new source resources")
	}
	if err := core.ValidateResourceReferences(m, o); err != nil {
		return err
	}
	if err := core.ValidateSessionAction(m, o, false); err != nil {
		return err
	}
	binding, err := auxiliarySessionSnapshot(o, "editing_session")
	if err != nil {
		return err
	}
	e := binding.Session
	if !(e.SourceVideoDuration >= 2 && e.SourceVideoDuration <= 5 || e.SourceVideoDuration >= 7 && e.SourceVideoDuration <= 10) || e.FrameCount <= 0 || e.FrameCount > 600 || !slices.Contains([]float64{24, 30, 60}, e.FPS) {
		return auxiliaryInvalid("selection session has invalid inspected video or frame evidence")
	}
	if m.SessionOperation.Mutates && o.SessionAction.ExpectedRevision == math.MaxInt64 {
		return auxiliaryInvalid("selection revision cannot be incremented")
	}
	// Clear intentionally works with uncertain prior point state. Every other
	// action requires the persisted selection set to satisfy the native bounds.
	if m.ID != SessionClear {
		if err := sessionSelectionState(e); err != nil {
			return err
		}
	}
	switch m.ID {
	case SessionAdd, SessionDelete, SessionClear:
		_, err := auxiliarySelections(e, m.SessionOperation.Action, o.SessionAction.Frame)
		return err
	}
	return nil
}
func sessionSelectionState(e *core.MediaSessionEvidence) error {
	if len(e.Selections) > 10 || e.HasSelection != (len(e.Selections) > 0) {
		return auxiliaryInvalid("selection session point state is inconsistent")
	}
	frames := map[int]bool{}
	for _, f := range e.Selections {
		if frames[f.FrameIndex] || f.FrameIndex < 0 || f.FrameIndex >= e.FrameCount || len(f.Points) == 0 || len(f.Points) > 10 {
			return auxiliaryInvalid("persisted selection frames exceed native bounds")
		}
		frames[f.FrameIndex] = true
		points := map[core.SelectionPoint]bool{}
		for _, p := range f.Points {
			if points[p] || p.X < 0 || p.X > 1 || p.Y < 0 || p.Y > 1 || math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
				return auxiliaryInvalid("persisted selection points are invalid")
			}
			points[p] = true
		}
	}
	return nil
}
func (a *Adapter) validateSessionOperation(ctx context.Context, m core.MediaModel, o core.MediaOptions) error {
	canonical, ok := sessionModel(m.ID)
	if !ok {
		return auxiliaryInvalid("unknown Kling selection operation")
	}
	if _, err := legacyScope(ctx); err != nil {
		return err
	}
	if err := sessionOptions(canonical, o); err != nil {
		return err
	}
	if m.ID != SessionInit {
		return auxiliaryBindingScope(ctx, o.ResourceSnapshots[0])
	}
	return nil
}

type sessionStateSummary struct {
	Action            string `json:"action"`
	SelectionRevision int64  `json:"selection_revision"`
	FrameCount        int    `json:"frame_count"`
	HasSelection      bool   `json:"has_selection"`
	SelectedFrames    int    `json:"selected_frames"`
}

func sessionProviderTask(result auxiliarySessionResult, action string) (core.ProviderTask, error) {
	if result.Resource.Session == nil {
		return core.ProviderTask{}, errors.New("Kling selection result omitted session evidence")
	}
	e := result.Resource.Session
	summary, err := json.Marshal(sessionStateSummary{Action: action, SelectionRevision: e.SelectionRevision, FrameCount: e.FrameCount, HasSelection: e.HasSelection, SelectedFrames: len(e.Selections)})
	if err != nil {
		return core.ProviderTask{}, err
	}
	// Append rather than prepend: mask summaries already contain archive indexes
	// for their PNG siblings. Native IDs and temporary URLs never enter this JSON.
	artifacts := append([]core.ProviderArtifact(nil), result.Artifacts...)
	artifacts = append(artifacts, core.ProviderArtifact{ID: "session_state", MediaType: "json", Format: "json", Text: string(summary)})
	result.Resource.Subtype = "selection"
	usage := result.Usage
	if usage == nil {
		usage = &core.ProviderUsage{CostStatus: "metered"}
	}
	task := core.ProviderTask{Artifacts: artifacts, Resources: []core.ProviderResource{result.Resource}, Usage: usage}
	if artifacts[0].URL != "" {
		task.ResultURL = artifacts[0].URL
	}
	return task, nil
}
func (a *Adapter) generateSessionOperation(ctx context.Context, m core.MediaModel, r core.MediaRequest) (core.ProviderTask, error) {
	if r.Model != m.ID || r.Prompt != "" {
		return core.ProviderTask{}, auxiliaryInvalid("selection operation does not accept a prompt or a different model")
	}
	if err := a.validateSessionOperation(ctx, m, r.Options); err != nil {
		return core.ProviderTask{}, err
	}
	m, _ = sessionModel(m.ID)
	if m.ID == SessionInit {
		start := time.Now().UTC()
		creation := core.CloneResourceCreation(r.Options.ResourceCreation)
		result, err := a.initializeAuxiliarySession(ctx, "editing_session", creation.Videos[0], time.Time{})
		if err != nil {
			return core.ProviderTask{}, err
		}
		result.Resource.Name = creation.Name
		// Provider lifetime starts during initialization, never after archive work.
		result.Resource.Session.ExpiresAt = start.Add(24 * time.Hour)
		return sessionProviderTask(result, "init")
	}
	intent := core.CloneSessionAction(r.Options.SessionAction)
	binding := r.Options.ResourceSnapshots[0]
	binding.Session = core.CloneMediaSession(binding.Session)
	action := m.SessionOperation.Action
	if action == "preview" {
		result, err := a.previewAuxiliarySelection(ctx, binding)
		if err != nil {
			return core.ProviderTask{}, err
		}
		tracking, err := a.createTrackingArtifact(ctx, result.TrackingURL)
		if err != nil {
			return core.ProviderTask{}, err
		}
		result.Artifacts = append(result.Artifacts, tracking)
		task, err := sessionProviderTask(result, action)
		task.Resources = nil
		return task, err
	}
	result, err := a.mutateAuxiliarySelection(ctx, binding, action, intent.Frame)
	if err != nil {
		return core.ProviderTask{}, err
	}
	task, err := sessionProviderTask(result, action)
	if err != nil {
		return core.ProviderTask{}, errors.Join(ErrAuxiliarySessionStateUncertain, err)
	}
	return task, nil
}
func (a *Adapter) quoteSessionOperation(m core.MediaModel, o core.MediaOptions) (core.TaskCost, string, error) {
	canonical, ok := sessionModel(m.ID)
	if !ok {
		return core.TaskCost{}, "", core.ErrModelUnavailable
	}
	if err := sessionOptions(canonical, o); err != nil {
		return core.TaskCost{}, "", err
	}
	return a.quoteFixedSessionOperation(m.ID)
}

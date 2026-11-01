package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
)

func sessionActionInvalid() error {
	return fmt.Errorf("%w: session action is unavailable, active or changed", core.ErrOptionsInvalid)
}

func storedSessionActionValid(options core.MediaOptions) bool {
	action := options.SessionAction
	if action == nil || len(options.ResourceReferences) != 1 || len(options.ResourceSnapshots) != 1 || options.ResourceCreation != nil || options.ResourceIntent != nil {
		return false
	}
	snapshot := options.ResourceSnapshots[0]
	if snapshot.Kind != "editing_session" || snapshot.IsPreset || snapshot.Session == nil || action.ExpectedRevision == math.MaxInt64 {
		return false
	}
	model := core.MediaModel{ResourceInputSchemas: []core.ResourceInputSchema{{Kind: "editing_session", MaxCount: 1}}, SessionOperation: &core.SessionOperationSchema{Kind: "editing_session", Action: action.Action, Mutates: action.Action != "preview", MaxFrames: 10, MaxPoints: 10}}
	if core.ValidateResourceReferences(model, options) != nil || core.ValidateSessionAction(model, options, false) != nil {
		return false
	}
	_, err := expectedSessionActionSelections(options)
	return err == nil
}

func sessionActionBindingMatches(task core.Task, b providerresources.Binding) bool {
	snapshot := task.Options.ResourceSnapshots[0]
	return b.OwnerID == task.OwnerID && b.ProviderOwnerID == task.ProviderOwnerID && b.ProviderID == task.Provider && b.ConnectionID == task.Provider && b.Revision == task.ProviderIdentity && b.ID == snapshot.BindingID && snapshot.ConnectionIdentity == b.Revision && snapshot.RemoteID == b.RemoteID && snapshot.Kind == b.Kind && snapshot.Subtype == b.Subtype && snapshot.IsPreset == b.IsPreset && providerresources.EqualSources(snapshot.Sources, b.Sources) && providerresources.EqualSession(snapshot.Session, b.Session) && providerresources.ValidateSessionProvenance(b.Kind, b.Session, b.Sources) == nil
}

// reserveTaskSessionAction runs after INSERT and idempotency resolution. The
// binding row lock serializes readers, writers and deletion before the task and
// reservation become visible in the same transaction.
func reserveTaskSessionAction(ctx context.Context, tx *sql.Tx, task core.Task) error {
	if !storedSessionActionValid(task.Options) || task.OwnerID != identity.OwnerID(ctx) || task.ProviderOwnerID != identity.ProviderOwnerID(ctx) || task.ProviderIdentity == "" {
		return sessionActionInvalid()
	}
	action := task.Options.SessionAction
	lock := " FOR UPDATE"
	if action.Action == "preview" {
		lock = " FOR SHARE"
	}
	b, err := resourceBindingTx(ctx, tx, action.BindingID, lock)
	if errors.Is(err, providerresources.ErrNotFound) {
		return sessionActionInvalid()
	}
	if err != nil {
		return err
	}
	if !sessionActionBindingMatches(task, b) || b.Session.SelectionRevision != action.ExpectedRevision || !b.Session.ExpiresAt.After(time.Now()) {
		return sessionActionInvalid()
	}
	recovering := action.Recover && action.Action == "clear" && b.Status == providerresources.Failed
	if b.Status != providerresources.Succeeded && !recovering {
		return sessionActionInvalid()
	}
	if action.Recover && !recovering {
		return sessionActionInvalid()
	}
	if action.Action == "preview" {
		return nil
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE owner_id=$1 AND id<>$2 AND status IN ('queued','running') AND (request_options @> jsonb_build_object('_reference_resources',jsonb_build_array(jsonb_build_object('binding_id',$3::text))) OR request_options @> jsonb_build_object('resource_references',jsonb_build_array(jsonb_build_object('binding_id',$3::text))) OR request_options->'session_action'->>'binding_id'=$3::text))`, task.OwnerID, task.ID, b.ID).Scan(&busy)
	if err != nil {
		return err
	}
	if busy {
		return sessionActionInvalid()
	}
	_, err = tx.ExecContext(ctx, `UPDATE provider_resources SET status='running',updated_at=now() WHERE id=$1`, b.ID)
	return err
}

// readSessionActionTask locks the task before looking up its reserved binding,
// matching the claim-fenced terminal transition's lock ordering.
func readSessionActionTask(ctx context.Context, tx *sql.Tx, id string) (core.Task, error) {
	var task core.Task
	// Most media completions have no session action. Probe only that JSON field
	// and avoid decoding/locking their complete options in this hook. The caller
	// has already enforced its worker claim before reaching this read.
	var intentRaw []byte
	if err := tx.QueryRowContext(ctx, "SELECT request_options->'session_action' FROM tasks WHERE id=$1", id).Scan(&intentRaw); err != nil {
		return task, err
	}
	if len(intentRaw) == 0 || string(intentRaw) == "null" {
		return task, nil
	}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT id,owner_id::text,provider_owner_id::text,provider,provider_identity,status,request_options FROM tasks WHERE id=$1 FOR UPDATE`, id).Scan(&task.ID, &task.OwnerID, &task.ProviderOwnerID, &task.Provider, &task.ProviderIdentity, &task.Status, &raw)
	if err != nil {
		return task, err
	}
	if err = json.Unmarshal(raw, &task.Options); err != nil {
		return task, err
	}
	if task.Options.SessionAction != nil && (task.OwnerID != identity.OwnerID(ctx) || task.ProviderOwnerID != identity.ProviderOwnerID(ctx) || !storedSessionActionValid(task.Options)) {
		return task, sessionActionInvalid()
	}
	return task, nil
}

// expectedSessionActionSelections applies the public action to a copied accepted
// selection. Completion must match this exact transformation, not merely a
// plausible mask carrying the next revision.
func expectedSessionActionSelections(options core.MediaOptions) ([]core.FrameSelection, error) {
	action := options.SessionAction
	if action == nil || len(options.ResourceSnapshots) != 1 || options.ResourceSnapshots[0].Session == nil {
		return nil, sessionActionInvalid()
	}
	if action.Action == "clear" {
		return nil, nil
	}
	old := core.CloneMediaSession(options.ResourceSnapshots[0].Session)
	if action.Action == "preview" {
		return old.Selections, nil
	}
	if action.Frame == nil {
		return nil, sessionActionInvalid()
	}
	selections := old.Selections
	index := -1
	for i, frame := range selections {
		if frame.FrameIndex == action.Frame.FrameIndex {
			index = i
			break
		}
	}
	switch action.Action {
	case "add":
		if index < 0 {
			selections = append(selections, core.FrameSelection{FrameIndex: action.Frame.FrameIndex})
			index = len(selections) - 1
		}
		for _, point := range action.Frame.Points {
			if slices.Contains(selections[index].Points, point) {
				return nil, sessionActionInvalid()
			}
			selections[index].Points = append(selections[index].Points, point)
		}
	case "delete":
		if index < 0 {
			return nil, sessionActionInvalid()
		}
		for _, point := range action.Frame.Points {
			found := slices.Index(selections[index].Points, point)
			if found < 0 {
				return nil, sessionActionInvalid()
			}
			selections[index].Points = append(selections[index].Points[:found], selections[index].Points[found+1:]...)
		}
		if len(selections[index].Points) == 0 {
			selections = append(selections[:index], selections[index+1:]...)
		}
	default:
		return nil, sessionActionInvalid()
	}
	if len(selections) > 10 {
		return nil, sessionActionInvalid()
	}
	for _, frame := range selections {
		if len(frame.Points) == 0 || len(frame.Points) > 10 {
			return nil, sessionActionInvalid()
		}
	}
	return selections, nil
}

func validSessionActionResult(task core.Task, result core.ProviderResource) bool {
	snapshot := task.Options.ResourceSnapshots[0]
	action := task.Options.SessionAction
	old, next := snapshot.Session, result.Session
	if result.Status != core.ProviderSucceeded || result.IsDeleted || result.IsPreset || result.BoundVoice != nil || result.RemoteID != snapshot.RemoteID || result.Kind != snapshot.Kind || result.Subtype != snapshot.Subtype || next == nil || providerresources.ValidateSessionProvenance(result.Kind, next, snapshot.Sources) != nil {
		return false
	}
	if !next.ExpiresAt.Equal(old.ExpiresAt) || next.SourceSHA256 != old.SourceSHA256 || next.SourceVideoDuration != old.SourceVideoDuration || next.FrameCount != old.FrameCount || next.FPS != old.FPS || !slices.Equal(next.Faces, old.Faces) || next.SelectionRevision != action.ExpectedRevision+1 || len(next.Selections) > 10 {
		return false
	}
	if next.HasSelection != (len(next.Selections) > 0) {
		return false
	}
	expected, err := expectedSessionActionSelections(task.Options)
	if err != nil || len(expected) != len(next.Selections) {
		return false
	}
	for i := range expected {
		if expected[i].FrameIndex != next.Selections[i].FrameIndex || !slices.Equal(expected[i].Points, next.Selections[i].Points) {
			return false
		}
	}

	for _, frame := range next.Selections {
		if len(frame.Points) > 10 {
			return false
		}
		seen := map[core.SelectionPoint]bool{}
		for _, point := range frame.Points {
			if seen[point] {
				return false
			}
			seen[point] = true
		}
	}
	if action.Action == "clear" && (next.HasSelection || len(next.Selections) > 0) {
		return false
	}
	return true
}

// validateSessionActionCompletion always runs after the task claim fence,
// including when a provider returns no resources. A successful paid mutation
// cannot leave its binding silently reserved or claim success without evidence.
func validateSessionActionCompletion(ctx context.Context, tx *sql.Tx, id string, resources []core.ProviderResource) error {
	task, err := readSessionActionTask(ctx, tx, id)
	if err != nil {
		return err
	}
	action := task.Options.SessionAction
	if action == nil {
		return nil
	}
	if action.Action == "preview" {
		if len(resources) != 0 {
			return sessionActionInvalid()
		}
		return nil
	}
	if len(resources) != 1 || !validSessionActionResult(task, resources[0]) {
		return sessionActionInvalid()
	}
	return nil
}

func completeTaskSessionAction(ctx context.Context, tx *sql.Tx, id string, resources []core.ProviderResource) ([]core.ResourceReference, error) {
	task, err := readSessionActionTask(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	action := task.Options.SessionAction
	if action == nil {
		return nil, sessionActionInvalid()
	}
	if action.Action == "preview" {
		if len(resources) != 0 {
			return nil, sessionActionInvalid()
		}
		return []core.ResourceReference{}, nil
	}
	if len(resources) != 1 || !validSessionActionResult(task, resources[0]) {
		return nil, sessionActionInvalid()
	}
	b, err := resourceBindingTx(ctx, tx, action.BindingID, " FOR UPDATE")
	if err != nil {
		return nil, err
	}
	if task.Status != core.TaskRunning || b.Status != providerresources.Running || !sessionActionBindingMatches(task, b) || b.Session.SelectionRevision != action.ExpectedRevision {
		return nil, sessionActionInvalid()
	}
	raw, err := json.Marshal(resources[0].Session)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provider_resources SET session=$2,status='succeeded',updated_at=now() WHERE id=$1`, b.ID, raw); err != nil {
		return nil, err
	}
	// Keep the original binding and source generation. Action generations simply
	// link to it; they never mint another native session or extend its expiry.
	return []core.ResourceReference{{BindingID: b.ID, ReferenceID: task.Options.ResourceReferences[0].ReferenceID}}, nil
}

// failTaskSessionAction conservatively invalidates a reserved mutable session.
// A failed/uncertain remote POST does not prove its selection remained unchanged;
// only a later explicit recovery clear may restore a known ready revision.
func failTaskSessionAction(ctx context.Context, tx *sql.Tx, id string) error {
	task, err := readSessionActionTask(ctx, tx, id)
	if err != nil {
		return err
	}
	action := task.Options.SessionAction
	if action == nil || action.Action == "preview" || (task.Status != core.TaskQueued && task.Status != core.TaskRunning) {
		return nil
	}
	b, err := resourceBindingTx(ctx, tx, action.BindingID, " FOR UPDATE")
	if err != nil {
		return err
	}
	if b.Status != providerresources.Running || !sessionActionBindingMatches(task, b) || b.Session.SelectionRevision != action.ExpectedRevision {
		return sessionActionInvalid()
	}
	_, err = tx.ExecContext(ctx, `UPDATE provider_resources SET status='failed',updated_at=now() WHERE id=$1`, b.ID)
	return err
}

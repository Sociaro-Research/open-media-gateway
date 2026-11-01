package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerresources"
	"github.com/google/uuid"
)

type ProviderResourceRepository struct{ db *sql.DB }

func NewProviderResourceRepository(db *sql.DB) *ProviderResourceRepository {
	return &ProviderResourceRepository{db: db}
}

var _ providerresources.Repository = (*ProviderResourceRepository)(nil)

const resourceColumns = `id::text,owner_id::text,actor_id,provider_id,connection_id,provider_owner_id::text,revision,remote_id,kind,subtype,name,description,status,is_preset,sources,session,bound_voice_binding_id::text,origin_task_id::text,created_at,updated_at,deleted_at`
const resourceJobColumns = `id::text,binding_id::text,owner_id::text,actor_id,provider_id,connection_id,provider_owner_id::text,revision,kind,operation,status,intent,handle,safe_error,warning,intent_hash,created_at,updated_at,started_at,completed_at`

type resourceScanner interface{ Scan(...any) error }

func scanResource(row resourceScanner) (providerresources.Binding, error) {
	var b providerresources.Binding
	var sources, session []byte
	var voice, origin sql.NullString
	var deleted sql.NullTime
	err := row.Scan(&b.ID, &b.OwnerID, &b.ActorID, &b.ProviderID, &b.ConnectionID, &b.ProviderOwnerID, &b.Revision, &b.RemoteID, &b.Kind, &b.Subtype, &b.Name, &b.Description, &b.Status, &b.IsPreset, &sources, &session, &voice, &origin, &b.CreatedAt, &b.UpdatedAt, &deleted)
	if errors.Is(err, sql.ErrNoRows) {
		return b, providerresources.ErrNotFound
	}
	if err != nil {
		return b, err
	}
	if err = json.Unmarshal(sources, &b.Sources); err != nil {
		return b, err
	}
	if len(session) > 0 {
		if err = json.Unmarshal(session, &b.Session); err != nil {
			return b, err
		}
	}
	if origin.Valid {
		b.OriginTaskID = origin.String
	}
	if voice.Valid {
		b.BoundVoiceBindingID = voice.String
	}
	if deleted.Valid {
		b.DeletedAt = &deleted.Time
	}
	return b, nil
}
func scanResourceJob(row resourceScanner) (providerresources.Job, error) {
	var j providerresources.Job
	var intent []byte
	var started, completed sql.NullTime
	err := row.Scan(&j.ID, &j.BindingID, &j.OwnerID, &j.ActorID, &j.ProviderID, &j.ConnectionID, &j.ProviderOwnerID, &j.Revision, &j.Kind, &j.Operation, &j.Status, &intent, &j.Handle, &j.SafeError, &j.Warning, &j.IntentHash, &j.CreatedAt, &j.UpdatedAt, &started, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return j, providerresources.ErrNotFound
	}
	if err != nil {
		return j, err
	}
	if err = json.Unmarshal(intent, &j.Intent); err != nil {
		return j, err
	}
	if started.Valid {
		j.StartedAt = &started.Time
	}
	if completed.Valid {
		j.CompletedAt = &completed.Time
	}
	return j, nil
}
func validResourceUUID(id string) bool { _, err := uuid.Parse(id); return err == nil }
func resourceScopeArgs(ctx context.Context) []any {
	return []any{identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), identity.ContentAuthorFilter(ctx)}
}

const resourceOwnerWhere = `owner_id=$2 AND provider_owner_id=$3 AND ($4='' OR actor_id=$4)`

func (r *ProviderResourceRepository) GetBinding(ctx context.Context, id string) (providerresources.Binding, error) {
	if !validResourceUUID(id) {
		return providerresources.Binding{}, providerresources.ErrNotFound
	}
	args := append([]any{id}, resourceScopeArgs(ctx)...)
	return scanResource(r.db.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM provider_resources WHERE id=$1 AND `+resourceOwnerWhere, args...))
}
func resourceBindingTx(ctx context.Context, tx *sql.Tx, id, lock string) (providerresources.Binding, error) {
	if !validResourceUUID(id) {
		return providerresources.Binding{}, providerresources.ErrNotFound
	}
	return scanResource(tx.QueryRowContext(ctx, `SELECT `+resourceColumns+` FROM provider_resources WHERE id=$1 AND `+resourceOwnerWhere+lock, append([]any{id}, resourceScopeArgs(ctx)...)...))
}
func (r *ProviderResourceRepository) ListBindings(ctx context.Context, provider string, limit, offset int) ([]providerresources.Binding, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, providerresources.ErrInvalid
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+resourceColumns+` FROM provider_resources WHERE owner_id=$1 AND provider_owner_id=$2 AND ($3='' OR actor_id=$3) AND provider_id=$4 ORDER BY updated_at DESC,id LIMIT $5 OFFSET $6`, identity.OwnerID(ctx), identity.ProviderOwnerID(ctx), identity.ContentAuthorFilter(ctx), provider, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []providerresources.Binding{}
	for rows.Next() {
		b, e := scanResource(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (r *ProviderResourceRepository) GetJob(ctx context.Context, id string) (providerresources.Job, error) {
	if !validResourceUUID(id) {
		return providerresources.Job{}, providerresources.ErrNotFound
	}
	return scanResourceJob(r.db.QueryRowContext(ctx, `SELECT `+resourceJobColumns+` FROM provider_resource_jobs WHERE id=$1 AND `+resourceOwnerWhere, append([]any{id}, resourceScopeArgs(ctx)...)...))
}
func resourceJobTx(ctx context.Context, tx *sql.Tx, id string) (providerresources.Job, error) {
	if !validResourceUUID(id) {
		return providerresources.Job{}, providerresources.ErrNotFound
	}
	return scanResourceJob(tx.QueryRowContext(ctx, `SELECT `+resourceJobColumns+` FROM provider_resource_jobs WHERE id=$1 AND `+resourceOwnerWhere+` FOR UPDATE`, append([]any{id}, resourceScopeArgs(ctx)...)...))
}
func normalizeResourceJob(ctx context.Context, j providerresources.Job, operation providerresources.Operation) (providerresources.Job, error) {
	owner, powner := identity.OwnerID(ctx), identity.ProviderOwnerID(ctx)
	if (j.OwnerID != "" && j.OwnerID != owner) || (j.ProviderOwnerID != "" && j.ProviderOwnerID != powner) {
		return j, providerresources.ErrNotFound
	}
	if !validResourceUUID(owner) || !validResourceUUID(powner) || j.ProviderID == "" || len(j.ProviderID) > 256 || j.ConnectionID == "" || len(j.ConnectionID) > 256 || j.Revision == "" || len(j.Revision) > 256 {
		return j, providerresources.ErrInvalid
	}
	j.OwnerID, j.ProviderOwnerID, j.ActorID = owner, powner, identity.ActorID(ctx)
	j.Operation = operation
	j.Status = providerresources.Queued
	j.Kind = j.Intent.Kind
	j.Intent = providerresources.CloneIntent(j.Intent)
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	if j.BindingID == "" {
		j.BindingID = uuid.NewString()
	}
	if !validResourceUUID(j.ID) || !validResourceUUID(j.BindingID) {
		return j, providerresources.ErrInvalid
	}
	hash, err := providerresources.HashIntent(j)
	j.IntentHash = hash
	return j, err
}
func reserveResourceJob(ctx context.Context, tx *sql.Tx, j providerresources.Job, key string) (providerresources.Job, bool, error) {
	if len(key) < 1 || len(key) > 200 {
		return j, false, providerresources.ErrInvalid
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, j.OwnerID+"/"+j.ProviderOwnerID+"/"+j.ProviderID+"/"+key); err != nil {
		return j, false, err
	}
	prior, err := scanResourceJob(tx.QueryRowContext(ctx, `SELECT `+resourceJobColumns+` FROM provider_resource_jobs WHERE owner_id=$1 AND provider_owner_id=$2 AND provider_id=$3 AND idempotency_key=$4`, j.OwnerID, j.ProviderOwnerID, j.ProviderID, key))
	if err == nil {
		if prior.IntentHash != j.IntentHash {
			return prior, false, providerresources.ErrConflict
		}
		if filter := identity.ContentAuthorFilter(ctx); filter != "" && prior.ActorID != filter {
			return j, false, providerresources.ErrNotFound
		}
		return prior, false, nil
	}
	if !errors.Is(err, providerresources.ErrNotFound) {
		return j, false, err
	}
	raw, err := json.Marshal(j.Intent)
	if err != nil {
		return j, false, err
	}
	saved, err := scanResourceJob(tx.QueryRowContext(ctx, `INSERT INTO provider_resource_jobs(id,binding_id,owner_id,actor_id,provider_id,connection_id,provider_owner_id,revision,kind,operation,status,intent,idempotency_key,intent_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'queued',$11,$12,$13) RETURNING `+resourceJobColumns, j.ID, j.BindingID, j.OwnerID, j.ActorID, j.ProviderID, j.ConnectionID, j.ProviderOwnerID, j.Revision, j.Kind, j.Operation, raw, key, j.IntentHash))
	return saved, true, err
}
func (r *ProviderResourceRepository) ReserveCreate(ctx context.Context, j providerresources.Job, key string) (providerresources.Job, bool, error) {
	j, err := normalizeResourceJob(ctx, j, providerresources.Create)
	if err != nil {
		return j, false, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return j, false, err
	}
	defer tx.Rollback()
	saved, created, err := reserveResourceJob(ctx, tx, j, key)
	if err != nil {
		return saved, false, err
	}
	if created && j.Intent.BoundVoiceBindingID != "" {
		voice, e := resourceBindingTx(ctx, tx, j.Intent.BoundVoiceBindingID, ` FOR SHARE`)
		if e != nil || voice.Kind != "voice" || voice.Status != providerresources.Succeeded || voice.ProviderID != j.ProviderID || voice.ConnectionID != j.ConnectionID || voice.Revision != j.Revision {
			return j, false, providerresources.ErrNotFound
		}
	}
	if err = tx.Commit(); err != nil {
		return j, false, err
	}
	return saved, created, nil
}
func (r *ProviderResourceRepository) StartJob(ctx context.Context, id string) (providerresources.Job, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return providerresources.Job{}, false, err
	}
	defer tx.Rollback()
	j, err := resourceJobTx(ctx, tx, id)
	if err != nil {
		return j, false, err
	}
	if j.Status != providerresources.Queued {
		return j, false, tx.Commit()
	}
	j, err = scanResourceJob(tx.QueryRowContext(ctx, `UPDATE provider_resource_jobs SET status='running',started_at=now(),updated_at=now() WHERE id=$1 RETURNING `+resourceJobColumns, id))
	if err != nil {
		return j, false, err
	}
	return j, true, tx.Commit()
}
func (r *ProviderResourceRepository) SaveHandle(ctx context.Context, id, handle, warning string) error {
	if !providerresources.ValidRemoteID(handle) {
		return providerresources.ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := resourceJobTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if j.Status != providerresources.Running || (j.Handle != "" && j.Handle != handle) {
		return providerresources.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE provider_resource_jobs SET handle=$2,warning=$3,updated_at=now() WHERE id=$1`, id, handle, providerresources.SafeWarning(warning))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ProviderResourceRepository) CompleteCreate(ctx context.Context, id, remote string, session ...*core.MediaSessionEvidence) (providerresources.Binding, error) {
	if len(session) > 1 {
		return providerresources.Binding{}, providerresources.ErrInvalid
	}
	j, err := r.GetJob(ctx, id)
	if err != nil {
		return providerresources.Binding{}, err
	}
	result := core.ProviderResource{RemoteID: remote, Kind: j.Intent.Kind, Subtype: j.Intent.Subtype, Status: core.ProviderSucceeded, IsPreset: j.Intent.IsPreset}
	if len(session) == 1 {
		result.Session = session[0]
	}
	return r.CompleteResource(ctx, id, result)
}
func (r *ProviderResourceRepository) CompleteResource(ctx context.Context, id string, result core.ProviderResource) (providerresources.Binding, error) {
	if !providerresources.ValidRemoteID(result.RemoteID) || result.Status != core.ProviderSucceeded || result.IsDeleted {
		return providerresources.Binding{}, providerresources.ErrInvalid
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return providerresources.Binding{}, err
	}
	defer tx.Rollback()
	j, err := resourceJobTx(ctx, tx, id)
	if err != nil {
		return providerresources.Binding{}, err
	}
	if j.Operation != providerresources.Create || result.Kind != j.Intent.Kind || result.Subtype != j.Intent.Subtype || result.IsPreset != j.Intent.IsPreset {
		return providerresources.Binding{}, providerresources.ErrConflict
	}
	if err = providerresources.ValidateIntent(j.Intent); err != nil {
		return providerresources.Binding{}, err
	}
	if err = providerresources.ValidateSessionProvenance(j.Kind, result.Session, j.Intent.Sources); err != nil {
		return providerresources.Binding{}, err
	}
	if j.Status == providerresources.Succeeded {
		b, e := resourceBindingTx(ctx, tx, j.BindingID, ``)
		if e != nil {
			return b, e
		}
		if b.RemoteID != result.RemoteID || !providerresources.EqualSession(b.Session, result.Session) {
			return b, providerresources.ErrConflict
		}
		if result.BoundVoice != nil {
			v, e := resourceBindingTx(ctx, tx, b.BoundVoiceBindingID, ``)
			if e != nil || v.RemoteID != result.BoundVoice.RemoteID {
				return b, providerresources.ErrConflict
			}
		}
		return b, tx.Commit()
	}
	if j.Status != providerresources.Running {
		return providerresources.Binding{}, providerresources.ErrConflict
	}
	b, err := insertCompletedResource(ctx, tx, j, result, "")
	if err != nil {
		return b, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provider_resource_jobs SET status='succeeded',completed_at=now(),updated_at=now() WHERE id=$1`, j.ID); err != nil {
		return b, err
	}
	return b, tx.Commit()
}
func insertCompletedResource(ctx context.Context, tx *sql.Tx, j providerresources.Job, result core.ProviderResource, originTaskID string) (providerresources.Binding, error) {
	if !providerresources.ValidRemoteID(result.RemoteID) || result.Status != core.ProviderSucceeded || result.IsDeleted || result.Kind != j.Intent.Kind || result.Subtype != j.Intent.Subtype || result.IsPreset != j.Intent.IsPreset {
		return providerresources.Binding{}, providerresources.ErrConflict
	}
	if err := providerresources.ValidateIntent(j.Intent); err != nil {
		return providerresources.Binding{}, err
	}
	if err := providerresources.ValidateSessionProvenance(j.Kind, result.Session, j.Intent.Sources); err != nil {
		return providerresources.Binding{}, err
	}
	var err error
	voiceID := j.Intent.BoundVoiceBindingID
	if voiceID != "" {
		voice, e := resourceBindingTx(ctx, tx, voiceID, ` FOR SHARE`)
		if e != nil || voice.Status != providerresources.Succeeded || voice.Kind != "voice" || voice.ProviderID != j.ProviderID || voice.ConnectionID != j.ConnectionID || voice.Revision != j.Revision {
			return providerresources.Binding{}, providerresources.ErrNotFound
		}
		if result.BoundVoice != nil && (result.BoundVoice.RemoteID != voice.RemoteID || result.BoundVoice.IsPreset != voice.IsPreset) {
			return providerresources.Binding{}, providerresources.ErrConflict
		}
	}
	if result.BoundVoice != nil {
		voice := result.BoundVoice
		if j.Kind != "element" || voice.Kind != "voice" || voice.Status != core.ProviderSucceeded || voice.IsDeleted || (voiceID == "" && voice.IsPreset) || voice.Session != nil || voice.BoundVoice != nil || !providerresources.ValidRemoteID(voice.RemoteID) {
			return providerresources.Binding{}, providerresources.ErrInvalid
		}
		if voiceID == "" {
			voiceID = uuid.NewString()
			if _, err = insertResourceBinding(ctx, tx, j, voiceID, voice.RemoteID, "voice", "video_bound", "Bound voice", "", false, j.Intent.Sources, nil, ""); err != nil {
				return providerresources.Binding{}, err
			}
			if originTaskID != "" {
				if _, err = tx.ExecContext(ctx, `UPDATE provider_resources SET origin_task_id=$2 WHERE id=$1`, voiceID, originTaskID); err != nil {
					return providerresources.Binding{}, err
				}
			}
		}
	}
	b, err := insertResourceBinding(ctx, tx, j, j.BindingID, result.RemoteID, j.Kind, j.Intent.Subtype, j.Intent.Name, j.Intent.Description, j.Intent.IsPreset, j.Intent.Sources, providerresources.CloneSession(result.Session), voiceID)
	if err != nil {
		return b, err
	}
	if originTaskID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE provider_resources SET origin_task_id=$2 WHERE id=$1`, b.ID, originTaskID); err != nil {
			return b, err
		}
		b.OriginTaskID = originTaskID
	}
	return b, nil
}

func insertResourceBinding(ctx context.Context, tx *sql.Tx, j providerresources.Job, id, remote, kind, subtype, name, description string, preset bool, sources []core.ResourceSourceEvidence, session *core.MediaSessionEvidence, voiceID string) (providerresources.Binding, error) {
	if sources == nil {
		sources = []core.ResourceSourceEvidence{}
	}
	sourceJSON, err := json.Marshal(sources)
	if err != nil {
		return providerresources.Binding{}, err
	}
	var sessionJSON any
	if session != nil {
		raw, e := json.Marshal(session)
		if e != nil {
			return providerresources.Binding{}, e
		}
		sessionJSON = raw
	}
	return scanResource(tx.QueryRowContext(ctx, `INSERT INTO provider_resources(id,owner_id,actor_id,provider_id,connection_id,provider_owner_id,revision,remote_id,kind,subtype,name,description,status,is_preset,sources,session,bound_voice_binding_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,'succeeded',$13,$14,$15,$16) RETURNING `+resourceColumns, id, j.OwnerID, j.ActorID, j.ProviderID, j.ConnectionID, j.ProviderOwnerID, j.Revision, remote, kind, subtype, name, description, preset, sourceJSON, sessionJSON, nullableString(voiceID)))
}
func (r *ProviderResourceRepository) RecordUncertain(ctx context.Context, id, warning string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := resourceJobTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if j.Status != providerresources.Running {
		return providerresources.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE provider_resource_jobs SET warning=$2,updated_at=now() WHERE id=$1`, id, "The provider outcome is uncertain. Check this job before submitting another operation.")
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ProviderResourceRepository) FailJob(ctx context.Context, id, safeError, warning string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := resourceJobTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if j.Status == providerresources.Failed {
		return tx.Commit()
	}
	if j.Status != providerresources.Queued && j.Status != providerresources.Running {
		return providerresources.ErrConflict
	}
	if j.Operation == providerresources.Delete {
		b, e := resourceBindingTx(ctx, tx, j.BindingID, ` FOR UPDATE`)
		if e != nil {
			return e
		}
		if b.Status != providerresources.Deleting {
			return providerresources.ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE provider_resources SET status='failed',updated_at=now() WHERE id=$1`, b.ID); err != nil {
			return err
		}
	}
	if safeError == "" {
		safeError = "provider failure"
	}
	_, err = tx.ExecContext(ctx, `UPDATE provider_resource_jobs SET status='failed',safe_error=$2,warning=$3,completed_at=now(),updated_at=now() WHERE id=$1`, id, providerresources.SafeFailure(safeError), providerresources.SafeWarning(warning))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ProviderResourceRepository) BeginDelete(ctx context.Context, id, key string) (providerresources.Job, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return providerresources.Job{}, false, err
	}
	defer tx.Rollback()
	b, err := resourceBindingTx(ctx, tx, id, ` FOR UPDATE`)
	if err != nil {
		return providerresources.Job{}, false, err
	}
	j, err := normalizeResourceJob(ctx, providerresources.Job{BindingID: b.ID, ProviderID: b.ProviderID, ConnectionID: b.ConnectionID, Revision: b.Revision, Intent: providerresources.Intent{Kind: b.Kind, Subtype: b.Subtype, Name: b.Name, Description: b.Description, IsPreset: b.IsPreset, Sources: b.Sources, BoundVoiceBindingID: b.BoundVoiceBindingID}}, providerresources.Delete)
	if err != nil {
		return j, false, err
	}
	saved, created, err := reserveResourceJob(ctx, tx, j, key)
	if err != nil {
		return saved, false, err
	}
	if !created {
		return saved, false, tx.Commit()
	}
	if b.IsPreset || (b.Status != providerresources.Succeeded && b.Status != providerresources.Failed) {
		return j, false, providerresources.ErrConflict
	}
	var busy bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM provider_resources WHERE bound_voice_binding_id=$1 AND status<>'deleted') OR EXISTS(SELECT 1 FROM provider_resource_jobs WHERE owner_id=$2 AND status IN ('queued','running') AND operation='create' AND intent->>'bound_voice_binding_id'=$3::text) OR EXISTS(SELECT 1 FROM tasks WHERE owner_id=$2 AND status IN ('queued','running') AND (request_options @> jsonb_build_object('_reference_resources',jsonb_build_array(jsonb_build_object('binding_id',$3::text))) OR request_options->'resource_intent'->>'bound_voice_binding_id'=$3::text))`, b.ID, b.OwnerID, b.ID).Scan(&busy)
	if err != nil {
		return j, false, err
	}
	if busy {
		return j, false, providerresources.ErrInUse
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provider_resources SET status='deleting',updated_at=now() WHERE id=$1`, b.ID); err != nil {
		return j, false, err
	}
	return saved, true, tx.Commit()
}
func (r *ProviderResourceRepository) CompleteDelete(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := resourceJobTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if j.Operation != providerresources.Delete {
		return providerresources.ErrConflict
	}
	if j.Status == providerresources.Succeeded {
		return tx.Commit()
	}
	if j.Status != providerresources.Running {
		return providerresources.ErrConflict
	}
	b, err := resourceBindingTx(ctx, tx, j.BindingID, ` FOR UPDATE`)
	if err != nil {
		return err
	}
	if b.Status != providerresources.Deleting {
		return providerresources.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provider_resources SET status='deleted',deleted_at=now(),updated_at=now() WHERE id=$1`, b.ID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provider_resource_jobs SET status='succeeded',completed_at=now(),updated_at=now() WHERE id=$1`, j.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *ProviderResourceRepository) PendingJobs(ctx context.Context, limit int) ([]providerresources.Job, error) {
	if limit < 1 || limit > 100 {
		return nil, providerresources.ErrInvalid
	}
	where := `status IN ('queued','running')`
	args := []any{limit}
	if _, user := identity.UserFromContext(ctx); user {
		where += ` AND owner_id=$2 AND provider_owner_id=$3 AND ($4='' OR actor_id=$4)`
		args = append(args, resourceScopeArgs(ctx)...)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+resourceJobColumns+` FROM provider_resource_jobs WHERE `+where+` ORDER BY updated_at,id LIMIT $1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []providerresources.Job{}
	for rows.Next() {
		j, e := scanResourceJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// validateTaskResourceSnapshots runs only for a new task admission. FOR SHARE
// serializes its task INSERT/commit with BeginDelete's FOR UPDATE and busy scan.
func validateTaskResourceSnapshots(ctx context.Context, tx *sql.Tx, task core.Task) error {
	refs, snapshots := slices.Clone(task.Options.ResourceReferences), slices.Clone(task.Options.ResourceSnapshots)
	if task.Options.ResourceIntent != nil || task.Options.ResourceCreation != nil {
		creation, err := resourceTaskCreationIntent(task.Options)
		if err != nil {
			return fmt.Errorf("%w: resource creation evidence changed", core.ErrOptionsInvalid)
		}
		if task.OwnerID != identity.OwnerID(ctx) || task.ProviderOwnerID != identity.ProviderOwnerID(ctx) || task.ProviderIdentity == "" {
			return fmt.Errorf("%w: resource creation scope unavailable", core.ErrOptionsInvalid)
		}
		if creation.BoundVoiceBindingID != "" {
			voice := *task.Options.ResourceCreation.BoundVoice
			voice.ReferenceID = "resource_bound_voice"
			refs = append(refs, core.ResourceReference{BindingID: creation.BoundVoiceBindingID, ReferenceID: voice.ReferenceID})
			snapshots = append(snapshots, voice)
		}
	}
	if len(refs) == 0 && len(snapshots) == 0 {
		return nil
	}
	invalid := fmt.Errorf("%w: provider resource binding is unavailable or changed", core.ErrOptionsInvalid)
	if len(refs) != len(snapshots) || len(refs) > 32 || task.OwnerID != identity.OwnerID(ctx) || task.ProviderOwnerID != identity.ProviderOwnerID(ctx) || task.ProviderIdentity == "" {
		return invalid
	}
	aliases := map[string]string{}
	for _, ref := range refs {
		if !validResourceUUID(ref.BindingID) || ref.ReferenceID == "" || aliases[ref.BindingID] != "" {
			return invalid
		}
		aliases[ref.BindingID] = ref.ReferenceID
	}
	ordered := slices.Clone(snapshots)
	slices.SortFunc(ordered, func(a, b core.ResourceSnapshot) int {
		if a.BindingID < b.BindingID {
			return -1
		}
		if a.BindingID > b.BindingID {
			return 1
		}
		return 0
	})
	for _, snapshot := range ordered {
		if aliases[snapshot.BindingID] != snapshot.ReferenceID {
			return invalid
		}
		delete(aliases, snapshot.BindingID)
		b, err := resourceBindingTx(ctx, tx, snapshot.BindingID, ` FOR SHARE`)
		if err != nil {
			if errors.Is(err, providerresources.ErrNotFound) {
				return invalid
			}
			return err
		}
		if b.Status != providerresources.Succeeded || b.ProviderID != task.Provider || b.ConnectionID != task.Provider || b.Revision != task.ProviderIdentity || snapshot.ConnectionIdentity != b.Revision || snapshot.RemoteID != b.RemoteID || snapshot.Kind != b.Kind || snapshot.Subtype != b.Subtype || snapshot.IsPreset != b.IsPreset || !providerresources.EqualSources(snapshot.Sources, b.Sources) || !providerresources.EqualSession(snapshot.Session, b.Session) {
			return invalid
		}
		if providerresources.ValidateSessionProvenance(b.Kind, b.Session, b.Sources) != nil {
			return invalid
		}
		if b.Session != nil && !time.Now().Before(b.Session.ExpiresAt) {
			return invalid
		}
	}
	if len(aliases) != 0 {
		return invalid
	}
	return nil
}

// captureTaskResourceBindings persists only sessions with accepted input-video
// evidence. Unknown remote-video metadata never creates a reusable binding.
func captureTaskResourceBindings(ctx context.Context, tx *sql.Tx, taskID string, resources []core.ProviderResource) ([]core.ResourceReference, error) {
	if len(resources) == 0 {
		return []core.ResourceReference{}, nil
	}
	if !validResourceUUID(taskID) || len(resources) > 32 {
		return nil, providerresources.ErrInvalid
	}
	var owner, actor, powner, provider, revision, status string
	var optionsJSON, storedJSON []byte
	err := tx.QueryRowContext(ctx, `SELECT owner_id::text,COALESCE(actor_id::text,''),provider_owner_id::text,provider,provider_identity,status,request_options,result_resource_bindings FROM tasks WHERE id=$1 FOR UPDATE`, taskID).Scan(&owner, &actor, &powner, &provider, &revision, &status, &optionsJSON, &storedJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrTaskNotFound
	}
	if err != nil {
		return nil, err
	}
	if owner != identity.OwnerID(ctx) || powner != identity.ProviderOwnerID(ctx) || provider == "" || revision == "" || (status != "running" && status != "succeeded") {
		return nil, providerresources.ErrNotFound
	}
	var options core.MediaOptions
	if err = json.Unmarshal(optionsJSON, &options); err != nil {
		return nil, err
	}
	var creation *providerresources.Intent
	if options.ResourceIntent != nil || options.ResourceCreation != nil {
		prepared, e := resourceTaskCreationIntent(options)
		if e != nil {
			return nil, e
		}
		creation = &prepared
		if len(resources) != 1 {
			return nil, providerresources.ErrConflict
		}
	}
	trusted := map[string]core.InputMediaEvidence{}
	for _, raw := range options.Videos() {
		evidence, ok := options.ReferenceMediaEvidence[raw]
		if !ok || evidence.Type != "video" || evidence.Duration <= 0 || evidence.SHA256 == "" {
			continue
		}
		duration := evidence.Duration
		source := core.ResourceSourceEvidence{ReferenceID: taskID, MediaType: "video", SHA256: evidence.SHA256, Duration: &duration}
		if providerresources.ValidateIntent(providerresources.Intent{Kind: "face_session", Name: "Session source", Sources: []core.ResourceSourceEvidence{source}}) != nil {
			continue
		}
		if prior, exists := trusted[evidence.SHA256]; exists && prior.Duration != evidence.Duration {
			return nil, providerresources.ErrConflict
		}
		trusted[evidence.SHA256] = evidence
	}
	// A recognition request using an uninspected remote video can finish and return
	// its JSON artifact; it has no trusted reusable session binding.
	if len(trusted) == 0 && creation == nil {
		for _, resource := range resources {
			if resource.Kind != "face_session" && resource.Kind != "editing_session" {
				return nil, providerresources.ErrInvalid
			}
		}
		return []core.ResourceReference{}, nil
	}
	var existing []core.ResourceReference
	if len(storedJSON) > 0 {
		if err = json.Unmarshal(storedJSON, &existing); err != nil {
			return nil, err
		}
	}
	if len(existing) > 0 {
		if len(existing) != len(resources) {
			return nil, providerresources.ErrConflict
		}
		for i, ref := range existing {
			b, e := resourceBindingTx(ctx, tx, ref.BindingID, ` FOR SHARE`)
			if e != nil {
				return nil, e
			}
			if b.OriginTaskID != taskID || b.ProviderID != provider || b.ConnectionID != provider || b.Revision != revision || b.RemoteID != resources[i].RemoteID || b.Kind != resources[i].Kind || !providerresources.EqualSession(b.Session, resources[i].Session) {
				return nil, providerresources.ErrConflict
			}
		}
		return existing, nil
	}
	out := make([]core.ResourceReference, 0, len(resources))
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, resource := range resources {
		if creation != nil {
			j := providerresources.Job{BindingID: uuid.NewString(), OwnerID: owner, ActorID: actor, ProviderID: provider, ConnectionID: provider, ProviderOwnerID: powner, Revision: revision, Kind: creation.Kind, Intent: providerresources.CloneIntent(*creation)}
			b, e := insertCompletedResource(ctx, tx, j, resource, taskID)
			if e != nil {
				return nil, e
			}
			out = append(out, core.ResourceReference{BindingID: b.ID, ReferenceID: fmt.Sprintf("%s_1", b.Kind)})
			continue
		}
		if resource.Kind != "face_session" && resource.Kind != "editing_session" {
			return nil, providerresources.ErrInvalid
		}
		if resource.Status != core.ProviderSucceeded || resource.IsDeleted || resource.IsPreset || resource.BoundVoice != nil || !providerresources.ValidRemoteID(resource.RemoteID) || providerresources.ValidateSession(resource.Kind, resource.Session) != nil {
			return nil, providerresources.ErrInvalid
		}
		if !time.Now().Before(resource.Session.ExpiresAt) {
			return nil, providerresources.ErrNotFound
		}
		evidence, known := trusted[resource.Session.SourceSHA256]
		if !known || evidence.Duration != resource.Session.SourceVideoDuration {
			return nil, providerresources.ErrConflict
		}
		remoteKey := resource.Kind + "/" + resource.RemoteID
		if seen[remoteKey] {
			return nil, providerresources.ErrConflict
		}
		seen[remoteKey] = true
		duration := evidence.Duration
		sources := []core.ResourceSourceEvidence{{ReferenceID: taskID, MediaType: "video", SHA256: evidence.SHA256, Duration: &duration}}
		j := providerresources.Job{OwnerID: owner, ActorID: actor, ProviderID: provider, ConnectionID: provider, ProviderOwnerID: powner, Revision: revision}
		name := "Generated face session"
		if resource.Kind == "editing_session" {
			name = "Generated editing session"
		}
		b, e := insertResourceBinding(ctx, tx, j, uuid.NewString(), resource.RemoteID, resource.Kind, resource.Subtype, name, "", false, sources, providerresources.CloneSession(resource.Session), "")
		if e != nil {
			return nil, e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE provider_resources SET origin_task_id=$2 WHERE id=$1`, b.ID, taskID); e != nil {
			return nil, e
		}
		counts[resource.Kind]++
		out = append(out, core.ResourceReference{BindingID: b.ID, ReferenceID: fmt.Sprintf("%s_%d", resource.Kind, counts[resource.Kind])})
	}
	return out, nil
}

// resourceTaskCreationIntent compares the immutable public local-ID intent with
// the server-only byte-inspected creation request; it never accepts provider
// source URLs or result-reported durations as provenance.
func resourceTaskCreationIntent(options core.MediaOptions) (providerresources.Intent, error) {
	public, private := options.ResourceIntent, options.ResourceCreation
	if public == nil || private == nil || (public.Kind != "element" && public.Kind != "voice" && public.Kind != "file_resource") || public.Kind != private.Kind || public.Subtype != private.Subtype || public.Name != private.Name || public.Description != private.Description || !slices.Equal(public.Tags, private.Tags) {
		return providerresources.Intent{}, providerresources.ErrInvalid
	}
	intent := providerresources.Intent{Kind: public.Kind, Subtype: public.Subtype, Name: public.Name, Description: public.Description, GeneratedTaskID: public.GeneratedTaskID, BoundVoiceBindingID: public.BoundVoiceBindingID, Tags: slices.Clone(public.Tags)}
	expected := map[string]bool{}
	for _, id := range public.ReferenceIDs {
		if !validResourceUUID(id) || expected[id] {
			return intent, providerresources.ErrInvalid
		}
		expected[id] = true
	}
	for _, group := range []struct {
		kind    string
		sources []core.ResourceSourceInput
	}{{"image", private.Images}, {"video", private.Videos}, {"voice", private.VoiceSources}, {"file", private.FileSources}} {
		for _, source := range group.sources {
			if !expected[source.ReferenceID] || (group.kind != "voice" && group.kind != "file" && source.MediaType != group.kind) || (group.kind == "voice" && source.MediaType != "audio" && source.MediaType != "video") {
				return intent, providerresources.ErrInvalid
			}
			delete(expected, source.ReferenceID)
			evidence := core.InspectedResourceSource(source)
			intent.Sources = append(intent.Sources, evidence)
		}
	}
	if len(expected) != 0 {
		return intent, providerresources.ErrInvalid
	}
	if public.GeneratedTaskID != "" {
		generated := private.GeneratedVideo
		if generated == nil || generated.TaskID != public.GeneratedTaskID || (generated.TaskHandle == "" && generated.OutputID == "") {
			return intent, providerresources.ErrInvalid
		}
		evidence := core.ResourceSourceEvidence{ReferenceID: public.GeneratedTaskID, MediaType: "video", SHA256: generated.SHA256}
		if generated.Inspection != nil {
			inspection := core.CloneInputMediaEvidence(map[string]core.InputMediaEvidence{"source": *generated.Inspection})["source"]
			if inspection.ReferenceID != generated.TaskID || inspection.Type != "video" || inspection.SHA256 != generated.SHA256 || inspection.SizeBytes <= 0 || inspection.Duration <= 0 {
				return intent, providerresources.ErrInvalid
			}
			evidence.Inspection = &inspection
		}
		if generated.Duration != nil {
			duration := *generated.Duration
			evidence.Duration = &duration
		}
		intent.Sources = append(intent.Sources, evidence)
	} else if private.GeneratedVideo != nil {
		return intent, providerresources.ErrInvalid
	}
	if public.BoundVoiceBindingID != "" {
		if private.BoundVoice == nil || private.BoundVoice.BindingID != public.BoundVoiceBindingID || private.BoundVoice.Kind != "voice" {
			return intent, providerresources.ErrInvalid
		}
	} else if private.BoundVoice != nil {
		return intent, providerresources.ErrInvalid
	}
	if len(intent.Sources) == 0 {
		return intent, providerresources.ErrInvalid
	}
	if err := providerresources.ValidateIntent(intent); err != nil {
		return intent, err
	}
	return intent, nil
}

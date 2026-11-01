package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path"
	"reflect"
	"strconv"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

var _ core.AcceptedReceiptRepository = (*TaskRepository)(nil)
var _ core.AcceptedReceiptProgressRepository = (*TaskRepository)(nil)

// lockAcceptedTask independently checks the supplied immutable task snapshot.
// A claim token alone never grants another owner or connection's receipt.
func lockAcceptedTask(ctx context.Context, tx *sql.Tx, t core.Task) (*string, error) {
	if core.ClaimToken(ctx) == "" {
		return nil, core.ErrLeaseLost
	}
	var actual core.Task
	var remote *string
	e := tx.QueryRowContext(ctx, `SELECT id::text,owner_id::text,provider_owner_id::text,storage_owner_id::text,provider,model,provider_identity,media_type,COALESCE(actor_id::text,''),status,provider_task_id FROM tasks WHERE id=$1 FOR UPDATE`, t.ID).Scan(&actual.ID, &actual.OwnerID, &actual.ProviderOwnerID, &actual.StorageOwnerID, &actual.Provider, &actual.Model, &actual.ProviderIdentity, &actual.MediaType, &actual.ActorID, &actual.Status, &remote)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, core.ErrAcceptedReceiptInvalid
	}
	if e != nil {
		return nil, e
	}
	if e = checkTaskClaim(ctx, tx, t.ID); e != nil {
		return nil, e
	}
	if expected := core.ProviderRevision(ctx); expected != "" && expected != actual.ProviderIdentity {
		return nil, core.ErrProviderChanged
	}
	if actual.Status != core.TaskRunning || actual.ID != t.ID || actual.OwnerID != t.OwnerID || actual.ProviderOwnerID != t.ProviderOwnerID || actual.StorageOwnerID != t.StorageOwnerID || actual.Provider != t.Provider || actual.Model != t.Model || actual.ProviderIdentity != t.ProviderIdentity || actual.MediaType != t.MediaType || actual.ActorID != t.ActorID || identity.OwnerID(ctx) != actual.OwnerID || identity.ProviderOwnerID(ctx) != actual.ProviderOwnerID || identity.StorageOwnerID(ctx) != actual.StorageOwnerID {
		return nil, core.ErrAcceptedReceiptInvalid
	}
	return remote, nil
}

func (r *TaskRepository) SaveAcceptedReceipt(ctx context.Context, t core.Task, p core.ProviderTask) error {
	if core.ClaimToken(ctx) == "" {
		return core.ErrLeaseLost
	}
	raw, digest, e := core.EncodeAcceptedReceipt(t, p)
	if e != nil {
		return e
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	remote, e := lockAcceptedTask(ctx, tx, t)
	if e != nil {
		return e
	}
	if remote != nil && *remote != p.ID {
		return core.ErrAcceptedReceiptInvalid
	}
	var prior []byte
	var priorDigest string
	e = tx.QueryRowContext(ctx, `SELECT receipt,sha256 FROM accepted_provider_receipts WHERE task_id=$1`, t.ID).Scan(&prior, &priorDigest)
	if e == nil {
		if !bytes.Equal(prior, raw) || priorDigest != digest {
			return core.ErrAcceptedReceiptInvalid
		}
	} else if errors.Is(e, sql.ErrNoRows) {
		if _, e = tx.ExecContext(ctx, `INSERT INTO accepted_provider_receipts(task_id,receipt,sha256) VALUES($1,$2,$3)`, t.ID, raw, digest); e != nil {
			return e
		}
	} else {
		return e
	}
	if _, e = tx.ExecContext(ctx, `UPDATE tasks SET provider_task_id=$2,updated_at=NOW() WHERE id=$1 AND status='running'`, t.ID, p.ID); e != nil {
		return e
	}
	return tx.Commit()
}
func loadAcceptedReceiptTx(ctx context.Context, tx *sql.Tx, t core.Task, remote *string) (*core.ProviderTask, error) {
	var raw []byte
	var digest string
	e := tx.QueryRowContext(ctx, `SELECT receipt,sha256 FROM accepted_provider_receipts WHERE task_id=$1`, t.ID).Scan(&raw, &digest)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	p, e := core.DecodeAcceptedReceipt(t, raw, digest)
	if e != nil {
		return nil, e
	}
	if remote == nil || *remote != p.ID || t.ProviderTaskID != nil && *t.ProviderTaskID != p.ID {
		return nil, core.ErrAcceptedReceiptInvalid
	}
	return p, nil
}
func (r *TaskRepository) LoadAcceptedReceipt(ctx context.Context, t core.Task) (*core.ProviderTask, error) {
	if core.ClaimToken(ctx) == "" {
		return nil, core.ErrLeaseLost
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	remote, e := lockAcceptedTask(ctx, tx, t)
	if e != nil {
		return nil, e
	}
	p, e := loadAcceptedReceiptTx(ctx, tx, t, remote)
	if e != nil {
		return nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, e
	}
	return p, nil
}

type acceptedReceiptProgress struct {
	Version   int                   `json:"version"`
	Artifacts []core.StoredArtifact `json:"artifacts"`
	LastFrame *core.StoredArtifact  `json:"last_frame,omitempty"`
}

func receiptProgressDigest(raw []byte) string {
	s := sha256.Sum256(raw)
	return hex.EncodeToString(s[:])
}
func acceptedOutputs(t core.Task, p core.ProviderTask) []core.ProviderArtifact {
	if len(p.Artifacts) > 0 {
		return p.Artifacts
	}
	if p.ResultURL != "" {
		return []core.ProviderArtifact{{MediaType: t.MediaType, URL: p.ResultURL}}
	}
	return nil
}
func validStoredReceiptArtifact(a core.StoredArtifact) bool {
	if a.StorageKey == "" || len(a.StorageKey) > 4096 || strings.HasPrefix(a.StorageKey, "/") || path.Clean(a.StorageKey) != a.StorageKey || strings.Contains(a.StorageKey, "\\") || a.SizeBytes < 0 || len(a.SHA256) != 64 || len(a.ContentType) > 256 || a.ContentType == "" || a.Duration < 0 || math.IsNaN(a.Duration) || math.IsInf(a.Duration, 0) {
		return false
	}
	b, e := hex.DecodeString(a.SHA256)
	return e == nil && len(b) == 32 && strings.ToLower(a.SHA256) == a.SHA256
}
func progressArtifactKindMatches(a core.StoredArtifact, kind string) bool {
	switch kind {
	case "speech", "music":
		kind = "audio"
	case "binary":
		return a.ContentType == "application/octet-stream"
	case "json":
		return a.ContentType == "application/json"
	case "text":
		return strings.SplitN(a.ContentType, ";", 2)[0] == "text/plain"
	}
	return strings.HasPrefix(a.ContentType, kind+"/")
}

// receiptTaskPath accepts only the storage wrappers produced by direct Local,
// Manager local, or Manager S3. S3 may prepend its configured object prefix.
func receiptTaskPath(t core.Task, key string) (string, bool) {
	if key == "" || len(key) > 4096 || path.Clean(key) != key || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\\x00") {
		return "", false
	}
	relative := key
	if strings.HasPrefix(key, "s3:") {
		connection, object, ok := strings.Cut(strings.TrimPrefix(key, "s3:"), "/")
		if !ok {
			return "", false
		}
		if parsed, e := uuid.Parse(connection); e != nil || parsed.String() != connection {
			return "", false
		}
		anchor := t.OwnerID + "/results/" + t.ID + "/attempts/"
		if strings.HasPrefix(object, anchor) {
			relative = strings.TrimPrefix(object, t.OwnerID+"/results/")
		} else {
			_, tail, found := strings.Cut(object, "/"+anchor)
			if !found {
				return "", false
			}
			relative = t.ID + "/attempts/" + tail
		}
	} else if strings.HasPrefix(relative, t.OwnerID+"/results/") {
		relative = strings.TrimPrefix(relative, t.OwnerID+"/results/")
	}
	if !strings.HasPrefix(relative, t.ID+"/attempts/") {
		return "", false
	}
	attempt, tail, ok := strings.Cut(strings.TrimPrefix(relative, t.ID+"/attempts/"), "/")
	if !ok || attempt == "" || len(attempt) > 256 || tail == "" {
		return "", false
	}
	return relative, true
}
func receiptHasTaskPath(t core.Task, key string) bool { _, ok := receiptTaskPath(t, key); return ok }
func receiptCurrentClaimPath(t core.Task, key, token string) bool {
	relative, ok := receiptTaskPath(t, key)
	return ok && strings.HasPrefix(relative, t.ID+"/attempts/"+token+"/")
}
func validateReceiptProgress(t core.Task, p core.ProviderTask, progress acceptedReceiptProgress) error {
	outputs := acceptedOutputs(t, p)
	if progress.Version != 1 || len(progress.Artifacts) > len(outputs) || len(progress.Artifacts) > 64 {
		return core.ErrAcceptedReceiptInvalid
	}
	for i, a := range progress.Artifacts {
		original := outputs[i]
		if !validStoredReceiptArtifact(a) || a.ID != strconv.Itoa(i+1) || a.ProviderID != original.ID || a.MediaType != original.MediaType || a.Format != original.Format || a.Duration != original.Duration || a.GroupID != original.GroupID || !progressArtifactKindMatches(a, original.MediaType) || !receiptHasTaskPath(t, a.StorageKey) {
			return core.ErrAcceptedReceiptInvalid
		}
	}
	if a := progress.LastFrame; a != nil {
		if p.LastFrameURL == "" || len(progress.Artifacts) != len(outputs) || !validStoredReceiptArtifact(*a) || a.ID != "last-frame" || a.ProviderID != "" || a.Format != "" || a.Duration != 0 || a.GroupID != "" || a.MediaType != "image" || !strings.HasPrefix(a.ContentType, "image/") || !receiptHasTaskPath(t, a.StorageKey) {
			return core.ErrAcceptedReceiptInvalid
		}
	}
	return nil
}
func loadAcceptedProgressTx(ctx context.Context, tx *sql.Tx, t core.Task, p core.ProviderTask) (acceptedReceiptProgress, error) {
	var raw []byte
	var digest sql.NullString
	e := tx.QueryRowContext(ctx, `SELECT progress,progress_sha256 FROM accepted_provider_receipts WHERE task_id=$1`, t.ID).Scan(&raw, &digest)
	if e != nil {
		return acceptedReceiptProgress{}, e
	}
	if raw == nil && !digest.Valid {
		return acceptedReceiptProgress{Version: 1}, nil
	}
	if len(raw) == 0 || len(raw) > core.MaxAcceptedReceiptBytes || !digest.Valid || receiptProgressDigest(raw) != digest.String {
		return acceptedReceiptProgress{}, core.ErrAcceptedReceiptInvalid
	}
	var progress acceptedReceiptProgress
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&progress) != nil || decoder.Decode(new(any)) != io.EOF || validateReceiptProgress(t, p, progress) != nil {
		return acceptedReceiptProgress{}, core.ErrAcceptedReceiptInvalid
	}
	return progress, nil
}
func (r *TaskRepository) SaveAcceptedReceiptProgress(ctx context.Context, t core.Task, artifacts []core.StoredArtifact, lastFrame *core.StoredArtifact) error {
	if core.ClaimToken(ctx) == "" {
		return core.ErrLeaseLost
	}
	if len(artifacts) > 64 {
		return core.ErrAcceptedReceiptInvalid
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	remote, e := lockAcceptedTask(ctx, tx, t)
	if e != nil {
		return e
	}
	p, e := loadAcceptedReceiptTx(ctx, tx, t, remote)
	if e != nil {
		return e
	}
	if p == nil {
		return core.ErrAcceptedReceiptInvalid
	}
	next := acceptedReceiptProgress{Version: 1, Artifacts: artifacts, LastFrame: lastFrame}
	if e = validateReceiptProgress(t, *p, next); e != nil {
		return e
	}
	prior, e := loadAcceptedProgressTx(ctx, tx, t, *p)
	if e != nil {
		return e
	}
	if len(prior.Artifacts) > len(artifacts) || !reflect.DeepEqual(prior.Artifacts, artifacts[:len(prior.Artifacts)]) && len(prior.Artifacts) > 0 || prior.LastFrame != nil && !reflect.DeepEqual(prior.LastFrame, lastFrame) {
		return core.ErrAcceptedReceiptInvalid
	}
	for _, a := range artifacts[len(prior.Artifacts):] {
		if !receiptCurrentClaimPath(t, a.StorageKey, core.ClaimToken(ctx)) {
			return core.ErrAcceptedReceiptInvalid
		}
	}
	if prior.LastFrame == nil && lastFrame != nil && !receiptCurrentClaimPath(t, lastFrame.StorageKey, core.ClaimToken(ctx)) {
		return core.ErrAcceptedReceiptInvalid
	}
	raw, e := json.Marshal(next)
	if e != nil || len(raw) > core.MaxAcceptedReceiptBytes {
		return core.ErrAcceptedReceiptInvalid
	}
	if _, e = tx.ExecContext(ctx, `UPDATE accepted_provider_receipts SET progress=$2,progress_sha256=$3 WHERE task_id=$1`, t.ID, raw, receiptProgressDigest(raw)); e != nil {
		return e
	}
	return tx.Commit()
}
func (r *TaskRepository) LoadAcceptedReceiptProgress(ctx context.Context, t core.Task) ([]core.StoredArtifact, *core.StoredArtifact, error) {
	if core.ClaimToken(ctx) == "" {
		return nil, nil, core.ErrLeaseLost
	}
	tx, e := r.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, nil, e
	}
	defer tx.Rollback()
	remote, e := lockAcceptedTask(ctx, tx, t)
	if e != nil {
		return nil, nil, e
	}
	p, e := loadAcceptedReceiptTx(ctx, tx, t, remote)
	if e != nil {
		return nil, nil, e
	}
	if p == nil {
		return nil, nil, nil
	}
	progress, e := loadAcceptedProgressTx(ctx, tx, t, *p)
	if e != nil {
		return nil, nil, e
	}
	if e = tx.Commit(); e != nil {
		return nil, nil, e
	}
	return progress.Artifacts, progress.LastFrame, nil
}

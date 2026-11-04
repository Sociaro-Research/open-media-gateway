package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
)

const recoveryColumns = `operation_id::text,owner_id::text,attempt_id::text,origin,client_name,client_origin,device_ciphertext,identity_ciphertext,base_version,expires_at,attempt_revision,expected_version,expected_revision,destination,result_version,result_revision,saved_present,saved_snapshot,saved_identity_ciphertext,state,created_at,confirmed_at,access_intent`

func scanLegacyRecovery(row interface{ Scan(...any) error }) (v remote.Recovery, e error) {
	var saved []byte
	e = row.Scan(&v.OperationID, &v.Owner, &v.Attempt.ID, &v.Attempt.Origin, &v.Attempt.Name, &v.Attempt.ClientOrigin, &v.Attempt.Cipher, &v.IdentityCipher, &v.Attempt.Version, &v.Attempt.Expires, &v.Attempt.ConnectionRevision, &v.ExpectedVersion, &v.ExpectedRevision, &v.Destination, &v.Version, &v.Revision, &v.SavedPresent, &saved, &v.SavedIdentityCipher, &v.State, &v.Created, &v.Confirmed, &v.Attempt.Access)
	if errors.Is(e, sql.ErrNoRows) {
		return v, remote.ErrNotFound
	}
	if e != nil {
		return v, e
	}
	v.Origin = v.Attempt.Origin
	v.Attempt.Owner = v.Owner
	e = json.Unmarshal(saved, &v.Saved)
	return
}
func legacyGlobalState(ctx context.Context, tx *sql.Tx, exclusive bool) (int64, bool, error) {
	lock := " FOR SHARE"
	if exclusive {
		lock = " FOR UPDATE"
	}
	var revision int64
	var active bool
	e := tx.QueryRowContext(ctx, `SELECT revision,grant_id IS NOT NULL FROM company_interface_local_control WHERE singleton=TRUE`+lock).Scan(&revision, &active)
	return revision, active, e
}
func legacyGlobalRevision(ctx context.Context, tx *sql.Tx, exclusive bool) (int64, error) {
	revision, active, e := legacyGlobalState(ctx, tx, exclusive)
	if e == nil && active {
		return 0, remote.ErrConflict
	}
	return revision, e
}

func legacyRecoveryFence(ctx context.Context, tx *sql.Tx, owner string, version int64) error {
	var minimum int64
	if e := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(result_version),0) FROM remote_workspace_recovery WHERE owner_id=$1`, owner).Scan(&minimum); e != nil {
		return e
	}
	if version < minimum {
		return remote.ErrConflict
	}
	return nil
}

// Capture before remote credential verification; never read a new fence after
// an uncertain response and silently adopt it as authority for an old request.
func (r *SystemRepository) ConnectionRevision(ctx context.Context, owner string, version int64) (int64, error) {
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return 0, e
	}
	revision, e := legacyGlobalRevision(ctx, tx, false)
	if e != nil {
		return 0, e
	}
	if _, e = systemActor(ctx, tx, owner); e != nil {
		return 0, e
	}
	if e = legacyRecoveryFence(ctx, tx, owner, version); e != nil {
		return 0, e
	}
	return revision, tx.Commit()
}
func (r *SystemRepository) legacyRecoveryStep(name string) error {
	if r.legacyRecoveryFault != nil {
		return r.legacyRecoveryFault(name)
	}
	return nil
}
func (r *SystemRepository) QuarantineAuthorization(ctx context.Context, owner string, in remote.RecoveryInput) (remote.Recovery, error) {
	var empty remote.Recovery
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return empty, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return empty, e
	}
	revision, active, e := legacyGlobalState(ctx, tx, true)
	if e != nil {
		return empty, e
	}
	actor, e := systemActor(ctx, tx, owner)
	if e != nil {
		return empty, e
	}
	prior, e := scanLegacyRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM remote_workspace_recovery WHERE operation_id=$1 FOR UPDATE`, in.OperationID))
	if e == nil {
		if prior.Owner != owner || prior.Attempt.ID != in.AttemptID || prior.ExpectedVersion != in.Version || prior.ExpectedRevision != in.Revision || prior.Destination != in.Destination {
			return empty, remote.ErrConflict
		}
		return prior, tx.Commit()
	}
	if !errors.Is(e, remote.ErrNotFound) {
		return empty, e
	}
	if active || revision != in.Revision {
		return empty, remote.ErrConflict
	}
	saved, e := scanRemote(tx.QueryRowContext(ctx, `SELECT `+remoteColumns+` FROM remote_workspaces WHERE owner_id=$1 FOR UPDATE`, owner))
	present := e == nil
	if e != nil && !errors.Is(e, remote.ErrNotFound) {
		return empty, e
	}
	if saved.Version != in.Version {
		return empty, remote.ErrConflict
	}
	attempt, e := scanRemoteAuthorization(tx.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM remote_workspace_authorizations WHERE owner_id=$1 FOR UPDATE`, owner))
	if e != nil {
		return empty, e
	}
	if attempt.ID != in.AttemptID || attempt.Version > in.Version {
		return empty, remote.ErrConflict
	}
	var identity []byte
	if e = tx.QueryRowContext(ctx, `SELECT proof_ciphertext FROM remote_installation_identities WHERE origin=$1`, attempt.Origin).Scan(&identity); e != nil {
		return empty, e
	}
	var savedIdentity []byte
	if present && saved.Active && saved.Provisioning == "dedicated" {
		origin, oe := remote.Origin(saved.Origin)
		if oe != nil {
			return empty, remote.ErrConflict
		}
		if e = tx.QueryRowContext(ctx, `SELECT proof_ciphertext FROM remote_installation_identities WHERE origin=$1`, origin).Scan(&savedIdentity); e != nil {
			return empty, e
		}
	}
	snapshot, e := json.Marshal(remote.SavedCustody(saved))
	if e != nil {
		return empty, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO remote_workspace_recovery(operation_id,owner_id,attempt_id,origin,client_name,client_origin,device_ciphertext,identity_ciphertext,base_version,expires_at,attempt_revision,expected_version,expected_revision,destination,result_version,result_revision,saved_present,saved_snapshot,saved_identity_ciphertext,access_intent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$12::bigint+1,$13::bigint+1,$15,$16,$17,$18)`, in.OperationID, owner, attempt.ID, attempt.Origin, attempt.Name, attempt.ClientOrigin, attempt.Cipher, identity, attempt.Version, attempt.Expires, attempt.ConnectionRevision, in.Version, in.Revision, in.Destination, present, snapshot, savedIdentity, attempt.Access)
	if e != nil {
		return empty, e
	}
	if e = r.legacyRecoveryStep("archive"); e != nil {
		return empty, e
	}
	// Existing active authority remains intact. Only a missing row becomes an
	// inactive tombstone; never call Disconnect before preserving its custody.
	if present {
		_, e = tx.ExecContext(ctx, `UPDATE remote_workspaces SET version=$2,connection_revision=$3 WHERE owner_id=$1`, owner, in.Version+1, in.Revision+1)
	} else {
		_, e = tx.ExecContext(ctx, `INSERT INTO remote_workspaces(owner_id,version,active,connection_revision) VALUES($1,$2,FALSE,$3)`, owner, in.Version+1, in.Revision+1)
	}
	if e != nil {
		return empty, e
	}
	if e = r.legacyRecoveryStep("legacy-fence"); e != nil {
		return empty, e
	}
	result, e := tx.ExecContext(ctx, `UPDATE company_interface_local_control SET revision=revision+1 WHERE singleton=TRUE AND revision=$1 AND grant_id IS NULL`, in.Revision)
	if e != nil {
		return empty, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return empty, remote.ErrConflict
	}
	if e = r.legacyRecoveryStep("global-fence"); e != nil {
		return empty, e
	}
	result, e = tx.ExecContext(ctx, `DELETE FROM remote_workspace_authorizations WHERE owner_id=$1 AND id=$2`, owner, attempt.ID)
	if e != nil {
		return empty, e
	}
	n, _ = result.RowsAffected()
	if n != 1 {
		return empty, remote.ErrConflict
	}
	if e = r.legacyRecoveryStep("active-attempt"); e != nil {
		return empty, e
	}
	if e = auditSystem(ctx, tx, actor, "legacy_authorization_quarantined", owner, map[string]any{"attempt_id": attempt.ID, "version": in.Version + 1, "unresolved": true}); e != nil {
		return empty, e
	}
	if e = r.legacyRecoveryStep("audit"); e != nil {
		return empty, e
	}
	value, e := scanLegacyRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM remote_workspace_recovery WHERE operation_id=$1`, in.OperationID))
	if e != nil {
		return empty, e
	}
	if e = tx.Commit(); e != nil {
		return empty, e
	}
	return value, nil
}
func (r *SystemRepository) LegacyRecovery(ctx context.Context, owner, id string) (remote.Recovery, error) {
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return remote.Recovery{}, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return remote.Recovery{}, e
	}
	if _, e = systemActor(ctx, tx, owner); e != nil {
		return remote.Recovery{}, e
	}
	v, e := scanLegacyRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM remote_workspace_recovery WHERE operation_id=$1 AND owner_id=$2`, id, owner))
	if e != nil {
		return v, e
	}
	return v, tx.Commit()
}
func (r *SystemRepository) SettleLegacyRecovery(ctx context.Context, owner string, expected remote.Recovery) error {
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return e
	}
	if _, e = systemActor(ctx, tx, owner); e != nil {
		return e
	}
	v, e := scanLegacyRecovery(tx.QueryRowContext(ctx, `SELECT `+recoveryColumns+` FROM remote_workspace_recovery WHERE operation_id=$1 AND owner_id=$2 FOR UPDATE`, expected.OperationID, owner))
	if e != nil {
		return e
	}
	if v.Attempt.ID != expected.Attempt.ID || v.Attempt.Origin != expected.Attempt.Origin || v.Attempt.Version != expected.Attempt.Version || !bytes.Equal(v.Attempt.Cipher, expected.Attempt.Cipher) || !bytes.Equal(v.IdentityCipher, expected.IdentityCipher) || v.Version != expected.Version || v.Revision != expected.Revision {
		return remote.ErrConflict
	}
	if v.State == "cancelled" {
		return tx.Commit()
	}
	// Retain ciphertext/history even after terminal confirmation. It is never
	// activation authority; cleanup revokes only the original device request.
	if _, e = tx.ExecContext(ctx, `UPDATE remote_workspace_recovery SET state='cancelled',confirmed_at=clock_timestamp() WHERE operation_id=$1 AND state='pending'`, v.OperationID); e != nil {
		return e
	}
	return tx.Commit()
}

// Verified network replies are committed only while the exact original attempt
// and both captured revisions still hold. This check precedes idempotent save.
func (r *SystemRepository) CommitRemoteAuthorization(ctx context.Context, next remote.Record, expected remote.Attempt) (remote.Record, error) {
	// Older internal callers omitted the generation-only intent. This default
	// can never authorize company-api-v1.
	if next.Access == "" {
		next.Access = "generation-v1"
	}
	if expected.Access == "" {
		expected.Access = "generation-v1"
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return remote.Record{}, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return remote.Record{}, e
	}
	revision, e := legacyGlobalRevision(ctx, tx, false)
	if e != nil {
		return remote.Record{}, e
	}
	if revision != expected.ConnectionRevision {
		return remote.Record{}, remote.ErrConflict
	}
	if _, e = systemActor(ctx, tx, next.Owner); e != nil {
		return remote.Record{}, e
	}
	// Preserve legacy lock order: saved record before active attempt.
	var ignored int64
	e = tx.QueryRowContext(ctx, `SELECT version FROM remote_workspaces WHERE owner_id=$1 FOR UPDATE`, next.Owner).Scan(&ignored)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return remote.Record{}, e
	}
	current, e := scanRemoteAuthorization(tx.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM remote_workspace_authorizations WHERE owner_id=$1 FOR UPDATE`, next.Owner))
	if e != nil {
		return remote.Record{}, e
	}
	if next.Access != expected.Access || current.Access != expected.Access || current.ID != expected.ID || current.Owner != expected.Owner || next.Owner != expected.Owner || current.Origin != expected.Origin || next.Origin != expected.Origin || current.Version != expected.Version || current.ConnectionRevision != expected.ConnectionRevision || !bytes.Equal(current.Cipher, expected.Cipher) {
		return remote.Record{}, remote.ErrConflict
	}
	next.ConnectionRevision = revision
	saved, e := saveRemoteWorkspaceTx(ctx, tx, next, expected.Version, r.legacyRecoveryStep)
	if e != nil {
		return remote.Record{}, e
	}
	if e = r.legacyRecoveryStep("authorization-save"); e != nil {
		return remote.Record{}, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO remote_workspace_authorization_receipts(attempt_id,owner_id,origin,base_version,connection_revision,saved_version,token_hash) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(attempt_id) DO NOTHING`, expected.ID, next.Owner, expected.Origin, expected.Version, revision, saved.Version, saved.TokenHash)
	if e != nil {
		return remote.Record{}, e
	}
	var match bool
	e = tx.QueryRowContext(ctx, `SELECT owner_id=$2 AND origin=$3 AND base_version=$4 AND connection_revision=$5 AND saved_version=$6 AND token_hash=$7 FROM remote_workspace_authorization_receipts WHERE attempt_id=$1`, expected.ID, next.Owner, expected.Origin, expected.Version, revision, saved.Version, saved.TokenHash).Scan(&match)
	if e != nil {
		return remote.Record{}, e
	}
	if !match {
		return remote.Record{}, remote.ErrConflict
	}
	if e = r.legacyRecoveryStep("authorization-receipt"); e != nil {
		return remote.Record{}, e
	}
	return saved, tx.Commit()
}

func (r *SystemRepository) LegacyRecoveryStatus(ctx context.Context, owner string) (remote.RecoveryStatus, error) {
	return r.LegacyRecoveryPage(ctx, owner, "")
}
func (r *SystemRepository) LegacyRecoveryPage(ctx context.Context, owner, cursor string) (remote.RecoveryStatus, error) {
	var out remote.RecoveryStatus
	position, e := remote.DecodeRecoveryCursor(cursor)
	if e != nil {
		return out, e
	}
	var before, id any
	if cursor != "" {
		before = position.Created
		id = position.ID
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return out, e
	}
	if _, e = systemActor(ctx, tx, owner); e != nil {
		return out, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT revision,grant_id IS NULL FROM company_interface_local_control WHERE singleton=TRUE FOR SHARE`).Scan(&out.Revision, &out.RecoveryAllowed); e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT `+recoveryColumns+` FROM remote_workspace_recovery WHERE owner_id=$1 AND ($2::timestamptz IS NULL OR (created_at,operation_id)<($2::timestamptz,$3::uuid)) ORDER BY created_at DESC,operation_id DESC LIMIT 101`, owner, before, id)
	if e != nil {
		return out, e
	}
	out.Items = []remote.Recovery{}
	for rows.Next() {
		v, e := scanLegacyRecovery(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		out.Items = append(out.Items, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if len(out.Items) > 100 {
		out.Items = out.Items[:100]
		out.NextCursor = remote.EncodeRecoveryCursor(out.Items[99])
	}
	return out, tx.Commit()
}

package postgres

import (
	"context"
	"database/sql"
	"errors"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
	wa "github.com/Sociaro-Research/open-media-gateway/internal/workspaceauth"
	"github.com/google/uuid"
)

const attemptColumns = `owner_id::text,id::text,origin,client_name,client_origin,device_ciphertext,base_version,expires_at,connection_revision,access_intent`

func scanRemoteAuthorization(row interface{ Scan(...any) error }) (v remote.Attempt, err error) {
	err = row.Scan(&v.Owner, &v.ID, &v.Origin, &v.Name, &v.ClientOrigin, &v.Cipher, &v.Version, &v.Expires, &v.ConnectionRevision, &v.Access)
	if errors.Is(err, sql.ErrNoRows) {
		err = remote.ErrNotFound
	}
	return
}
func (r *SystemRepository) AuthorizationAttempt(ctx context.Context, owner string) (remote.Attempt, error) {
	return scanRemoteAuthorization(r.db.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM remote_workspace_authorizations WHERE owner_id=$1`, owner))
}
func (r *SystemRepository) SaveAuthorizationAttempt(ctx context.Context, next remote.Attempt) (remote.Attempt, error) {
	if next.Access == "" {
		next.Access = wa.GenerationAccess
	}
	if next.Access != wa.GenerationAccess && next.Access != wa.CompanyAccess {
		return next, remote.ErrConflict
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return next, err
	}
	next.ConnectionRevision, err = legacyGlobalRevision(ctx, tx, false)
	if err != nil {
		return next, err
	}
	if _, err = systemActor(ctx, tx, next.Owner); err != nil {
		return next, err
	}
	var version int64
	err = tx.QueryRowContext(ctx, `SELECT version FROM remote_workspaces WHERE owner_id=$1 FOR UPDATE`, next.Owner).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return next, err
	}
	if version != next.Version {
		return next, remote.ErrConflict
	}
	current, err := scanRemoteAuthorization(tx.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM remote_workspace_authorizations WHERE owner_id=$1 FOR UPDATE`, next.Owner))
	if err == nil {
		if current.Access != next.Access || current.Origin != next.Origin || current.Name != next.Name || current.ClientOrigin != next.ClientOrigin || current.Version != next.Version || current.ConnectionRevision != next.ConnectionRevision {
			return next, remote.ErrConflict
		}
		return current, tx.Commit()
	}
	if !errors.Is(err, remote.ErrNotFound) {
		return next, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO remote_workspace_authorizations(owner_id,id,origin,client_name,client_origin,device_ciphertext,base_version,expires_at,connection_revision,access_intent) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(owner_id) DO UPDATE SET id=EXCLUDED.id,origin=EXCLUDED.origin,client_name=EXCLUDED.client_name,client_origin=EXCLUDED.client_origin,device_ciphertext=EXCLUDED.device_ciphertext,base_version=EXCLUDED.base_version,expires_at=EXCLUDED.expires_at,connection_revision=EXCLUDED.connection_revision,access_intent=EXCLUDED.access_intent`, next.Owner, next.ID, next.Origin, next.Name, next.ClientOrigin, next.Cipher, next.Version, next.Expires, next.ConnectionRevision, next.Access)
	if err != nil {
		return next, err
	}
	return next, tx.Commit()
}
func (r *SystemRepository) ClearAuthorizationAttempt(ctx context.Context, owner, id string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	if _, err = systemActor(ctx, tx, owner); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM remote_workspace_authorizations WHERE owner_id=$1 AND id::text=$2`, owner, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return remote.ErrConflict
	}
	return tx.Commit()
}

// A random proof identifies this installation's connection to one central origin. Never derive it from display names, origins or browser state.
// It survives disconnect and device request cancellation; only ciphertext is local.
func (r *SystemRepository) InstallationIdentity(ctx context.Context, owner, origin string, candidate []byte) ([]byte, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return nil, err
	}
	if _, err = systemActor(ctx, tx, owner); err != nil {
		return nil, err
	}
	var ciphertext []byte
	if candidate == nil {
		if err = tx.QueryRowContext(ctx, `SELECT proof_ciphertext FROM remote_installation_identities WHERE origin=$1`, origin).Scan(&ciphertext); err != nil {
			return nil, err
		}
		return ciphertext, tx.Commit()
	}
	if err = tx.QueryRowContext(ctx, `INSERT INTO remote_installation_identities(created_by,origin,proof_ciphertext) VALUES($1,$2,$3) ON CONFLICT(origin) DO UPDATE SET origin=EXCLUDED.origin RETURNING proof_ciphertext`, owner, origin, candidate).Scan(&ciphertext); err != nil {
		return nil, err
	}
	return ciphertext, tx.Commit()
}

// FinishSavedAuthorization only clears the original attempt after its dedicated
// connection has already committed. Dedicated writes come from verified device
// redemption; manual key replacement cannot create or replace such a connection.
// The exact one-step revision and owner/origin checks exclude disconnects, other
// attempts, legacy replacements and later connection changes. Expiry blocks new
// issuance, not cleanup of this already-verified save. No key/grant is revoked.
func (r *SystemRepository) FinishSavedAuthorization(ctx context.Context, owner, id string) (remote.Record, bool, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return remote.Record{}, false, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return remote.Record{}, false, err
	}
	if _, err = systemActor(ctx, tx, owner); err != nil {
		return remote.Record{}, false, err
	}
	revision, err := legacyGlobalRevision(ctx, tx, false)
	if err != nil {
		return remote.Record{}, false, err
	}
	saved, err := scanRemote(tx.QueryRowContext(ctx, `SELECT `+remoteColumns+` FROM remote_workspaces WHERE owner_id=$1 FOR UPDATE`, owner))
	if errors.Is(err, remote.ErrNotFound) {
		return remote.Record{}, false, nil
	}
	if err != nil {
		return remote.Record{}, false, err
	}
	attempt, err := scanRemoteAuthorization(tx.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM remote_workspace_authorizations WHERE owner_id=$1 FOR UPDATE`, owner))
	if err != nil {
		return remote.Record{}, false, err
	}
	if attempt.ID != id {
		return remote.Record{}, false, remote.ErrConflict
	}
	if attempt.ConnectionRevision != revision || saved.ConnectionRevision != revision {
		return remote.Record{}, false, remote.ErrConflict
	}
	if err = legacyRecoveryFence(ctx, tx, owner, attempt.Version); err != nil {
		return remote.Record{}, false, err
	}
	if saved.Version == attempt.Version {
		return remote.Record{}, false, nil
	}
	a, ae := remote.Origin(saved.Origin)
	b, be := remote.Origin(attempt.Origin)
	_, keyErr := uuid.Parse(saved.KeyID)
	_, userErr := uuid.Parse(saved.UserID)
	_, companyErr := uuid.Parse(saved.OrganizationID)
	if saved.Access != attempt.Access || !saved.Active || saved.Provisioning != "dedicated" || saved.Version != attempt.Version+1 || ae != nil || be != nil || a != b || len(saved.TokenHash) != 32 || saved.VerifiedAt == nil || keyErr != nil || userErr != nil || companyErr != nil {
		return remote.Record{}, false, remote.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM remote_workspace_authorizations WHERE owner_id=$1 AND id::text=$2`, owner, id); err != nil {
		return remote.Record{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return remote.Record{}, false, err
	}
	return saved, true, nil
}

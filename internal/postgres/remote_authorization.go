package postgres

import (
	"context"
	"database/sql"
	"errors"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
)

const attemptColumns = `owner_id::text,id::text,origin,client_name,client_origin,device_ciphertext,base_version,expires_at`

func scanRemoteAuthorization(row interface{ Scan(...any) error }) (v remote.Attempt, err error) {
	err = row.Scan(&v.Owner, &v.ID, &v.Origin, &v.Name, &v.ClientOrigin, &v.Cipher, &v.Version, &v.Expires)
	if errors.Is(err, sql.ErrNoRows) {
		err = remote.ErrNotFound
	}
	return
}
func (r *SystemRepository) AuthorizationAttempt(ctx context.Context, owner string) (remote.Attempt, error) {
	return scanRemoteAuthorization(r.db.QueryRowContext(ctx, `SELECT `+attemptColumns+` FROM remote_workspace_authorizations WHERE owner_id=$1`, owner))
}
func (r *SystemRepository) SaveAuthorizationAttempt(ctx context.Context, next remote.Attempt) (remote.Attempt, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
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
		if current.Origin != next.Origin || current.Name != next.Name || current.ClientOrigin != next.ClientOrigin || current.Version != next.Version {
			return next, remote.ErrConflict
		}
		return current, tx.Commit()
	}
	if !errors.Is(err, remote.ErrNotFound) {
		return next, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO remote_workspace_authorizations(owner_id,id,origin,client_name,client_origin,device_ciphertext,base_version,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(owner_id) DO UPDATE SET id=EXCLUDED.id,origin=EXCLUDED.origin,client_name=EXCLUDED.client_name,client_origin=EXCLUDED.client_origin,device_ciphertext=EXCLUDED.device_ciphertext,base_version=EXCLUDED.base_version,expires_at=EXCLUDED.expires_at`, next.Owner, next.ID, next.Origin, next.Name, next.ClientOrigin, next.Cipher, next.Version, next.Expires)
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

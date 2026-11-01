package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	remote "github.com/Sociaro-Research/open-media-gateway/internal/remoteworkspace"
)

const remoteColumns = `owner_id::text,version,active,origin,organization_id,organization_name,remote_user_id,remote_key_id,token_hash,token_ciphertext,verified_at`

func scanRemote(row interface{ Scan(...any) error }) (r remote.Record, err error) {
	err = row.Scan(&r.Owner, &r.Version, &r.Active, &r.Origin, &r.OrganizationID, &r.OrganizationName, &r.UserID, &r.KeyID, &r.TokenHash, &r.Ciphertext, &r.VerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		err = remote.ErrNotFound
	}
	return
}
func (r *SystemRepository) RemoteWorkspace(ctx context.Context, owner string) (remote.Record, error) {
	return scanRemote(r.db.QueryRowContext(ctx, `SELECT `+remoteColumns+` FROM remote_workspaces WHERE owner_id=$1`, owner))
}
func (r *SystemRepository) SaveRemoteWorkspace(ctx context.Context, next remote.Record, version int64) (remote.Record, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return remote.Record{}, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return remote.Record{}, err
	}
	name, err := systemActor(ctx, tx, next.Owner)
	if err != nil {
		return remote.Record{}, err
	}
	current, err := scanRemote(tx.QueryRowContext(ctx, `SELECT `+remoteColumns+` FROM remote_workspaces WHERE owner_id=$1 FOR UPDATE`, next.Owner))
	if errors.Is(err, remote.ErrNotFound) {
		if version != 0 {
			return remote.Record{}, remote.ErrConflict
		}
		current = remote.Record{Owner: next.Owner}
	} else if err != nil {
		return remote.Record{}, err
	}
	if version != current.Version {
		if current.Active && current.Version == version+1 && current.Origin == next.Origin && current.OrganizationID == next.OrganizationID && bytes.Equal(current.TokenHash, next.TokenHash) {
			return current, tx.Commit()
		}
		return remote.Record{}, remote.ErrConflict
	}
	next.Version = current.Version + 1
	_, err = tx.ExecContext(ctx, `INSERT INTO remote_workspaces(owner_id,version,active,origin,organization_id,organization_name,remote_user_id,remote_key_id,token_hash,token_ciphertext,verified_at) VALUES($1,$2,TRUE,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT(owner_id) DO UPDATE SET version=EXCLUDED.version,active=TRUE,origin=EXCLUDED.origin,organization_id=EXCLUDED.organization_id,organization_name=EXCLUDED.organization_name,remote_user_id=EXCLUDED.remote_user_id,remote_key_id=EXCLUDED.remote_key_id,token_hash=EXCLUDED.token_hash,token_ciphertext=EXCLUDED.token_ciphertext,verified_at=EXCLUDED.verified_at`, next.Owner, next.Version, next.Origin, next.OrganizationID, next.OrganizationName, next.UserID, next.KeyID, next.TokenHash, next.Ciphertext, next.VerifiedAt)
	if err != nil {
		return remote.Record{}, err
	}
	if err = auditSystem(ctx, tx, name, "remote_workspace_connected", next.Owner, map[string]any{"origin": next.Origin, "organization_id": next.OrganizationID, "version": next.Version}); err != nil {
		return remote.Record{}, err
	}
	// Connecting remote work never changes local provider/funding policy.
	if _, err = tx.ExecContext(ctx, `UPDATE system_settings SET setup_pending=FALSE WHERE id=TRUE`); err != nil {
		return remote.Record{}, err
	}
	return next, tx.Commit()
}
func (r *SystemRepository) DisconnectRemoteWorkspace(ctx context.Context, owner string, version int64) (remote.Record, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return remote.Record{}, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return remote.Record{}, err
	}
	name, err := systemActor(ctx, tx, owner)
	if err != nil {
		return remote.Record{}, err
	}
	current, err := scanRemote(tx.QueryRowContext(ctx, `SELECT `+remoteColumns+` FROM remote_workspaces WHERE owner_id=$1 FOR UPDATE`, owner))
	if err != nil {
		return current, err
	}
	if !current.Active && current.Version == version+1 {
		return current, tx.Commit()
	}
	if current.Version != version {
		return current, remote.ErrConflict
	}
	current = remote.Record{Owner: owner, Version: version + 1}
	if _, err = tx.ExecContext(ctx, `UPDATE remote_workspaces SET version=$2,active=FALSE,origin='',organization_id='',organization_name='',remote_user_id='',remote_key_id='',token_hash=NULL,token_ciphertext=NULL,verified_at=NULL WHERE owner_id=$1`, owner, current.Version); err != nil {
		return current, err
	}
	if err = auditSystem(ctx, tx, name, "remote_workspace_disconnected", owner, map[string]any{"version": current.Version}); err != nil {
		return current, err
	}
	return current, tx.Commit()
}

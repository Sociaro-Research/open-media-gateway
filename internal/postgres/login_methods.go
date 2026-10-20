package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Sociaro-Research/media-gateway-dev/internal/auth"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

func (r *SystemRepository) LoginProviders(ctx context.Context) ([]auth.LoginProviderRecord, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT provider,enabled,connected,client_id,secret_ciphertext,version FROM login_providers ORDER BY provider`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []auth.LoginProviderRecord{}
	for rows.Next() {
		var next auth.LoginProviderRecord
		if err = rows.Scan(&next.Provider, &next.Enabled, &next.Connected, &next.ClientID, &next.SecretCiphertext, &next.Version); err != nil {
			return nil, err
		}
		result = append(result, next)
	}
	return result, rows.Err()
}
func (r *SystemRepository) SaveLoginProvider(ctx context.Context, actor string, next auth.LoginProviderRecord) (auth.LoginProviderRecord, error) {
	if actor == "" {
		return next, system.ErrForbidden
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return next, err
	}
	_, err = systemActor(ctx, tx, actor)
	if err != nil {
		return next, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE login_providers SET enabled=$2,connected=$3,client_id=$4,secret_ciphertext=$5,version=version+1,updated_at=NOW() WHERE provider=$1 AND version=$6 RETURNING version`, next.Provider, next.Enabled, next.Connected, next.ClientID, next.SecretCiphertext, next.Version).Scan(&next.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return next, system.ErrConflict
	}
	if err != nil {
		return next, err
	}
	return next, tx.Commit()
}
func (r *SystemRepository) BootstrapGoogleLogin(ctx context.Context, next auth.LoginProviderRecord) error {
	_, err := r.db.ExecContext(ctx, `UPDATE login_providers SET connected=TRUE,client_id=$1,secret_ciphertext=$2,version=version+1,updated_at=NOW() WHERE provider='google' AND version=1 AND connected=FALSE`, next.ClientID, next.SecretCiphertext)
	return err
}

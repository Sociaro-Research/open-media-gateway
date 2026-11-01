package postgres

import (
	"context"
	"crypto/subtle"
	"database/sql"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/google/uuid"
)

func (r *AuthRepository) InstallationSetup(ctx context.Context) (auth.SetupState, error) {
	var s auth.SetupState
	err := r.db.QueryRowContext(ctx, `SELECT completed_at IS NULL AND NOT EXISTS(SELECT 1 FROM users),code_hash,code_ciphertext FROM installation_setup WHERE id=TRUE`).Scan(&s.Required, &s.CodeHash, &s.CodeCiphertext)
	return s, err
}

func (r *AuthRepository) EnsureInstallationSetup(ctx context.Context, hash, encrypted []byte) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE installation_setup SET code_hash=$1,code_ciphertext=$2 WHERE id=TRUE AND completed_at IS NULL AND code_hash IS NULL AND NOT EXISTS(SELECT 1 FROM users)`, hash, encrypted)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *AuthRepository) CompleteInstallationSetup(ctx context.Context, in auth.SetupInput) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize with all trusted first-administrator commands as well as other
	// setup requests. User, password, role, session and consumed code commit once.
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	var hash []byte
	var completed sql.NullTime
	var occupied bool
	if err = tx.QueryRowContext(ctx, `SELECT code_hash,completed_at,EXISTS(SELECT 1 FROM users) FROM installation_setup WHERE id=TRUE FOR UPDATE`).Scan(&hash, &completed, &occupied); err != nil {
		return err
	}
	if completed.Valid || occupied {
		return auth.ErrSetupClosed
	}
	if len(in.CodeHash) != 32 || subtle.ConstantTimeCompare(hash, in.CodeHash) != 1 {
		return auth.ErrSetupCode
	}
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name,is_system_admin) VALUES($1,$2,$3,TRUE)`, id, in.Email, in.Name); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_credentials(user_id,password_hash) VALUES($1,$2)`, id, in.PasswordHash); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, in.Session.Hash, id, in.Session.ExpiresAt); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE login_providers SET enabled=TRUE,version=version+1,updated_at=NOW() WHERE provider='email' AND NOT enabled`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE installation_setup SET completed_at=NOW(),code_hash=NULL,code_ciphertext=NULL WHERE id=TRUE`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE system_settings SET setup_pending=TRUE WHERE id=TRUE`); err != nil {
		return err
	}
	if err = setAuditActor(ctx, tx, id); err != nil {
		return err
	}
	if err = auditSystem(ctx, tx, "["+id+"]", "administrator_setup", id, map[string]string{"method": "installation_code"}); err != nil {
		return err
	}
	return tx.Commit()
}

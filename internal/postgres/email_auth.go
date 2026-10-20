package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/google/uuid"
)

func (r *AuthRepository) EmailAccount(ctx context.Context, email string) (auth.PasswordAccount, error) {
	var a auth.PasswordAccount
	err := r.db.QueryRowContext(ctx, `SELECT u.id,u.email,u.display_name,COALESCE(u.picture_url,''),c.password_hash FROM users u JOIN email_credentials c ON c.user_id=u.id WHERE LOWER(u.email)=$1`, email).Scan(&a.User.ID, &a.User.Email, &a.User.Name, &a.User.Picture, &a.PasswordHash)
	return a, err
}

func (r *AuthRepository) SaveEmailChallenge(ctx context.Context, email, purpose string, hash []byte, expires time.Time, invitation []byte) (bool, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Serialize requests for this canonical mailbox, including first registration.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 26))`, email); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM email_auth_challenges WHERE expires_at<=NOW()`); err != nil {
		return false, err
	}
	var userID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE LOWER(email)=$1 FOR UPDATE`, email).Scan(&userID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if (purpose == "verify" && userID != "") || (purpose == "reset" && userID == "") {
		return false, nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO email_auth_challenges(token_hash,email,purpose,password_hash,user_id,expires_at,invitation_ciphertext) VALUES($1,$2,$3,'',$4,$5,$6) ON CONFLICT(email,purpose) DO UPDATE SET token_hash=EXCLUDED.token_hash,password_hash='',user_id=EXCLUDED.user_id,expires_at=EXCLUDED.expires_at,invitation_ciphertext=EXCLUDED.invitation_ciphertext`, hash, email, purpose, nullableString(userID), expires, invitation)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *AuthRepository) EmailChallenge(ctx context.Context, hash []byte, purpose string, now time.Time) (auth.EmailChallenge, error) {
	var c auth.EmailChallenge
	err := r.db.QueryRowContext(ctx, `SELECT email,purpose,invitation_ciphertext FROM email_auth_challenges WHERE token_hash=$1 AND purpose=$2 AND expires_at>$3`, hash, purpose, now).Scan(&c.Email, &c.Purpose, &c.InvitationCiphertext)
	if errors.Is(err, sql.ErrNoRows) {
		err = auth.ErrEmailCredential
	}
	return c, err
}

// VerifyEmailRegistration consumes the emailed link and issues a limited grant.
// No user, credential or normal session exists until a password is chosen.
func (r *AuthRepository) VerifyEmailRegistration(ctx context.Context, hash []byte, grant auth.EmailSession, now time.Time) (auth.EmailChallenge, error) {
	var challenge auth.EmailChallenge
	if len(grant.Hash) != sha256.Size || !grant.ExpiresAt.After(now) {
		return challenge, auth.ErrEmailCredential
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return challenge, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT email FROM email_auth_challenges WHERE token_hash=$1 AND purpose='verify' AND expires_at>$2`, hash, now).Scan(&challenge.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return challenge, auth.ErrEmailCredential
	}
	if err != nil {
		return challenge, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 26))`, challenge.Email); err != nil {
		return challenge, err
	}
	err = tx.QueryRowContext(ctx, `DELETE FROM email_auth_challenges WHERE token_hash=$1 AND purpose='verify' AND expires_at>GREATEST($2,clock_timestamp()) RETURNING email,invitation_ciphertext`, hash, now).Scan(&challenge.Email, &challenge.InvitationCiphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return challenge, auth.ErrEmailCredential
	}
	if err != nil {
		return challenge, err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email)=$1)`, challenge.Email).Scan(&exists); err != nil {
		return challenge, err
	}
	if exists {
		return challenge, auth.ErrEmailCredential
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_auth_challenges(token_hash,email,purpose,expires_at,invitation_ciphertext) VALUES($1,$2,'setup',$3,$4) ON CONFLICT(email,purpose) DO UPDATE SET token_hash=EXCLUDED.token_hash,password_hash='',expires_at=EXCLUDED.expires_at,invitation_ciphertext=EXCLUDED.invitation_ciphertext`, grant.Hash, challenge.Email, grant.ExpiresAt, challenge.InvitationCiphertext); err != nil {
		return challenge, err
	}
	challenge.Purpose = "setup"
	return challenge, tx.Commit()
}

func (r *AuthRepository) CompleteEmailChallenge(ctx context.Context, hash []byte, purpose, passwordHash string, now time.Time, session *auth.EmailSession) (identity.User, error) {
	if (purpose == "setup" && (session == nil || len(session.Hash) != sha256.Size || !session.ExpiresAt.After(now))) || (purpose == "reset" && session != nil) || (purpose != "setup" && purpose != "reset") {
		return identity.User{}, auth.ErrEmailCredential
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return identity.User{}, err
	}
	defer tx.Rollback()
	var email string
	var userID sql.NullString
	// Read before the mailbox lock so every mutating flow takes locks in the same
	// order. The DELETE below rechecks expiry and ensures single use atomically.
	err = tx.QueryRowContext(ctx, `SELECT email FROM email_auth_challenges WHERE token_hash=$1 AND purpose=$2 AND expires_at>$3`, hash, purpose, now).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.User{}, auth.ErrEmailCredential
	}
	if err != nil {
		return identity.User{}, err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 26))`, email); err != nil {
		return identity.User{}, err
	}
	err = tx.QueryRowContext(ctx, `DELETE FROM email_auth_challenges WHERE token_hash=$1 AND purpose=$2 AND expires_at>GREATEST($3,clock_timestamp()) RETURNING email,user_id`, hash, purpose, now).Scan(&email, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return identity.User{}, auth.ErrEmailCredential
	}
	if err != nil {
		return identity.User{}, err
	}
	var user identity.User
	if purpose == "setup" {
		err = tx.QueryRowContext(ctx, `INSERT INTO users(id,email,display_name) VALUES($1,$2,$3) ON CONFLICT (LOWER(email)) DO NOTHING RETURNING id,email,display_name`, uuid.NewString(), email, strings.Split(email, "@")[0]).Scan(&user.ID, &user.Email, &user.Name)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT id,email,display_name,COALESCE(picture_url,'') FROM users WHERE id=$1 AND LOWER(email)=$2 FOR UPDATE`, userID, email).Scan(&user.ID, &user.Email, &user.Name, &user.Picture)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return user, auth.ErrEmailCredential
	}
	if err != nil {
		return user, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO email_credentials(user_id,password_hash) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET password_hash=EXCLUDED.password_hash,updated_at=NOW()`, user.ID, passwordHash); err != nil {
		return user, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM email_auth_challenges WHERE email=$1`, email); err != nil {
		return user, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE user_id=$1`, user.ID); err != nil {
		return user, err
	}
	if session != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO user_sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, session.Hash, user.ID, session.ExpiresAt); err != nil {
			return user, err
		}
	}
	return user, tx.Commit()
}

func (r *AuthRepository) CreateEmailSession(ctx context.Context, userID, passwordHash string, hash []byte, expires time.Time) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the credential until session insertion commits; a concurrent reset
	// either invalidates this session afterward or makes the hash comparison fail.
	var current string
	err = tx.QueryRowContext(ctx, `SELECT password_hash FROM email_credentials WHERE user_id=$1 FOR UPDATE`, userID).Scan(&current)
	if err != nil || current != passwordHash {
		return auth.ErrEmailCredential
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE expires_at<=NOW()`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO user_sessions(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, hash, userID, expires); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *AuthRepository) AllowEmailAttempt(ctx context.Context, key []byte, limit int, window time.Duration) (bool, error) {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM email_auth_limits WHERE expires_at<=NOW()`); err != nil {
		return false, err
	}
	var attempts int
	err := r.db.QueryRowContext(ctx, `INSERT INTO email_auth_limits(key_hash,attempts,expires_at) VALUES($1,1,NOW()+$2 * interval '1 second') ON CONFLICT(key_hash) DO UPDATE SET attempts=email_auth_limits.attempts+1 RETURNING attempts`, key, int(window.Seconds())).Scan(&attempts)
	return attempts <= limit, err
}

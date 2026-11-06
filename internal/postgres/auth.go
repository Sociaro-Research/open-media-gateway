package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
)

type AuthRepository struct {
	db *sql.DB
}

func NewAuthRepository(db *sql.DB) *AuthRepository {
	return &AuthRepository{db: db}
}

func (r *AuthRepository) UpsertGoogleUser(ctx context.Context, subject, email, name, picture string) (identity.User, error) {
	return r.upsertGoogleUser(ctx, subject, email, name, picture, false)
}

// SignInGoogleUser only accepts verified Google claims. Email-based linking is
// allowed when Google is authoritative for the mailbox, never on address alone.
func (r *AuthRepository) SignInGoogleUser(ctx context.Context, google auth.GoogleIdentity) (identity.User, error) {
	if google.Subject == "" || strings.TrimSpace(google.Email) == "" || !google.EmailVerified {
		return identity.User{}, auth.ErrUnauthenticated
	}
	return r.upsertGoogleUser(ctx, google.Subject, google.Email, google.Name, google.Picture, google.AuthoritativeEmail())
}

func (r *AuthRepository) upsertGoogleUser(ctx context.Context, subject, email, name, picture string, allowEmailLink bool) (identity.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	// The stable Google subject takes precedence over a possibly changed email.
	user, err := scanProfile(r.db.QueryRowContext(ctx, `
		UPDATE users SET
			email = $2,
			display_name = CASE WHEN custom_display_name THEN display_name ELSE $3 END,
			picture_url = COALESCE($4, picture_url),
			updated_at = NOW()
		WHERE google_subject = $1
		RETURNING id, email, display_name, COALESCE(picture_url, ''), theme, report_columns, color_preset, contacts, table_columns, table_wide`,
		subject, email, name, nullableString(picture),
	))
	if errors.Is(err, sql.ErrNoRows) {
		// The unique email index serializes concurrent first logins. Only an
		// unbound account may gain a subject; an existing binding is immutable.
		// Preserve the original profile, credentials, memberships and role flags.
		user, err = scanProfile(r.db.QueryRowContext(ctx, `
			INSERT INTO users (id, google_subject, email, display_name, picture_url, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
			ON CONFLICT (LOWER(email)) DO UPDATE SET
				google_subject = EXCLUDED.google_subject, updated_at = NOW()
			WHERE (users.google_subject IS NULL AND $6) OR users.google_subject = EXCLUDED.google_subject
			RETURNING id, email, display_name, COALESCE(picture_url, ''), theme, report_columns, color_preset, contacts, table_columns, table_wide`,
			uuid.NewString(), subject, email, name, nullableString(picture), allowEmailLink,
		))
	}
	var conflict *pgconn.PgError
	if errors.Is(err, sql.ErrNoRows) || (errors.As(err, &conflict) && conflict.Code == "23505" &&
		(conflict.ConstraintName == "users_email_unique_idx" || conflict.ConstraintName == "users_google_subject_key")) {
		return identity.User{}, auth.ErrGoogleAccountConflict
	}
	if err != nil {
		return identity.User{}, fmt.Errorf("upsert Google user: %w", err)
	}
	return user, nil
}

func (r *AuthRepository) CreateSession(ctx context.Context, tokenHash []byte, userID string, expiresAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_sessions WHERE expires_at <= NOW()`); err != nil {
		return fmt.Errorf("remove expired sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_sessions (token_hash, user_id, expires_at, created_at)
		VALUES ($1, $2, $3, NOW())`, tokenHash, userID, expiresAt); err != nil {
		return fmt.Errorf("create user session: %w", err)
	}
	return tx.Commit()
}

func (r *AuthRepository) UserBySession(ctx context.Context, tokenHash []byte, now time.Time) (identity.User, error) {
	user, err := scanProfile(r.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.display_name, COALESCE(u.picture_url, ''), u.theme, u.report_columns, u.color_preset, u.contacts, u.table_columns, u.table_wide
		FROM user_sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > $2`, tokenHash, now,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return identity.User{}, errors.New("session not found")
	}
	if err != nil {
		return identity.User{}, fmt.Errorf("read user session: %w", err)
	}
	// This second read must still find the same live cookie session. A concurrent
	// logout fails closed; the stable anchor is never derived from browser SessionID.
	if err = r.db.QueryRowContext(ctx, `SELECT session_id::text FROM user_sessions WHERE token_hash=$1 AND user_id=$2 AND expires_at>$3`, tokenHash, user.ID, now).Scan(&user.SourceSessionID); err != nil {
		return identity.User{}, errors.New("session not found")
	}
	return user, nil
}

func (r *AuthRepository) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete user session: %w", err)
	}
	return nil
}

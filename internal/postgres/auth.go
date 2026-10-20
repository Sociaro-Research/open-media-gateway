package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
)

type AuthRepository struct {
	db *sql.DB
}

func NewAuthRepository(db *sql.DB) *AuthRepository {
	return &AuthRepository{db: db}
}

func (r *AuthRepository) UpsertGoogleUser(ctx context.Context, subject, email, name, picture string) (identity.User, error) {
	user, err := scanProfile(r.db.QueryRowContext(ctx, `
		INSERT INTO users (id, google_subject, email, display_name, picture_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
		ON CONFLICT (google_subject) DO UPDATE SET
			email = EXCLUDED.email,
			display_name = CASE WHEN users.custom_display_name THEN users.display_name ELSE EXCLUDED.display_name END,
			picture_url = COALESCE(EXCLUDED.picture_url, users.picture_url),
			updated_at = NOW()
		RETURNING id, email, display_name, COALESCE(picture_url, ''), theme, report_columns, color_preset, contacts`,
		uuid.NewString(), subject, email, name, nullableString(picture),
	))
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
		SELECT u.id, u.email, u.display_name, COALESCE(u.picture_url, ''), u.theme, u.report_columns, u.color_preset, u.contacts
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
	return user, nil
}

func (r *AuthRepository) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM user_sessions WHERE token_hash = $1`, tokenHash)
	if err != nil {
		return fmt.Errorf("delete user session: %w", err)
	}
	return nil
}

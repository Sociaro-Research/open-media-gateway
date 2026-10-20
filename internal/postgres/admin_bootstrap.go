package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/apikey"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
)

// InitializeAdministrator is a trusted server command. User creation, role and
// first credential commit together; it never promotes an existing email account.
func (r *SystemRepository) InitializeAdministrator(ctx context.Context, email, name, keyName string) (apikey.Key, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
		return apikey.Key{}, "", errors.New("provide a valid email address")
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return apikey.Key{}, "", err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return apikey.Key{}, "", err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_system_admin`).Scan(&count); err != nil {
		return apikey.Key{}, "", err
	}
	if count > 0 {
		return apikey.Key{}, "", system.ErrBootstrapped
	}
	id := uuid.NewString()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email)=$1)`, email).Scan(&exists); err != nil {
		return apikey.Key{}, "", err
	}
	if exists {
		return apikey.Key{}, "", errors.New("email already belongs to a user; use admin bootstrap --user-id, then admin issue-key")
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO users(id,email,display_name,is_system_admin) VALUES($1,$2,$3,TRUE)`, id, email, strings.TrimSpace(name)); err != nil {
		return apikey.Key{}, "", err
	}
	key, secret, err := issueAdministratorKey(ctx, tx, id, keyName)
	if err != nil {
		return apikey.Key{}, "", err
	}
	if err = auditSystem(ctx, tx, "Server command", "administrator_bootstrap", id, map[string]string{"email": email, "key_id": key.ID}); err != nil {
		return apikey.Key{}, "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE installation_setup SET completed_at=NOW(),code_hash=NULL,code_ciphertext=NULL WHERE id=TRUE`); err != nil {
		return apikey.Key{}, "", err
	}
	return key, secret, tx.Commit()
}

// IssueAdministratorKey recovers API access using server-side database access.
// The current administrator role and the normal active-key policy still apply.
func (r *SystemRepository) IssueAdministratorKey(ctx context.Context, id, name string) (apikey.Key, string, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return apikey.Key{}, "", err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return apikey.Key{}, "", err
	}
	var admin bool
	err = tx.QueryRowContext(ctx, `SELECT is_system_admin FROM users WHERE id=$1 FOR UPDATE`, id).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !admin {
		return apikey.Key{}, "", system.ErrForbidden
	}
	if err != nil {
		return apikey.Key{}, "", err
	}
	key, secret, err := issueAdministratorKey(ctx, tx, id, name)
	if err != nil {
		return apikey.Key{}, "", err
	}
	if err = auditSystem(ctx, tx, "Server command", "administrator_key", id, map[string]string{"key_id": key.ID}); err != nil {
		return apikey.Key{}, "", err
	}
	return key, secret, tx.Commit()
}

func issueAdministratorKey(ctx context.Context, tx *sql.Tx, id, name string) (apikey.Key, string, error) {
	key, secret, err := apikey.Generate(apikey.CreateInput{UserID: id, Name: name, Scopes: []string{apikey.Generation, apikey.ProvidersManage, apikey.StorageManage, apikey.KeysManage, apikey.OrganizationsManage, apikey.SystemManage}}, time.Now())
	if err != nil {
		return key, "", err
	}
	key.CreatedBy = id
	if err = createAPIKey(ctx, tx, id, key, apikey.Hash(secret)); err != nil {
		return key, "", err
	}
	return key, secret, nil
}

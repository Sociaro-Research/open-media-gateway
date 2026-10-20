package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

type SystemRepository struct{ db *sql.DB }

func NewSystemRepository(db *sql.DB) *SystemRepository { return &SystemRepository{db: db} }
func (r *SystemRepository) IsAdmin(ctx context.Context, id string) (bool, error) {
	var admin bool
	err := r.db.QueryRowContext(ctx, `SELECT is_system_admin FROM users WHERE id::text=$1`, id).Scan(&admin)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return admin, err
}
func readSystemSettings(ctx context.Context, q rowQuerier, lock bool) (system.Settings, error) {
	var s system.Settings
	query := `SELECT generation_ui,personal_providers,version,shared_resources,shared_storage_limit_bytes,allow_inline_references,markup_basis_points FROM system_settings WHERE id=TRUE`
	if lock {
		query += ` FOR UPDATE`
	}
	err := q.QueryRowContext(ctx, query).Scan(&s.GenerationUI, &s.PersonalProviders, &s.Version, &s.SharedResources, &s.SharedStorageLimitBytes, &s.AllowInlineReferences, &s.MarkupPercent)
	return s, err
}
func (r *SystemRepository) Settings(ctx context.Context) (system.Settings, error) {
	return readSystemSettings(ctx, r.db, false)
}
func (r *SystemRepository) Users(ctx context.Context, search string, admins bool, limit, offset int) ([]system.User, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,display_name,email,COALESCE(picture_url,''),is_system_admin,is_client_manager,CASE WHEN is_client_manager THEN sales_role ELSE '' END,created_at FROM users WHERE ($1='' OR display_name ILIKE '%'||$1||'%' OR email ILIKE '%'||$1||'%' OR id::text=$1) AND (NOT $2 OR is_system_admin) ORDER BY is_system_admin DESC,created_at,id LIMIT $3 OFFSET $4`, search, admins, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []system.User{}
	for rows.Next() {
		var u system.User
		if err = rows.Scan(&u.ID, &u.Name, &u.Email, &u.Picture, &u.Admin, &u.ClientManager, &u.SalesRole, &u.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
func systemActor(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	if !identity.SystemPermissionAllowed(ctx) {
		return "", system.ErrForbidden
	}
	if id == "" {
		return "Server command", nil
	}
	var name, email string
	var admin bool
	err := tx.QueryRowContext(ctx, `SELECT display_name,email,is_system_admin FROM users WHERE id::text=$1`, id).Scan(&name, &email, &admin)
	if errors.Is(err, sql.ErrNoRows) || err == nil && !admin {
		return "", system.ErrForbidden
	}
	if err == nil {
		err = setAuditActor(ctx, tx, id)
	}
	return name + " <" + email + "> [" + id + "]", err
}
func (r *SystemRepository) SetAdmin(ctx context.Context, actor, target string, admin, bootstrap bool) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	name, err := systemActor(ctx, tx, actor)
	if err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_system_admin`).Scan(&count); err != nil {
		return err
	}
	if bootstrap && count > 0 {
		return system.ErrBootstrapped
	}
	var previous, manager bool
	var email string
	err = tx.QueryRowContext(ctx, `SELECT is_system_admin,is_client_manager,email FROM users WHERE id::text=$1 FOR UPDATE`, target).Scan(&previous, &manager, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return system.ErrNotFound
	}
	if err != nil {
		return err
	}
	if admin && manager {
		return system.ErrRoleConflict
	}
	if previous == admin {
		return tx.Commit()
	}
	if previous && !admin && count <= 1 {
		return system.ErrLastAdmin
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET is_system_admin=$2 WHERE id::text=$1`, target, admin); err != nil {
		return err
	}
	if err = auditSystem(ctx, tx, name, "administrator_role", target, map[string]any{"email": email, "before": previous, "after": admin}); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *SystemRepository) SaveSettings(ctx context.Context, actor string, next system.Settings) (system.Settings, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	before, err := readSystemSettings(ctx, tx, true)
	if err != nil {
		return next, err
	}
	_, err = systemActor(ctx, tx, actor)
	if err != nil {
		return next, err
	}
	if before.Version != next.Version {
		return next, system.ErrConflict
	}
	if err = next.Validate(); err != nil {
		return next, err
	}
	next.Version++
	_, err = tx.ExecContext(ctx, `UPDATE system_settings SET generation_ui=$1,personal_providers=$2,version=$3,shared_resources=$4,shared_storage_limit_bytes=$5,allow_inline_references=$6,markup_basis_points=$7 WHERE id=TRUE`, next.GenerationUI, next.PersonalProviders, next.Version, next.SharedResources, next.SharedStorageLimitBytes, next.AllowInlineReferences, next.MarkupPercent)
	if err != nil {
		return next, err
	}
	return next, tx.Commit()
}

// ImportShared copies only the caller's saved connections and never overwrites shared settings.
func (r *SystemRepository) ImportShared(ctx context.Context, actor string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	name, err := systemActor(ctx, tx, actor)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO provider_settings(owner_id,provider_id,enabled,credentials_ciphertext,credential_source,created_at,updated_at,name,archived) SELECT $2,provider_id,enabled,credentials_ciphertext,'console',NOW(),NOW(),name,archived FROM provider_settings WHERE owner_id=$1 AND credentials_ciphertext IS NOT NULL ON CONFLICT(owner_id,provider_id) DO UPDATE SET enabled=EXCLUDED.enabled,credentials_ciphertext=EXCLUDED.credentials_ciphertext,credential_source='console',updated_at=NOW() WHERE provider_settings.credentials_ciphertext IS NULL`, actor, identity.SharedOwnerID)
	if err != nil {
		return err
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM storage_settings WHERE owner_id=$1)`, identity.SharedOwnerID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		id := uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO storage_connections(owner_id,id,config_ciphertext) SELECT $2,$3,c.config_ciphertext FROM storage_settings s JOIN storage_connections c ON c.owner_id=s.owner_id AND c.id=s.connection_id WHERE s.owner_id=$1 AND s.driver='s3'`, actor, identity.SharedOwnerID, id)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO storage_settings(owner_id,driver,connection_id) SELECT $1,'s3',id FROM storage_connections WHERE owner_id=$1 AND id=$2`, identity.SharedOwnerID, id)
		if err != nil {
			return err
		}
	}
	if err = auditSystem(ctx, tx, name, "shared_import", "installation", map[string]any{"source_user_id": actor}); err != nil {
		return err
	}
	return tx.Commit()
}

package postgres

import (
	"context"
	"database/sql"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

// Callers performing writes hold the shared settings lock before authorization.
func commercialPermission(ctx context.Context, tx *sql.Tx, actor, org, permission string) error {
	var admin bool
	if err := tx.QueryRowContext(ctx, `SELECT is_system_admin FROM users WHERE id::text=$1`, actor).Scan(&admin); err != nil {
		return system.ErrForbidden
	}
	if admin && identity.SystemPermissionAllowed(ctx) {
		if err := setAuditActor(ctx, tx, actor); err != nil {
			return err
		}
		return nil
	}
	if org == "" || identity.APIKeyID(ctx) != nil || !system.SalesPermissionValid(permission) {
		return system.ErrForbidden
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT g.id FROM effective_sales_permissions g JOIN organizations o ON o.id::text=$2 WHERE g.employee_id::text=$1 AND g.permission=$3 AND sales_manages(g.employee_id,o.client_manager_id)`, actor, org, permission).Scan(&id)
	if err == sql.ErrNoRows {
		return system.ErrForbidden
	}
	if err != nil {
		return err
	}
	return setAuditActor(ctx, tx, actor)
}
func (r *SystemRepository) CommercialPermission(ctx context.Context, actor, org, permission string) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return commercialPermission(ctx, tx, actor, org, permission)
}
func (r *SystemRepository) SalesPermissions(ctx context.Context, actor string) (system.SalesPermissions, error) {
	s := system.SalesPermissions{ActorID: actor, Grants: []system.SalesGrant{}}
	if identity.APIKeyID(ctx) != nil {
		return s, system.ErrForbidden
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return s, err
	}
	defer tx.Rollback()
	var sales bool
	if err = tx.QueryRowContext(ctx, `SELECT is_system_admin,is_client_manager FROM users WHERE id::text=$1`, actor).Scan(&s.Administrator, &sales); err != nil || !s.Administrator && !sales {
		return s, system.ErrForbidden
	}
	scope := actor
	if s.Administrator {
		scope = ""
	}
	if err = tx.QueryRowContext(ctx, `SELECT version FROM sales_structure WHERE id=TRUE`).Scan(&s.Version); err != nil {
		return s, err
	}
	s.Members, err = salesMembers(ctx, tx, scope)
	if err != nil {
		return s, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.id,g.employee_id,g.permission,g.issuer_id,COALESCE(NULLIF(u.display_name,''),u.email),COALESCE(u.picture_url,''),g.created_at FROM effective_sales_permissions g JOIN users u ON u.id=g.issuer_id WHERE $1='' OR sales_manages(NULLIF($1,'')::uuid,g.employee_id) ORDER BY g.employee_id,g.permission`, scope)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var g system.SalesGrant
		if err = rows.Scan(&g.ID, &g.EmployeeID, &g.Permission, &g.IssuerID, &g.IssuerName, &g.IssuerPicture, &g.CreatedAt); err != nil {
			return s, err
		}
		s.Grants = append(s.Grants, g)
	}
	return s, rows.Err()
}
func (r *SystemRepository) SetSalesPermission(ctx context.Context, actor, target, permission string, in system.SalesPermissionChange) error {
	if identity.APIKeyID(ctx) != nil || actor == target {
		return system.ErrForbidden
	}
	if !system.SalesPermissionValid(permission) || in.Version < 1 {
		return system.ErrSalesPermission
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	var admin, manages bool
	if err = tx.QueryRowContext(ctx, `SELECT is_system_admin,sales_manages(id,$2::uuid) FROM users WHERE id::text=$1`, actor, target).Scan(&admin, &manages); err != nil {
		return system.ErrForbidden
	}
	var member bool
	if err = tx.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id::text=$1`, target).Scan(&member); err != nil || !member {
		return system.ErrForbidden
	}
	if !admin && !manages {
		return system.ErrForbidden
	}
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM sales_structure WHERE id=TRUE`).Scan(&version); err != nil {
		return err
	}
	if version != in.Version {
		return system.ErrConflict
	}
	var parent *string
	if in.Enabled && !admin {
		var id string
		if err = tx.QueryRowContext(ctx, `SELECT id FROM effective_sales_permissions WHERE employee_id::text=$1 AND permission=$2`, actor, permission).Scan(&id); err == sql.ErrNoRows {
			return system.ErrForbidden
		}
		if err != nil {
			return err
		}
		parent = &id
	}
	if err = setAuditActor(ctx, tx, actor); err != nil {
		return err
	}
	// Replacing a grant revokes its entire delegation lineage, even for unchanged values.
	if _, err = tx.ExecContext(ctx, `WITH RECURSIVE revoked AS (
 SELECT id FROM sales_permission_grants WHERE employee_id::text=$1 AND permission=$2 AND revoked_at IS NULL
 UNION ALL SELECT g.id FROM sales_permission_grants g JOIN revoked p ON g.parent_id=p.id WHERE g.revoked_at IS NULL)
 UPDATE sales_permission_grants SET revoked_at=clock_timestamp() WHERE id IN (SELECT id FROM revoked)`, target, permission); err != nil {
		return err
	}
	if in.Enabled {
		if _, err = tx.ExecContext(ctx, `INSERT INTO sales_permission_grants(id,employee_id,permission,issuer_id,parent_id) VALUES($1,$2,$3,$4,$5)`, uuid.NewString(), target, permission, actor, parent); err != nil {
			return err
		}
	}
	return tx.Commit()
}

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

// SetUserRole performs a direct role transition atomically, including key revocation.
func (r *SystemRepository) SetUserRole(ctx context.Context, actor, target, role string) error {
	sales := system.SalesRank(role) > 0
	if role != "user" && role != "system_admin" && !sales {
		return system.ErrRoleConflict
	}
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
	var admin, manager bool
	var salesRole, parent string
	err = tx.QueryRowContext(ctx, `SELECT is_system_admin,is_client_manager,sales_role,COALESCE(sales_supervisor_id::text,'') FROM users WHERE id::text=$1 FOR UPDATE`, target).Scan(&admin, &manager, &salesRole, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return system.ErrNotFound
	}
	if err != nil {
		return err
	}
	before := "user"
	if admin {
		before = "system_admin"
	}
	if manager {
		before = salesRole
	}
	if before == role {
		return tx.Commit()
	}
	if admin && role != "system_admin" {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_system_admin`).Scan(&count); err != nil {
			return err
		}
		if count <= 1 {
			return system.ErrLastAdmin
		}
	}
	if sales && !manager {
		if err = setAuditRevocationReason(ctx, tx, "account_role_changed"); err != nil {
			return err
		}
		var manages bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE user_id=$1 AND role IN ('admin','supermanager') AND removed_at IS NULL)`, target).Scan(&manages); err != nil {
			return err
		}
		if manages {
			return system.ErrRoleConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE user_api_keys SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL`, target); err != nil {
			return err
		}
	}
	if manager && !sales {
		if _, err = tx.ExecContext(ctx, `UPDATE client_onboardings SET manager_id=NULL WHERE manager_id=$1 AND activated_at IS NULL`, target); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE organizations SET client_manager_id=NULL WHERE client_manager_id=$1`, target); err != nil {
			return err
		}
	}
	if sales && parent != "" {
		var parentRank int
		if err = tx.QueryRowContext(ctx, `SELECT sales_rank(sales_role) FROM users WHERE id::text=$1 AND is_client_manager`, parent).Scan(&parentRank); err != nil {
			return err
		}
		if parentRank <= system.SalesRank(role) {
			parent = ""
		}
	}
	if admin && sales {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET is_system_admin=FALSE WHERE id::text=$1`, target); err != nil {
			return err
		}
	}
	placementRole := ""
	if sales {
		placementRole = role
	}
	if err = writeSalesPlacement(ctx, tx, target, placementRole, parent); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE users SET is_system_admin=$2 WHERE id::text=$1`, target, role == "system_admin"); err != nil {
		return err
	}
	if err = auditSystem(ctx, tx, name, "user_role", target, map[string]string{"before": before, "after": role}); err != nil {
		return err
	}
	return tx.Commit()
}

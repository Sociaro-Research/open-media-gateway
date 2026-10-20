package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func (r *SystemRepository) IsClientManager(ctx context.Context, id string) (bool, error) {
	var manager bool
	err := r.db.QueryRowContext(ctx, `SELECT is_client_manager FROM users WHERE id::text=$1`, id).Scan(&manager)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return manager, err
}

func (r *SystemRepository) SetClientManager(ctx context.Context, actor, target string, enabled bool) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize role changes with system administrator changes and assignments.
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	name, err := systemActor(ctx, tx, actor)
	if err != nil {
		return err
	}
	var admin, previous bool
	var parent string
	err = tx.QueryRowContext(ctx, `SELECT is_system_admin,is_client_manager,COALESCE(sales_supervisor_id::text,'') FROM users WHERE id::text=$1 FOR UPDATE`, target).Scan(&admin, &previous, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return system.ErrNotFound
	}
	if err != nil {
		return err
	}
	if enabled && admin {
		return system.ErrRoleConflict
	}
	if previous == enabled {
		return tx.Commit()
	}
	if enabled {
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
		// Previously issued credentials must not become usable again if the role is later removed.
		if _, err = tx.ExecContext(ctx, `UPDATE user_api_keys SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL`, target); err != nil {
			return err
		}
	} else {
		if _, err = tx.ExecContext(ctx, `UPDATE client_onboardings SET manager_id=NULL WHERE manager_id=$1 AND activated_at IS NULL`, target); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE organizations SET client_manager_id=NULL WHERE client_manager_id=$1`, target); err != nil {
			return err
		}
	}
	role := ""
	if enabled {
		role = system.ClientManager
	}
	if err = writeSalesPlacement(ctx, tx, target, role, parent); err != nil {
		return err
	}
	if err = auditSystem(ctx, tx, name, "client_manager_role", target, map[string]any{"before": previous, "after": enabled}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SystemRepository) AssignClientManager(ctx context.Context, actor, id, target string) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return err
	}
	_, err = systemActor(ctx, tx, actor)
	administrator := err == nil
	if !administrator {
		if !errors.Is(err, system.ErrForbidden) {
			return err
		}
		var rank int
		if identity.APIKeyID(ctx) != nil {
			return system.ErrForbidden
		}
		if err = tx.QueryRowContext(ctx, `SELECT sales_rank(sales_role) FROM users WHERE id::text=$1 AND is_client_manager`, actor).Scan(&rank); err != nil || rank < 2 {
			return system.ErrForbidden
		}
		if err = setAuditActor(ctx, tx, actor); err != nil {
			return err
		}
	}
	var manager any
	if target = strings.TrimSpace(target); target != "" {
		var uid string
		err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE (id::text=$1 OR lower(email)=lower($1)) AND is_client_manager`, target).Scan(&uid)
		if errors.Is(err, sql.ErrNoRows) {
			return system.ErrNotFound
		}
		if err != nil {
			return err
		}
		manager = uid
	}
	var before *string
	if err = tx.QueryRowContext(ctx, `SELECT client_manager_id::text FROM organizations WHERE id=$1 FOR UPDATE`, id).Scan(&before); errors.Is(err, sql.ErrNoRows) {
		return org.ErrNotFound
	}
	if err != nil {
		return err
	}
	if !administrator {
		if before == nil || manager == nil {
			return system.ErrForbidden
		}
		var permitted bool
		if err = tx.QueryRowContext(ctx, `SELECT sales_manages($1::uuid,$2::uuid) AND sales_manages($1::uuid,$3::uuid)`, actor, *before, manager).Scan(&permitted); err != nil {
			return err
		}
		if !permitted {
			return system.ErrForbidden
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE organizations SET client_manager_id=$2 WHERE id=$1`, id, manager); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE client_onboardings SET manager_id=NULL WHERE organization_id=$1 AND activated_at IS NULL`, id); err != nil {
		return err
	}
	return tx.Commit()
}

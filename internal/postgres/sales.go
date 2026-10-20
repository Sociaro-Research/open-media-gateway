package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

func (r *SystemRepository) SalesRole(ctx context.Context, actor string) (string, error) {
	var role string
	err := r.db.QueryRowContext(ctx, `SELECT sales_role FROM users WHERE id::text=$1 AND is_client_manager`, actor).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func salesMembers(ctx context.Context, tx *sql.Tx, actor string) ([]system.SalesMember, error) {
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.display_name,u.email,COALESCE(u.picture_url,''),u.sales_role,COALESCE(u.sales_supervisor_id::text,''),
 (SELECT COUNT(*) FROM organizations WHERE client_manager_id=u.id)
 FROM users u WHERE u.is_client_manager AND ($1='' OR sales_manages(NULLIF($1,'')::uuid,u.id)) ORDER BY u.display_name,u.email,u.id`, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []system.SalesMember{}
	for rows.Next() {
		var m system.SalesMember
		if err = rows.Scan(&m.ID, &m.Name, &m.Email, &m.Picture, &m.Role, &m.SupervisorID, &m.OwnClients); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}

func readSalesStructure(ctx context.Context, tx *sql.Tx) (system.SalesStructure, error) {
	var s system.SalesStructure
	if err := tx.QueryRowContext(ctx, `SELECT version FROM sales_structure WHERE id=TRUE`).Scan(&s.Version); err != nil {
		return s, err
	}
	var err error
	s.Members, err = salesMembers(ctx, tx, "")
	return s, err
}

func (r *SystemRepository) SalesStructure(ctx context.Context, actor string) (system.SalesStructure, error) {
	if identity.APIKeyID(ctx) != nil {
		return system.SalesStructure{}, system.ErrForbidden
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return system.SalesStructure{}, err
	}
	defer tx.Rollback()
	var admin bool
	if err = tx.QueryRowContext(ctx, `SELECT is_system_admin FROM users WHERE id::text=$1`, actor).Scan(&admin); err != nil {
		return system.SalesStructure{}, system.ErrForbidden
	}
	if !admin {
		return system.SalesStructure{}, system.ErrForbidden
	}
	s, err := readSalesStructure(ctx, tx)
	return s, err
}

func (r *SystemRepository) SalesTeam(ctx context.Context, actor string) ([]system.SalesMember, error) {
	if identity.APIKeyID(ctx) != nil {
		return nil, system.ErrForbidden
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var rank int
	if err = tx.QueryRowContext(ctx, `SELECT sales_rank(sales_role) FROM users WHERE id::text=$1 AND is_client_manager`, actor).Scan(&rank); err != nil || rank < 2 {
		return nil, system.ErrForbidden
	}
	return salesMembers(ctx, tx, actor)
}

func validateSalesSupervisor(ctx context.Context, tx *sql.Tx, target, role, parent string) error {
	if parent != "" {
		var pRole string
		var descendant bool
		if err := tx.QueryRowContext(ctx, `SELECT sales_role,sales_manages($2::uuid,id) FROM users WHERE id::text=$1 AND is_client_manager`, parent, target).Scan(&pRole, &descendant); err != nil || descendant || parent == target || system.SalesRank(pRole) <= system.SalesRank(role) {
			return system.ErrSalesPlacement
		}
	}
	return nil
}

// Called under the shared settings lock by every account-role transition.
// Incompatible direct reports move up; their own roles and clients are retained.
func writeSalesPlacement(ctx context.Context, tx *sql.Tx, target, role, parent string) error {
	if err := validateSalesSupervisor(ctx, tx, target, role, parent); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `WITH RECURSIVE ancestors AS (
 SELECT id,sales_supervisor_id,sales_rank(sales_role) AS rank,0 AS depth FROM users WHERE id::text=$2 AND is_client_manager
 UNION ALL SELECT u.id,u.sales_supervisor_id,sales_rank(u.sales_role),a.depth+1 FROM users u JOIN ancestors a ON u.id=a.sales_supervisor_id
 ) UPDATE users child SET sales_supervisor_id=(SELECT id FROM ancestors WHERE rank>sales_rank(child.sales_role) ORDER BY depth LIMIT 1)
 WHERE child.sales_supervisor_id=$1::uuid AND ($3=0 OR sales_rank(child.sales_role)>=$3)`, target, parent, system.SalesRank(role)); err != nil {
		return err
	}
	stored := role
	if stored == "" {
		stored = system.ClientManager
		parent = ""
	}
	_, err := tx.ExecContext(ctx, `UPDATE users SET is_client_manager=$2,sales_role=$3,sales_supervisor_id=NULLIF($4,'')::uuid WHERE id::text=$1`, target, role != "", stored, parent)
	return err
}

func (r *SystemRepository) PlaceSalesMember(ctx context.Context, actor, target string, in system.SalesPlacement) (system.SalesStructure, error) {
	var empty system.SalesStructure
	if identity.APIKeyID(ctx) != nil {
		return empty, system.ErrForbidden
	}
	if system.SalesRank(in.Role) == 0 || in.Version < 1 || (in.FillVacancy || in.NewTeam) && system.SalesRank(in.Role) < 2 || in.FillVacancy && in.NewTeam {
		return empty, system.ErrSalesPlacement
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return empty, err
	}
	if _, err = systemActor(ctx, tx, actor); err != nil {
		return empty, err
	}
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM sales_structure WHERE id=TRUE`).Scan(&version); err != nil {
		return empty, err
	}
	if version != in.Version {
		return empty, system.ErrConflict
	}
	var admin, member bool
	var previousParent string
	if err = tx.QueryRowContext(ctx, `SELECT is_system_admin,is_client_manager,COALESCE(sales_supervisor_id::text,'') FROM users WHERE id::text=$1 FOR UPDATE`, target).Scan(&admin, &member, &previousParent); errors.Is(err, sql.ErrNoRows) {
		return empty, system.ErrNotFound
	} else if err != nil {
		return empty, err
	}
	if admin {
		return empty, system.ErrRoleConflict
	}
	if !member {
		var manages bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_members WHERE user_id=$1 AND role IN ('admin','supermanager') AND removed_at IS NULL)`, target).Scan(&manages); err != nil {
			return empty, err
		}
		if manages {
			return empty, system.ErrRoleConflict
		}
		if err = setAuditRevocationReason(ctx, tx, "account_role_changed"); err != nil {
			return empty, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE user_api_keys SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL`, target); err != nil {
			return empty, err
		}
	}
	if in.NewTeam {
		if err = validateSalesSupervisor(ctx, tx, target, in.Role, in.SupervisorID); err != nil {
			return empty, err
		}
		// A new empty branch moves only the employee. Their former reports stay
		// at the old location, under the former supervisor (or Administrator).
		// Any invalid placement below rolls back this update and its audit events.
		if _, err = tx.ExecContext(ctx, `UPDATE users SET sales_supervisor_id=NULLIF($2,'')::uuid WHERE sales_supervisor_id=$1::uuid`, target, previousParent); err != nil {
			return empty, err
		}
	}
	if err = writeSalesPlacement(ctx, tx, target, in.Role, in.SupervisorID); err != nil {
		return empty, err
	}
	if in.FillVacancy {
		// Filling a skipped rank takes responsibility for the existing unled
		// branch. Independent placements keep those peers with their supervisor.
		// The shared settings lock and structure version cover the entire move.
		if _, err = tx.ExecContext(ctx, `UPDATE users SET sales_supervisor_id=$1::uuid WHERE is_client_manager AND id<>$1::uuid AND sales_supervisor_id IS NOT DISTINCT FROM NULLIF($2,'')::uuid AND sales_rank(sales_role)<$3`, target, in.SupervisorID, system.SalesRank(in.Role)); err != nil {
			return empty, err
		}
	}
	s, err := readSalesStructure(ctx, tx)
	if err != nil {
		return empty, err
	}
	return s, tx.Commit()
}

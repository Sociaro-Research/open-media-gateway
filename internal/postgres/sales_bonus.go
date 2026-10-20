package postgres

import (
	"context"
	"database/sql"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
	"strings"
	"time"
)

func readBonusSettings(ctx context.Context, tx *sql.Tx) (system.BonusSettings, error) {
	var s system.BonusSettings
	e := tx.QueryRowContext(ctx, `SELECT manager_percent,supervisor_percent,version,starts_at FROM sales_bonus_settings WHERE id=TRUE`).Scan(&s.ManagerPercent, &s.SupervisorPercent, &s.Version, &s.StartsAt)
	return s, e
}
func bonusAccess(ctx context.Context, tx *sql.Tx, actor, target string, write bool) error {
	if identity.APIKeyID(ctx) != nil {
		return system.ErrForbidden
	}
	var admin, active, manages, grant, eligible bool
	e := tx.QueryRowContext(ctx, `SELECT is_system_admin,is_client_manager,sales_manages(id,$2::uuid),EXISTS(SELECT 1 FROM effective_sales_permissions WHERE employee_id=users.id AND permission='bonuses'),EXISTS(SELECT 1 FROM users target WHERE target.id=$2::uuid AND (target.is_client_manager OR EXISTS(SELECT 1 FROM sales_bonus_entries b WHERE b.employee_id=target.id))) FROM users WHERE id::text=$1`, actor, target).Scan(&admin, &active, &manages, &grant, &eligible)
	if e == sql.ErrNoRows {
		return system.ErrForbidden
	}
	if e != nil {
		return e
	}
	if write {
		if !eligible || actor == target || !admin && (!manages || !grant) {
			return system.ErrForbidden
		}
	} else if !admin && !(active && (actor == target || manages)) {
		return system.ErrForbidden
	}
	return nil
}
func (r *SystemRepository) SalesBonuses(ctx context.Context, actor string) (system.BonusDashboard, error) {
	out := system.BonusDashboard{ActorID: actor, People: []system.BonusPerson{}}
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	if e = bonusAccess(ctx, tx, actor, actor, false); e != nil {
		return out, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT is_system_admin FROM users WHERE id::text=$1`, actor).Scan(&out.Administrator); e != nil {
		return out, e
	}
	out.Settings, e = readBonusSettings(ctx, tx)
	if e != nil {
		return out, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT u.id,u.display_name,u.email,COALESCE(u.picture_url,''),CASE WHEN u.is_client_manager THEN u.sales_role ELSE 'former_sales_employee' END,
 COALESCE(SUM(b.amount_micros) FILTER(WHERE b.kind<>'payout'),0)::bigint,-COALESCE(SUM(b.amount_micros) FILTER(WHERE b.kind='payout'),0)::bigint,
 COALESCE(SUM(b.amount_micros),0)::bigint,
 u.id::text<>$1 AND ($2 OR sales_manages($1::uuid,u.id) AND EXISTS(SELECT 1 FROM effective_sales_permissions g WHERE g.employee_id::text=$1 AND g.permission='bonuses'))
 FROM users u LEFT JOIN sales_bonus_entries b ON b.employee_id=u.id
 WHERE (u.is_client_manager OR EXISTS(SELECT 1 FROM sales_bonus_entries WHERE employee_id=u.id)) AND ($2 OR sales_manages($1::uuid,u.id))
 GROUP BY u.id ORDER BY u.id::text=$1 DESC,u.display_name,u.email`, actor, out.Administrator)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var p system.BonusPerson
		if e = rows.Scan(&p.ID, &p.Name, &p.Email, &p.Picture, &p.Role, &p.Accrued, &p.Paid, &p.Balance, &p.CanPay); e != nil {
			return out, e
		}
		out.People = append(out.People, p)
	}
	return out, rows.Err()
}
func (r *SystemRepository) BonusEntries(ctx context.Context, actor, target string, offset int) ([]system.BonusEntry, error) {
	tx, e := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = bonusAccess(ctx, tx, actor, target, false); e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT b.id,b.employee_id,COALESCE(b.task_id::text,''),b.kind,COALESCE(b.beneficiary,''),b.amount_micros,b.created_at,COALESCE(b.paid_at::text,''),b.method,b.reason,COALESCE(NULLIF(u.display_name,''),u.email,''),COALESCE(b.actor_id::text,''),COALESCE(u.picture_url,'') FROM sales_bonus_entries b LEFT JOIN users u ON u.id=b.actor_id WHERE b.employee_id::text=$1 ORDER BY b.id DESC LIMIT 51 OFFSET $2`, target, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []system.BonusEntry{}
	for rows.Next() {
		var b system.BonusEntry
		if e = rows.Scan(&b.ID, &b.EmployeeID, &b.TaskID, &b.Kind, &b.Beneficiary, &b.Amount, &b.CreatedAt, &b.PaidAt, &b.Method, &b.Reason, &b.ActorName, &b.ActorID, &b.ActorPicture); e != nil {
			return nil, e
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (r *SystemRepository) RecordBonusPayout(ctx context.Context, actor, target string, in system.BonusPayout) (int64, error) {
	in.Method = strings.TrimSpace(in.Method)
	in.Reason = strings.TrimSpace(in.Reason)
	if _, e := time.Parse("2006-01-02", in.PaidAt); e != nil {
		return 0, system.ErrBonusInput
	}
	if _, e := uuid.Parse(in.ExternalID); e != nil || in.Amount <= 0 || in.Method == "" || len(in.Method) > 200 || len(in.Reason) > 1000 {
		return 0, system.ErrBonusInput
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return 0, e
	}
	if e = bonusAccess(ctx, tx, actor, target, true); e != nil {
		return 0, e
	}
	if e = setAuditActor(ctx, tx, actor); e != nil {
		return 0, e
	}
	var id int64
	var same bool
	e = tx.QueryRowContext(ctx, `SELECT id,employee_id::text=$3 AND amount_micros=-$4::bigint AND paid_at=$5::date AND method=$6 AND reason=$7 FROM sales_bonus_entries WHERE actor_id::text=$1 AND external_id=$2`, actor, in.ExternalID, target, in.Amount, in.PaidAt, in.Method, in.Reason).Scan(&id, &same)
	if e == nil {
		if !same {
			return 0, system.ErrConflict
		}
		return id, tx.Commit()
	}
	if e != sql.ErrNoRows {
		return 0, e
	}
	e = tx.QueryRowContext(ctx, `INSERT INTO sales_bonus_entries(employee_id,amount_micros,kind,actor_id,paid_at,method,reason,external_id) VALUES($1,-$2::bigint,'payout',$3,$4,$5,$6,$7) RETURNING id`, target, in.Amount, actor, in.PaidAt, in.Method, in.Reason, in.ExternalID).Scan(&id)
	if e != nil {
		return 0, e
	}
	return id, tx.Commit()
}
func (r *SystemRepository) SaveBonusSettings(ctx context.Context, actor string, in system.BonusSettings) (system.BonusSettings, error) {
	if identity.APIKeyID(ctx) != nil {
		return in, system.ErrForbidden
	}
	if in.Version < 1 || in.ManagerPercent < 0 || in.SupervisorPercent < 0 || int64(in.ManagerPercent)+int64(in.SupervisorPercent) > 10000 {
		return in, system.ErrBonusInput
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return in, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return in, e
	}
	if _, e = systemActor(ctx, tx, actor); e != nil {
		return in, e
	}
	result, e := tx.ExecContext(ctx, `UPDATE sales_bonus_settings SET manager_percent=$1,supervisor_percent=$2,version=version+1 WHERE id=TRUE AND version=$3`, in.ManagerPercent, in.SupervisorPercent, in.Version)
	if e != nil {
		return in, e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return in, system.ErrConflict
	}
	out, e := readBonusSettings(ctx, tx)
	if e != nil {
		return in, e
	}
	return out, tx.Commit()
}

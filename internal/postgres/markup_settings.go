package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Sociaro-Research/media-gateway-dev/internal/markup"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

// One database snapshot covers every model in a catalog. Admission takes row
// locks so a concurrent settings update cannot alter the accepted task's rate.
func readMarkupPolicy(ctx context.Context, q rowQuerier, owner string, lock bool) (markup.Policy, error) {
	var p markup.Policy
	var global, company []byte
	query := `SELECT markup_basis_points,markup_exceptions,version FROM system_settings WHERE id=TRUE`
	if lock {
		query += ` FOR SHARE`
	}
	if err := q.QueryRowContext(ctx, query).Scan(&p.System.Default, &global, &p.System.Version); err != nil {
		return p, err
	}
	if err := json.Unmarshal(global, &p.System.Exceptions); err != nil {
		return p, err
	}
	if owner == "" {
		p.Company.Exceptions = []markup.Rule{}
		return p, nil
	}
	query = `SELECT markup_basis_points,markup_exceptions,markup_version FROM organizations WHERE id=$1`
	if lock {
		query += ` FOR SHARE`
	}
	err := q.QueryRowContext(ctx, query, owner).Scan(&p.Company.Default, &company, &p.Company.Version)
	if errors.Is(err, sql.ErrNoRows) {
		p.Company.Exceptions = []markup.Rule{}
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal(company, &p.Company.Exceptions)
	return p, err
}
func (r *TaskRepository) MarkupPolicy(ctx context.Context, owner string) (markup.Policy, error) {
	// A transaction keeps the global and organization rules in one snapshot.
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return markup.Policy{}, err
	}
	defer tx.Rollback()
	p, err := readMarkupPolicy(ctx, tx, owner, false)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (r *SystemRepository) Markups(ctx context.Context, actor, org string) (markup.Policy, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return markup.Policy{}, err
	}
	defer tx.Rollback()
	if actor == "" {
		return markup.Policy{}, system.ErrForbidden
	}
	if err = commercialPermission(ctx, tx, actor, org, "pricing"); err != nil {
		return markup.Policy{}, err
	}
	p, err := readMarkupPolicy(ctx, tx, org, false)
	if err != nil {
		return p, err
	}
	if org != "" && p.Company.Version == 0 {
		return p, system.ErrNotFound
	}
	return p, tx.Commit()
}
func (r *SystemRepository) SaveMarkups(ctx context.Context, actor, org string, next markup.Config) (markup.Policy, error) {
	if err := next.Validate(org == ""); err != nil {
		return markup.Policy{}, err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return markup.Policy{}, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return markup.Policy{}, err
	}
	if actor == "" {
		return markup.Policy{}, system.ErrForbidden
	}
	err = commercialPermission(ctx, tx, actor, org, "pricing")
	if err != nil {
		return markup.Policy{}, err
	}
	if next.Exceptions == nil {
		next.Exceptions = []markup.Rule{}
	}
	rules, err := json.Marshal(next.Exceptions)
	if err != nil {
		return markup.Policy{}, err
	}
	var result sql.Result
	if org == "" {
		result, err = tx.ExecContext(ctx, `UPDATE system_settings SET markup_basis_points=$1,markup_exceptions=$2,version=version+1 WHERE id=TRUE AND version=$3`, next.Default, rules, next.Version)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE organizations SET markup_basis_points=$1,markup_exceptions=$2,markup_version=markup_version+1 WHERE id=$3 AND markup_version=$4`, next.Default, rules, org, next.Version)
	}
	if err != nil {
		return markup.Policy{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return markup.Policy{}, err
	}
	if n == 0 {
		return markup.Policy{}, system.ErrConflict
	}
	next.Version++
	p, err := readMarkupPolicy(ctx, tx, org, false)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

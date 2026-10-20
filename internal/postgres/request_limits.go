package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/Sociaro-Research/media-gateway-dev/internal/limits"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

func readRequestLimits(ctx context.Context, q rowQuerier, org string) (limits.Policy, error) {
	var p limits.Policy
	var global, company []byte
	// A single statement sees both configurations in one snapshot.
	err := q.QueryRowContext(ctx, `SELECT s.request_limits,s.request_limits_version,COALESCE(o.request_limits,'{}'::jsonb),COALESCE(o.request_limits_version,0) FROM system_settings s LEFT JOIN organizations o ON o.id=NULLIF($1,'')::uuid WHERE s.id=TRUE`, org).Scan(&global, &p.System.Version, &company, &p.Organization.Version)
	if err != nil {
		return p, err
	}
	if org != "" && p.Organization.Version == 0 {
		return p, system.ErrNotFound
	}
	if err = json.Unmarshal(global, &p.System.Values); err != nil {
		return p, err
	}
	p.Organization.Values = map[string]*int{}
	for k := range limits.Defaults {
		p.Organization.Values[k] = nil
	}
	if err = json.Unmarshal(company, &p.Organization.Values); err != nil {
		return p, err
	}
	p.Resolve()
	return p, nil
}
func (r *SystemRepository) RequestLimits(ctx context.Context, org string) (limits.Policy, error) {
	return readRequestLimits(ctx, r.db, org)
}
func (r *SystemRepository) LimitSettings(ctx context.Context, actor, org string) (limits.Policy, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return limits.Policy{}, err
	}
	defer tx.Rollback()
	if _, err = systemActor(ctx, tx, actor); err != nil {
		return limits.Policy{}, err
	}
	p, err := readRequestLimits(ctx, tx, org)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func (r *SystemRepository) SaveLimits(ctx context.Context, actor, org string, next limits.Config) (limits.Policy, error) {
	if err := next.Validate(org == ""); err != nil {
		return limits.Policy{}, err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return limits.Policy{}, err
	}
	defer tx.Rollback()
	_, err = systemActor(ctx, tx, actor)
	if err != nil {
		return limits.Policy{}, err
	}
	data, err := json.Marshal(next.Values)
	if err != nil {
		return limits.Policy{}, err
	}
	var result sql.Result
	if org == "" {
		result, err = tx.ExecContext(ctx, `UPDATE system_settings SET request_limits=$1,request_limits_version=request_limits_version+1 WHERE id=TRUE AND request_limits_version=$2`, data, next.Version)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE organizations SET request_limits=$1,request_limits_version=request_limits_version+1 WHERE id=$2 AND request_limits_version=$3`, data, org, next.Version)
	}
	if err != nil {
		return limits.Policy{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return limits.Policy{}, err
	}
	if n == 0 {
		return limits.Policy{}, system.ErrConflict
	}
	p, err := readRequestLimits(ctx, tx, org)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}

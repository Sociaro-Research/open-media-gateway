package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/modelaccess"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Ensure registers new native models without resetting administrator choices.
// ON CONFLICT handles concurrent discovery and reserved public names atomically.
func (r *SystemRepository) Ensure(ctx context.Context, refs []modelaccess.Ref) error {
	if len(refs) == 0 {
		return nil
	}
	data, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO model_settings(provider,model,name,public_id)
 SELECT DISTINCT ON (x.provider,x.model) x.provider,x.model,x.name,x.provider || '/' || x.model
 FROM jsonb_to_recordset($1::jsonb) x(provider text,model text,name text)
 WHERE NOT EXISTS(SELECT 1 FROM model_settings s WHERE s.provider=x.provider AND s.model=x.model)
 ORDER BY x.provider,x.model ON CONFLICT DO NOTHING`, data)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE model_settings s SET name=x.name FROM jsonb_to_recordset($1::jsonb) x(provider text,model text,name text) WHERE s.provider=x.provider AND s.model=x.model AND s.name IS DISTINCT FROM x.name`, data)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT x.provider,x.model,x.name
 FROM jsonb_to_recordset($1::jsonb) x(provider text,model text,name text)
 WHERE NOT EXISTS(SELECT 1 FROM model_settings s WHERE s.provider=x.provider AND s.model=x.model)`, data)
	if err != nil {
		return err
	}
	missing := []modelaccess.Ref{}
	for rows.Next() {
		var v modelaccess.Ref
		if err = rows.Scan(&v.Provider, &v.Model, &v.Name); err != nil {
			rows.Close()
			return err
		}
		missing = append(missing, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, ref := range missing {
		for suffix := 2; ; suffix++ {
			result, e := tx.ExecContext(ctx, `INSERT INTO model_settings(provider,model,name,public_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, ref.Provider, ref.Model, ref.Name, fmt.Sprintf("%s/%s-%d", ref.Provider, ref.Model, suffix))
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n > 0 {
				break
			}
			var exists bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_settings WHERE provider=$1 AND model=$2)`, ref.Provider, ref.Model).Scan(&exists); e != nil {
				return e
			}
			if exists {
				break
			}
		}
	}
	return tx.Commit()
}
func (r *SystemRepository) List(ctx context.Context, org string) ([]modelaccess.Model, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,provider,model,name,public_id,enabled_by_default,version,
 EXISTS(SELECT 1 FROM organization_model_grants g WHERE g.model_id=s.id AND g.organization_id=NULLIF($1,'')::uuid)
 FROM model_settings s ORDER BY provider,name,model`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []modelaccess.Model{}
	for rows.Next() {
		var m modelaccess.Model
		if err = rows.Scan(&m.ID, &m.Provider, &m.Model, &m.Name, &m.PublicID, &m.EnabledByDefault, &m.Version, &m.Granted); err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func (r *SystemRepository) Update(ctx context.Context, actor, id string, u modelaccess.Update) (modelaccess.Model, error) {
	var m modelaccess.Model
	if actor == "" {
		return m, system.ErrForbidden
	}
	if err := u.Validate(); err != nil {
		return m, err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return m, err
	}
	defer tx.Rollback()
	_, err = systemActor(ctx, tx, actor)
	if err != nil {
		return m, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE model_settings SET public_id=$1,enabled_by_default=$2,version=version+1
 WHERE id=$3 AND version=$4 RETURNING id,provider,model,name,public_id,enabled_by_default,version`, u.PublicID, *u.EnabledByDefault, id, u.Version).Scan(&m.ID, &m.Provider, &m.Model, &m.Name, &m.PublicID, &m.EnabledByDefault, &m.Version)
	var pg *pgconn.PgError
	if errors.Is(err, sql.ErrNoRows) || errors.As(err, &pg) && pg.Code == "23505" {
		return m, system.ErrConflict
	}
	if err != nil {
		return m, err
	}
	return m, tx.Commit()
}
func readModelAccess(ctx context.Context, tx *sql.Tx, org string) (modelaccess.Access, error) {
	a := modelaccess.Access{ModelIDs: []string{}}
	if err := tx.QueryRowContext(ctx, `SELECT model_access_version FROM organizations WHERE id=$1`, org).Scan(&a.Version); errors.Is(err, sql.ErrNoRows) {
		return a, system.ErrNotFound
	} else if err != nil {
		return a, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT model_id FROM organization_model_grants WHERE organization_id=$1 ORDER BY model_id`, org)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return a, err
		}
		a.ModelIDs = append(a.ModelIDs, id)
	}
	return a, rows.Err()
}
func (r *SystemRepository) Organization(ctx context.Context, actor, org string) (modelaccess.Access, error) {
	if actor == "" {
		return modelaccess.Access{}, system.ErrForbidden
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return modelaccess.Access{}, err
	}
	defer tx.Rollback()
	if err = commercialPermission(ctx, tx, actor, org, "models"); err != nil {
		return modelaccess.Access{}, err
	}
	a, err := readModelAccess(ctx, tx, org)
	if err != nil {
		return a, err
	}
	return a, tx.Commit()
}
func (r *SystemRepository) SaveOrganization(ctx context.Context, actor, org string, a modelaccess.Access) (modelaccess.Access, error) {
	if actor == "" {
		return a, system.ErrForbidden
	}
	if a.Version < 1 || a.ModelIDs == nil || len(a.ModelIDs) > 10000 {
		return a, errors.New("model_ids and current version are required")
	}
	seen := map[string]bool{}
	for _, id := range a.ModelIDs {
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			return a, errors.New("invalid or duplicate model ID")
		}
		seen[id] = true
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return a, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return a, err
	}
	if err = commercialPermission(ctx, tx, actor, org, "models"); err != nil {
		return a, err
	}
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&locked); err != nil {
		return a, err
	}
	before, err := readModelAccess(ctx, tx, org)
	if err != nil {
		return a, err
	}
	if before.Version != a.Version {
		return a, system.ErrConflict
	}
	oldIDs := append([]string{}, before.ModelIDs...)
	newIDs := append([]string{}, a.ModelIDs...)
	slices.Sort(oldIDs)
	slices.Sort(newIDs)
	if slices.Equal(oldIDs, newIDs) {
		return before, tx.Commit()
	}
	res, err := tx.ExecContext(ctx, `UPDATE organizations SET model_access_version=model_access_version+1 WHERE id=$1 AND model_access_version=$2`, org, a.Version)
	if err != nil {
		return a, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return a, system.ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM organization_model_grants WHERE organization_id=$1`, org); err != nil {
		return a, err
	}
	for _, id := range a.ModelIDs {
		_, err = tx.ExecContext(ctx, `INSERT INTO organization_model_grants(organization_id,model_id) VALUES($1,$2)`, org, id)
		if err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) && pg.Code == "23503" {
				return a, system.ErrNotFound
			}
			return a, err
		}
	}
	a.Version++
	if err = orgEvent(ctx, tx, org, actor, "model_access", map[string]any{"before": map[string]any{"model_ids": oldIDs}, "after": map[string]any{"model_ids": newIDs}}); err != nil {
		return a, err
	}
	return a, tx.Commit()
}

// Lock policy alongside admission so a concurrent disable/revoke cannot race
// between catalog resolution and reserving the customer's balance.
func checkTaskModelAccess(ctx context.Context, tx *sql.Tx, task *core.Task) error {
	var id, public string
	var enabled bool
	err := tx.QueryRowContext(ctx, `SELECT id,public_id,enabled_by_default FROM model_settings WHERE provider=$1 AND model=$2 FOR SHARE`, task.Provider, task.Model).Scan(&id, &public, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		if task.PublicModel != "" && task.PublicModel != task.Provider+"/"+task.Model {
			return core.ErrModelUnavailable
		}
		task.PublicModel = task.Provider + "/" + task.Model
		return nil
	}
	if err != nil {
		return err
	}
	if task.PublicModel != "" && task.PublicModel != public {
		return core.ErrModelUnavailable
	}
	if !enabled {
		org := identity.OrganizationFromContext(ctx).ID
		if org == "" {
			return core.ErrModelUnavailable
		}
		var version int64
		if err = tx.QueryRowContext(ctx, `SELECT model_access_version FROM organizations WHERE id=$1 FOR SHARE`, org).Scan(&version); err != nil {
			return err
		}
		var granted bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM organization_model_grants WHERE organization_id=$1 AND model_id=$2)`, org, id).Scan(&granted); err != nil {
			return err
		}
		if !granted {
			return core.ErrModelUnavailable
		}
	}
	task.PublicModel = public
	return nil
}

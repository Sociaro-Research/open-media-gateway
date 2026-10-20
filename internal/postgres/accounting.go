package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func costJSON(cost *core.TaskCost) (any, error) {
	if cost == nil {
		return nil, nil
	}
	return json.Marshal(cost)
}

// Only completed, reported gross costs consume funds. Estimates and in-flight tasks
// do not occupy budget; package coverage does not restore allowance.
const taskProviderConsumedSQL = `CASE WHEN status IN ('queued','running') THEN 0::numeric ELSE COALESCE(
 CASE WHEN cost->>'actual_source' IN ('free_provider','provider_failed_no_charge') THEN 0::numeric END,
 (cost->>'tariff_usd')::numeric, (cost->>'metered_usd')::numeric,
 CASE WHEN provider_usage->>'currency'='USD' AND jsonb_typeof(provider_usage->'estimated_cost')='number'
 THEN GREATEST(0,(provider_usage->>'estimated_cost')::numeric) END) END`

// Round each charge up once, exactly like money.Percent.Apply. Old snapshots
// have no markup field and therefore remain unchanged.
const taskConsumedSQL = `CEIL((` + taskProviderConsumedSQL + `)*(1+COALESCE((cost->>'markup_percent')::numeric,0)/100)*1000000)/1000000`

func monthBounds(at time.Time) (time.Time, time.Time) {
	at = at.UTC()
	start := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Key validity is independent of financial limits, which belong to organizations.
func checkBudgetKey(ctx context.Context, db rowQuerier, owner, key string, lock bool) error {
	query := `SELECT revoked_at,expires_at FROM user_api_keys WHERE id=$1 AND (organization_id=$2 OR (organization_id IS NULL AND user_id=$3))`
	if lock {
		query += ` FOR NO KEY UPDATE`
	}
	var revoked, expires *time.Time
	err := db.QueryRowContext(ctx, query, key, owner, identity.ActorID(ctx)).Scan(&revoked, &expires)
	if errors.Is(err, sql.ErrNoRows) || revoked != nil || (expires != nil && !expires.After(time.Now())) {
		return core.ErrKeyUnavailable
	}
	return err
}

func readBudget(ctx context.Context, db rowQuerier, owner, key string, at time.Time, exclude string, lock bool) (*money.USD, *money.USD, error) {
	var organizationID *string
	var mode string
	if err := db.QueryRowContext(ctx, `SELECT organization_id,resource_mode FROM user_api_keys WHERE id=$1 AND (organization_id=$2 OR (organization_id IS NULL AND user_id=$3))`, key, owner, identity.ActorID(ctx)).Scan(&organizationID, &mode); err != nil {
		return nil, nil, core.ErrKeyUnavailable
	}
	if organizationID != nil {
		return readOrganizationBudget(ctx, db, owner, key, at, exclude, lock)
	}
	if mode == "shared" {
		return nil, nil, core.ErrKeyUnavailable
	}
	return nil, nil, checkBudgetKey(ctx, db, owner, key, lock)
}

func checkAvailableBudget(ctx context.Context, tx *sql.Tx, task core.Task) error {
	_, remaining, err := readBudget(ctx, tx, identity.OwnerID(ctx), *task.APIKeyID, task.CreatedAt, task.ID, false)
	if err != nil {
		return err
	}
	if remaining != nil && *remaining <= 0 {
		return core.ErrBudgetExceeded
	}
	return nil
}

func (r *TaskRepository) Budget(ctx context.Context, key string, at time.Time) (*money.USD, *money.USD, error) {
	return readBudget(ctx, r.db, identity.OwnerID(ctx), key, at, "", false)
}

func (r *TaskRepository) updateCost(ctx context.Context, id string, apply func(*core.Task)) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockProfitAssignment(ctx, tx); err != nil {
		return err
	}
	var task core.Task
	if err = scanTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id=$1 FOR UPDATE`, id), &task); err != nil {
		return err
	}
	if err = checkTaskClaim(ctx, tx, id); err != nil {
		return err
	}
	apply(&task)
	var usage any
	if task.Usage != nil {
		if task.MinimizeRequestLogging && (task.Status == core.TaskSucceeded || task.Status == core.TaskFailed) {
			task.Usage.Raw = nil
		}
		usage, err = json.Marshal(task.Usage)
		if err != nil {
			return err
		}
	}
	encodedCost, err := costJSON(task.Cost)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE tasks SET cost=$2, provider_usage=$3, updated_at=CASE WHEN status IN ('queued','running') THEN NOW() ELSE updated_at END WHERE id=$1`, id, encodedCost, usage)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// ConfirmNoCharge is used only when the provider explicitly reports a failed
// generation. Transport timeouts and storage failures do not establish a refund.
func (r *TaskRepository) ConfirmNoCharge(ctx context.Context, id string) error {
	return r.updateCost(ctx, id, func(task *core.Task) {
		if task.Cost == nil {
			return
		}
		zero := money.USD(0)
		now := time.Now().UTC()
		task.Cost.ActualUSD = &zero
		task.Cost.Status = "confirmed"
		task.Cost.ActualSource = "provider_failed_no_charge"
		task.Cost.ReconciledAt = &now
	})
}

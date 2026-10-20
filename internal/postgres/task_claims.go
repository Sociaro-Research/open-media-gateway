package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
)

func (r *TaskRepository) AcquireTask(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	if core.ClaimToken(ctx) == "" {
		return false, core.ErrLeaseLost
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE tasks
		SET worker_token=$2, worker_lease_until=clock_timestamp()+$3*interval '1 millisecond'
		WHERE id=$1 AND status IN ('queued','running')
		AND worker_next_at<=clock_timestamp()
		AND (worker_lease_until IS NULL OR worker_lease_until<=clock_timestamp())`,
		id, core.ClaimToken(ctx), ttl.Milliseconds())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *TaskRepository) RenewTask(ctx context.Context, id string, ttl time.Duration) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE tasks SET worker_lease_until=clock_timestamp()+$3*interval '1 millisecond'
		WHERE id=$1 AND worker_token=$2 AND worker_lease_until>clock_timestamp()`,
		id, core.ClaimToken(ctx), ttl.Milliseconds())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return core.ErrLeaseLost
	}
	return nil
}

func (r *TaskRepository) ReleaseTask(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE tasks SET worker_token=NULL, worker_lease_until=NULL
		WHERE id=$1 AND worker_token=$2`, id, core.ClaimToken(ctx))
	return err
}

func (r *TaskRepository) ListRecoverableBatch(ctx context.Context, after string, limit int) ([]string, error) {
	if limit < 1 || limit > 1000 {
		limit = 100
	}
	if after == "" {
		after = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM tasks WHERE status IN ('queued','running') AND id>$1
		AND worker_next_at<=clock_timestamp()
		AND (worker_lease_until IS NULL OR worker_lease_until<=clock_timestamp())
		ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Scheduling is persisted under the live claim before Redis acknowledgment.
// A Redis outage/restart cannot lose the next poll or reset its error counter.
func (r *TaskRepository) ScheduleTask(ctx context.Context, id string, delay time.Duration, pollErrors int) error {
	result, err := r.workerExec(ctx, id, `UPDATE tasks SET worker_next_at=clock_timestamp()+$2*interval '1 millisecond',worker_poll_errors=$3 WHERE id=$1 AND status='running'`, id, delay.Milliseconds(), pollErrors)
	return changed(result, err, "schedule task poll")
}

func (r *TaskRepository) MarkEnqueuedBatch(ctx context.Context, ids []string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM task_queue_outbox WHERE task_id=ANY($1::uuid[])`, ids)
	return err
}

// Hold the row lock through the write. Renewal and ownership transfer cannot
// interleave between validation and persistence. Non-worker billing updates
// intentionally use their existing accounting transaction without a token.
func checkTaskClaim(ctx context.Context, tx *sql.Tx, id string) error {
	token := core.ClaimToken(ctx)
	if token == "" {
		return nil
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(worker_token=$2 AND worker_lease_until>clock_timestamp(),false)
		FROM tasks WHERE id=$1 FOR UPDATE`, id, token).Scan(&valid)
	if err == sql.ErrNoRows {
		return core.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if !valid {
		return core.ErrLeaseLost
	}
	return nil
}

func (r *TaskRepository) workerExec(ctx context.Context, id, query string, args ...any) (sql.Result, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = checkTaskClaim(ctx, tx, id); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

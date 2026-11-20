package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

// A repeated local write can observe a committed terminal transaction whose
// acknowledgment was lost. No second UPDATE or webhook is needed.
var errImageRejectionCommitted = errors.New("image rejection already committed")

func (r *TaskRepository) MarkImagesRejectedWithoutCharge(ctx context.Context, task core.Task, rejection core.UnchargedImageRejection) error {
	if core.ClaimToken(ctx) == "" {
		return core.ErrLeaseLost
	}
	usage, err := json.Marshal(rejection.Usage())
	if err != nil {
		return err
	}
	args := []any{rejection.Error(), usage, nil}
	// transition holds the claim and profit-assignment locks until the task,
	// audit effects, privacy cleanup and webhook commit together.
	err = r.transition(ctx, task.ID, core.EventTaskFailed, `UPDATE tasks
 SET status='failed', error=CASE WHEN minimize_request_logging THEN 'Generation failed' ELSE $2 END,
 provider_usage=$3, cost=$4, updated_at=NOW()
 WHERE id=$1 AND status='running' AND provider_task_id IS NULL AND provider_usage IS NULL
 RETURNING `+taskColumns, args, func(tx *sql.Tx) error {
		var status core.TaskStatus
		var raw []byte
		var sameUsage bool
		if err := tx.QueryRowContext(ctx, "SELECT status, cost, COALESCE(provider_usage=$2::jsonb,false) FROM tasks WHERE id=$1", task.ID, usage).Scan(&status, &raw, &sameUsage); err != nil {
			return err
		}
		if status != core.TaskRunning && status != core.TaskFailed {
			return core.ErrAcceptedReceiptInvalid
		}
		remote, err := lockProviderResultTask(ctx, tx, task, status)
		if err != nil {
			return err
		}
		if remote != nil || task.MediaType != "image" {
			return core.ErrAcceptedReceiptInvalid
		}
		cost := core.TaskCost{QuantityUnknown: true}
		if len(raw) > 0 {
			cost = core.TaskCost{}
			if err := json.Unmarshal(raw, &cost); err != nil {
				return err
			}
		}
		if status == core.TaskFailed {
			if sameUsage && cost.Status == "confirmed" && cost.ActualUSD != nil && *cost.ActualUSD == 0 && cost.MeteredUSD != nil && *cost.MeteredUSD == 0 && cost.UsageRecordedAt != nil {
				return errImageRejectionCommitted
			}
			return core.ErrAcceptedReceiptInvalid
		}
		if cost.ActualUSD != nil && *cost.ActualUSD != 0 || cost.MeteredUSD != nil && *cost.MeteredUSD != 0 {
			return core.ErrAcceptedReceiptInvalid
		}
		zero, now := money.USD(0), time.Now().UTC()
		cost.MeteredUSD, cost.UsageRecordedAt = &zero, &now
		if cost.ActualUSD == nil {
			cost.ActualUSD, cost.ActualSource, cost.ReconciledAt = &zero, "provider_failed_no_charge", &now
		}
		cost.Status = "confirmed"
		args[2], err = costJSON(&cost)
		if err != nil {
			return err
		}
		return failTaskSessionAction(ctx, tx, task.ID)
	})
	if errors.Is(err, errImageRejectionCommitted) {
		return nil
	}
	return err
}

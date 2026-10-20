package postgres

import (
	"context"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/core"
)

func (r *TaskRepository) reconcileUsageBills(ctx context.Context, owner, fingerprint string, bills []core.BillRecord, provider string) error {
	usageBillSource := provider + "_usage_window_bill"
	// Include every credential and uncertain request in the owner-level match.
	rows, err := r.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE (provider_owner_id=$1 OR split_part(provider_identity,':',1)=split_part($2,':',1)) AND split_part(provider,'--',1)=split_part($3,'--',1) AND cost IS NOT NULL`, owner, fingerprint, provider)
	if err != nil {
		return err
	}
	var tasks []core.Task
	for rows.Next() {
		var task core.Task
		if err = scanTask(rows, &task); err != nil {
			break
		}
		tasks = append(tasks, task)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	matches := core.MatchUsageBills(tasks, bills, fingerprint)
	for _, task := range tasks {
		bill, matched := matches[task.ID]
		if task.ProviderIdentity != fingerprint || (!matched && task.Cost.ActualSource != usageBillSource) {
			continue
		}
		if matched && task.Cost.ActualSource == usageBillSource && task.Cost.ActualUSD != nil && *task.Cost.ActualUSD == *bill.PaidUSD && task.Cost.TariffUSD != nil && *task.Cost.TariffUSD == *bill.TariffUSD && equalUnits(task.Cost.PackageUnits, bill.PackageUnits) && len(task.Cost.InvoiceIDs) == 1 && task.Cost.InvoiceIDs[0] == bill.ID {
			continue
		}
		usageAt := task.UpdatedAt
		if err := r.updateCost(ctx, task.ID, func(current *core.Task) {
			// Preserve the original completion timestamp for legacy tasks before a
			// reconciliation changes updated_at. Future calls record usage explicitly.
			if current.Cost.UsageRecordedAt == nil {
				current.Cost.UsageRecordedAt = &usageAt
			}
			if !matched {
				current.Cost.ActualUSD, current.Cost.TariffUSD, current.Cost.PackageUnits = nil, nil, nil
				current.Cost.ActualSource, current.Cost.BillingMatch, current.Cost.BillingInstanceID = "", "", ""
				current.Cost.InvoiceIDs, current.Cost.ReconciledAt = nil, nil
				current.Cost.Status = "pending_billing"
				return
			}
			now := time.Now().UTC()
			current.Cost.ActualUSD, current.Cost.TariffUSD, current.Cost.PackageUnits = bill.PaidUSD, bill.TariffUSD, bill.PackageUnits
			current.Cost.ActualSource = usageBillSource
			current.Cost.BillingMatch = "model_usage_window"
			current.Cost.BillingInstanceID = bill.InstanceID
			current.Cost.InvoiceIDs = []string{bill.ID}
			current.Cost.Status = "confirmed"
			current.Cost.ReconciledAt = &now
		}); err != nil {
			return err
		}
	}
	return nil
}

func equalUnits(a, b *float64) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

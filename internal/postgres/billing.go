package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/core"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providers"
)

type BillSource interface {
	FetchBills(context.Context, string, string) ([]core.BillRecord, string, error)
}

// RunBillingSync imports recent months and older requests still awaiting profit verification. Unmatched or
// unsettled rows remain pending; an aggregate bill is never allocated by guess.
func (r *TaskRepository) RunBillingSync(ctx context.Context, source BillSource) {
	for {
		r.syncBills(ctx, source)
		timer := time.NewTimer(providers.BillingRefreshInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *TaskRepository) syncBills(ctx context.Context, source BillSource) {
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT provider_owner_id,provider,to_char(created_at AT TIME ZONE 'UTC','YYYY-MM'),provider_identity FROM tasks
 WHERE cost IS NOT NULL AND (created_at >= date_trunc('month',NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' - INTERVAL '1 month' OR EXISTS(SELECT 1 FROM generation_profits p WHERE p.task_id=tasks.id AND p.state='awaiting_billing'))
 ORDER BY 1,2,3`)
	if err != nil {
		return
	}
	type target struct{ owner, provider, month, revision string }
	var targets []target
	for rows.Next() {
		var item target
		if err = rows.Scan(&item.owner, &item.provider, &item.month, &item.revision); err != nil {
			break
		}
		targets = append(targets, item)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil {
		return
	}
	for _, item := range targets {
		if supported, ok := source.(interface{ SupportsBills(string) bool }); ok && !supported.SupportsBills(item.provider) {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		callCtx, cancel := context.WithTimeout(identity.WithOwnerID(ctx, item.owner), 90*time.Second)
		var bills []core.BillRecord
		var fingerprint string
		if history, ok := source.(interface {
			FetchBillsForRevision(context.Context, string, string, string) ([]core.BillRecord, string, error)
		}); ok {
			bills, fingerprint, err = history.FetchBillsForRevision(callCtx, item.provider, item.month, item.revision)
		} else {
			bills, fingerprint, err = source.FetchBills(callCtx, item.provider, item.month)
		}
		status, message := "synced", "Provider bills imported. Unpaid or ambiguous usage windows remain unconfirmed."
		if errors.Is(err, providers.ErrBillingUnconfigured) {
			status, message = "not_configured", providers.ErrBillingUnconfigured.Error()
		}
		if err != nil && status == "synced" {
			status, message = "error", "Billing sync failed. Existing costs were preserved; check provider access and the billing service."
		}
		if err == nil {
			if err = r.importBills(callCtx, item.owner, item.provider, item.month, fingerprint, bills); err != nil {
				status, message = "error", "Provider bill import failed; existing usage costs and confirmed charges were preserved."
			}
		}
		cancel()
		_, _ = r.db.ExecContext(ctx, `INSERT INTO provider_billing_sync(owner_id,provider,checked_at,status,message) VALUES ($1,$2,NOW(),$3,$4)
 ON CONFLICT(owner_id,provider) DO UPDATE SET checked_at=EXCLUDED.checked_at,status=EXCLUDED.status,message=EXCLUDED.message`, item.owner, item.provider, status, message)
	}
}

func (r *TaskRepository) BillingStatus(ctx context.Context) ([]core.BillingStatus, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT provider,status,message,checked_at FROM provider_billing_sync WHERE owner_id=$1 ORDER BY provider`, identity.OwnerID(ctx))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []core.BillingStatus{}
	for rows.Next() {
		var item core.BillingStatus
		if err := rows.Scan(&item.Provider, &item.Status, &item.Message, &item.CheckedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (r *TaskRepository) importBills(ctx context.Context, owner, provider, month, fingerprint string, bills []core.BillRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Replace this completed snapshot so removed/revised bill items cannot linger.
	prefix := fingerprint + ":" + month + ":"
	if _, err = tx.ExecContext(ctx, `DELETE FROM provider_bill_records WHERE owner_id=$1 AND provider=$2 AND billing_month=$3 AND starts_with(record_id,$4)`, owner, provider, month, prefix); err != nil {
		return err
	}
	// Keep the whole snapshot, including unmatchable rows, for an auditable second
	// pass once a provider's account-specific export supplies task identifiers.
	for _, bill := range bills {
		payload, err := json.Marshal(bill)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO provider_bill_records(owner_id,provider,record_id,billing_month,payload)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(owner_id,provider,record_id) DO UPDATE SET payload=EXCLUDED.payload,fetched_at=NOW()`, owner, provider, fingerprint+":"+month+":"+bill.ID, month, payload)
		if err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// Reconcile across imported months: late or split bills must not overwrite
	// another month's charge for the same generation.
	rows, err := r.db.QueryContext(ctx, `SELECT record_id,payload FROM provider_bill_records WHERE owner_id=$1 AND provider=$2 AND starts_with(record_id,$3)`, owner, provider, fingerprint+":")
	if err != nil {
		return err
	}
	bills = nil
	for rows.Next() {
		var recordID string
		var raw []byte
		if err = rows.Scan(&recordID, &raw); err != nil {
			break
		}
		var bill core.BillRecord
		if err = json.Unmarshal(raw, &bill); err != nil {
			break
		}
		bill.ID = recordID
		bills = append(bills, bill)
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	if err := r.reconcileUsageBills(ctx, owner, fingerprint, bills, provider); err != nil {
		return err
	}
	// Group all bill items for an instance. Every item must be paid in USD before
	// the total may replace a reservation; a zero payment alone is not free usage.
	type total struct {
		amount  money.USD
		ids     []string
		settled bool
	}
	grouped := map[string]*total{}
	for _, bill := range bills {
		if bill.InstanceID == "" || bill.UsageStart != nil {
			continue
		}
		item := grouped[bill.InstanceID]
		if item == nil {
			item = &total{settled: true}
			grouped[bill.InstanceID] = item
		}
		if !bill.Settled || bill.PaidUSD == nil {
			item.settled = false
		} else {
			if *bill.PaidUSD < 0 || int64(item.amount) > math.MaxInt64-int64(*bill.PaidUSD) {
				return errors.New("billed amount overflow")
			}
			item.amount += *bill.PaidUSD
		}
		item.ids = append(item.ids, bill.ID)
	}
	for instance, item := range grouped {
		if !item.settled {
			continue
		}
		// Never use substring matching, model names, balance differences, or allocation
		// across several Gateway tasks. Exact provider-task ID + credential identity only.
		var id string
		err = r.db.QueryRowContext(ctx, `SELECT MIN(id::text) FROM tasks WHERE provider_owner_id=$1 AND provider=$2 AND provider_identity=$3 AND provider_task_id=$4
 AND status IN ('succeeded','failed') AND cost IS NOT NULL HAVING COUNT(*)=1`, owner, provider, fingerprint, instance).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err = r.updateCost(ctx, id, func(task *core.Task) {
			now := time.Now().UTC()
			task.Cost.ActualUSD = &item.amount
			task.Cost.ActualSource = provider + "_settled_bill"
			task.Cost.InvoiceIDs = item.ids
			task.Cost.Status = "confirmed"
			task.Cost.ReconciledAt = &now
		}); err != nil {
			return fmt.Errorf("reconcile billed task: %w", err)
		}
	}
	return nil
}

package postgres

import (
	"context"
	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
)

// A deadline closes only unpaid requests. Settled payments keep their financial
// reconciliation for refunds/disputes; expired requests never grant late credit.
func (r *SystemRepository) ExpirePayments(ctx context.Context) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = setAuditActor(ctx, tx, identity.SystemOwnerID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE payments SET status='expired',checkout_url='',updated_at=NOW() WHERE NOT settled AND status IN ('pending','confirming') AND expires_at<=clock_timestamp()`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

var _ payments.ExpiryRepository = (*SystemRepository)(nil)

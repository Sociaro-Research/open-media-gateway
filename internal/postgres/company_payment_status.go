package postgres

import (
	"context"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"time"
)

// CompanyPaymentStatus is an authenticated projection, not payment maintenance.
// No expiration sweep, checkout/customer creation, reconciliation, invoice
// issuance or settlement occurs. Browser management and SDK recipient authority
// are already separated by central dispatch and rechecked in this transaction.
func (r *CompanyInterfaceLifecycle) CompanyPaymentStatus(ctx context.Context, id string) (payments.Payment, error) {
	var empty payments.Payment
	a, ok := ci.MutationAuthorityFromContext(ctx)
	if !ok || !ci.ValidID(id) || !organization.Manage(a.Effective.Role) || identity.ActorID(ctx) != a.Effective.Actor {
		return empty, ci.ErrDenied
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	if err = guardCompanyMutation(ctx, tx, a.Original.Grant.Company); err != nil {
		return empty, err
	}
	// Deliberately no FOR UPDATE / implicit payment FK: settlement takes the
	// payment lock before its company ledger lock. Navigation never needs it.
	p, err := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 AND organization_id=$2`, id, a.Original.Grant.Company))
	if err != nil || !p.Live {
		return empty, ci.ErrDenied
	}
	// Match the visible expired state without invoking ExpirePayments or touching
	// any stored payment, scheduler, reservation, invoice or credit.
	return payments.StatusView(p, time.Now()), tx.Commit()
}

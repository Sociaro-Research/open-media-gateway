package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
)

func (r *SystemRepository) PaymentCustomer(ctx context.Context, org, provider string, create func(string) (string, error)) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO payment_customers(organization_id,provider_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, org, provider)
	if err != nil {
		return "", err
	}
	var id string
	if err = tx.QueryRowContext(ctx, `SELECT customer_id FROM payment_customers WHERE organization_id=$1 AND provider_id=$2 FOR UPDATE`, org, provider).Scan(&id); err != nil {
		return "", err
	}
	if id == "" {
		// Stable even if the first transaction rolls back after Stripe succeeds.
		id, err = create("gateway-customer-" + provider + "-" + org)
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(id, "cus_") {
			return "", payments.ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `UPDATE payment_customers SET customer_id=$3 WHERE organization_id=$1 AND provider_id=$2`, org, provider, id); err != nil {
			return "", err
		}
	}
	return id, tx.Commit()
}

// Document updates are tenant-bound and cannot modify the financial ledger.
func (r *SystemRepository) SavePaymentInvoice(ctx context.Context, p payments.Payment, doc *payments.Invoice) error {
	if p.Invoice == nil || doc.ID != p.Invoice.ID || doc.Status != "paid" {
		return payments.ErrConflict
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE payments SET invoice=$2 WHERE id=$1 AND provider_id=$3 AND organization_id=$4 AND customer_id=$5 AND settled AND invoice->>'id'=$6`, p.ID, data, p.ProviderID, p.OrganizationID, p.CustomerID, doc.ID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return payments.ErrConflict
	}
	return nil
}

package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/google/uuid"
)

func (r *SystemRepository) BeginLocalCard(ctx context.Context, actor, org, provider string, cents int64, key, feeMode string, profile payments.BillingProfile, vat *payments.VATCalculator) (payments.Payment, error) {
	if !payments.ValidReference(key) || cents < payments.MinCents || cents > payments.MaxCents {
		return payments.Payment{}, payments.ErrInvalid
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return payments.Payment{}, e
	}
	defer tx.Rollback()
	role, e := orgLock(ctx, tx, actor, org)
	if e != nil {
		return payments.Payment{}, e
	}
	old, e := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE organization_id=$1 AND actor_id=$2 AND idempotency_key=$3`, org, actor, key))
	if e == nil {
		if old.ProviderID != provider || old.AmountCents != cents || old.FeeMode != feeMode {
			return old, payments.ErrConflict
		}
		if old.InvoiceMode == "local" && (old.Billing == nil || !reflect.DeepEqual(profile, payments.BillingProfile{BusinessName: old.Billing.BusinessName, Email: old.Billing.Email, Address: old.Billing.Address, TaxIDs: old.Billing.TaxIDs})) {
			return old, payments.ErrConflict
		}
		return old, tx.Commit()
	}
	if !errors.Is(e, payments.ErrNotFound) {
		return old, e
	}
	pr, e := scanPaymentProvider(tx.QueryRowContext(ctx, `SELECT `+paymentProviderColumns+` FROM payment_providers WHERE id=$1 FOR SHARE`, provider))
	if e != nil {
		return old, e
	}
	if !pr.Enabled || pr.Kind != "stripe" {
		return old, payments.ErrUnavailable
	}
	if pr.StripeInvoices {
		if e = tx.Commit(); e != nil {
			return old, e
		}
		return r.BeginPayment(ctx, actor, org, provider, cents, key, feeMode)
	}
	if !pr.Live && role != "system_admin" {
		return old, payments.ErrForbidden
	}
	if pr.FeeMode != feeMode {
		return old, payments.ErrConflict
	}
	p := payments.Payment{ID: uuid.NewString(), OrganizationID: org, ProviderID: provider, ActorID: actor, AmountCents: cents, Currency: "usd", Live: pr.Live, FeeMode: feeMode, TaxMode: "exclusive", InvoiceMode: "local"}
	settings, e := readVATSettings(ctx, tx)
	if e != nil {
		return p, e
	}
	manual, e := readVATVerification(ctx, tx, org)
	if e != nil {
		return p, e
	}
	billing, snapshot, e := vat.Quote(ctx, settings, p, profile, manual)
	if e != nil {
		return p, e
	}
	bill, e := json.Marshal(billing)
	if e != nil {
		return p, e
	}
	data, e := json.Marshal(snapshot)
	if e != nil {
		return p, e
	}
	p, e = scanPayment(tx.QueryRowContext(ctx, `INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,amount_cents,currency,live,fee_mode,tax_mode,billing,invoice_mode,vat,expires_at) VALUES($1,$2,$3,$4,$5,$6,'usd',$7,$8,'exclusive',$9,'local',$10,NOW()+INTERVAL '720 hours') RETURNING `+paymentColumns, p.ID, org, provider, actor, key, cents, pr.Live, feeMode, bill, data))
	if e != nil {
		return p, paymentDBError(e)
	}
	return p, tx.Commit()
}

// A payment lock serializes issuance. The stored PDF, number and issue date are
// immutable, including on retries after company/logo or payment-state changes.
func (r *SystemRepository) LocalInvoice(ctx context.Context, id string, render func(payments.Payment, string, time.Time) ([]byte, error)) (payments.InvoiceDocument, error) {
	var doc payments.InvoiceDocument
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return doc, e
	}
	defer tx.Rollback()
	p, e := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 FOR UPDATE`, id))
	if e != nil {
		return doc, e
	}
	if p.InvoiceMode != "local" || p.VAT == nil || p.Billing == nil {
		return doc, payments.ErrUnavailable
	}
	if p.Status == "cancelled" {
		return doc, payments.ErrConflict
	}
	e = tx.QueryRowContext(ctx, `SELECT number,created_at,pdf FROM payment_invoice_documents WHERE payment_id=$1`, id).Scan(&doc.Number, &doc.CreatedAt, &doc.PDF)
	if e == nil {
		return doc, tx.Commit()
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return doc, e
	}
	if p.RequestExpired(time.Now()) || p.Status == "expired" {
		return doc, payments.ErrExpired
	}
	// The invoice uses a durable account link; an expired Stripe session can be
	// renewed for the same payment without changing its invoice.
	if p.Stablecoin == nil && !p.Settled && (p.SessionID == "" || (p.Status != "pending" && p.Status != "expired")) {
		return doc, payments.ErrConflict
	}
	var sequence int64
	if e = tx.QueryRowContext(ctx, `SELECT nextval('payment_invoice_numbers')`).Scan(&sequence); e != nil {
		return doc, e
	}
	// Match PostgreSQL timestamp precision for the first response and retries.
	doc.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
	doc.Number = fmt.Sprintf("INV-%04d-%06d", doc.CreatedAt.Year(), sequence)
	doc.PDF, e = render(p, doc.Number, doc.CreatedAt)
	if e != nil {
		return doc, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO payment_invoice_documents(payment_id,number,created_at,pdf) VALUES($1,$2,$3,$4)`, id, doc.Number, doc.CreatedAt, doc.PDF)
	if e != nil {
		return doc, e
	}
	meta, e := json.Marshal(payments.Invoice{ID: p.ID, Number: doc.Number, Status: "issued", PDFURL: "/v1/organizations/" + p.OrganizationID + "/payments/" + p.ID + "/invoice"})
	if e != nil {
		return doc, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE payments SET invoice=$2 WHERE id=$1`, id, meta)
	if e != nil {
		return doc, e
	}
	return doc, tx.Commit()
}

var _ payments.LocalInvoiceRepository = (*SystemRepository)(nil)

// Commit the attempt timestamp before contacting Stripe. A lost creation
// response retries the exact same idempotency key and expires_at parameters.
func (r *SystemRepository) PrepareInvoiceCheckout(ctx context.Context, id string) (payments.Payment, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return payments.Payment{}, err
	}
	defer tx.Rollback()
	p, err := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return p, err
	}
	if p.Stablecoin != nil || p.ExpiresAt == nil {
		return p, payments.ErrInvalid
	}
	if p.Settled {
		return p, tx.Commit()
	}
	if p.RequestExpired(time.Now()) || p.Status == "expired" {
		return p, payments.ErrExpired
	}
	if p.CheckoutExpired {
		p, err = scanPayment(tx.QueryRowContext(ctx, `UPDATE payments SET checkout_attempt=checkout_attempt+1,checkout_started_at=NOW(),session_id='',intent_id='',checkout_url='',checkout_expired=FALSE,status='pending',next_reconcile_at=NOW(),updated_at=NOW() WHERE id=$1 RETURNING `+paymentColumns, id))
		if err != nil {
			return p, err
		}
	} else if p.Status != "pending" {
		return p, payments.ErrConflict
	}
	return p, tx.Commit()
}

var _ payments.InvoiceCheckoutRepository = (*SystemRepository)(nil)

package postgres

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const paymentProviderColumns = `fee_mode,id,kind,name,enabled,live,account_id,version,created_by,secret_cipher,token_hash,last_webhook_at,stablecoin,stripe_invoices`

type paymentScanner interface{ Scan(...any) error }

func scanPaymentProvider(row paymentScanner) (payments.Provider, error) {
	var p payments.Provider
	var stablecoin []byte
	e := row.Scan(&p.FeeMode, &p.ID, &p.Kind, &p.Name, &p.Enabled, &p.Live, &p.AccountID, &p.Version, &p.CreatedBy, &p.SecretCipher, &p.TokenHash, &p.LastWebhook, &stablecoin, &p.StripeInvoices)
	if e == nil && len(stablecoin) > 0 {
		e = json.Unmarshal(stablecoin, &p.Stablecoin)
	}
	return p, paymentDBError(e)
}
func paymentDBError(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return payments.ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(e, &pe) && (pe.Code == "23505" || pe.Code == "23503") {
		return payments.ErrConflict
	}
	return e
}
func (r *SystemRepository) PaymentProvider(ctx context.Context, id string) (payments.Provider, error) {
	return scanPaymentProvider(r.db.QueryRowContext(ctx, `SELECT `+paymentProviderColumns+` FROM payment_providers WHERE id=$1`, id))
}
func (r *SystemRepository) PaymentProviders(ctx context.Context) ([]payments.Provider, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT `+paymentProviderColumns+` FROM payment_providers ORDER BY created_at,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []payments.Provider{}
	for rows.Next() {
		p, e := scanPaymentProvider(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *SystemRepository) SavePaymentProvider(ctx context.Context, actor string, p payments.Provider) (payments.Provider, error) {
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return p, e
	}
	defer tx.Rollback()
	if _, e = readSystemSettings(ctx, tx, true); e != nil {
		return p, e
	}
	_, e = systemActor(ctx, tx, actor)
	if e != nil {
		return p, e
	}
	config, e := json.Marshal(p.Stablecoin)
	if e != nil {
		return p, e
	}
	if p.Version == 0 {
		p.Version = 1
		p.CreatedBy = actor
		_, e = tx.ExecContext(ctx, `INSERT INTO payment_providers(fee_mode,id,kind,name,enabled,live,account_id,created_by,secret_cipher,token_hash,stablecoin,stripe_invoices) VALUES($10,$1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($11::jsonb,'null'::jsonb),$12)`, p.ID, p.Kind, p.Name, p.Enabled, p.Live, p.AccountID, actor, p.SecretCipher, p.TokenHash, p.FeeMode, config, p.StripeInvoices)
	} else {
		old, err := scanPaymentProvider(tx.QueryRowContext(ctx, `SELECT `+paymentProviderColumns+` FROM payment_providers WHERE id=$1 FOR UPDATE`, p.ID))
		if err != nil {
			return p, err
		}
		if old.Version != p.Version || old.Kind != p.Kind || old.Live != p.Live || old.AccountID != p.AccountID {
			return p, system.ErrConflict
		}
		p.Version++
		p.CreatedBy = old.CreatedBy
		_, e = tx.ExecContext(ctx, `UPDATE payment_providers SET name=$2,enabled=$3,version=$4,secret_cipher=$5,fee_mode=$6,stablecoin=NULLIF($7::jsonb,'null'::jsonb),stripe_invoices=$8,updated_at=NOW() WHERE id=$1`, p.ID, p.Name, p.Enabled, p.Version, p.SecretCipher, p.FeeMode, config, p.StripeInvoices)
	}
	if e != nil {
		return p, paymentDBError(e)
	}
	return p, tx.Commit()
}

const paymentColumns = `fee_mode,fee_cents,id,organization_id,provider_id,actor_id,idempotency_key,amount_cents,currency,live,status,session_id,intent_id,checkout_url,receipt_url,credited_cents,refunded_cents,held_cents,settled,revision,created_at,updated_at,settlement,tax_mode,customer_id,billing,invoice,stablecoin,fee_micros,invoice_mode,vat,checkout_attempt,checkout_started_at,expires_at,checkout_expired`

func scanPayment(row paymentScanner) (payments.Payment, error) {
	var p payments.Payment
	var settlement, billing, invoice, stablecoin, vat []byte
	e := row.Scan(&p.FeeMode, &p.FeeCents, &p.ID, &p.OrganizationID, &p.ProviderID, &p.ActorID, &p.IdempotencyKey, &p.AmountCents, &p.Currency, &p.Live, &p.Status, &p.SessionID, &p.IntentID, &p.CheckoutURL, &p.ReceiptURL, &p.CreditedCents, &p.RefundedCents, &p.HeldCents, &p.Settled, &p.Revision, &p.CreatedAt, &p.UpdatedAt, &settlement, &p.TaxMode, &p.CustomerID, &billing, &invoice, &stablecoin, &p.FeeMicros, &p.InvoiceMode, &vat, &p.CheckoutAttempt, &p.CheckoutStartedAt, &p.ExpiresAt, &p.CheckoutExpired)
	if e == nil && len(settlement) > 0 {
		e = json.Unmarshal(settlement, &p.Settlement)
	}
	if e == nil && len(billing) > 0 {
		e = json.Unmarshal(billing, &p.Billing)
	}
	if e == nil && len(invoice) > 0 {
		e = json.Unmarshal(invoice, &p.Invoice)
	}

	if e == nil && len(stablecoin) > 0 {
		e = json.Unmarshal(stablecoin, &p.Stablecoin)
	}
	if e == nil && len(vat) > 0 {
		e = json.Unmarshal(vat, &p.VAT)
	}
	return p, paymentDBError(e)
}
func (r *SystemRepository) Payment(ctx context.Context, id string) (payments.Payment, error) {
	return scanPayment(r.db.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1`, id))
}
func (r *SystemRepository) Payments(ctx context.Context, org string, offset int) ([]payments.Payment, error) {
	rows, e := r.db.QueryContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE organization_id=$1 ORDER BY created_at DESC,id LIMIT 51 OFFSET $2`, org, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []payments.Payment{}
	for rows.Next() {
		p, e := scanPayment(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (r *SystemRepository) BeginPayment(ctx context.Context, actor, org, provider string, cents int64, key, feeMode string) (payments.Payment, error) {
	if !payments.ValidReference(key) || cents < payments.MinCents || cents > payments.MaxCents {
		return payments.Payment{}, payments.ErrInvalid
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return payments.Payment{}, e
	}
	defer tx.Rollback()
	pr, e := scanPaymentProvider(tx.QueryRowContext(ctx, `SELECT `+paymentProviderColumns+` FROM payment_providers WHERE id=$1 FOR SHARE`, provider))
	if e != nil {
		return payments.Payment{}, e
	}
	if !pr.Enabled || pr.Kind != "stripe" {
		return payments.Payment{}, payments.ErrUnavailable
	}
	role, e := orgLock(ctx, tx, actor, org)
	if e != nil {
		return payments.Payment{}, e
	}
	if !pr.Live && role != "system_admin" {
		return payments.Payment{}, payments.ErrForbidden
	}
	p, e := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE organization_id=$1 AND actor_id=$2 AND idempotency_key=$3`, org, actor, key))
	if e == nil {
		if p.ProviderID != provider || p.AmountCents != cents || p.FeeMode != feeMode {
			return p, payments.ErrConflict
		}
		return p, tx.Commit()
	}
	if !errors.Is(e, payments.ErrNotFound) {
		return p, e
	}
	if pr.FeeMode != feeMode {
		return p, payments.ErrConflict
	}
	p, e = scanPayment(tx.QueryRowContext(ctx, `INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,amount_cents,currency,live,fee_mode,tax_mode,invoice_mode,expires_at) VALUES($1,$2,$3,$4,$5,$6,'usd',$7,$8,'exclusive','stripe',NOW()+INTERVAL '720 hours') RETURNING `+paymentColumns, uuid.NewString(), org, provider, actor, key, cents, pr.Live, pr.FeeMode))
	if e != nil {
		return p, e
	}
	return p, tx.Commit()
}

// The callback runs under a payment row lock. Stripe state is fetched *inside*
// that lock so concurrent/out-of-order events cannot overwrite newer state.
func (r *SystemRepository) WithPayment(ctx context.Context, id string, fn func(payments.Payment) (payments.Snapshot, error)) (payments.Payment, error) {
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return payments.Payment{}, e
	}
	defer tx.Rollback()
	// This transaction reconciles provider state; even a user-triggered refresh
	// must attribute its derived billing-profile and credit changes to the service.
	if e = setAuditActor(ctx, tx, identity.SystemOwnerID); e != nil {
		return payments.Payment{}, e
	}
	p, e := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 FOR UPDATE`, id))
	if e != nil {
		return p, e
	}
	s, e := fn(p)
	if e != nil {
		return p, e
	}
	p, e = applyPayment(ctx, tx, p, s)
	if e != nil {
		return p, e
	}
	return p, tx.Commit()
}
func applyPayment(ctx context.Context, tx *sql.Tx, p payments.Payment, s payments.Snapshot) (payments.Payment, error) {
	if p.RequestExpired(time.Now()) && s.Paid && !p.Settled {
		return p, payments.ErrExpired
	}
	if s.Status == "expired" && !s.Paid && p.ExpiresAt != nil && time.Now().Before(*p.ExpiresAt) && p.Stablecoin == nil {
		s.Status = "pending"
		s.CheckoutExpired = true
	}

	if p.CustomerID != "" && s.CustomerID != p.CustomerID {
		return p, payments.ErrConflict
	}
	if p.Settled && p.Billing != nil && (s.Billing == nil || s.Billing.SubtotalCents != p.Billing.SubtotalCents || s.Billing.TotalCents != p.Billing.TotalCents || s.Billing.TaxCents != p.Billing.TaxCents) {
		return p, payments.ErrConflict
	}
	firstCredit := !p.Settled && s.Paid && p.Live
	if p.SessionID != "" && s.SessionID != p.SessionID || p.IntentID != "" && s.IntentID != p.IntentID || p.Settled && !s.Paid {
		return p, payments.ErrConflict
	}
	net, e := payments.Net(p, s)
	if e != nil {
		return p, e
	}
	if s.Revision > 0 && s.Revision < p.Revision {
		return p, payments.ErrConflict
	}
	unchanged := p.CheckoutExpired == s.CheckoutExpired && p.FeeCents == s.FeeCents && p.Status == s.Status && p.SessionID == s.SessionID && p.IntentID == s.IntentID && p.CheckoutURL == s.CheckoutURL && p.ReceiptURL == s.ReceiptURL && p.RefundedCents == s.RefundedCents && p.HeldCents == s.HeldCents && p.Settled == s.Paid
	if s.Revision > 0 && s.Revision == p.Revision && !unchanged {
		return p, payments.ErrConflict
	}
	// Document enrichment does not create credits or financial revisions.
	if s.CustomerID != "" || s.Billing != nil || s.Invoice != nil {
		billing, err := json.Marshal(s.Billing)
		if err != nil {
			return p, err
		}
		invoice, err := json.Marshal(s.Invoice)
		if err != nil {
			return p, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE payments SET customer_id=$2,billing=COALESCE(NULLIF($3::jsonb,'null'::jsonb),billing),invoice=COALESCE(NULLIF($4::jsonb,'null'::jsonb),invoice) WHERE id=$1`, p.ID, s.CustomerID, billing, invoice); err != nil {
			return p, err
		}
		p.CustomerID = s.CustomerID
		if s.Billing != nil {
			p.Billing = s.Billing
		}
		if s.Invoice != nil {
			p.Invoice = s.Invoice
		}
	}
	// Update company identity only on the first successful live settlement. Replays,
	// refunds and late older payments must not replace newer company details.
	if firstCredit && s.Billing != nil && strings.TrimSpace(s.Billing.BusinessName) != "" {
		b := s.Billing
		profile, err := json.Marshal(payments.BillingProfile{BusinessName: b.BusinessName, Email: b.Email, Address: b.Address, TaxIDs: b.TaxIDs})
		if err != nil {
			return p, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE organizations SET billing_profile=$2,billing_payment_created_at=$3,billing_payment_id=$4 WHERE id=$1 AND (billing_payment_created_at IS NULL OR (billing_payment_created_at,billing_payment_id)<($3,$4::uuid))`, p.OrganizationID, profile, p.CreatedAt, p.ID); err != nil {
			return p, err
		}
	}
	// Settlement enrichment must not create a financial revision or duplicate credit.
	if s.Settlement != nil && (p.Settlement == nil || *s.Settlement != *p.Settlement) {
		data, err := json.Marshal(s.Settlement)
		if err != nil {
			return p, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE payments SET settlement=$2 WHERE id=$1`, p.ID, data); err != nil {
			return p, err
		}
		p.Settlement = s.Settlement
	}
	if unchanged && (s.Revision == 0 || s.Revision == p.Revision) {
		return p, nil
	}
	revision := p.Revision + 1
	if s.Revision > 0 {
		revision = s.Revision
	}
	credited := net
	if !p.Live {
		credited = 0
	}
	delta := credited - p.CreditedCents
	if delta != 0 {
		// Coordinate with generation budget checks, existing manual credits and corrections.
		if _, e = tx.ExecContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR NO KEY UPDATE`, p.OrganizationID); e != nil {
			return p, e
		}
		kind := "adjustment"
		if !p.Settled && delta > 0 {
			kind = "payment"
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO organization_credits(id,organization_id,actor_id,amount_micros,kind,reason,external_id,payment_id,payment_revision) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, uuid.NewString(), p.OrganizationID, p.ActorID, delta*10000, kind, "Payment "+s.Status, "payment:"+p.ID+":"+fmt.Sprint(revision), p.ID, revision)
		if e != nil {
			return p, paymentDBError(e)
		}
		if e = orgEvent(ctx, tx, p.OrganizationID, p.ActorID, "payment_settlement", map[string]any{"payment_id": p.ID, "delta_cents": delta, "status": s.Status, "revision": revision}); e != nil {
			return p, e
		}
	}
	p, e = scanPayment(tx.QueryRowContext(ctx, `UPDATE payments SET status=$2,session_id=$3,intent_id=$4,checkout_url=$5,receipt_url=$6,credited_cents=$7,refunded_cents=$8,held_cents=$9,settled=$10,revision=$11,fee_cents=$12,checkout_expired=$13,updated_at=NOW() WHERE id=$1 RETURNING `+paymentColumns, p.ID, s.Status, s.SessionID, s.IntentID, s.CheckoutURL, s.ReceiptURL, credited, s.RefundedCents, s.HeldCents, s.Paid, revision, s.FeeCents, s.CheckoutExpired))
	if e == nil && firstCredit && credited > 0 {
		var notice payments.Notice
		notice, e = paymentNotice(ctx, tx, p.ProviderID, p.ID)
		if e == nil {
			e = queuePaymentNotice(ctx, tx, "payment:"+p.ID+":credited", "success", notice)
		}
	}
	return p, e
}
func (r *SystemRepository) WebhookSeen(ctx context.Context, id string) error {
	_, e := r.db.ExecContext(ctx, `UPDATE payment_providers SET last_webhook_at=NOW() WHERE id=$1`, id)
	return e
}
func (r *SystemRepository) ExternalSettlement(ctx context.Context, id string, hash []byte, in payments.ExternalInput) (payments.Payment, error) {
	if e := in.Validate(); e != nil {
		return payments.Payment{}, e
	}
	tx, e := beginAuditTx(ctx, r.db)
	if e != nil {
		return payments.Payment{}, e
	}
	defer tx.Rollback()
	pr, e := scanPaymentProvider(tx.QueryRowContext(ctx, `SELECT `+paymentProviderColumns+` FROM payment_providers WHERE id=$1 FOR SHARE`, id))
	if e != nil {
		return payments.Payment{}, payments.ErrForbidden
	}
	if pr.Kind != "external" || !pr.Enabled || len(pr.TokenHash) != 32 || subtle.ConstantTimeCompare(hash, pr.TokenHash) != 1 {
		return payments.Payment{}, payments.ErrForbidden
	}
	// Serialize first delivery of a source reference before INSERT, without races.
	if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, id+":"+in.ExternalID); e != nil {
		return payments.Payment{}, e
	}
	p, e := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE provider_id=$1 AND external_id=$2 FOR UPDATE`, id, in.ExternalID))
	if errors.Is(e, payments.ErrNotFound) {
		p, e = scanPayment(tx.QueryRowContext(ctx, `INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,external_id,amount_cents,currency,live,fee_mode) VALUES($1,$2,$3,$4,$5,$6,$7,'usd',$8,$9) RETURNING `+paymentColumns, uuid.NewString(), in.OrganizationID, id, pr.CreatedBy, "external:"+uuid.NewString(), in.ExternalID, in.AmountCents, pr.Live, pr.FeeMode))
	}
	if e != nil {
		return p, e
	}
	if p.OrganizationID != in.OrganizationID || p.AmountCents != in.AmountCents {
		return p, payments.ErrConflict
	}
	state := "paid"
	if in.RefundedCents > 0 {
		state = "partially_refunded"
	}
	if in.RefundedCents == in.AmountCents {
		state = "refunded"
	}
	if in.HeldCents > 0 {
		state = "disputed"
	}
	p, e = applyPayment(ctx, tx, p, payments.Snapshot{FeeCents: in.FeeCents, Status: state, Paid: true, RefundedCents: in.RefundedCents, HeldCents: in.HeldCents, Revision: in.Revision})
	if e != nil {
		return p, e
	}
	return p, tx.Commit()
}

var _ payments.Repository = (*SystemRepository)(nil)

func (r *SystemRepository) ClaimPaymentChecks(ctx context.Context) ([]string, error) {
	rows, e := r.db.QueryContext(ctx, `WITH due AS (SELECT p.id FROM payments p JOIN payment_providers pr ON pr.id=p.provider_id WHERE pr.kind='stripe' AND p.next_reconcile_at<=NOW() AND (p.settled OR ((p.expires_at IS NULL OR p.expires_at>clock_timestamp()) AND p.status<>'expired' AND NOT p.checkout_expired)) AND (p.session_id<>'' OR p.checkout_attempt>0 OR p.created_at>NOW()-INTERVAL '23 hours') ORDER BY p.next_reconcile_at LIMIT 10 FOR UPDATE OF p SKIP LOCKED) UPDATE payments p SET next_reconcile_at=NOW()+INTERVAL '5 minutes' FROM due WHERE p.id=due.id RETURNING p.id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			return nil, e
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
func (r *SystemRepository) SchedulePaymentCheck(ctx context.Context, id string, delay time.Duration) error {
	_, e := r.db.ExecContext(ctx, `UPDATE payments SET next_reconcile_at=NOW()+($2*INTERVAL '1 second') WHERE id=$1`, id, int64(delay/time.Second))
	return e
}

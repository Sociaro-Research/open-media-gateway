package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/google/uuid"
)

const companyPaymentReturnLifetime = 24 * time.Hour

type companyPaymentReturnBinding struct {
	Purpose                                              string
	GrantID, Company, Actor, Recipient, KeyID, SessionID string
	InstallationHash, LocalSessionHash                   [32]byte
	GrantEpoch, SessionEpoch, ConnectionRevision         int64
	CentralOrigin, LocalOrigin, OperationID, PaymentID   string
	CheckoutAttempt                                      int64
}

// This boundary is browser-management only. An SDK key is not a user session;
// the future SDK checkout contract must bind its recipient explicitly instead
// of manufacturing an owner's session. No payment route is enabled by this code.
func (r *CompanyInterfaceLifecycle) paymentReturnBinding(ctx context.Context, tx *sql.Tx, in payments.ReturnIntentInput) (companyPaymentReturnBinding, error) {
	var out companyPaymentReturnBinding
	a, ok := ci.MutationAuthorityFromContext(ctx)
	p := a.Original
	if !ok || p.BrowserActor == "" || p.SessionID == "" || a.Effective.Actor != p.BrowserActor || !organization.Manage(a.Effective.Role) || !ci.ValidID(in.OperationID) || !ci.ValidID(in.PaymentID) || in.CheckoutAttempt < 0 {
		return out, ci.ErrDenied
	}
	if err := guardCompanyMutation(ctx, tx, p.Grant.Company); err != nil {
		return out, err
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT binding FROM company_interface_attempts WHERE session_id=$1 AND grant_id=$2 AND actor_id=$3 AND state='approved'`, p.SessionID, p.Grant.ID, p.BrowserActor).Scan(&raw); err != nil {
		return out, ci.ErrDenied
	}
	var issued ci.Attempt
	if json.Unmarshal(raw, &issued) != nil || issued.InstallationHash != p.Grant.InstallationHash || issued.LocalSessionHash != a.LocalSessionHash || issued.LocalOrigin != p.Grant.LocalOrigin || issued.CentralOrigin != r.central || issued.ConnectionRevision < 0 {
		return out, ci.ErrDenied
	}
	out = companyPaymentReturnBinding{Purpose: "company-payment-return/v1", GrantID: p.Grant.ID, Company: p.Grant.Company, Actor: p.BrowserActor, Recipient: p.Recipient, KeyID: p.KeyID, SessionID: p.SessionID, InstallationHash: p.Grant.InstallationHash, LocalSessionHash: a.LocalSessionHash, GrantEpoch: p.Grant.Epoch, SessionEpoch: p.SessionEpoch, ConnectionRevision: issued.ConnectionRevision, CentralOrigin: r.central, LocalOrigin: p.Grant.LocalOrigin, OperationID: in.OperationID, PaymentID: in.PaymentID, CheckoutAttempt: in.CheckoutAttempt}
	return out, nil
}

func returnAAD(binding companyPaymentReturnBinding) any {
	return []any{"company-payment-return-receipt/v1", binding}
}

type companyPaymentReturnRow struct {
	binding       companyPaymentReturnBinding
	hash, receipt []byte
	expires       time.Time
}

func scanCompanyPaymentReturn(row interface{ Scan(...any) error }) (companyPaymentReturnRow, error) {
	var v companyPaymentReturnRow
	var raw []byte
	err := row.Scan(&raw, &v.hash, &v.receipt, &v.expires)
	if err != nil {
		return v, err
	}
	if json.Unmarshal(raw, &v.binding) != nil || len(v.hash) != 32 {
		return v, ci.ErrDenied
	}
	return v, nil
}
func (r *CompanyInterfaceLifecycle) returnReceipt(v companyPaymentReturnRow) (payments.ReturnIntent, error) {
	var result payments.ReturnIntent
	if !v.expires.After(time.Now()) {
		return result, ci.ErrDenied
	}
	// The DTO is deliberately not JSON-serializable, so encrypt a private wire
	// receipt instead of accidentally storing an empty public projection.
	var wire struct{ Token string }
	if err := r.cipher.Open(v.receipt, returnAAD(v.binding), &wire); err != nil {
		return result, ci.ErrDenied
	}
	digest := ci.Digest(wire.Token)
	if !ci.ValidSecret(wire.Token) || !bytes.Equal(digest[:], v.hash) {
		return result, ci.ErrDenied
	}
	result = payments.ReturnIntent{Token: wire.Token, PaymentID: v.binding.PaymentID, CheckoutAttempt: v.binding.CheckoutAttempt, ExpiresAt: v.expires.UTC()}
	return result, nil
}

func (r *CompanyInterfaceLifecycle) IssuePaymentReturn(ctx context.Context, in payments.ReturnIntentInput) (payments.ReturnIntent, error) {
	var empty payments.ReturnIntent
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	binding, err := r.paymentReturnBinding(ctx, tx, in)
	if err != nil {
		return empty, err
	}
	prior, err := scanCompanyPaymentReturn(tx.QueryRowContext(ctx, `SELECT binding,token_hash,receipt,expires_at FROM company_interface_payment_returns WHERE grant_id=$1 AND session_id=$2 AND operation_id=$3`, binding.GrantID, binding.SessionID, binding.OperationID))
	if err == nil {
		binding.Recipient, binding.KeyID = prior.binding.Recipient, prior.binding.KeyID
		if prior.binding != binding {
			return empty, ci.ErrConflict
		}
		result, err := r.returnReceipt(prior)
		if err != nil {
			return empty, err
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return empty, err
	}
	// MVCC only: no payment locks, provider calls, expiry or ledger mutation.
	payment, err := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 AND organization_id=$2`, in.PaymentID, binding.Company))
	if err != nil || !payment.Live || payment.CheckoutAttempt != in.CheckoutAttempt {
		return empty, ci.ErrDenied
	}
	now := time.Now()
	expires := now.Add(companyPaymentReturnLifetime).UTC().Truncate(time.Microsecond)
	if payment.ExpiresAt != nil && payment.ExpiresAt.Before(expires) {
		expires = payment.ExpiresAt.UTC().Truncate(time.Microsecond)
	}
	if !expires.After(now) {
		return empty, ci.ErrDenied
	}
	token, err := ci.NewSecret()
	if err != nil {
		return empty, err
	}
	encrypted, err := r.cipher.Seal(struct{ Token string }{token}, returnAAD(binding))
	if err != nil {
		return empty, err
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return empty, err
	}
	digest := ci.Digest(token)
	_, err = tx.ExecContext(ctx, `INSERT INTO company_interface_payment_returns(id,grant_id,session_id,operation_id,payment_id,binding,token_hash,receipt,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, uuid.NewString(), binding.GrantID, binding.SessionID, binding.OperationID, binding.PaymentID, raw, digest[:], encrypted, expires)
	if err != nil {
		return empty, err
	}
	if err = r.checkpoint("payment-return-receipt"); err != nil {
		return empty, err
	}
	result := payments.ReturnIntent{Token: token, PaymentID: in.PaymentID, CheckoutAttempt: in.CheckoutAttempt, ExpiresAt: expires}
	return result, tx.Commit()
}

// ConsumePaymentReturn changes only the navigation receipt. Every repeat
// revalidates current actor/source/grant/session/membership; token rotation is
// allowed because the immutable binding excludes access-token version. The
// returned payment is an MVCC status read, never a manager/reconcile operation.
func (r *CompanyInterfaceLifecycle) ConsumePaymentReturn(ctx context.Context, token string) (payments.Payment, error) {
	return r.consumePaymentReturn(ctx, token, "")
}

// ConsumeCheckoutReturn permits a current company manager to continue an
// existing checkout using their OWN return receipt. The immutable PSP callback
// is correlation only; it neither lends the creator's authority nor requires
// the creator's browser session to remain logged in. No PSP URL is regenerated.
func (r *CompanyInterfaceLifecycle) ConsumeCheckoutReturn(ctx context.Context, checkoutState, viewerReceipt string) (payments.Payment, error) {
	if !ci.ValidSecret(checkoutState) {
		return payments.Payment{}, ci.ErrDenied
	}
	return r.consumePaymentReturn(ctx, viewerReceipt, checkoutState)
}

func samePaymentCheckout(a, b companyPaymentReturnBinding) bool {
	return a.PaymentID == b.PaymentID && a.CheckoutAttempt == b.CheckoutAttempt &&
		a.Company == b.Company && a.GrantID == b.GrantID && a.GrantEpoch == b.GrantEpoch &&
		a.InstallationHash == b.InstallationHash && a.CentralOrigin == b.CentralOrigin &&
		a.LocalOrigin == b.LocalOrigin
}

func (r *CompanyInterfaceLifecycle) consumePaymentReturn(ctx context.Context, token, checkoutState string) (payments.Payment, error) {
	var empty payments.Payment
	if !ci.ValidSecret(token) {
		return empty, ci.ErrDenied
	}
	digest := ci.Digest(token)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback()
	prior, err := scanCompanyPaymentReturn(tx.QueryRowContext(ctx, `SELECT binding,token_hash,receipt,expires_at FROM company_interface_payment_returns WHERE token_hash=$1`, digest[:]))
	if err != nil {
		return empty, ci.ErrDenied
	}
	in := payments.ReturnIntentInput{OperationID: prior.binding.OperationID, PaymentID: prior.binding.PaymentID, CheckoutAttempt: prior.binding.CheckoutAttempt}
	current, err := r.paymentReturnBinding(ctx, tx, in)
	if err != nil {
		return empty, err
	}
	// Management is performed by the verified browser actor, not the selected
	// generation key. Preserve original recipient attribution without imposing a
	// new blanket actor==key-recipient rule on later status reads.
	current.Recipient, current.KeyID = prior.binding.Recipient, prior.binding.KeyID
	if current != prior.binding {
		return empty, ci.ErrDenied
	}
	if _, err = r.returnReceipt(prior); err != nil {
		return empty, err
	}
	if checkoutState != "" {
		anchorHash := ci.Digest(checkoutState)
		anchor, err := scanCompanyPaymentReturn(tx.QueryRowContext(ctx, `SELECT binding,token_hash,receipt,expires_at FROM company_interface_payment_returns WHERE token_hash=$1`, anchorHash[:]))
		if err != nil || !samePaymentCheckout(anchor.binding, prior.binding) {
			return empty, ci.ErrDenied
		}
		if _, err = r.returnReceipt(anchor); err != nil {
			return empty, err
		}
	}
	p, err := scanPayment(tx.QueryRowContext(ctx, `SELECT `+paymentColumns+` FROM payments WHERE id=$1 AND organization_id=$2`, current.PaymentID, current.Company))
	if err != nil || !p.Live {
		return empty, ci.ErrDenied
	}
	if identity.ActorID(ctx) != current.Actor {
		return empty, ci.ErrDenied
	}
	changed, err := tx.ExecContext(ctx, `UPDATE company_interface_payment_returns SET consumed_at=COALESCE(consumed_at,clock_timestamp()) WHERE token_hash=$1 AND expires_at>clock_timestamp()`, digest[:])
	if err != nil {
		return empty, err
	}
	if count, err := changed.RowsAffected(); err != nil || count != 1 {
		return empty, ci.ErrDenied
	}
	if err = r.checkpoint("payment-return-consumed"); err != nil {
		return empty, err
	}
	return p, tx.Commit()
}

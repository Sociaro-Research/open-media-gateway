package payments

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
)

const CompanyPaymentReturnPath = "/auth/company-interface/v1/payment-return"

// CheckoutReturn is a server-built navigation target, not payment or company
// authority. HTTP decoding cannot populate its private fields. The issuer must
// first persist and authorize the checkout-specific return intent; this type
// deliberately cannot accept an arbitrary success/cancel URL or local origin.
// A callback, including a successful one, is never evidence of payment.
type CheckoutReturn struct {
	destination string
}

// CompanyCheckoutReturn uses trusted central deployment configuration and the
// opaque nonce of an already persisted return intent. The future callback
// resolves its local destination from that intent, never from this URL.
func CompanyCheckoutReturn(centralOrigin, state string) (CheckoutReturn, error) {
	origin, err := installation.NormalizeOrigin(centralOrigin)
	if err != nil || origin != centralOrigin || !strings.HasPrefix(origin, "https://") {
		return CheckoutReturn{}, ErrInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(state)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != state {
		return CheckoutReturn{}, ErrInvalid
	}
	return CheckoutReturn{destination: origin + CompanyPaymentReturnPath + "?state=" + url.QueryEscape(state)}, nil
}

// URLs intentionally makes success and cancellation identical: the provider's
// navigation hint must not select any settlement, cancellation or credit action.
func (r CheckoutReturn) URLs() (success, cancel string, err error) {
	if r.destination == "" {
		return "", "", ErrInvalid
	}
	return r.destination, r.destination, nil
}

// ReturnCheckoutAdapter is optional while the durable return-intent protocol is
// being integrated. Existing standalone Adapter.Checkout remains unchanged.
// No production route may use this without the persisted intent/authority guard.
type ReturnCheckoutAdapter interface {
	CheckoutWithReturn(context.Context, Provider, Credentials, Payment, CheckoutReturn) (Snapshot, error)
}

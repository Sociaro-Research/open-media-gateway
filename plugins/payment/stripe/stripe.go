// Package stripe is a replaceable payment adapter. It does not access balances
// or the database; validated provider state is passed to the funding ledger.
package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/google/uuid"
)

const APIVersion = "2025-09-30.clover"

type Client struct {
	http *http.Client
	base string
	now  func() time.Time
}

func New() *Client {
	return &Client{http: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, base: "https://api.stripe.com", now: time.Now}
}
func (c *Client) request(ctx context.Context, credentials payments.Credentials, method, path string, values url.Values, key string, out any) error {
	var body io.Reader
	if method == "GET" {
		if len(values) > 0 {
			path += "?" + values.Encode()
		}
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, e := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if e != nil {
		return payments.ErrUnavailable
	}
	req.SetBasicAuth(credentials.SecretKey, "")
	req.Header.Set("Stripe-Version", APIVersion)
	req.Header.Set("User-Agent", "Sociaro-Gateway-Payments/1.0")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return payments.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return payments.ErrUnavailable
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if e != nil || len(b) > 1<<20 {
		return payments.ErrUnavailable
	}
	if json.Unmarshal(b, out) != nil {
		return payments.ErrUnavailable
	}
	return nil
}
func (c *Client) Check(ctx context.Context, credentials payments.Credentials) (payments.Account, error) {
	var a struct {
		ID      string `json:"id"`
		Charges bool   `json:"charges_enabled"`
	}
	if e := c.request(ctx, credentials, "GET", "/v1/account", nil, "", &a); e != nil {
		return payments.Account{}, e
	}
	var b struct {
		Live bool `json:"livemode"`
	}
	if e := c.request(ctx, credentials, "GET", "/v1/balance", nil, "", &b); e != nil {
		return payments.Account{}, e
	}
	if !strings.HasPrefix(a.ID, "acct_") {
		return payments.Account{}, payments.ErrUnavailable
	}
	return payments.Account{ID: a.ID, Live: b.Live, ChargesEnabled: a.Charges}, nil
}

type session struct {
	Subtotal     int64  `json:"amount_subtotal"`
	Customer     string `json:"customer"`
	InvoiceID    string `json:"invoice"`
	AutomaticTax struct {
		Enabled bool   `json:"enabled"`
		Status  string `json:"status"`
	} `json:"automatic_tax"`
	TotalDetails struct {
		Tax      int64 `json:"amount_tax"`
		Discount int64 `json:"amount_discount"`
		Shipping int64 `json:"amount_shipping"`
	} `json:"total_details"`
	CustomerDetails struct {
		Name         string           `json:"name"`
		BusinessName string           `json:"business_name"`
		Email        string           `json:"email"`
		Address      payments.Address `json:"address"`
		TaxIDs       []payments.TaxID `json:"tax_ids"`
	} `json:"customer_details"`

	ID            string            `json:"id"`
	Mode          string            `json:"mode"`
	Status        string            `json:"status"`
	PaymentStatus string            `json:"payment_status"`
	Amount        int64             `json:"amount_total"`
	Currency      string            `json:"currency"`
	Live          bool              `json:"livemode"`
	Reference     string            `json:"client_reference_id"`
	Metadata      map[string]string `json:"metadata"`
	URL           string            `json:"url"`
	Intent        string            `json:"payment_intent"`
}

func validSession(s session, p payments.Payment) bool {
	amountOK := s.Amount == p.AmountCents
	if p.InvoiceMode == "local" {
		amountOK = p.Billing != nil && s.Amount == p.Billing.TotalCents && !s.AutomaticTax.Enabled && s.TotalDetails.Discount == 0 && s.TotalDetails.Shipping == 0 && s.TotalDetails.Tax == 0
	} else if p.TaxMode == "exclusive" {
		amountOK = s.AutomaticTax.Enabled && s.Subtotal == p.AmountCents && s.TotalDetails.Tax >= 0 && s.TotalDetails.Tax <= payments.MaxCents && s.Amount == p.AmountCents+s.TotalDetails.Tax && s.TotalDetails.Discount == 0 && s.TotalDetails.Shipping == 0 && s.Customer == p.CustomerID && strings.HasPrefix(p.CustomerID, "cus_")
		if s.PaymentStatus == "paid" {
			amountOK = amountOK && s.AutomaticTax.Status == "complete"
		} else if s.AutomaticTax.Status != "complete" {
			// Totals may be null until Checkout collects the billing address.
			amountOK = s.AutomaticTax.Enabled && (s.Subtotal == 0 || s.Subtotal == p.AmountCents) && (s.Amount == 0 || s.Amount == p.AmountCents+s.TotalDetails.Tax) && s.TotalDetails.Tax >= 0 && s.TotalDetails.Tax <= payments.MaxCents && s.TotalDetails.Discount == 0 && s.TotalDetails.Shipping == 0 && s.Customer == p.CustomerID && strings.HasPrefix(p.CustomerID, "cus_")
		}
	}
	return amountOK && strings.HasPrefix(s.ID, "cs_") && s.Mode == "payment" && s.Currency == "usd" && s.Live == p.Live && s.Reference == p.ID && s.Metadata["gateway_payment_id"] == p.ID && s.Metadata["organization_id"] == p.OrganizationID && s.Metadata["provider_id"] == p.ProviderID
}
func checkoutURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && u.Scheme == "https" && u.Host == "checkout.stripe.com" && u.User == nil
}
func (c *Client) Checkout(ctx context.Context, pr payments.Provider, credentials payments.Credentials, p payments.Payment, publicURL string) (payments.Snapshot, error) {
	expires := p.CheckoutStart().Add(24 * time.Hour)
	if p.ExpiresAt != nil && p.ExpiresAt.Before(expires) {
		expires = *p.ExpiresAt
	}
	if p.RequestExpired(c.now()) {
		return payments.Snapshot{}, payments.ErrExpired
	}
	if expires.Before(c.now().Add(31 * time.Minute)) {
		return payments.Snapshot{}, payments.ErrCheckoutWindow
	}
	v := url.Values{"adaptive_pricing[enabled]": {"false"}, "mode": {"payment"}, "payment_method_types[0]": {"card"}, "client_reference_id": {p.ID}, "line_items[0][price_data][currency]": {"usd"}, "line_items[0][price_data][unit_amount]": {strconv.FormatInt(p.AmountCents, 10)}, "line_items[0][price_data][product_data][name]": {"Sociaro organization balance"}, "line_items[0][quantity]": {"1"}, "success_url": {publicURL + "/organizations?payment=" + p.ID + "&organization=" + p.OrganizationID}, "cancel_url": {publicURL + "/organizations?payment=" + p.ID + "&organization=" + p.OrganizationID + "&cancelled=1"}, "expires_at": {strconv.FormatInt(expires.Unix(), 10)}}
	var billing *payments.Billing
	if p.InvoiceMode == "local" {
		if p.Billing == nil || p.VAT == nil {
			return payments.Snapshot{}, payments.ErrConflict
		}
		billing = p.Billing
		v.Set("invoice_creation[enabled]", "false")
		v.Set("automatic_tax[enabled]", "false")
		v.Set("line_items[0][price_data][product_data][name]", "Content generation balance top-up")
		// Collect the exact locally quoted gross total. This line is a payment
		// request; the provider-independent invoice carries the VAT breakdown.
		v.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(billing.TotalCents, 10))
		v.Set("customer_email", billing.Email)
		if billing.Email == "" {
			v.Del("customer_email")
		}
		v.Set("custom_text[submit][message]", checkoutBillingSummary(billing))
	} else if p.TaxMode == "exclusive" {
		billing = &payments.Billing{SubtotalCents: p.AmountCents, TotalCents: p.AmountCents}
		v.Set("customer", p.CustomerID)
		v.Set("customer_update[address]", "auto")
		v.Set("customer_update[name]", "auto")
		v.Set("billing_address_collection", "required")
		v.Set("name_collection[business][enabled]", "true")
		v.Set("name_collection[business][optional]", "false")
		v.Set("tax_id_collection[enabled]", "true")
		v.Set("automatic_tax[enabled]", "true")
		v.Set("line_items[0][price_data][tax_behavior]", "exclusive")
		v.Set("invoice_creation[enabled]", "true")
		v.Set("invoice_creation[invoice_data][metadata][gateway_payment_id]", p.ID)
		v.Set("invoice_creation[invoice_data][metadata][organization_id]", p.OrganizationID)
		if err := c.checkoutBilling(ctx, credentials, p, v, billing); err != nil {
			return payments.Snapshot{}, err
		}
	}
	if p.InvoiceMode == "local" || p.CheckoutAttempt > 0 {
		v.Set("metadata[gateway_checkout_attempt]", strconv.FormatInt(p.CheckoutAttempt, 10))
	}
	for k, value := range map[string]string{"gateway_payment_id": p.ID, "organization_id": p.OrganizationID, "provider_id": p.ProviderID} {
		v.Set("metadata["+k+"]", value)
		v.Set("payment_intent_data[metadata]["+k+"]", value)
	}
	var s session
	if e := c.request(ctx, credentials, "POST", "/v1/checkout/sessions", v, p.CheckoutKey(), &s); e != nil {
		return payments.Snapshot{}, e
	}
	if !validSession(s, p) || !checkoutURL(s.URL) {
		return payments.Snapshot{}, payments.ErrConflict
	}
	return payments.Snapshot{Billing: billing, CustomerID: p.CustomerID, Status: "pending", SessionID: s.ID, IntentID: s.Intent, CheckoutURL: s.URL}, nil
}

type intent struct {
	ID       string            `json:"id"`
	Currency string            `json:"currency"`
	Status   string            `json:"status"`
	Amount   int64             `json:"amount"`
	Received int64             `json:"amount_received"`
	Live     bool              `json:"livemode"`
	Metadata map[string]string `json:"metadata"`
	Charge   json.RawMessage   `json:"latest_charge"`
}

type balanceTransaction struct {
	ID           string   `json:"id"`
	Currency     string   `json:"currency"`
	Amount       int64    `json:"amount"`
	Fee          int64    `json:"fee"`
	Source       string   `json:"source"`
	ExchangeRate *float64 `json:"exchange_rate"`
}

// feeUSD expresses the original charge's actual fee in USD cents. Stripe's
// balance transaction amount and fee share settlement-currency minor units,
// which cancel in this ratio (including zero/three-decimal currencies). Using
// the settled gross amount preserves Stripe's currency rounding; do not treat
// an EUR fee as USD cents or use a current market exchange rate.
func (b balanceTransaction) feeUSD(chargeID string, chargedCents int64) (int64, error) {
	if b.ID == "" || b.Source != chargeID || chargedCents <= 0 || b.Amount <= 0 || b.Fee < 0 || b.Fee > b.Amount || len(b.Currency) != 3 {
		return 0, payments.ErrConflict
	}
	for _, c := range b.Currency {
		if c < 'a' || c > 'z' {
			return 0, payments.ErrConflict
		}
	}
	if b.Currency == "usd" {
		if b.Amount != chargedCents {
			return 0, payments.ErrConflict
		}
		return b.Fee, nil
	}
	if b.ExchangeRate == nil || *b.ExchangeRate <= 0 || math.IsNaN(*b.ExchangeRate) || math.IsInf(*b.ExchangeRate, 0) {
		return 0, payments.ErrConflict
	}
	// Integer arithmetic avoids floating-point rounding and overflow. Round to
	// the nearest USD cent, with exact half cents rounded up.
	numerator := new(big.Int).Mul(big.NewInt(b.Fee), big.NewInt(chargedCents))
	denominator := big.NewInt(b.Amount)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if remainder.Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64(), nil
}

func objectID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	var o struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &o)
	return o.ID
}
func (c *Client) Reconcile(ctx context.Context, pr payments.Provider, credentials payments.Credentials, p payments.Payment) (payments.Snapshot, error) {
	if p.SessionID == "" && p.CheckoutAttempt > 0 {
		id, err := c.recoverCheckout(ctx, credentials, p)
		if err != nil {
			return payments.Snapshot{}, err
		}
		if id == "" {
			return payments.Snapshot{CustomerID: p.CustomerID, Billing: p.Billing, Invoice: p.Invoice, Status: "expired"}, nil
		}
		p.SessionID = id
	}
	if p.SessionID == "" && p.IntentID != "" {
		var list struct {
			Data []session `json:"data"`
		}
		if e := c.request(ctx, credentials, "GET", "/v1/checkout/sessions", url.Values{"payment_intent": {p.IntentID}, "limit": {"2"}}, "", &list); e != nil {
			return payments.Snapshot{}, e
		}
		if len(list.Data) != 1 {
			return payments.Snapshot{}, payments.ErrConflict
		}
		p.SessionID = list.Data[0].ID
	}
	var s session
	if e := c.request(ctx, credentials, "GET", "/v1/checkout/sessions/"+url.PathEscape(p.SessionID), nil, "", &s); e != nil {
		return payments.Snapshot{}, e
	}
	if s.ID != p.SessionID || !validSession(s, p) {
		return payments.Snapshot{}, payments.ErrConflict
	}
	next := payments.Snapshot{CustomerID: p.CustomerID, Billing: p.Billing, Invoice: p.Invoice, Status: "pending", SessionID: s.ID, IntentID: s.Intent}
	if s.Status == "expired" {
		next.Status = "expired"
	}
	if s.Status == "complete" && s.PaymentStatus == "unpaid" {
		next.Status = "failed"
	}
	if s.URL != "" && checkoutURL(s.URL) {
		next.CheckoutURL = s.URL
	}
	if s.PaymentStatus != "paid" {
		return next, nil
	}
	if s.Status != "complete" || s.Intent == "" {
		return next, payments.ErrConflict
	}
	var pi intent
	if e := c.request(ctx, credentials, "GET", "/v1/payment_intents/"+url.PathEscape(s.Intent), url.Values{"expand[]": {"latest_charge.balance_transaction"}}, "", &pi); e != nil {
		return next, e
	}
	if pi.ID != s.Intent || pi.Status != "succeeded" || pi.Amount != s.Amount || pi.Received != s.Amount || pi.Currency != "usd" || pi.Live != p.Live || pi.Metadata["gateway_payment_id"] != p.ID || pi.Metadata["organization_id"] != p.OrganizationID || pi.Metadata["provider_id"] != pr.ID {
		return next, payments.ErrConflict
	}
	var ch struct {
		Balance  *balanceTransaction `json:"balance_transaction"`
		ID       string              `json:"id"`
		Amount   int64               `json:"amount"`
		Captured int64               `json:"amount_captured"`
		Refunded int64               `json:"amount_refunded"`
		Paid     bool                `json:"paid"`
		Currency string              `json:"currency"`
		Live     bool                `json:"livemode"`
		Receipt  string              `json:"receipt_url"`
		Intent   string              `json:"payment_intent"`
	}
	if json.Unmarshal(pi.Charge, &ch) != nil || ch.ID == "" || !ch.Paid || ch.Amount != s.Amount || ch.Captured != s.Amount || ch.Currency != "usd" || ch.Live != p.Live || ch.Intent != pi.ID {
		return next, payments.ErrConflict
	}
	if ch.Balance == nil {
		return next, payments.ErrSettlementPending
	}
	feeCents, e := ch.Balance.feeUSD(ch.ID, s.Amount)
	if e != nil {
		return next, e
	}
	next.FeeCents = feeCents
	next.Settlement = &payments.Settlement{Currency: ch.Balance.Currency, AmountMinor: ch.Balance.Amount, FeeMinor: ch.Balance.Fee, DecimalPlaces: settlementDecimals(ch.Balance.Currency)}
	var disputes struct {
		Data []struct {
			Amount   int64  `json:"amount"`
			Status   string `json:"status"`
			Currency string `json:"currency"`
			Intent   string `json:"payment_intent"`
		} `json:"data"`
		More bool `json:"has_more"`
	}
	if e := c.request(ctx, credentials, "GET", "/v1/disputes", url.Values{"payment_intent": {pi.ID}, "limit": {"100"}}, "", &disputes); e != nil {
		return next, e
	}
	if disputes.More {
		return next, payments.ErrUnavailable
	}
	for _, d := range disputes.Data {
		if d.Currency != "usd" || d.Intent != pi.ID || d.Amount < 0 || d.Amount > s.Amount {
			return next, payments.ErrConflict
		}
		if d.Status != "won" && d.Status != "warning_closed" {
			next.HeldCents += d.Amount
			if next.HeldCents > s.Amount {
				next.HeldCents = s.Amount
			}
		}
	}
	next.Paid = true
	next.Status = "paid"
	next.CheckoutURL = ""
	if ch.Refunded < 0 || ch.Refunded > s.Amount {
		return next, payments.ErrConflict
	}
	next.RefundedCents = ch.Refunded
	if p.InvoiceMode == "local" {
		if p.Billing == nil {
			return next, payments.ErrConflict
		}
		copyBill := *p.Billing
		next.Billing = &copyBill
		next.Billing.RefundedCents = ch.Refunded
		next.Billing.HeldCents = next.HeldCents
		next.RefundedCents = principalReduction(ch.Refunded, p.AmountCents, s.Amount)
		next.HeldCents = principalReduction(next.HeldCents, p.AmountCents, s.Amount)
	} else if p.TaxMode == "exclusive" {
		next.Billing = &payments.Billing{SubtotalCents: s.Subtotal, TaxCents: s.TotalDetails.Tax, TotalCents: s.Amount, RefundedCents: ch.Refunded, HeldCents: next.HeldCents, BusinessName: s.CustomerDetails.BusinessName, Email: s.CustomerDetails.Email, Address: s.CustomerDetails.Address, TaxIDs: s.CustomerDetails.TaxIDs}
		if p.Billing != nil {
			if next.Billing.BusinessName == "" {
				next.Billing.BusinessName = p.Billing.BusinessName
			}
			if p.Billing.Address.Country != "" {
				next.Billing.Address = p.Billing.Address
			}
			if len(next.Billing.TaxIDs) == 0 {
				next.Billing.TaxIDs = p.Billing.TaxIDs
			}
		}
		if next.Billing.BusinessName == "" {
			next.Billing.BusinessName = s.CustomerDetails.Name
		}
		next.RefundedCents = principalReduction(ch.Refunded, p.AmountCents, s.Amount)
		next.HeldCents = principalReduction(next.HeldCents, p.AmountCents, s.Amount)
		if next.Invoice == nil || next.Invoice.PDFURL == "" {
			next.Invoice = &payments.Invoice{ID: s.InvoiceID, Status: "pending"}
		}
	}
	if next.RefundedCents > 0 {
		next.Status = "partially_refunded"
	}
	if next.RefundedCents == p.AmountCents {
		next.Status = "refunded"
	}
	if next.HeldCents > 0 {
		next.Status = "disputed"
	}
	if u, e := url.Parse(ch.Receipt); e == nil && u.Scheme == "https" && u.User == nil && (u.Host == "pay.stripe.com" || u.Host == "receipt.stripe.com") {
		next.ReceiptURL = ch.Receipt
	}
	return next, nil
}
func verify(body []byte, header, secret string, now time.Time) bool {
	var stamp string
	var signatures []string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		if kv[0] == "t" {
			if stamp != "" {
				return false
			}
			stamp = kv[1]
		}
		if kv[0] == "v1" {
			signatures = append(signatures, kv[1])
		}
	}
	t, e := strconv.ParseInt(stamp, 10, 64)
	if e != nil || t < now.Add(-5*time.Minute).Unix() || t > now.Add(5*time.Minute).Unix() {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s.", stamp)
	mac.Write(body)
	expected := mac.Sum(nil)
	for _, sig := range signatures {
		b, e := hex.DecodeString(sig)
		if e == nil && hmac.Equal(b, expected) {
			return true
		}
	}
	return false
}
func (c *Client) Webhook(ctx context.Context, pr payments.Provider, credentials payments.Credentials, body []byte, signature string) (payments.Event, error) {
	empty := payments.Event{}
	if credentials.WebhookSecret == "" || !verify(body, signature, credentials.WebhookSecret, c.now()) {
		return empty, payments.ErrSignature
	}
	var ev struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Live    bool   `json:"livemode"`
		Account string `json:"account"`
		Data    struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &ev) != nil || ev.ID == "" {
		return empty, payments.ErrInvalid
	}
	if ev.Live != pr.Live || ev.Account != "" && ev.Account != pr.AccountID {
		return empty, payments.ErrConflict
	}
	var obj struct {
		ID       string            `json:"id"`
		Metadata map[string]string `json:"metadata"`
		Intent   json.RawMessage   `json:"payment_intent"`
	}
	if json.Unmarshal(ev.Data.Object, &obj) != nil {
		return empty, payments.ErrInvalid
	}
	out := payments.Event{}
	switch ev.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded", "checkout.session.async_payment_failed", "checkout.session.expired":
		out.PaymentID = obj.Metadata["gateway_payment_id"]
		out.SessionID = obj.ID
		out.IntentID = objectID(obj.Intent)
	case "charge.updated", "charge.refunded", "charge.dispute.created", "charge.dispute.updated", "charge.dispute.closed", "charge.dispute.funds_withdrawn", "charge.dispute.funds_reinstated", "refund.created", "refund.updated", "refund.failed", "payment_intent.succeeded":
		out.IntentID = objectID(obj.Intent)
		if ev.Type == "payment_intent.succeeded" {
			out.IntentID = obj.ID
		}
		if out.IntentID == "" {
			return empty, nil
		}
		var pi intent
		if e := c.request(ctx, credentials, "GET", "/v1/payment_intents/"+url.PathEscape(out.IntentID), nil, "", &pi); e != nil {
			return empty, e
		}
		out.PaymentID = pi.Metadata["gateway_payment_id"]
	default:
		return empty, nil
	}
	if out.PaymentID == "" {
		return empty, nil
	} // another product in the same merchant account
	if _, e := uuid.Parse(out.PaymentID); e != nil {
		return empty, payments.ErrInvalid
	}
	return out, nil
}

var _ payments.Adapter = (*Client)(nil)

// Stripe API representation includes two-decimal ISK and UGX compatibility.
// https://docs.stripe.com/currencies#special-cases
func settlementDecimals(currency string) int {
	switch currency {
	case "bif", "clp", "djf", "gnf", "jpy", "kmf", "krw", "mga", "pyg", "rwf", "vnd", "vuv", "xaf", "xof", "xpf":
		return 0
	case "bhd", "jod", "kwd", "omr", "tnd":
		return 3
	default:
		return 2
	}
}

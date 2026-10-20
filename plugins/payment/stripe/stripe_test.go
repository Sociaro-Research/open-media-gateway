package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
)

func signature(body []byte, secret string, at time.Time) string {
	stamp := fmt.Sprint(at.Unix())
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(stamp + "."))
	m.Write(body)
	return "t=" + stamp + ",v1=" + hex.EncodeToString(m.Sum(nil))
}
func TestSignatures(t *testing.T) {
	now := time.Now()
	body := []byte(`{"id":"evt_test"}`)
	secret := "whsec_test"
	good := signature(body, secret, now)
	for _, tc := range []struct {
		body   []byte
		header string
		want   bool
	}{{body, good, true}, {body, good + ",v1=00", true}, {[]byte(`{ "id":"evt_test"}`), good, false}, {body, signature(body, secret, now.Add(-6*time.Minute)), false}, {body, signature(body, secret, now.Add(6*time.Minute)), false}, {body, signature(body, "other", now), false}, {body, "t=broken,v1=123", false}, {body, good + ",t=" + fmt.Sprint(now.Unix()), false}} {
		if got := verify(tc.body, tc.header, secret, now); got != tc.want {
			t.Fatalf("signature got %v want %v", got, tc.want)
		}
	}
}
func TestCheckoutAndAuthoritativeReconciliation(t *testing.T) {
	ctx := context.Background()
	p := payments.Payment{ID: "11111111-1111-4111-8111-111111111111", OrganizationID: "22222222-2222-4222-8222-222222222222", ProviderID: "33333333-3333-4333-8333-333333333333", AmountCents: 5000, Currency: "usd", Live: true, FeeMode: "organization", CreatedAt: time.Now()}
	pr := payments.Provider{ID: p.ProviderID, AccountID: "acct_fixture", Live: true}
	cred := payments.Credentials{SecretKey: "sk_live_fixture", WebhookSecret: "whsec_fixture"}
	meta := map[string]string{"gateway_payment_id": p.ID, "organization_id": p.OrganizationID, "provider_id": p.ProviderID}
	s := session{ID: "cs_fixture", Mode: "payment", Status: "complete", PaymentStatus: "paid", Amount: 5000, Currency: "usd", Live: true, Reference: p.ID, Metadata: meta, URL: "https://checkout.stripe.com/c/pay/test", Intent: "pi_fixture"}
	fee := 500
	settlementCurrency := "usd"
	settlementAmount := 5000
	var exchangeRate *float64
	refunded := 0
	dispute := ""
	missingFee := false
	badCharge := false
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != cred.SecretKey || pass != "" || r.Header.Get("Stripe-Version") != APIVersion {
			t.Error("missing Stripe credentials/version")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/checkout/sessions":
			if r.Method == "POST" {
				posts++
				r.ParseForm()
				if r.Form.Get("line_items[0][price_data][unit_amount]") != "5000" || r.Form.Get("payment_method_types[0]") != "card" || r.Form.Get("metadata[gateway_payment_id]") != p.ID || r.Header.Get("Idempotency-Key") != "gateway-topup-"+p.ID {
					t.Error("unsafe checkout parameters")
				}
				json.NewEncoder(w).Encode(s)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"data": []session{s}})
			}
		case "/v1/checkout/sessions/cs_fixture":
			json.NewEncoder(w).Encode(s)
		case "/v1/payment_intents/pi_fixture":
			charge := map[string]any{"id": "ch_fixture", "amount": 5000, "amount_captured": 5000, "amount_refunded": refunded, "paid": true, "currency": "usd", "livemode": true, "receipt_url": "https://pay.stripe.com/receipts/test", "payment_intent": "pi_fixture"}
			if !missingFee {
				charge["balance_transaction"] = map[string]any{"id": "txn_fixture", "currency": settlementCurrency, "amount": settlementAmount, "fee": fee, "source": "ch_fixture", "exchange_rate": exchangeRate}
			}
			if badCharge {
				charge["amount_captured"] = 4000
			}
			pi := map[string]any{"id": "pi_fixture", "status": "succeeded", "amount": 5000, "amount_received": 5000, "currency": "usd", "livemode": true, "metadata": meta, "latest_charge": charge}
			json.NewEncoder(w).Encode(pi)
		case "/v1/disputes":
			if r.URL.Query().Get("payment_intent") != "pi_fixture" {
				t.Error("unscoped dispute query")
			}
			data := []any{}
			if dispute != "" {
				data = append(data, map[string]any{"amount": 5000, "status": dispute, "currency": "usd", "payment_intent": "pi_fixture"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := New()
	client.base = server.URL
	start, e := client.Checkout(ctx, pr, cred, p, "https://gateway.test")
	if e != nil || start.Paid || posts != 1 || start.SessionID != "cs_fixture" {
		t.Fatal(start, e)
	}
	p.SessionID = start.SessionID
	next, e := client.Reconcile(ctx, pr, cred, p)
	if e != nil || !next.Paid || next.FeeCents != 500 || next.CheckoutURL != "" {
		t.Fatal(next, e)
	}
	net, _ := payments.Net(p, next)
	if net != 4500 {
		t.Fatal(net)
	}
	// Reproduce a USD Checkout settled in EUR, including a delayed fee and
	// subsequent refund/dispute reconciliation in the original USD currency.
	settlementCurrency, settlementAmount, fee = "eur", 4330, 400
	rate := 0.865888
	exchangeRate = &rate
	for _, mode := range []string{"organization", "service"} {
		p.FeeMode = mode
		next, e = client.Reconcile(ctx, pr, cred, p)
		net, _ = payments.Net(p, next)
		want := int64(4538)
		if mode == "service" {
			want = 5000
		}
		if e != nil || !next.Paid || next.FeeCents != 462 || net != want || next.Settlement == nil || *next.Settlement != (payments.Settlement{Currency: "eur", AmountMinor: 4330, FeeMinor: 400, DecimalPlaces: 2}) {
			t.Fatalf("FX settlement %s: %+v, net=%d, err=%v", mode, next, net, e)
		}
	}
	p.FeeMode = "organization"
	refunded = 1000
	next, e = client.Reconcile(ctx, pr, cred, p)
	net, _ = payments.Net(p, next)
	if e != nil || next.Status != "partially_refunded" || net != 3538 {
		t.Fatal("FX refund", next, net, e)
	}
	dispute = "needs_response"
	next, e = client.Reconcile(ctx, pr, cred, p)
	net, _ = payments.Net(p, next)
	if e != nil || next.Status != "disputed" || net != 0 {
		t.Fatal("FX dispute", next, net, e)
	}
	dispute = "won"
	next, e = client.Reconcile(ctx, pr, cred, p)
	net, _ = payments.Net(p, next)
	if e != nil || net != 3538 {
		t.Fatal("FX dispute released", next, net, e)
	}
	settlementCurrency, settlementAmount, fee = "usd", 5000, 500
	exchangeRate, dispute = nil, ""
	refunded = 1000
	next, e = client.Reconcile(ctx, pr, cred, p)
	net, _ = payments.Net(p, next)
	if e != nil || net != 3500 || next.Status != "partially_refunded" {
		t.Fatal(next, e)
	}
	dispute = "needs_response"
	next, e = client.Reconcile(ctx, pr, cred, p)
	net, _ = payments.Net(p, next)
	if e != nil || net != 0 {
		t.Fatal(next, e)
	}
	dispute = "won"
	next, e = client.Reconcile(ctx, pr, cred, p)
	net, _ = payments.Net(p, next)
	if e != nil || net != 3500 {
		t.Fatal(next, e)
	}
	missingFee = true
	if _, e = client.Reconcile(ctx, pr, cred, p); !errors.Is(e, payments.ErrUnavailable) {
		t.Fatal("credited before actual fees", e)
	}
	missingFee = false
	badCharge = true
	if _, e = client.Reconcile(ctx, pr, cred, p); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("partial capture credited", e)
	}
	badCharge = false
	s.Currency = "eur"
	if _, e = client.Reconcile(ctx, pr, cred, p); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("currency mismatch", e)
	}
	s.Currency = "usd"
	s.Live = false
	if _, e = client.Reconcile(ctx, pr, cred, p); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("test live mismatch", e)
	}
	s.Live = true
	s.Metadata["organization_id"] = "other"
	if _, e = client.Reconcile(ctx, pr, cred, p); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("wrong org credited", e)
	}
	s.Metadata["organization_id"] = p.OrganizationID
	event := map[string]any{"id": "evt_fixture", "type": "charge.updated", "livemode": true, "data": map[string]any{"object": map[string]any{"id": "ch_fixture", "payment_intent": "pi_fixture"}}}
	body, _ := json.Marshal(event)
	parsed, e := client.Webhook(ctx, pr, cred, body, signature(body, cred.WebhookSecret, time.Now()))
	if e != nil || parsed.PaymentID != p.ID || parsed.IntentID != "pi_fixture" {
		t.Fatal(parsed, e)
	}
	if _, e = client.Webhook(ctx, pr, cred, body, strings.Repeat("x", 30)); !errors.Is(e, payments.ErrSignature) {
		t.Fatal(e)
	}
	event["livemode"] = false
	body, _ = json.Marshal(event)
	if _, e = client.Webhook(ctx, pr, cred, body, signature(body, cred.WebhookSecret, time.Now())); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("test webhook accepted", e)
	}
}

func TestSettlementFeeCurrency(t *testing.T) {
	rate := 0.865888
	for _, tc := range []struct {
		name, currency            string
		amount, fee, charge, want int64
	}{
		{"USD unchanged", "usd", 500, 40, 500, 40},
		{"live EUR regression", "eur", 433, 40, 500, 46},
		{"zero-decimal settlement", "jpy", 750, 50, 500, 33},
		{"three-decimal settlement", "kwd", 1500, 100, 500, 33},
		{"half cent rounds up", "eur", 400, 2, 500, 3},
		{"no fee", "eur", 433, 0, 500, 0},
		{"fee consumes payment", "eur", 433, 433, 500, 500},
		{"large amounts do not overflow", "eur", math.MaxInt64, math.MaxInt64, 1000000, 1000000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := balanceTransaction{ID: "txn_fixture", Source: "ch_fixture", Currency: tc.currency, Amount: tc.amount, Fee: tc.fee, ExchangeRate: &rate}
			got, err := b.feeUSD("ch_fixture", tc.charge)
			if err != nil || got != tc.want {
				t.Fatalf("fee=%d, err=%v, want=%d", got, err, tc.want)
			}
		})
	}
	for _, name := range []string{"wrong source", "missing transaction", "missing currency", "invalid currency", "zero amount", "negative amount", "negative fee", "excess fee", "USD amount mismatch", "missing rate", "zero rate", "negative rate", "infinite rate", "NaN rate"} {
		t.Run(name, func(t *testing.T) {
			r := rate
			b := balanceTransaction{ID: "txn_fixture", Source: "ch_fixture", Currency: "eur", Amount: 433, Fee: 40, ExchangeRate: &r}
			switch name {
			case "wrong source":
				b.Source = "ch_other"
			case "missing transaction":
				b.ID = ""
			case "missing currency":
				b.Currency = ""
			case "invalid currency":
				b.Currency = "123"
			case "zero amount":
				b.Amount = 0
			case "negative amount":
				b.Amount = -433
			case "negative fee":
				b.Fee = -1
			case "excess fee":
				b.Fee = 434
			case "USD amount mismatch":
				b.Currency = "usd"
			case "missing rate":
				b.ExchangeRate = nil
			case "zero rate":
				r = 0
			case "negative rate":
				r = -1
			case "infinite rate":
				r = math.Inf(1)
			case "NaN rate":
				r = math.NaN()
			}
			if _, err := b.feeUSD("ch_fixture", 500); !errors.Is(err, payments.ErrConflict) {
				t.Fatalf("invalid settlement accepted: %v", err)
			}
		})
	}
}

func TestSettlementDecimalPlaces(t *testing.T) {
	for currency, want := range map[string]int{"eur": 2, "usd": 2, "cad": 2, "jpy": 0, "kwd": 3, "isk": 2, "ugx": 2, "huf": 2, "twd": 2} {
		if got := settlementDecimals(currency); got != want {
			t.Fatalf("%s: got %d want %d", currency, got, want)
		}
	}
}

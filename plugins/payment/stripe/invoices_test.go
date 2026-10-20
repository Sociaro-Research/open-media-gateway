package stripe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
)

func TestBusinessCheckoutTaxAndInvoices(t *testing.T) {
	ctx := context.Background()
	p := payments.Payment{ID: "payment", OrganizationID: "organization", ProviderID: "provider", CustomerID: "cus_org", TaxMode: "exclusive", AmountCents: 5000, Currency: "usd", Live: true, FeeMode: "organization", CreatedAt: time.Now()}
	meta := map[string]string{"gateway_payment_id": p.ID, "organization_id": p.OrganizationID, "provider_id": p.ProviderID}
	s := session{ID: "cs_test", Customer: p.CustomerID, Mode: "payment", Status: "open", Currency: "usd", Live: true, Reference: p.ID, Metadata: meta, URL: "https://checkout.stripe.com/c/pay/test"}
	s.AutomaticTax.Enabled = true
	s.AutomaticTax.Status = "requires_location_inputs"
	invoiceFail := false
	invoiceURLValue := "https://pay.stripe.com/invoice/test/pdf"
	invoiceCalls := 0
	refund, hold := int64(0), int64(0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/customers":
			r.ParseForm()
			if r.Form.Get("metadata[organization_id]") != p.OrganizationID || r.Header.Get("Idempotency-Key") != "customer-key" {
				t.Error("customer identity missing")
			}
			json.NewEncoder(w).Encode(map[string]string{"id": "cus_org"})
		case "/v1/customers/cus_org":
			json.NewEncoder(w).Encode(map[string]any{"id": "cus_org", "livemode": true, "metadata": map[string]string{"organization_id": p.OrganizationID}})
		case "/v1/checkout/sessions":
			r.ParseForm()
			for key, want := range map[string]string{"customer": "cus_org", "billing_address_collection": "required", "name_collection[business][enabled]": "true", "name_collection[business][optional]": "false", "tax_id_collection[enabled]": "true", "automatic_tax[enabled]": "true", "line_items[0][price_data][tax_behavior]": "exclusive", "line_items[0][price_data][unit_amount]": "5000", "invoice_creation[enabled]": "true", "invoice_creation[invoice_data][metadata][gateway_payment_id]": p.ID, "customer_update[address]": "auto"} {
				if r.Form.Get(key) != want {
					t.Errorf("%s=%q, want %q", key, r.Form.Get(key), want)
				}
			}
			if r.Header.Get("Stripe-Version") != "2025-09-30.clover" {
				t.Error("business name API version")
			}
			json.NewEncoder(w).Encode(s)
		case "/v1/checkout/sessions/cs_test":
			json.NewEncoder(w).Encode(s)
		case "/v1/payment_intents/pi_test":
			json.NewEncoder(w).Encode(map[string]any{"id": "pi_test", "currency": "usd", "status": "succeeded", "amount": s.Amount, "amount_received": s.Amount, "livemode": true, "metadata": meta, "latest_charge": map[string]any{"id": "ch_test", "payment_intent": "pi_test", "currency": "usd", "amount": s.Amount, "amount_captured": s.Amount, "amount_refunded": refund, "paid": true, "livemode": true, "balance_transaction": map[string]any{"id": "txn_test", "source": "ch_test", "currency": "usd", "amount": s.Amount, "fee": 200}}})
		case "/v1/disputes":
			data := []any{}
			if hold > 0 {
				data = append(data, map[string]any{"amount": hold, "status": "needs_response", "currency": "usd", "payment_intent": "pi_test"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/invoices/in_test":
			invoiceCalls++
			if invoiceFail {
				w.WriteHeader(503)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"id": "in_test", "customer": p.CustomerID, "currency": "usd", "total": s.Amount, "livemode": true, "status": "paid", "number": "INV-100", "hosted_invoice_url": "https://invoice.stripe.com/i/test", "invoice_pdf": invoiceURLValue, "metadata": meta})
		default:
			t.Error("unexpected request", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c := &Client{http: server.Client(), base: server.URL, now: time.Now}
	pr := payments.Provider{ID: p.ProviderID}
	cred := payments.Credentials{}
	if id, err := c.CreateCustomer(ctx, cred, p.OrganizationID, "customer-key"); err != nil || id != p.CustomerID {
		t.Fatal(id, err)
	}
	snap, err := c.Checkout(ctx, pr, cred, p, "https://gateway.test")
	if err != nil {
		t.Fatal("Checkout with totals pending address", err)
	}
	if _, err = payments.Net(p, snap); err != nil {
		t.Fatal("pending billing contract", err)
	}
	p.Billing = snap.Billing
	p.SessionID = snap.SessionID
	s.Status = "complete"
	s.PaymentStatus = "paid"
	s.Intent = "pi_test"
	s.InvoiceID = "in_test"
	s.Subtotal = 5000
	s.TotalDetails.Tax = 1000
	s.Amount = 6000
	s.AutomaticTax.Status = "complete"
	s.CustomerDetails.BusinessName = "Buyer Ltd"
	s.CustomerDetails.Address.Country = "CY"
	s.CustomerDetails.TaxIDs = []payments.TaxID{{Type: "eu_vat", Value: "CY12345678X"}}
	snap, err = c.Reconcile(ctx, pr, cred, p)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Billing.TotalCents != 6000 || snap.Billing.TaxCents != 1000 || snap.Billing.BusinessName != "Buyer Ltd" || invoiceCalls != 0 {
		t.Fatal("billing or independent document collection", snap)
	}
	// Link may return the cardholder's name/address; the saved company snapshot wins.
	p.Billing = &payments.Billing{BusinessName: "Saved Company Ltd", Address: payments.Address{Line1: "Company street", Country: "CY"}, TaxIDs: []payments.TaxID{{Type: "eu_vat", Value: "CY12345678X"}}}
	s.CustomerDetails.BusinessName = ""
	s.CustomerDetails.Name = "Personal card holder"
	s.CustomerDetails.Address.Country = "FR"
	s.CustomerDetails.TaxIDs = nil
	repeated, err := c.Reconcile(ctx, pr, cred, p)
	if err != nil || repeated.Billing.BusinessName != "Saved Company Ltd" || repeated.Billing.Address.Country != "CY" || len(repeated.Billing.TaxIDs) != 1 {
		t.Fatal("company identity replaced by cardholder", repeated, err)
	}
	s.CustomerDetails.BusinessName = "Buyer Ltd"
	s.CustomerDetails.Address.Country = "CY"
	p.Billing = snap.Billing
	for mode, want := range map[string]int64{"organization": 4800, "service": 5000} {
		p.FeeMode = mode
		net, e := payments.Net(p, snap)
		if e != nil || net != want {
			t.Fatal(mode, net, e)
		}
	}
	p.FeeMode = "organization"
	p.Billing = snap.Billing
	p.Invoice = snap.Invoice
	doc, err := c.FetchInvoice(ctx, cred, p)
	if err != nil || doc.Number != "INV-100" || doc.PDFURL == "" {
		t.Fatal(doc, err)
	}
	invoiceURLValue = "https://attacker.test/invoice.pdf"
	if _, err = c.FetchInvoice(ctx, cred, p); err == nil {
		t.Fatal("unsafe invoice link accepted")
	}
	invoiceFail = true
	snap, err = c.Reconcile(ctx, pr, cred, p)
	if err != nil || !snap.Paid {
		t.Fatal("invoice outage blocked funding", err)
	}
	if _, err = c.FetchInvoice(ctx, cred, p); err == nil {
		t.Fatal("invoice outage not retryable")
	}
	for _, tc := range []struct{ refund, hold, want int64 }{{1200, 0, 3800}, {6000, 0, 0}, {0, 6000, 0}, {1, 0, 4799}} {
		refund, hold = tc.refund, tc.hold
		snap, err = c.Reconcile(ctx, pr, cred, p)
		if err != nil {
			t.Fatal(err)
		}
		net, e := payments.Net(p, snap)
		if e != nil || net != tc.want {
			t.Fatal(tc, net, e)
		}
	}
	refund, hold = 0, 0
	// Reverse charge/zero tax remains a valid payment without changing principal.
	s.TotalDetails.Tax = 0
	s.Amount = 5000
	snap, err = c.Reconcile(ctx, pr, cred, p)
	if err != nil || snap.Billing.TaxCents != 0 {
		t.Fatal("zero tax", err)
	}
	s.Amount = 6000 // Unexplained excess must never be accepted as tax.
	if _, err = c.Reconcile(ctx, pr, cred, p); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("mismatched total", err)
	}
	s.Amount = 5000
	s.Customer = "cus_other"
	if _, err = c.Reconcile(ctx, pr, cred, p); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("cross-organization customer", err)
	}
	s.Customer = p.CustomerID
	s.AutomaticTax.Status = "failed"
	if _, err = c.Reconcile(ctx, pr, cred, p); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("incomplete tax calculation", err)
	}
}

func TestCheckoutReusesCompanyBilling(t *testing.T) {
	for _, tc := range []struct {
		name, business, legacy               string
		taxID, address, foreign, unavailable bool
	}{
		{name: "new"}, {name: "returning", business: "Buyer Ltd", address: true, taxID: true},
		{name: "legacy tax company", legacy: "Buyer Ltd", taxID: true, address: true},
		{name: "personal name is not a company", legacy: "Card Holder", address: true},
		{name: "missing address", business: "Buyer Ltd"},
		{name: "foreign customer", business: "Other Ltd", foreign: true},
		{name: "lookup failure", unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.unavailable {
					w.WriteHeader(503)
					return
				}
				org := "org"
				if tc.foreign {
					org = "other"
				}
				customer := map[string]any{"id": "cus_org", "livemode": true, "metadata": map[string]string{"organization_id": org}, "business_name": tc.business, "name": tc.legacy}
				if tc.address {
					customer["address"] = payments.Address{Line1: "Company street", City: "Nicosia", Country: "CY"}
				}
				if tc.taxID {
					customer["tax_ids"] = map[string]any{"data": []payments.TaxID{{Type: "eu_vat", Value: "CY12345678X"}}}
				}
				json.NewEncoder(w).Encode(customer)
			}))
			defer srv.Close()
			c := &Client{http: srv.Client(), base: srv.URL, now: time.Now}
			v := url.Values{"name_collection[business][enabled]": {"true"}, "name_collection[business][optional]": {"false"}, "customer_update[name]": {"auto"}, "customer_update[address]": {"auto"}, "billing_address_collection": {"required"}}
			err := c.checkoutBilling(context.Background(), payments.Credentials{}, payments.Payment{CustomerID: "cus_org", OrganizationID: "org", Live: true}, v, &payments.Billing{})
			if tc.foreign || tc.unavailable {
				if err == nil {
					t.Fatal("unsafe customer accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			summary := v.Get("custom_text[submit][message]")
			if tc.business != "" && !strings.Contains(summary, tc.business) {
				t.Fatal("saved company not shown at checkout")
			}
			if tc.business == "" && !tc.taxID && summary != "" {
				t.Fatal("personal payer shown as company")
			}
			known := tc.business != "" || (tc.legacy != "" && tc.taxID)
			if (v.Get("name_collection[business][enabled]") == "") != known {
				t.Fatal("incorrect name collection", v)
			}
			if known && tc.taxID && v.Get("customer_update[name]") != "never" {
				t.Fatal("company name can be overwritten")
			}
			if known && tc.taxID && v.Get("tax_id_collection[enabled]") != "false" {
				t.Fatal("saved tax IDs requested again")
			}
			if v.Get("tax_id_collection[enabled]") != "false" && v.Get("customer_update[name]") != "auto" {
				t.Fatal("Stripe rejects tax collection without name updates")
			}
			if tc.address && (v.Get("billing_address_collection") != "auto" || v.Get("customer_update[address]") != "never") {
				t.Fatal("company address not reused", v)
			}
			if !tc.address && v.Get("billing_address_collection") != "required" {
				t.Fatal("missing address not collected")
			}
		})
	}
}

func TestCheckoutBillingSummary(t *testing.T) {
	b := &payments.Billing{BusinessName: "Sunday Games LTD", Address: payments.Address{Line1: "Company street 7", City: "Erimi", PostalCode: "4630", Country: "CY"}, TaxIDs: []payments.TaxID{{Type: "eu_vat", Value: "CY12345678X"}}}
	got := checkoutBillingSummary(b)
	want := "Billing to: Sunday Games LTD\nAddress: Company street 7, Erimi, 4630, CY\nTax ID: CY12345678X"
	if got != want {
		t.Fatalf("summary=%q", got)
	}
	b.BusinessName = strings.Repeat("É", 600)
	b.Address.Line1 = strings.Repeat("X", 600)
	b.TaxIDs[0].Value = strings.Repeat("9", 600)
	if len([]rune(checkoutBillingSummary(b))) > 500 {
		t.Fatal("summary exceeds Stripe custom text limit")
	}
}

func TestPaymentOnlyCheckoutWithoutStripeTaxOrInvoices(t *testing.T) {
	p := payments.Payment{ID: "local", OrganizationID: "org", ProviderID: "provider", AmountCents: 5000, InvoiceMode: "local", TaxMode: "exclusive", Currency: "usd", Live: true, CreatedAt: time.Now(), VAT: &payments.VATSnapshot{}, Billing: &payments.Billing{SubtotalCents: 5000, TaxCents: 1000, TotalCents: 6000, BusinessName: "Local Buyer Ltd", Address: payments.Address{Line1: "1 Buyer Street", Country: "GB"}}}
	s := session{ID: "cs_local", Mode: "payment", Currency: "usd", Amount: 6000, Live: true, Reference: p.ID, Metadata: map[string]string{"gateway_payment_id": p.ID, "organization_id": p.OrganizationID, "provider_id": p.ProviderID}, URL: "https://checkout.stripe.com/c/pay/cs_local"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/checkout/sessions" {
			t.Error("payment-only used a customer, invoice or tax service", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		r.ParseForm()
		for k, want := range map[string]string{"invoice_creation[enabled]": "false", "automatic_tax[enabled]": "false", "line_items[0][price_data][unit_amount]": "6000"} {
			if r.Form.Get(k) != want {
				t.Error(k, r.Form.Get(k), want)
			}
		}
		if r.Form.Get("customer") != "" || r.Form.Get("tax_id_collection[enabled]") != "" {
			t.Error("unexpected customer/tax collection")
		}
		json.NewEncoder(w).Encode(s)
	}))
	defer server.Close()
	client := New()
	client.base = server.URL
	next, e := client.Checkout(context.Background(), payments.Provider{ID: p.ProviderID}, payments.Credentials{}, p, "https://gateway.test")
	if e != nil || next.Billing.TaxCents != 1000 || next.SessionID != "cs_local" {
		t.Fatal(next, e)
	}
	if !validSession(s, p) {
		t.Fatal("exact local quote rejected")
	}
	s.Amount = 5000
	if validSession(s, p) {
		t.Fatal("underpayment accepted")
	}
	s.Amount = 6000
	s.AutomaticTax.Enabled = true
	if validSession(s, p) {
		t.Fatal("unexpected provider tax accepted")
	}
}

func TestRecoverRenewedCheckoutBeforeAnotherAttempt(t *testing.T) {
	ctx := context.Background()
	start := time.Now().Add(-25 * time.Hour)
	p := payments.Payment{ID: "local", OrganizationID: "org", ProviderID: "provider", InvoiceMode: "local", CheckoutAttempt: 2, CheckoutStartedAt: &start, AmountCents: 5000, Billing: &payments.Billing{SubtotalCents: 5000, TaxCents: 1000, TotalCents: 6000}, Currency: "usd", Live: true}
	candidate := session{ID: "cs_recovered", Mode: "payment", Status: "expired", Amount: 6000, Currency: "usd", Live: true, Reference: p.ID, Metadata: map[string]string{"gateway_payment_id": p.ID, "organization_id": p.OrganizationID, "provider_id": p.ProviderID, "gateway_checkout_attempt": "2"}}
	empty, duplicate, bad := false, false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("recovery created another Checkout")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/v1/checkout/sessions" {
			if r.URL.Query().Get("created[gte]") == "" || r.URL.Query().Get("created[lte]") == "" {
				t.Error("unbounded recovery")
			}
			list := []session{}
			if !empty {
				s := candidate
				if bad {
					s.Amount = 5000
				}
				list = append(list, s)
				if duplicate {
					list = append(list, s)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": list, "has_more": false})
			return
		}
		if r.URL.Path == "/v1/checkout/sessions/cs_recovered" {
			json.NewEncoder(w).Encode(candidate)
			return
		}
		t.Error("unexpected lookup", r.URL.Path)
		w.WriteHeader(500)
	}))
	defer server.Close()
	client := New()
	client.base = server.URL
	s, err := client.Reconcile(ctx, payments.Provider{}, payments.Credentials{}, p)
	if err != nil || s.SessionID != candidate.ID || s.Status != "expired" {
		t.Fatal(s, err)
	}
	empty = true
	s, err = client.Reconcile(ctx, payments.Provider{}, payments.Credentials{}, p)
	if err != nil || s.SessionID != "" || s.Status != "expired" {
		t.Fatal("absent unsubmitted attempt", s, err)
	}
	empty = false
	duplicate = true
	if _, err = client.Reconcile(ctx, payments.Provider{}, payments.Credentials{}, p); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("ambiguous attempt recovered", err)
	}
	duplicate = false
	bad = true
	if _, err = client.Reconcile(ctx, payments.Provider{}, payments.Credentials{}, p); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("inconsistent attempt accepted", err)
	}
}

func TestCheckoutNeverOutlivesPaymentDeadline(t *testing.T) {
	now := time.Now()
	deadline := now.Add(2 * time.Hour)
	p := payments.Payment{ID: "local", OrganizationID: "org", ProviderID: "pr", CreatedAt: now, ExpiresAt: &deadline, AmountCents: 5000, Currency: "usd", Live: true, InvoiceMode: "local", VAT: &payments.VATSnapshot{}, Billing: &payments.Billing{SubtotalCents: 5000, TotalCents: 5000}}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		r.ParseForm()
		if r.Form.Get("expires_at") != fmt.Sprint(deadline.Unix()) {
			t.Error("Checkout exceeds request deadline", r.Form.Get("expires_at"))
		}
		json.NewEncoder(w).Encode(session{ID: "cs_deadline", Mode: "payment", Amount: 5000, Currency: "usd", Live: true, Reference: p.ID, Metadata: map[string]string{"gateway_payment_id": p.ID, "organization_id": p.OrganizationID, "provider_id": p.ProviderID}, URL: "https://checkout.stripe.com/c/pay/cs_deadline"})
	}))
	defer server.Close()
	client := New()
	client.base = server.URL
	if _, err := client.Checkout(context.Background(), payments.Provider{}, payments.Credentials{}, p, "https://gateway.test"); err != nil {
		t.Fatal(err)
	}
	deadline = now.Add(10 * time.Minute)
	if _, err := client.Checkout(context.Background(), payments.Provider{}, payments.Credentials{}, p, "https://gateway.test"); !errors.Is(err, payments.ErrCheckoutWindow) {
		t.Fatal("invalid short Checkout window", err)
	}
	deadline = now.Add(-time.Minute)
	if _, err := client.Checkout(context.Background(), payments.Provider{}, payments.Credentials{}, p, "https://gateway.test"); !errors.Is(err, payments.ErrExpired) {
		t.Fatal("expired request created Checkout", err)
	}
	if requests != 1 {
		t.Fatal("expired/window request reached provider", requests)
	}
}

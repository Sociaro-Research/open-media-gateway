package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

type localCardAdapter struct{ invoicingAdapter }

func (a *localCardAdapter) Checkout(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment, _ string) (payments.Snapshot, error) {
	return payments.Snapshot{Billing: p.Billing, Invoice: p.Invoice, CustomerID: p.CustomerID, Status: "pending", SessionID: "cs_" + p.ID, CheckoutURL: "https://checkout.stripe.com/c/pay/cs_" + p.ID}, nil
}
func (a *localCardAdapter) Reconcile(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment) (payments.Snapshot, error) {
	return payments.Snapshot{Billing: p.Billing, Invoice: p.Invoice, CustomerID: p.CustomerID, Status: "paid", SessionID: p.SessionID, IntentID: "pi_" + p.ID, Paid: true, FeeCents: 213}, nil
}
func TestLocalInvoiceIssuanceAndCheckoutMode(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	org, e := repo.CreateOrganization(ctx, admin, "Invoice buyer", owner, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	policy, e := repo.SaveVATSettings(ctx, admin, payments.VATSettings{Version: 1, Seller: payments.SellerCompany{LegalName: "Configured Seller Ltd", Address: payments.Address{Line1: "1 Seller Street", City: "London", Country: "GB"}, VATRegistered: true, VATNumber: "GB123456789"}, Rules: []payments.VATRule{{Country: "*", Status: "any", Treatment: "standard", Rate: 2000}}})
	if e != nil {
		t.Fatal(e)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("x", 32))
	adapter := &localCardAdapter{}
	m := payments.New(repo, cipher, "https://gateway.test", map[string]payments.Adapter{"stripe": adapter})
	off := false
	pr, e := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stripe", Name: "Stripe", Enabled: true, SecretKey: "sk_live_fixture", WebhookSecret: "whsec_fixture", FeeMode: "organization", StripeInvoices: &off})
	if e != nil || pr.StripeInvoices {
		t.Fatal("switch not saved", e)
	}
	profile := payments.BillingProfile{BusinessName: "Buyer Ltd", Address: payments.Address{Line1: "1 Buyer Street", City: "Nicosia", Country: "CY"}}
	p, e := m.CheckoutBilling(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "local-card", "organization", profile)
	if e != nil || p.InvoiceMode != "local" || p.CustomerID != "" || p.VAT == nil || p.Billing.TaxCents != 1000 {
		t.Fatal("local checkout", p, e)
	}
	// Concurrent first downloads issue exactly one PDF/number.
	var wg sync.WaitGroup
	results := make(chan payments.InvoiceDocument, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); d, e := m.InvoicePDF(ctx, p.ID); results <- d; errs <- e }()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first payments.InvoiceDocument
	for d := range results {
		if first.Number == "" {
			first = d
		}
		if d.Number != first.Number || !bytes.Equal(d.PDF, first.PDF) || !d.CreatedAt.Equal(first.CreatedAt) {
			t.Fatalf("invoice reissued on retry: number=%s/%s PDF equal=%t issue date=%s/%s", d.Number, first.Number, bytes.Equal(d.PDF, first.PDF), d.CreatedAt.Format(time.RFC3339Nano), first.CreatedAt.Format(time.RFC3339Nano))
		}
	}
	policy.Seller.LegalName = "New Seller Ltd"
	policy.Rules[0].Rate = 2100
	if _, e = repo.SaveVATSettings(ctx, admin, policy); e != nil {
		t.Fatal(e)
	}
	on := true
	pr, e = m.Save(ctx, admin, pr.ID, payments.ProviderUpdate{Kind: "stripe", Name: pr.Name, Enabled: true, FeeMode: pr.FeeMode, Version: pr.Version, StripeInvoices: &on})
	if e != nil || !pr.StripeInvoices {
		t.Fatal(e)
	}
	retried, e := m.CheckoutBilling(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "local-card", "organization", profile)
	if e != nil || retried.ID != p.ID || retried.InvoiceMode != "local" || retried.Billing.TaxCents != 1000 {
		t.Fatal("toggle changed old payment", retried, e)
	}
	different := profile
	different.BusinessName = "Different Buyer"
	if _, e = m.CheckoutBilling(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "local-card", "organization", different); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("changed identity accepted", e)
	}
	p, e = m.Reconcile(ctx, p.ID)
	if e != nil || p.CreditedCents != 4787 || p.Billing.TaxCents != 1000 {
		t.Fatal("local settlement changed", p, e)
	}
	d, e := m.InvoicePDF(ctx, p.ID)
	if e != nil || d.Number != first.Number || !bytes.Equal(d.PDF, first.PDF) {
		t.Fatal("issued PDF changed after settlement", e)
	}
	var count int
	if e = db.QueryRow(`SELECT count(*) FROM payment_invoice_documents WHERE payment_id=$1`, p.ID).Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
	// Native invoices remain available for later payments without local documents.
	native, e := repo.BeginPayment(ctx, owner, org.ID, pr.ID, 5000, "native", "organization")
	if e != nil || native.InvoiceMode != "stripe" {
		t.Fatal(native, e)
	}
	if _, e = m.InvoicePDF(ctx, native.ID); !errors.Is(e, payments.ErrUnavailable) {
		t.Fatal("local invoice for native payment", e)
	}
}

type renewingCardAdapter struct {
	localCardAdapter
	mu           sync.Mutex
	sessions     map[string]payments.Snapshot
	starts       map[string]time.Time
	expired      bool
	paid         bool
	failCreation bool
	failRead     bool
	oldSession   string
}

func (a *renewingCardAdapter) Checkout(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment, _ string) (payments.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := p.CheckoutKey()
	if a.sessions == nil {
		a.sessions = map[string]payments.Snapshot{}
		a.starts = map[string]time.Time{}
	}
	s, ok := a.sessions[key]
	if !ok {
		s = payments.Snapshot{Billing: p.Billing, Invoice: p.Invoice, CustomerID: p.CustomerID, Status: "pending", SessionID: fmt.Sprintf("cs_%s_%d", p.ID, p.CheckoutAttempt), CheckoutURL: "https://checkout.stripe.com/c/pay/" + key}
		a.sessions[key] = s
		a.starts[key] = p.CheckoutStart()
	} else if !a.starts[key].Equal(p.CheckoutStart()) {
		return s, errors.New("retry changed expires_at")
	}
	if a.failCreation {
		a.failCreation = false
		return payments.Snapshot{}, payments.ErrUnavailable
	}
	return s, nil
}
func (a *renewingCardAdapter) Reconcile(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment) (payments.Snapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failRead {
		return payments.Snapshot{}, payments.ErrUnavailable
	}
	s := payments.Snapshot{Billing: p.Billing, Invoice: p.Invoice, CustomerID: p.CustomerID, Status: "pending", SessionID: p.SessionID, IntentID: p.IntentID, CheckoutURL: p.CheckoutURL}
	if p.CheckoutAttempt == 0 && a.expired {
		s.Status = "expired"
		s.CheckoutURL = ""
	} else if a.paid {
		s.Status = "paid"
		s.Paid = true
		s.IntentID = "pi_current"
		s.FeeCents = 213
		s.CheckoutURL = ""
	}
	return s, nil
}
func (a *renewingCardAdapter) Webhook(_ context.Context, _ payments.Provider, _ payments.Credentials, _ []byte, _ string) (payments.Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return payments.Event{PaymentID: a.oldSession, SessionID: "cs_" + a.oldSession + "_0"}, nil
}

func TestInvoiceCheckoutRenewalKeepsInvoiceAndCredit(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	org, err := repo.CreateOrganization(ctx, admin, "Invoice renewal buyer", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.SaveVATSettings(ctx, admin, payments.VATSettings{Version: 1, Seller: payments.SellerCompany{LegalName: "Seller Ltd", Address: payments.Address{Line1: "1 Seller Street", City: "London", Country: "GB"}, VATRegistered: true, VATNumber: "GB123456789"}, Rules: []payments.VATRule{{Country: "*", Status: "any", Treatment: "standard", Rate: 2000}}})
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("x", 32))
	adapter := &renewingCardAdapter{}
	m := payments.New(repo, cipher, "https://gateway.test", map[string]payments.Adapter{"stripe": adapter})
	off := false
	pr, err := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stripe", Name: "Stripe", Enabled: true, SecretKey: "sk_live_fixture", WebhookSecret: "whsec_fixture", FeeMode: "organization", StripeInvoices: &off})
	if err != nil {
		t.Fatal(err)
	}
	profile := payments.BillingProfile{BusinessName: "Buyer Ltd", Address: payments.Address{Line1: "1 Buyer Street", City: "Nicosia", Country: "CY"}}
	p, err := m.CheckoutBilling(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "invoice-renewal", "organization", profile)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := m.InvoicePDF(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A still-open session is reused; provider read failure cannot create a second.
	same, err := m.ResumeInvoiceCheckout(ctx, p.ID)
	if err != nil || same.SessionID != p.SessionID || same.CheckoutAttempt != 0 {
		t.Fatal(same, err)
	}
	adapter.failRead = true
	if _, err = m.ResumeInvoiceCheckout(ctx, p.ID); !errors.Is(err, payments.ErrUnavailable) {
		t.Fatal("unverified old checkout renewed", err)
	}
	adapter.failRead = false
	if _, err = db.Exec(`UPDATE payments SET created_at=NOW()-INTERVAL '20 days' WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	// The provider created the new session, but its response was lost. The attempt
	// reservation must already be durable before that call is retried.
	adapter.expired = true
	adapter.failCreation = true
	if _, err = m.ResumeInvoiceCheckout(ctx, p.ID); !errors.Is(err, payments.ErrUnavailable) {
		t.Fatal("expected simulated response loss", err)
	}
	reserved, err := repo.Payment(ctx, p.ID)
	if err != nil || reserved.CheckoutAttempt != 1 || reserved.CheckoutStartedAt == nil || reserved.SessionID != "" {
		t.Fatal("reservation not durable", reserved, err)
	}
	adapter.oldSession = p.ID
	if err = m.Webhook(ctx, pr.ID, nil, ""); err != nil {
		t.Fatal("old event attached to pending renewal", err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, e := m.ResumeInvoiceCheckout(ctx, p.ID)
			if e == nil && (next.ID != p.ID || next.CheckoutAttempt != 1 || next.SessionID == p.SessionID || next.Billing.TotalCents != 6000) {
				e = fmt.Errorf("changed invoice payment: %+v", next)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if len(adapter.sessions) != 2 {
		t.Fatal("duplicate Checkout creations", len(adapter.sessions))
	}
	if err = m.Webhook(ctx, pr.ID, nil, ""); err != nil {
		t.Fatal("stale expiry event retried forever", err)
	}
	unchanged, err := m.InvoicePDF(ctx, p.ID)
	if err != nil || unchanged.Number != doc.Number || !bytes.Equal(unchanged.PDF, doc.PDF) {
		t.Fatal("renewal reissued invoice", err)
	}
	adapter.paid = true
	for range 2 {
		paid, e := m.ResumeInvoiceCheckout(ctx, p.ID)
		if e != nil || !paid.Settled || paid.CreditedCents != 4787 || paid.ID != p.ID {
			t.Fatal(paid, e)
		}
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM organization_credits WHERE payment_id=$1`, p.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate credit", count, err)
	}
	if len(adapter.sessions) != 2 {
		t.Fatal("paid invoice created another checkout")
	}
}

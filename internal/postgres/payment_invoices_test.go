package postgres

import (
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/identity"
	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
)

type invoicingAdapter struct {
	paymentAdapter
	db           *sql.DB
	failInvoice  atomic.Bool
	sawCommitted atomic.Bool
	customers    atomic.Int32
}

func (a *invoicingAdapter) CreateCustomer(ctx context.Context, c payments.Credentials, org, key string) (string, error) {
	a.customers.Add(1)
	return "cus_" + org, nil
}
func (a *invoicingAdapter) Reconcile(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment) (payments.Snapshot, error) {
	return payments.Snapshot{CustomerID: p.CustomerID, SessionID: p.SessionID, IntentID: "pi_" + p.ID, Status: "paid", Paid: true, FeeCents: 200, Billing: &payments.Billing{SubtotalCents: 5000, TaxCents: 1000, TotalCents: 6000, BusinessName: "Buyer Ltd"}, Invoice: &payments.Invoice{ID: "in_" + p.ID, Status: "pending"}}, nil
}
func (a *invoicingAdapter) FetchInvoice(ctx context.Context, _ payments.Credentials, p payments.Payment) (*payments.Invoice, error) {
	var count int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM organization_credits WHERE payment_id=$1`, p.ID).Scan(&count); err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, errors.New("funding did not commit before invoice request")
	}
	a.sawCommitted.Store(true)
	if a.failInvoice.Load() {
		return nil, payments.ErrUnavailable
	}
	return &payments.Invoice{ID: "in_" + p.ID, Number: "INV-1", Status: "paid", URL: "https://invoice.stripe.com/i/test", PDFURL: "https://pay.stripe.com/invoice/test/pdf"}, nil
}
func TestPaymentInvoicesCommitAndCustomerIsolation(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	org, err := repo.CreateOrganization(ctx, admin, "Tax buyer", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateOrganization(ctx, admin, "Other tax buyer", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("x", 32))
	adapter := &invoicingAdapter{db: db}
	adapter.failInvoice.Store(true)
	m := payments.New(repo, cipher, "https://gateway.test", map[string]payments.Adapter{"stripe": adapter})
	pr, err := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stripe", Name: "Stripe", Enabled: true, SecretKey: "sk_live_fixture", WebhookSecret: "whsec_fixture", FeeMode: "organization"})
	if err != nil {
		t.Fatal(err)
	}
	// A single connection detects accidental nested transactions during customer setup.
	db.SetMaxOpenConns(1)
	checkoutCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	p, err := m.Checkout(checkoutCtx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "taxed", "organization")
	if err != nil {
		t.Fatal(err)
	}
	if p.TaxMode != "exclusive" || p.CustomerID == "" {
		t.Fatal("new purchase contract not persisted", p)
	}
	db.SetMaxOpenConns(10)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := m.Reconcile(identity.WithUser(ctx, identity.User{ID: owner}), p.ID)
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
	var automatic, misattributed int
	if err = db.QueryRow(`SELECT COUNT(*),COUNT(*) FILTER(WHERE actor_type<>'service') FROM audit_events WHERE organization_id=$1 AND (action IN ('credit','payment_settlement') OR action='settings' AND details->'after' ? 'billing_profile')`, org.ID).Scan(&automatic, &misattributed); err != nil || automatic == 0 || misattributed != 0 {
		t.Fatal("automatic reconciliation attributed to the refreshing user", automatic, misattributed, err)
	}
	p, err = repo.Payment(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !adapter.sawCommitted.Load() || p.CreditedCents != 4800 || p.Billing.TaxCents != 1000 || p.Invoice.Status != "pending" {
		t.Fatal("invoice failure lost funding", p)
	}
	var profileName string
	if err = db.QueryRow(`SELECT billing_profile->>'business_name' FROM organizations WHERE id=$1`, org.ID).Scan(&profileName); err != nil || profileName != "Buyer Ltd" {
		t.Fatal("company profile not saved", profileName, err)
	}
	listed, err := repo.Organizations(ctx, owner, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range listed {
		if o.ID == org.ID {
			found = o.BillingProfile != nil && o.BillingProfile.BusinessName == "Buyer Ltd"
		}
	}
	if !found {
		t.Fatal("company profile missing from organization API")
	}
	// A replay of an old payment must not replace a subsequently updated profile.
	if _, err = db.Exec(`UPDATE organizations SET billing_profile=jsonb_set(billing_profile,'{business_name}','"New company name"') WHERE id=$1`, org.ID); err != nil {
		t.Fatal(err)
	}
	t.Run("backfill existing payments", func(t *testing.T) {
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		if _, e = tx.Exec(`ALTER TABLE organizations DROP COLUMN billing_profile, DROP COLUMN billing_payment_created_at, DROP COLUMN billing_payment_id`); e != nil {
			t.Fatal(e)
		}
		migration, e := migrations.ReadFile("migrations/039_organization_billing_profile.sql")
		if e != nil {
			t.Fatal(e)
		}
		if _, e = tx.Exec(strings.Split(string(migration), "-- +goose Down")[0]); e != nil {
			t.Fatal(e)
		}
		var name string
		if e = tx.QueryRow(`SELECT billing_profile->>'business_name' FROM organizations WHERE id=$1`, org.ID).Scan(&name); e != nil || name != "Buyer Ltd" {
			t.Fatal("existing billing not backfilled", name, e)
		}
	})
	revision := p.Revision
	adapter.failInvoice.Store(false)
	p, err = m.Reconcile(ctx, p.ID)
	if err != nil || p.Invoice.PDFURL == "" || p.Revision != revision {
		t.Fatal("document enrichment changed financial revision", p, err)
	}
	if err = db.QueryRow(`SELECT billing_profile->>'business_name' FROM organizations WHERE id=$1`, org.ID).Scan(&profileName); err != nil || profileName != "New company name" {
		t.Fatal("payment replay overwrote company profile", profileName, err)
	}
	var count int
	var sum int64
	if err = db.QueryRow(`SELECT COUNT(*),SUM(amount_micros) FROM organization_credits WHERE payment_id=$1`, p.ID).Scan(&count, &sum); err != nil || count != 1 || sum != 4800*10000 {
		t.Fatal("tax credited or duplicate ledger", count, sum, err)
	}
	entries, err := repo.BalanceHistory(ctx, owner, org.ID, 0)
	if err != nil || len(entries) != 1 || entries[0].Payment.Invoice.PDFURL == "" || entries[0].Payment.Billing.TotalCents != 6000 {
		t.Fatal("history missing document/tax", entries, err)
	}
	// Another administrator in the same organization reuses the payer profile.
	again, err := m.Checkout(ctx, admin, org.ID, pr.ID, money.USD(50*money.Scale), "second-admin", "organization")
	if err != nil || again.CustomerID != p.CustomerID || adapter.customers.Load() != 1 {
		t.Fatal("customer not shared across admins", again, err)
	}
	another, err := m.Checkout(ctx, owner, other.ID, pr.ID, money.USD(50*money.Scale), "other-org", "organization")
	if err != nil || another.CustomerID == p.CustomerID || adapter.customers.Load() != 2 {
		t.Fatal("customer shared across organizations", another, err)
	}
	wrong := p
	wrong.OrganizationID = other.ID
	if err = repo.SavePaymentInvoice(ctx, wrong, p.Invoice); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("cross-tenant invoice update", err)
	}
	_, err = repo.WithPayment(ctx, p.ID, func(p payments.Payment) (payments.Snapshot, error) {
		s, _ := adapter.Reconcile(ctx, pr.Provider, payments.Credentials{}, p)
		s.Billing.TaxCents = 2000
		s.Billing.TotalCents = 7000
		return s, nil
	})
	if !errors.Is(err, payments.ErrConflict) {
		t.Fatal("paid tax basis changed", err)
	}
}

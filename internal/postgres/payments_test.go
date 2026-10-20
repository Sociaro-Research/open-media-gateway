package postgres

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
	"github.com/google/uuid"
)

func paymentDatabase(t *testing.T) (*sql.DB, *SystemRepository, string, string) {
	t.Helper()
	dsn := os.Getenv("GATEWAY_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := context.Background()
	base, e := Open(ctx, dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { base.Close() })
	schema := "payment_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = base.Exec(`CREATE SCHEMA ` + schema); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { base.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, e := Open(ctx, u.String())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	repo := NewSystemRepository(db)
	users := NewAuthRepository(db)
	a, e := users.UpsertGoogleUser(ctx, "payment_admin", "admin@payment.test", "Admin", "")
	if e != nil {
		t.Fatal(e)
	}
	if e = repo.SetAdmin(ctx, "", a.ID, true, false); e != nil {
		t.Fatal(e)
	}
	b, e := users.UpsertGoogleUser(ctx, "payment_user", "user@payment.test", "Owner", "")
	if e != nil {
		t.Fatal(e)
	}
	return db, repo, a.ID, b.ID
}

type paymentAdapter struct{}

func (paymentAdapter) CreateCustomer(_ context.Context, _ payments.Credentials, org, key string) (string, error) {
	return "cus_" + org, nil
}

func (paymentAdapter) Check(_ context.Context, c payments.Credentials) (payments.Account, error) {
	return payments.Account{ID: "acct_fixture", Live: !strings.Contains(c.SecretKey, "test"), ChargesEnabled: true}, nil
}
func (paymentAdapter) Checkout(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment, _ string) (payments.Snapshot, error) {
	return payments.Snapshot{CustomerID: p.CustomerID, Status: "pending", SessionID: "cs_" + p.ID, CheckoutURL: "https://checkout.stripe.com/c/pay/" + p.ID}, nil
}
func (paymentAdapter) Reconcile(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment) (payments.Snapshot, error) {
	return payments.Snapshot{CustomerID: p.CustomerID, Billing: &payments.Billing{SubtotalCents: p.AmountCents, TotalCents: p.AmountCents}, Status: "paid", SessionID: p.SessionID, IntentID: "pi_" + p.ID, Paid: true, FeeCents: 500}, nil
}
func (paymentAdapter) Webhook(context.Context, payments.Provider, payments.Credentials, []byte, string) (payments.Event, error) {
	return payments.Event{}, nil
}
func TestPaymentLedgerAndFeeModes(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	cipher, _ := providerconfig.NewCipher(strings.Repeat("x", 32))
	m := payments.New(repo, cipher, "https://gateway.example.test", map[string]payments.Adapter{"stripe": paymentAdapter{}})
	org, e := repo.CreateOrganization(ctx, admin, "Payments team", owner, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	in := payments.ProviderUpdate{Kind: "stripe", Name: "Cards", Enabled: true, SecretKey: "sk_live_fixture", WebhookSecret: "whsec_fixture", FeeMode: "organization"}
	if _, e = m.Save(ctx, owner, "", in); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("organization owner configured provider", e)
	}
	pr, e := m.Save(ctx, admin, "", in)
	if e != nil {
		t.Fatal(e)
	}
	var persisted []byte
	if e = db.QueryRow(`SELECT secret_cipher FROM payment_providers WHERE id=$1`, pr.ID).Scan(&persisted); e != nil || strings.Contains(string(persisted), "sk_live_fixture") {
		t.Fatal("secret storage", e)
	}
	if _, e = m.Checkout(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "same", "service"); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("fee disclosure race", e)
	}
	p, e := m.Checkout(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "same", "organization")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Checkout(ctx, owner, org.ID, pr.ID, money.USD(60*money.Scale), "same", "organization"); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("changed amount accepted", e)
	}
	in.Version = pr.Version
	in.FeeMode = "service"
	in.SecretKey = ""
	in.WebhookSecret = ""
	pr, e = m.Save(ctx, admin, pr.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Save(ctx, admin, pr.ID, in); !errors.Is(e, system.ErrConflict) {
		t.Fatal("stale configuration", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := m.Reconcile(ctx, p.ID); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	check := func(want int64, entries int) {
		t.Helper()
		var sum int64
		var count int
		if e = db.QueryRow(`SELECT COALESCE(SUM(amount_micros),0),COUNT(*) FROM organization_credits WHERE payment_id=$1`, p.ID).Scan(&sum, &count); e != nil || sum != want*10000 || count != entries {
			t.Fatalf("ledger got %d/%d want %d/%d: %v", sum, count, want*10000, entries, e)
		}
	}
	check(4500, 1)
	// A new mode applies only to a new payment; the original keeps its deduction.
	saved, e := repo.Payment(ctx, p.ID)
	if e != nil || saved.FeeMode != "organization" || saved.FeeCents != 500 || saved.CreditedCents != 4500 {
		t.Fatal(saved, e)
	}
	next, e := m.Checkout(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "next", "service")
	if e != nil {
		t.Fatal(e)
	}
	next, e = m.Reconcile(ctx, next.ID)
	if e != nil || next.CreditedCents != 5000 {
		t.Fatal(next, e)
	}
	update := func(refund, hold int64) {
		t.Helper()
		_, e = repo.WithPayment(ctx, p.ID, func(p payments.Payment) (payments.Snapshot, error) {
			state := "paid"
			if refund > 0 {
				state = "partially_refunded"
			}
			if hold > 0 {
				state = "disputed"
			}
			return payments.Snapshot{CustomerID: p.CustomerID, Billing: p.Billing, Status: state, SessionID: p.SessionID, IntentID: p.IntentID, Paid: true, FeeCents: 500, RefundedCents: refund, HeldCents: hold}, nil
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	update(1000, 0)
	check(3500, 2)
	update(1000, 5000)
	check(0, 3)
	update(1000, 0)
	check(3500, 4)
	update(5000, 0)
	check(0, 5)
	update(5000, 0)
	check(0, 5)
	// Callback failure rolls back all changes.
	_, e = repo.WithPayment(ctx, p.ID, func(payments.Payment) (payments.Snapshot, error) { return payments.Snapshot{}, payments.ErrUnavailable })
	if !errors.Is(e, payments.ErrUnavailable) {
		t.Fatal(e)
	}
	check(0, 5)
	// Disabled methods stop new payments but keep accepting settlement of previous ones.
	if _, e = m.Disable(ctx, admin, pr.ID, pr.Version); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Checkout(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "disabled", "service"); !errors.Is(e, payments.ErrUnavailable) {
		t.Fatal(e)
	}
	testProvider, e := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stripe", Name: "Test", Enabled: true, FeeMode: "service", SecretKey: "sk_test_fixture", WebhookSecret: "whsec_test"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.Checkout(ctx, owner, org.ID, testProvider.ID, money.USD(50*money.Scale), "test", "service"); !errors.Is(e, payments.ErrForbidden) {
		t.Fatal("test funding exposed", e)
	}
	testPayment, e := m.Checkout(ctx, admin, org.ID, testProvider.ID, money.USD(50*money.Scale), "test", "service")
	if e != nil {
		t.Fatal(e)
	}
	testPayment, e = m.Reconcile(ctx, testPayment.ID)
	if e != nil || !testPayment.Settled || testPayment.CreditedCents != 0 {
		t.Fatal("test funds became spendable", testPayment, e)
	}
	var leaked int
	if e = db.QueryRow(`SELECT count(*) FROM organization_credits WHERE payment_id=$1`, testPayment.ID).Scan(&leaked); e != nil || leaked != 0 {
		t.Fatal("test ledger leak", e)
	}
}
func TestExternalSettlementTrustAndRevisions(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	cipher, _ := providerconfig.NewCipher(strings.Repeat("y", 32))
	m := payments.New(repo, cipher, "https://example.test", nil)
	org, e := repo.CreateOrganization(ctx, admin, "External team", owner, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	pr, token, e := m.NewIntegration(ctx, admin, "Bank transfer module", true)
	if e != nil {
		t.Fatal(e)
	}
	in := payments.ExternalInput{OrganizationID: org.ID, ExternalID: "transfer-42", AmountCents: 5000, Currency: "usd", Revision: 1}
	if _, e = repo.ExternalSettlement(ctx, pr.ID, payments.HashToken("wrong"), in); !errors.Is(e, payments.ErrForbidden) {
		t.Fatal(e)
	}
	p, e := repo.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		if _, e = repo.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in); e != nil {
			t.Fatal(e)
		}
	}
	in.AmountCents = 6000
	if _, e = repo.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("reused reference changed amount", e)
	}
	in.AmountCents = 5000
	in.RefundedCents = 1000
	if _, e = repo.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("same revision changed state", e)
	}
	in.Revision = 2
	if _, e = repo.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in); e != nil {
		t.Fatal(e)
	}
	in.Revision = 1
	in.RefundedCents = 0
	if _, e = repo.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("stale receipt restored credit", e)
	}
	var sum, count int64
	if e = db.QueryRow(`SELECT SUM(amount_micros),COUNT(*) FROM organization_credits WHERE payment_id=$1`, p.ID).Scan(&sum, &count); e != nil || sum != 4000*10000 || count != 2 {
		t.Fatal(sum, count, e)
	}
	if _, e = m.Disable(ctx, admin, pr.ID, pr.Version); e != nil {
		t.Fatal(e)
	}
	in.ExternalID = "transfer-43"
	if _, e = repo.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in); !errors.Is(e, payments.ErrForbidden) {
		t.Fatal("revoked token accepted", e)
	}
}

package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

func TestPaymentExpiryStopsMonitoringAndLateCredits(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	org, err := repo.CreateOrganization(ctx, admin, "Expiry buyer", owner, uuid.NewString()+".fixture.test")
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
	card, err := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stripe", Name: "Stripe", Enabled: true, SecretKey: "sk_live_fixture", WebhookSecret: "whsec_fixture", FeeMode: "service", StripeInvoices: &off})
	if err != nil {
		t.Fatal(err)
	}
	profile := payments.BillingProfile{BusinessName: "Buyer Ltd", Address: payments.Address{Line1: "1 Buyer Street", City: "Nicosia", Country: "CY"}}
	stripe, err := m.CheckoutBilling(ctx, owner, org.ID, card.ID, money.USD(50*money.Scale), "expiring-card", "service", profile)
	if err != nil {
		t.Fatal(err)
	}
	cardDoc, err := m.InvoicePDF(ctx, stripe.ID)
	if err != nil {
		t.Fatal(err)
	}
	chain := &stablecoinChain{head: payments.ChainHead{Latest: 100, Confirmed: 80}}
	m.Chain = chain
	stable, err := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stablecoin", Name: "Stablecoins", Enabled: true, Stablecoin: &payments.StablecoinConfig{Wallets: []payments.StablecoinWallet{{Network: "ethereum", Address: "0x1111111111111111111111111111111111111111", Tokens: []string{"USDC"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	in := payments.StablecoinInput{ProviderID: stable.ID, Amount: money.USD(50 * money.Scale), Network: "ethereum", Token: "USDC", Billing: profile}
	expired, err := m.StablecoinCheckout(ctx, owner, org.ID, in, "expiring-stable")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []payments.Payment{stripe, expired} {
		if p.ExpiresAt == nil || p.ExpiresAt.Sub(p.CreatedAt) != 30*24*time.Hour {
			t.Fatal("wrong request lifetime", p)
		}
	}
	if _, err = db.Exec(`UPDATE payments SET expires_at=NOW()-INTERVAL '1 second',status=CASE WHEN stablecoin IS NOT NULL THEN 'confirming' ELSE status END WHERE id IN($1,$2)`, stripe.ID, expired.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.ExpirePayments(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{stripe.ID, expired.ID} {
		p, e := repo.Payment(ctx, id)
		if e != nil || p.Status != "expired" || p.Settled || p.CheckoutURL != "" {
			t.Fatal("not expired", p, e)
		}
	}
	if _, err = m.ResumeInvoiceCheckout(ctx, stripe.ID); !errors.Is(err, payments.ErrExpired) {
		t.Fatal("expired invoice resumed", err)
	}
	adapter.failRead = true
	p, err := m.Reconcile(ctx, stripe.ID)
	if err != nil || p.Status != "expired" {
		t.Fatal("expired request still checked provider", p, err)
	}
	adapter.failRead = false
	queued, err := repo.ClaimPaymentChecks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range queued {
		if id == stripe.ID {
			t.Fatal("expired card still scheduled")
		}
	}
	sameDoc, err := m.InvoicePDF(ctx, stripe.ID)
	if err != nil || !bytes.Equal(sameDoc.PDF, cardDoc.PDF) {
		t.Fatal("historical invoice lost", err)
	}
	if _, err = m.InvoicePDF(ctx, expired.ID); !errors.Is(err, payments.ErrExpired) {
		t.Fatal("new invoice for expired request", err)
	}
	chain.fail = true
	if err = repo.ScanStablecoins(ctx, chain); err != nil {
		t.Fatal("inactive wallet still called RPC", err)
	}
	chain.fail = false
	// A later request reactivates this wallet at the current chain head, avoiding
	// a month-long backlog. The expired exact amount stays permanently reserved.
	chain.head = payments.ChainHead{Latest: 1000000, Confirmed: 999900}
	fresh, err := m.StablecoinCheckout(ctx, owner, org.ID, in, "replacement-stable")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Stablecoin.Amount == expired.Stablecoin.Amount {
		t.Fatal("expired amount reused")
	}
	var next uint64
	if err = db.QueryRow(`SELECT next_block FROM stablecoin_watchers WHERE network='ethereum'`).Scan(&next); err != nil || next != 1000001 {
		t.Fatal("inactive watcher not advanced", next, err)
	}
	chain.head = payments.ChainHead{Latest: 1000003, Confirmed: 1000003}
	chain.transfers = []payments.StablecoinTransfer{
		{Contract: fresh.Stablecoin.Contract, Address: fresh.Stablecoin.Address, Amount: expired.Stablecoin.Amount, Hash: "0x" + strings.Repeat("a", 64), BlockHash: "0x" + strings.Repeat("b", 64), Block: 1000001, Index: 0},
		{Contract: fresh.Stablecoin.Contract, Address: fresh.Stablecoin.Address, Amount: fresh.Stablecoin.Amount, Hash: "0x" + strings.Repeat("c", 64), BlockHash: "0x" + strings.Repeat("b", 64), Block: 1000002, Index: 0}}
	if err = repo.ScanStablecoins(ctx, chain); err != nil {
		t.Fatal(err)
	}
	old, err := repo.Payment(ctx, expired.ID)
	if err != nil || old.Status != "expired" || old.CreditedCents != 0 {
		t.Fatal("late transfer credited", old, err)
	}
	paid, err := repo.Payment(ctx, fresh.ID)
	if err != nil || !paid.Settled || paid.CreditedCents != 5000 {
		t.Fatal("replacement request not paid", paid, err)
	}
	var assigned sql.NullString
	if err = db.QueryRow(`SELECT payment_id FROM stablecoin_transfers WHERE transaction_hash=$1`, "0x"+strings.Repeat("a", 64)).Scan(&assigned); err != nil || assigned.Valid {
		t.Fatal("late transfer assigned", assigned, err)
	}
	if _, err = db.Exec(`UPDATE payments SET expires_at=NOW()-INTERVAL '1 second' WHERE id=$1`, paid.ID); err != nil {
		t.Fatal(err)
	}
	if err = repo.ExpirePayments(ctx); err != nil {
		t.Fatal(err)
	}
	paid, err = repo.Payment(ctx, paid.ID)
	if err != nil || paid.Status != "paid" || !paid.Settled {
		t.Fatal("paid request expired", paid, err)
	}
	chain.fail = true
	if err = repo.ScanStablecoins(ctx, chain); err != nil {
		t.Fatal("completed wallet still scanned", err)
	}
}

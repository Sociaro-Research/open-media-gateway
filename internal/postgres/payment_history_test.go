package postgres

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	org "github.com/Sociaro-Research/media-gateway-dev/internal/organization"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/google/uuid"
)

func TestPaymentBalanceHistory(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	organization, err := repo.CreateOrganization(ctx, admin, "History team", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.CreateOrganization(ctx, admin, "Other team", admin, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BalanceHistory(ctx, owner, other.ID, 0); !errors.Is(err, org.ErrForbidden) {
		t.Fatal("cross-tenant history", err)
	}
	if _, err = repo.AddOrganizationCredit(ctx, admin, organization.ID, org.Credit{Amount: money.USD(50 * money.Scale), Kind: "payment", Reason: "Initial funding", ExternalID: "history-manual"}); err != nil {
		t.Fatal(err)
	}
	pr, err := repo.SavePaymentProvider(ctx, admin, payments.Provider{ID: uuid.NewString(), Kind: "stripe", Name: "Stripe", AccountID: "acct_history", Enabled: true, Live: true, FeeMode: "organization"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := repo.BeginPayment(ctx, owner, organization.ID, pr.ID, 500, "history-payment", "organization")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := repo.BalanceHistory(ctx, owner, organization.ID, 0)
	if err != nil || len(entries) != 1 {
		t.Fatal("unpaid attempt appeared in history", entries, err)
	}
	snap := payments.Snapshot{Billing: &payments.Billing{SubtotalCents: 500, TotalCents: 500}, Status: "paid", Paid: true, FeeCents: 46}
	if _, err = repo.WithPayment(ctx, p.ID, func(payments.Payment) (payments.Snapshot, error) { return snap, nil }); err != nil {
		t.Fatal(err)
	}
	entries, err = repo.BalanceHistory(ctx, owner, organization.ID, 0)
	if err != nil || len(entries) != 2 {
		t.Fatal("duplicate credit/payment rows", entries, err)
	}
	var paymentEntry payments.BalanceEntry
	for _, entry := range entries {
		if entry.Payment != nil {
			paymentEntry = entry
		}
	}
	if paymentEntry.Amount != 4540000 || paymentEntry.Provider != "Stripe" || paymentEntry.Payment.Settlement != nil {
		t.Fatal(paymentEntry)
	}
	// Enrich an already-settled payment without another financial revision or notification.
	before, err := repo.Payment(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	snap.Settlement = &payments.Settlement{Currency: "eur", AmountMinor: 433, FeeMinor: 40, DecimalPlaces: 2}
	after, err := repo.WithPayment(ctx, p.ID, func(payments.Payment) (payments.Snapshot, error) { return snap, nil })
	if err != nil || after.Revision != before.Revision || after.Settlement == nil || *after.Settlement != *snap.Settlement {
		t.Fatal(after, err)
	}
	entries, err = repo.BalanceHistory(ctx, owner, organization.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Payment != nil && (entry.Payment.Settlement == nil || *entry.Payment.Settlement != *snap.Settlement) {
			t.Fatal("lost settlement metadata", entry)
		}
	}
	snap.RefundedCents = 200
	snap.Status = "partially_refunded"
	if _, err = repo.WithPayment(ctx, p.ID, func(payments.Payment) (payments.Snapshot, error) { return snap, nil }); err != nil {
		t.Fatal(err)
	}
	entries, err = repo.BalanceHistory(ctx, owner, organization.ID, 0)
	if err != nil || len(entries) != 3 {
		t.Fatal("missing correction", entries, err)
	}
	var total money.USD
	for _, entry := range entries {
		total += entry.Amount
	}
	if total != money.USD(52540000) {
		t.Fatal("history changes do not sum to ledger", total)
	}
	// Test attempts are omitted before pagination for organization administrators.
	testProvider, err := repo.SavePaymentProvider(ctx, admin, payments.Provider{ID: uuid.NewString(), Kind: "stripe", Name: "Test Stripe", AccountID: "acct_history", Enabled: true, Live: false, FeeMode: "service"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 55; i++ {
		if _, err = repo.BeginPayment(ctx, admin, organization.ID, testProvider.ID, 500, fmt.Sprintf("test-%d", i), "service"); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = repo.BalanceHistory(ctx, owner, organization.ID, 0)
	if err != nil || len(entries) != 3 {
		t.Fatal("test payments changed visible pagination", len(entries), err)
	}
	// Abandoned live attempts are hidden for system administrators too.
	for i := 0; i < 55; i++ {
		attempt, e := repo.BeginPayment(ctx, owner, organization.ID, pr.ID, 500, fmt.Sprintf("abandoned-%d", i), "organization")
		if e != nil {
			t.Fatal(e)
		}
		state := []string{"pending", "failed", "expired"}[i%3]
		if _, e = db.Exec(`UPDATE payments SET status=$2 WHERE id=$1`, attempt.ID, state); e != nil {
			t.Fatal(e)
		}
		if _, e = repo.Payment(ctx, attempt.ID); e != nil {
			t.Fatal("internal attempt was lost", e)
		}
	}
	for _, actor := range []string{owner, admin} {
		visible, e := repo.BalanceHistory(ctx, actor, organization.ID, 0)
		if e != nil || len(visible) != 3 {
			t.Fatal("attempts polluted balance history", len(visible), e)
		}
	}
	// Pagination still applies to real ledger changes.
	for i := 0; i < 55; i++ {
		if _, e := repo.AddOrganizationCredit(ctx, admin, organization.ID, org.Credit{Amount: money.USD(money.Scale), Kind: "payment", Reason: "Funding", ExternalID: fmt.Sprintf("page-credit-%d", i)}); e != nil {
			t.Fatal(e)
		}
	}
	first, err := repo.BalanceHistory(ctx, admin, organization.ID, 0)
	if err != nil || len(first) != 51 {
		t.Fatal("first page", len(first), err)
	}
	second, err := repo.BalanceHistory(ctx, admin, organization.ID, 50)
	if err != nil || len(second) != 8 {
		t.Fatal("second page", len(second), err)
	}
	seen := map[string]bool{}
	for _, entry := range first[:50] {
		seen[entry.ID] = true
	}
	for _, entry := range second {
		if seen[entry.ID] {
			t.Fatal("duplicate between pages")
		}
	}
	// Downgrading the organization's administrator removes financial access.
	if _, err = db.Exec(`UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, organization.ID, owner); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BalanceHistory(ctx, owner, organization.ID, 0); !errors.Is(err, org.ErrForbidden) {
		t.Fatal("member read history", err)
	}
}

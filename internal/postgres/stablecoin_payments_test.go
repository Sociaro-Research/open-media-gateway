package postgres

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/google/uuid"
)

type stablecoinChain struct {
	head      payments.ChainHead
	transfers []payments.StablecoinTransfer
	fail      bool
}

func (c *stablecoinChain) Head(context.Context, string) (payments.ChainHead, error) {
	if c.fail {
		return payments.ChainHead{}, payments.ErrUnavailable
	}
	return c.head, nil
}
func (c *stablecoinChain) Transfers(_ context.Context, _ string, address string, from, to uint64) ([]payments.StablecoinTransfer, error) {
	if c.fail {
		return nil, payments.ErrUnavailable
	}
	out := []payments.StablecoinTransfer{}
	for _, v := range c.transfers {
		if v.Address == address && v.Block >= from && v.Block <= to {
			out = append(out, v)
		}
	}
	return out, nil
}
func TestStablecoinPaymentsExactOnceAndCancellation(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	org, err := repo.CreateOrganization(ctx, admin, "Stablecoin buyer", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	m := payments.New(repo, nil, "https://gateway.test", nil)
	chain := &stablecoinChain{head: payments.ChainHead{Latest: 100, Confirmed: 80}}
	m.Chain = chain
	_, err = repo.SaveVATSettings(ctx, admin, payments.VATSettings{Version: 1, Seller: payments.SellerCompany{LegalName: "Example Seller Ltd", Address: payments.Address{Country: "CY", Line1: "1 Seller Street", City: "Nicosia"}, VATRegistered: true, VATNumber: "CY12345678X"}, Rules: []payments.VATRule{{Country: "*", Status: "any", Treatment: "standard", Rate: 2000}}})
	if err != nil {
		t.Fatal(err)
	}
	config := &payments.StablecoinConfig{Wallets: []payments.StablecoinWallet{{Network: "ethereum", Address: "0x1111111111111111111111111111111111111111", Tokens: []string{"USDT", "USDC"}}}}
	provider, err := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stablecoin", Name: "Stablecoins", Enabled: true, Stablecoin: config})
	if err != nil {
		t.Fatal(err)
	}
	input := payments.StablecoinInput{ProviderID: provider.ID, Amount: money.USD(50 * money.Scale), Network: "ethereum", Token: "USDC", Billing: payments.BillingProfile{BusinessName: "Buyer Ltd", Address: payments.Address{Country: "CY", Line1: "1 Test Street", City: "Nicosia"}}}
	var wg sync.WaitGroup
	results := make(chan payments.Payment, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := m.StablecoinCheckout(ctx, owner, org.ID, input, "stable-retry")
			results <- p
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first payments.Payment
	for p := range results {
		if first.ID == "" {
			first = p
		}
		if p.ID != first.ID {
			t.Fatal("retry created another payment")
		}
	}
	if first.Billing.TaxCents != 1000 || first.Stablecoin.FeeMicros < 1 || first.Stablecoin.FeeMicros > 9999 || first.Stablecoin.Amount != payments.StablecoinAmount(60000000+first.Stablecoin.FeeMicros) {
		t.Fatal("tax, exact fee or idempotency", first)
	}
	changed := input
	changed.Amount = 51 * money.USD(money.Scale)
	if _, e := m.StablecoinCheckout(ctx, owner, org.ID, changed, "stable-retry"); !errors.Is(e, payments.ErrConflict) {
		t.Fatal("changed retry accepted", e)
	}

	// Existing requests and idempotent retries retain their tax snapshot when
	// the operator changes the VAT policy for future payments.
	doc, docErr := m.InvoicePDF(ctx, first.ID)
	if docErr != nil || len(doc.PDF) == 0 {
		t.Fatal("stablecoin invoice", docErr)
	}
	policy, err := repo.VATSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	policy.Rules[0].Rate = 2100
	if _, err = repo.SaveVATSettings(ctx, admin, policy); err != nil {
		t.Fatal(err)
	}
	retry, err := m.StablecoinCheckout(ctx, owner, org.ID, input, "stable-retry")
	if err != nil || retry.Billing.TaxCents != 1000 || retry.Stablecoin.VAT.Rule.Rate != 2000 {
		t.Fatal("existing VAT snapshot changed", retry, err)
	}
	next, err := m.StablecoinCheckout(ctx, owner, org.ID, input, "another-payment")
	if err != nil {
		t.Fatal(err)
	}
	if next.Billing.TaxCents != 1050 || next.Stablecoin.VAT.Rule.Rate != 2100 {
		t.Fatal("new VAT policy ignored", next)
	}
	if first.Stablecoin.Amount == next.Stablecoin.Amount {
		t.Fatal("duplicate amount")
	}
	if _, err = repo.CancelStablecoin(ctx, owner, org.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	asset := first.Stablecoin
	hash := "0x" + strings.Repeat("a", 64)
	blockHash := "0x" + strings.Repeat("b", 64)
	chain.head = payments.ChainHead{Latest: 105, Confirmed: 100}
	chain.transfers = []payments.StablecoinTransfer{{Contract: asset.Contract, Address: asset.Address, Amount: asset.Amount, Hash: hash, BlockHash: blockHash, Block: 101, Index: 0}, {Contract: asset.Contract, Address: asset.Address, Amount: next.Stablecoin.Amount, Hash: "0x" + strings.Repeat("c", 64), BlockHash: blockHash, Block: 102, Index: 0}}
	if err = repo.ScanStablecoins(ctx, chain); err != nil {
		t.Fatal(err)
	}
	first, err = repo.Payment(ctx, first.ID)
	if err != nil || first.Status != "confirming" || first.Settled {
		t.Fatal("unconfirmed payment credited", first, err)
	}
	if _, err = repo.CancelStablecoin(ctx, owner, org.ID, first.ID); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("confirming request cancelled", err)
	}
	// A transient outage must preserve the pending match and scanner cursor.
	chain.fail = true
	if err = repo.ScanStablecoins(ctx, chain); err == nil {
		t.Fatal("RPC failure ignored")
	}
	chain.fail = false
	// A reorganization removes the provisional transfer. No credit is posted.
	chain.transfers = nil
	if err = repo.ScanStablecoins(ctx, chain); err != nil {
		t.Fatal(err)
	}
	reorg, _ := repo.Payment(ctx, first.ID)
	if reorg.Status != "pending" || reorg.Stablecoin.TransactionHash != "" {
		t.Fatal("orphan match retained", reorg)
	}
	// The same transaction/log can execute differently on a replacement fork.
	// An unassigned provisional observation must keep the current canonical amount.
	chain.transfers = []payments.StablecoinTransfer{{Contract: asset.Contract, Address: asset.Address, Amount: next.Stablecoin.Amount, Hash: hash, BlockHash: blockHash, Block: 101, Index: 0}}
	if err = repo.ScanStablecoins(ctx, chain); err != nil {
		t.Fatal(err)
	}
	var observedAmount string
	if err = db.QueryRow(`SELECT amount FROM stablecoin_transfers WHERE network='ethereum' AND transaction_hash=$1`, hash).Scan(&observedAmount); err != nil || observedAmount != next.Stablecoin.Amount {
		t.Fatal("fork observation not replaced", observedAmount, err)
	}

	chain.transfers = []payments.StablecoinTransfer{{Contract: asset.Contract, Address: asset.Address, Amount: asset.Amount, Hash: hash, BlockHash: blockHash, Block: 101, Index: 0}}
	chain.head = payments.ChainHead{Latest: 200, Confirmed: 150}
	// A disabled method still settles its existing payments.
	if _, err = m.Disable(ctx, admin, provider.ID, provider.Version); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err = repo.ScanStablecoins(ctx, chain); err != nil {
			t.Fatal(err)
		}
	}
	paid, err := repo.Payment(ctx, first.ID)
	if err != nil || !paid.Settled || paid.CreditedCents != 5000 || paid.FeeMicros != asset.FeeMicros || paid.Stablecoin.TransactionHash != hash {
		t.Fatal("settlement", paid, err)
	}
	var credits int
	var amount int64
	if err = db.QueryRow(`SELECT COUNT(*),COALESCE(SUM(amount_micros),0) FROM organization_credits WHERE payment_id=$1`, first.ID).Scan(&credits, &amount); err != nil || credits != 1 || amount != 50000000 {
		t.Fatal("duplicate or incorrect ledger", credits, amount, err)
	}
	if _, err = repo.CancelStablecoin(ctx, owner, org.ID, first.ID); !errors.Is(err, payments.ErrConflict) {
		t.Fatal("paid request cancelled", err)
	}
	cancelled, _ := repo.Payment(ctx, next.ID)
	if cancelled.Status != "cancelled" || cancelled.CreditedCents != 0 {
		t.Fatal("cancelled payment credited", cancelled)
	}
	// Unique index also reserves cancelled requests permanently.
	_, err = db.Exec(`UPDATE payments SET stablecoin=jsonb_set(stablecoin,'{amount}',to_jsonb($2::text)) WHERE id=$1`, next.ID, asset.Amount)
	if err == nil {
		t.Fatal("duplicate reservation allowed")
	}
	var audited int
	if err = db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE action='payment_provider' AND details->'after' ? 'stablecoin'`).Scan(&audited); err != nil || audited == 0 {
		t.Fatal("wallet configuration not audited", audited, err)
	}
}

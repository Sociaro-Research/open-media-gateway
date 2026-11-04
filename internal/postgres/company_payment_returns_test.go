package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/google/uuid"
)

type returnFixture struct {
	db      *sql.DB
	r       *CompanyInterfaceLifecycle
	u       identity.User
	start   ci.Start
	tokens  ci.Tokens
	payment string
}

func paymentReturnFixture(t *testing.T) *returnFixture {
	t.Helper()
	db := auditDatabase(t, true)
	r := ciRepository(t, db)
	u, _ := ciUser(t, db)
	start := ciStart(t)
	start.ConnectionRevision = 7
	tokens := ciIssue(t, r, u, start)
	provider, id := uuid.NewString(), uuid.NewString()
	ciExec(t, db, `INSERT INTO payment_providers(id,kind,name,enabled,live,account_id,created_by) VALUES($1,'stripe','Temporary PSP',TRUE,TRUE,$2,$3)`, provider, "acct_"+provider, u.ID)
	ciExec(t, db, `INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,fee_mode,amount_cents,currency,live,expires_at) VALUES($1,$2,$3,$4,'fixture','service',1000,'usd',TRUE,clock_timestamp()+INTERVAL '1 day')`, id, tokens.Company, provider, u.ID)
	return &returnFixture{db, r, u, start, tokens, id}
}
func (f *returnFixture) ctx(t *testing.T) context.Context {
	t.Helper()
	credentials := ciCredentials(f.start, f.tokens)
	p, err := ciGate(f.db).AuthenticateBrowser(ciHost(), credentials, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithCompanyBoundary(identity.WithUser(ciHost(), f.u), p.Grant.Company)
	return ci.WithMutationAuthority(ctx, p, p, credentials, "organizations:manage", "company-payment-return")
}
func returnFinancialState(t *testing.T, db *sql.DB) string {
	t.Helper()
	var raw string
	err := db.QueryRow(`SELECT jsonb_build_object('payments',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM payments p),'credits',(SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM organization_credits c),'invoices',(SELECT jsonb_agg(to_jsonb(d) ORDER BY payment_id) FROM payment_invoice_documents d))::text`).Scan(&raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestCompanyPaymentReturnExactReceiptRotationAndReadOnlyConsumption(t *testing.T) {
	f := paymentReturnFixture(t)
	ctx := f.ctx(t)
	before := returnFinancialState(t, f.db)
	in := payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment}
	first, err := f.r.IssuePaymentReturn(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var binding companyPaymentReturnBinding
	var raw []byte
	var receipt []byte
	if err = f.db.QueryRow(`SELECT binding,receipt FROM company_interface_payment_returns`).Scan(&raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(receipt), first.Token) || strings.Contains(string(raw), first.Token) {
		t.Fatal("plaintext return token persisted")
	}
	// Lost reply + repository restart yields the exact receipt, including expiry.
	restarted := ciRepository(t, f.db)
	next, err := restarted.IssuePaymentReturn(ctx, in)
	if err != nil || !reflect.DeepEqual(first, next) {
		t.Fatal("receipt recovery", first, next, err)
	}
	if err = json.Unmarshal(raw, &binding); err != nil || binding.ConnectionRevision != 7 || binding.Actor != f.u.ID || binding.LocalOrigin != f.start.LocalOrigin {
		t.Fatal(binding, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := restarted.IssuePaymentReturn(ctx, in)
			if e == nil && v.Token != first.Token {
				e = errors.New("duplicate return token")
			}
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal(e)
		}
	}
	bad := in
	bad.CheckoutAttempt++
	if _, err = restarted.IssuePaymentReturn(ctx, bad); !errors.Is(err, ci.ErrConflict) {
		t.Fatal("changed intent", err)
	}
	rotated, err := f.r.Refresh(ciHost(), ciRefresh(f.start, f.tokens))
	if err != nil {
		t.Fatal(err)
	}
	// Refresh rotates the user family, not the separately stored installation proof.
	rotated.InstallationCredential = f.tokens.InstallationCredential
	f.tokens = rotated
	ctx = f.ctx(t)
	var consumed time.Time
	for i := 0; i < 2; i++ {
		p, err := restarted.ConsumePaymentReturn(ctx, first.Token)
		if err != nil || p.ID != f.payment || p.Settled || p.Status != "pending" {
			t.Fatal("consume status", p, err)
		}
		var at time.Time
		if err = f.db.QueryRow(`SELECT consumed_at FROM company_interface_payment_returns`).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			consumed = at
		} else if !consumed.Equal(at) {
			t.Fatal("repeat consumed twice")
		}
	}
	if after := returnFinancialState(t, f.db); before != after {
		t.Fatal("return changed payment/ledger/invoice state")
	}
	// Current authority remains required even for an already consumed receipt.
	if err = f.r.Logout(ciHost(), ciCredentials(f.start, f.tokens)); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.ConsumePaymentReturn(ctx, first.Token); err == nil {
		t.Fatal("logged out viewer recovered status")
	}
}

func TestCompanyPaymentReturnRollbacksSubstitutionAndNoLedgerWrites(t *testing.T) {
	f := paymentReturnFixture(t)
	ctx := f.ctx(t)
	in := payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment}
	before := returnFinancialState(t, f.db)
	f.r.failWrite = func(point string) error {
		if point == "payment-return-receipt" {
			return errors.New("synthetic crash")
		}
		return nil
	}
	if _, err := f.r.IssuePaymentReturn(ctx, in); err == nil {
		t.Fatal("fault ignored")
	}
	var n int
	if err := f.db.QueryRow(`SELECT count(*) FROM company_interface_payment_returns`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	f.r.failWrite = nil
	receipt, err := f.r.IssuePaymentReturn(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	f.r.failWrite = func(point string) error {
		if point == "payment-return-consumed" {
			return errors.New("synthetic consume crash")
		}
		return nil
	}
	if _, err = f.r.ConsumePaymentReturn(ctx, receipt.Token); err == nil {
		t.Fatal("consume fault ignored")
	}
	var at *time.Time
	if err = f.db.QueryRow(`SELECT consumed_at FROM company_interface_payment_returns`).Scan(&at); err != nil || at != nil {
		t.Fatal("partial consumption", at, err)
	}
	f.r.failWrite = nil
	// Valid nonce alone has no authority; neither another central actor nor a
	// forged same-company boundary can substitute for the exact delegated family.
	if _, err = f.r.ConsumePaymentReturn(identity.WithUser(ciHost(), f.u), receipt.Token); err == nil {
		t.Fatal("direct actor consumed delegated state")
	}
	other, _ := ciUser(t, f.db)
	otherContext := identity.WithUser(ctx, other)
	if _, err = f.r.ConsumePaymentReturn(otherContext, receipt.Token); err == nil {
		t.Fatal("actor substitution")
	}
	for _, bad := range []string{"", receipt.Token + "=", ciSecret(t)} {
		if _, err = f.r.ConsumePaymentReturn(ctx, bad); err == nil {
			t.Fatal("invalid/unknown token")
		}
	}
	// A different verified installation/session of the same user has no access.
	otherStart := ciStart(t)
	otherTokens := ciIssue(t, f.r, f.u, otherStart)
	a := ciCredentials(otherStart, otherTokens)
	p, err := ciGate(f.db).AuthenticateBrowser(ciHost(), a, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	foreign := ci.WithMutationAuthority(identity.WithCompanyBoundary(identity.WithUser(ciHost(), f.u), p.Grant.Company), p, p, a, "organizations:manage", "company-payment-return")
	if _, err = f.r.ConsumePaymentReturn(foreign, receipt.Token); err == nil {
		t.Fatal("foreign installation/company consumed")
	}
	if _, err = f.r.IssuePaymentReturn(foreign, in); err == nil {
		t.Fatal("foreign company payment issued")
	}
	// Stored custody and its expiry/binding are immutable; no application reset.
	if _, err = f.db.Exec(`UPDATE company_interface_payment_returns SET binding=jsonb_set(binding,'{LocalOrigin}','"https://evil.invalid"')`); err == nil {
		t.Fatal("return origin rebound")
	}
	if _, err = f.r.ConsumePaymentReturn(ctx, receipt.Token); err != nil {
		t.Fatal(err)
	}
	if after := returnFinancialState(t, f.db); before != after {
		t.Fatal("return changed financial state")
	}
}

func TestCompanyPaymentReturnCurrentAuthorityAndExpiredCustody(t *testing.T) {
	for _, change := range []string{"grant", "source_logout", "membership", "role", "expired", "receipt_swap"} {
		t.Run(change, func(t *testing.T) {
			f := paymentReturnFixture(t)
			ctx := f.ctx(t)
			before := returnFinancialState(t, f.db)
			in := payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment}
			receipt, err := f.r.IssuePaymentReturn(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			token := receipt.Token
			switch change {
			case "grant":
				if err = f.r.Revoke(identity.WithUser(ciHost(), f.u), f.tokens.GrantID); err != nil {
					t.Fatal(err)
				}
			case "source_logout":
				ciExec(t, f.db, `UPDATE company_interface_source_sessions SET revoked_at=clock_timestamp() WHERE id=$1`, f.u.SourceSessionID)
			case "membership":
				ciExec(t, f.db, `UPDATE organization_members SET removed_at=clock_timestamp() WHERE organization_id=$1 AND user_id=$2`, f.tokens.Company, f.u.ID)
			case "role":
				ciExec(t, f.db, `UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, f.tokens.Company, f.u.ID)
			case "expired", "receipt_swap":
				// Create a deliberately invalid isolated row without weakening the immutable
				// production UPDATE trigger. No real payment or stored file is touched.
				var binding companyPaymentReturnBinding
				var raw, encrypted []byte
				if err = f.db.QueryRow(`SELECT binding,receipt FROM company_interface_payment_returns`).Scan(&raw, &encrypted); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal(raw, &binding); err != nil {
					t.Fatal(err)
				}
				binding.OperationID = uuid.NewString()
				token = ciSecret(t)
				digest := ci.Digest(token)
				if change == "expired" {
					encrypted, err = f.r.cipher.Seal(struct{ Token string }{token}, returnAAD(binding))
					if err != nil {
						t.Fatal(err)
					}
				}
				raw, err = json.Marshal(binding)
				if err != nil {
					t.Fatal(err)
				}
				expires := time.Now().Add(time.Hour)
				if change == "expired" {
					expires = time.Now().Add(-time.Hour)
				}
				ciExec(t, f.db, `INSERT INTO company_interface_payment_returns(id,grant_id,session_id,operation_id,payment_id,binding,token_hash,receipt,expires_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.NewString(), binding.GrantID, binding.SessionID, binding.OperationID, binding.PaymentID, raw, digest[:], encrypted, expires, time.Now().Add(-2*time.Hour))
			}
			if _, err = f.r.ConsumePaymentReturn(ctx, token); err == nil {
				t.Fatal("stale or tampered authority disclosed payment")
			}
			var consumed int
			if err = f.db.QueryRow(`SELECT count(*) FROM company_interface_payment_returns WHERE consumed_at IS NOT NULL`).Scan(&consumed); err != nil || consumed != 0 {
				t.Fatal("denied return consumed", consumed, err)
			}
			if after := returnFinancialState(t, f.db); after != before {
				t.Fatal("denied navigation changed financial state")
			}
		})
	}
}

func TestCompanyPaymentReturnConcurrentConsumptionDoesNotSerializeWithPaymentWrite(t *testing.T) {
	f := paymentReturnFixture(t)
	ctx := f.ctx(t)
	receipt, err := f.r.IssuePaymentReturn(ctx, payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment})
	if err != nil {
		t.Fatal(err)
	}
	before := returnFinancialState(t, f.db)
	// Settlement's payment row lock must not deadlock with navigation's company
	// authority lock. This writer changes nothing and stands in for that lock edge.
	tx, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var id string
	if err = tx.QueryRow(`SELECT id::text FROM payments WHERE id=$1 FOR UPDATE`, f.payment).Scan(&id); err != nil {
		t.Fatal(err)
	}
	timeout, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, e := f.r.ConsumePaymentReturn(timeout, receipt.Token)
			if e == nil && p.ID != f.payment {
				e = errors.New("wrong payment")
			}
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal(e)
		}
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if after := returnFinancialState(t, f.db); after != before {
		t.Fatal("concurrent navigation changed financial state")
	}
	var n int
	if err = f.db.QueryRow(`SELECT count(*) FROM company_interface_payment_returns WHERE consumed_at IS NOT NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
}

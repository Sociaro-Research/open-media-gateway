package postgres

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

// No network: explicit Continue may reconcile its existing session under the
// direct-central contract, but cannot create a second checkout or payment.
type companyContinuePSP struct{ checkout, reconcile int }

func (a *companyContinuePSP) Check(context.Context, payments.Credentials) (payments.Account, error) {
	return payments.Account{ID: "synthetic", Live: true, ChargesEnabled: true}, nil
}
func (a *companyContinuePSP) Checkout(context.Context, payments.Provider, payments.Credentials, payments.Payment, string) (payments.Snapshot, error) {
	a.checkout++
	return payments.Snapshot{}, payments.ErrConflict
}
func (a *companyContinuePSP) Webhook(context.Context, payments.Provider, payments.Credentials, []byte, string) (payments.Event, error) {
	return payments.Event{}, payments.ErrSignature
}
func (a *companyContinuePSP) Reconcile(_ context.Context, _ payments.Provider, _ payments.Credentials, p payments.Payment) (payments.Snapshot, error) {
	a.reconcile++
	return payments.Snapshot{Status: p.Status, SessionID: p.SessionID, CheckoutURL: p.CheckoutURL, CustomerID: p.CustomerID, Revision: p.Revision}, nil
}

func TestCompanyPaymentDirectCentralSecondManagerCheckoutPolicy(t *testing.T) {
	f := paymentReturnFixture(t)
	manager, _ := ciUser(t, f.db)
	member, _ := ciUser(t, f.db)
	ciExec(t, f.db, `INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'admin'),($1,$3,'member')`, f.tokens.Company, manager.ID, member.ID)
	cipher, err := providerconfig.NewCipher(strings.Repeat("p", 32))
	if err != nil {
		t.Fatal(err)
	}
	secret, err := cipher.Encrypt([]byte(`{"secret_key":"sk_fixture","webhook_secret":"whsec_fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	ciExec(t, f.db, `UPDATE payment_providers SET secret_cipher=$1`, secret)
	ciExec(t, f.db, `UPDATE payments SET session_id='cs_existing',checkout_url='https://checkout.stripe.com/c/pay/existing',invoice_mode='local' WHERE id=$1`, f.payment)
	adapter := &companyContinuePSP{}
	sys := NewSystemRepository(f.db)
	funding := payments.New(sys, cipher, ciTestCentral, map[string]payments.Adapter{"stripe": adapter})
	keys := NewAPIKeyRepository(f.db)
	keyFor := func(recipient identity.User) string {
		k, value, e := apikey.Generate(apikey.CreateInput{UserID: recipient.ID, OrganizationID: f.tokens.Company, ResourceMode: "shared", StorageMode: "shared", Name: "Temporary checkout fixture", Scopes: []string{apikey.Generation, apikey.OrganizationsManage}}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if e = keys.Create(identity.WithUser(ciHost(), f.u), f.u.ID, k, apikey.Hash(value)); e != nil {
			t.Fatal(e)
		}
		return value
	}
	managerKey := keyFor(manager)
	memberKey := keyFor(member)
	h := api.NewGateway(api.Dependencies{Health: admissionHealth{}, System: sys, Payments: funding, Keys: keys})
	send := func(org, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", ciTestCentral+"/v1/organizations/"+org+"/payments/"+f.payment+"/checkout", nil)
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	beforeCredit := ciCounts(t, f.db)["organization_credits"]
	for i := 0; i < 2; i++ {
		w := send(f.tokens.Company, managerKey)
		if w.Code != 200 {
			t.Fatal("current company manager denied creator's payment", w.Code, w.Body.String())
		}
		var p payments.Payment
		if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.ID != f.payment || p.CheckoutURL != "https://checkout.stripe.com/c/pay/existing" {
			t.Fatal(p, err)
		}
	}
	if adapter.checkout != 0 || adapter.reconcile != 2 {
		t.Fatal("checkout was duplicated or explicit Continue not exercised", adapter)
	}
	for _, tc := range []struct{ org, key string }{{f.tokens.Company, memberKey}, {"11111111-1111-4111-8111-111111111111", managerKey}} {
		if w := send(tc.org, tc.key); w.Code != 403 {
			t.Fatal("role/tenant bypass", w.Code, w.Body.String())
		}
	}
	if adapter.checkout != 0 || adapter.reconcile != 2 {
		t.Fatal("denied viewer reached PSP", adapter)
	}
	var count int
	var actor string
	if err = f.db.QueryRow(`SELECT count(*),min(actor_id::text) FROM payments`).Scan(&count, &actor); err != nil || count != 1 || actor != f.u.ID {
		t.Fatal("payment duplicated or creator changed", count, actor, err)
	}
	if ciCounts(t, f.db)["organization_credits"] != beforeCredit {
		t.Fatal("Continue invented funds")
	}
}

func TestCompanyPaymentSecondManagerOwnReceiptAndCheckoutCorrelation(t *testing.T) {
	f := paymentReturnFixture(t)
	creatorContext := f.ctx(t)
	creatorReceipt, err := f.r.IssuePaymentReturn(creatorContext, payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment})
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := ciUser(t, f.db)
	ciExec(t, f.db, `INSERT INTO organization_members(organization_id,user_id,role) VALUES($1,$2,'admin')`, f.tokens.Company, manager.ID)
	login := ciStart(t)
	login.Mode = "login"
	login.InstallationProof = f.start.InstallationProof
	login.PreviousGrantID = f.tokens.GrantID
	login.GrantCredential = f.tokens.InstallationCredential
	login.ConnectionRevision = 11
	v := ciIssue(t, f.r, manager, login)
	v.InstallationCredential = f.tokens.InstallationCredential
	managerFixture := &returnFixture{f.db, f.r, manager, login, v, f.payment}
	managerContext := managerFixture.ctx(t)
	before := returnFinancialState(t, f.db)
	own, err := f.r.IssuePaymentReturn(managerContext, payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment})
	if err != nil || own.Token == creatorReceipt.Token {
		t.Fatal("manager cannot get independent receipt", err)
	}
	if _, err = f.r.ConsumePaymentReturn(managerContext, creatorReceipt.Token); err == nil {
		t.Fatal("manager consumed creator authority")
	}
	// Different browser families/revisions are intentional: each viewer retains
	// their own immutable binding. Logout of the creator cannot revoke the company.
	if err = f.r.Logout(ciHost(), ciCredentials(f.start, f.tokens)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		p, e := f.r.ConsumeCheckoutReturn(managerContext, creatorReceipt.Token, own.Token)
		if e != nil || p.ID != f.payment {
			t.Fatal("authorized manager continuation denied", p, e)
		}
	}
	var creatorConsumed, managerConsumed bool
	if err = f.db.QueryRow(`SELECT consumed_at IS NOT NULL FROM company_interface_payment_returns WHERE session_id=$1`, f.tokens.SessionID).Scan(&creatorConsumed); err != nil {
		t.Fatal(err)
	}
	if err = f.db.QueryRow(`SELECT consumed_at IS NOT NULL FROM company_interface_payment_returns WHERE session_id=$1`, v.SessionID).Scan(&managerConsumed); err != nil || creatorConsumed || !managerConsumed {
		t.Fatal("wrong family consumed", creatorConsumed, managerConsumed, err)
	}
	if after := returnFinancialState(t, f.db); before != after {
		t.Fatal("return navigation changed payment state")
	}
	// A current member's valid login still cannot borrow either manager's receipt.
	ciExec(t, f.db, `UPDATE organization_members SET role='member' WHERE organization_id=$1 AND user_id=$2`, f.tokens.Company, manager.ID)
	if _, err = f.r.ConsumeCheckoutReturn(managerContext, creatorReceipt.Token, own.Token); err == nil {
		t.Fatal("demoted viewer inherited creator permission")
	}
}

func TestCompanyPaymentCheckoutCorrelationRejectsOtherPaymentAttemptAndInstallation(t *testing.T) {
	f := paymentReturnFixture(t)
	ctx := f.ctx(t)
	first, err := f.r.IssuePaymentReturn(ctx, payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment})
	if err != nil {
		t.Fatal(err)
	}
	ciExec(t, f.db, `UPDATE payments SET checkout_attempt=1 WHERE id=$1`, f.payment)
	next, err := f.r.IssuePaymentReturn(ctx, payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment, CheckoutAttempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	otherPayment := uuid.NewString()
	ciExec(t, f.db, `INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,fee_mode,amount_cents,currency,live,expires_at) SELECT $1,organization_id,provider_id,actor_id,'different-payment',fee_mode,amount_cents,currency,live,expires_at FROM payments WHERE id=$2`, otherPayment, f.payment)
	otherReceipt, err := f.r.IssuePaymentReturn(ctx, payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: otherPayment})
	if err != nil {
		t.Fatal(err)
	}
	before := returnFinancialState(t, f.db)
	if _, err = f.r.ConsumeCheckoutReturn(ctx, otherReceipt.Token, next.Token); err == nil {
		t.Fatal("another payment correlated")
	}
	if _, err = f.r.ConsumeCheckoutReturn(ctx, first.Token, next.Token); err == nil {
		t.Fatal("another checkout attempt correlated")
	}
	if _, err = f.r.ConsumeCheckoutReturn(ctx, ciSecret(t), next.Token); err == nil {
		t.Fatal("unknown callback correlated")
	}
	// Another approved installation has its own company and origin; neither token
	// can substitute its destination, even when the central user is identical.
	otherStart := ciStart(t)
	otherStart.LocalOrigin = "https://another-installation.example"
	otherTokens := ciIssue(t, f.r, f.u, otherStart)
	other := &returnFixture{f.db, f.r, f.u, otherStart, otherTokens, f.payment}
	if _, err = f.r.ConsumeCheckoutReturn(other.ctx(t), next.Token, next.Token); err == nil {
		t.Fatal("foreign installation consumed")
	}
	var consumed int
	if err = f.db.QueryRow(`SELECT count(*) FROM company_interface_payment_returns WHERE consumed_at IS NOT NULL`).Scan(&consumed); err != nil || consumed != 0 {
		t.Fatal("failed correlation consumed state", consumed, err)
	}
	if after := returnFinancialState(t, f.db); before != after {
		t.Fatal("failed correlation changed financial state")
	}
	if _, err = f.r.ConsumeCheckoutReturn(ctx, next.Token, next.Token); err != nil {
		t.Fatal("valid checkout correlation denied", err)
	}
}

func TestCompanyPaymentReturnRebindCannotRetargetOriginalCheckout(t *testing.T) {
	f := paymentReturnFixture(t)
	old, err := f.r.IssuePaymentReturn(f.ctx(t), payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment})
	if err != nil {
		t.Fatal(err)
	}
	before := returnFinancialState(t, f.db)
	next := ciStart(t)
	next.Mode = "rebind"
	next.InstallationProof = f.start.InstallationProof
	next.PreviousGrantID = f.tokens.GrantID
	next.LocalOrigin = "https://new-approved-installation.example"
	next.ConnectionRevision = 8
	tokens := ciIssue(t, f.r, f.u, next)
	if tokens.Company != f.tokens.Company || tokens.GrantID == f.tokens.GrantID {
		t.Fatal("bad rebind fixture")
	}
	current := &returnFixture{f.db, f.r, f.u, next, tokens, f.payment}
	own, err := f.r.IssuePaymentReturn(current.ctx(t), payments.ReturnIntentInput{OperationID: uuid.NewString(), PaymentID: f.payment})
	if err != nil {
		t.Fatal("current manager cannot access existing company payment", err)
	}
	if _, err = f.r.ConsumeCheckoutReturn(current.ctx(t), old.Token, own.Token); err == nil {
		t.Fatal("old callback silently retargeted to new grant/origin")
	}
	var n int
	if err = f.db.QueryRow(`SELECT count(*) FROM company_interface_payment_returns WHERE consumed_at IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatal("rebind mismatch consumed state", n, err)
	}
	if after := returnFinancialState(t, f.db); before != after {
		t.Fatal("rebind/return changed financial history")
	}
}

package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	"github.com/Sociaro-Research/open-media-gateway/internal/apikey"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/identity"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

type paymentNavigationProbe struct {
	*SystemRepository
	mutations int
}

func (p *paymentNavigationProbe) ExpirePayments(context.Context) error {
	p.mutations++
	return payments.ErrConflict
}
func (p *paymentNavigationProbe) WithPayment(context.Context, string, func(payments.Payment) (payments.Snapshot, error)) (payments.Payment, error) {
	p.mutations++
	return payments.Payment{}, payments.ErrConflict
}
func (p *paymentNavigationProbe) LocalInvoice(context.Context, string, func(payments.Payment, string, time.Time) ([]byte, error)) (payments.InvoiceDocument, error) {
	p.mutations++
	return payments.InvoiceDocument{}, payments.ErrConflict
}

func TestCompanyPaymentStatusActualHandlerIsReadOnlyAndCurrent(t *testing.T) {
	f := paymentReturnFixture(t)
	sys := NewSystemRepository(f.db)
	probe := &paymentNavigationProbe{SystemRepository: sys}
	cipher, err := providerconfig.NewCipher(strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	psp := &companyContinuePSP{}
	funding := payments.New(probe, cipher, ciTestCentral, map[string]payments.Adapter{"stripe": psp})
	keys := NewAPIKeyRepository(f.db)
	key, value, err := apikey.Generate(apikey.CreateInput{UserID: f.u.ID, OrganizationID: f.tokens.Company, Name: "Temporary status", ResourceMode: "shared", StorageMode: "shared", Scopes: []string{apikey.Generation, apikey.OrganizationsManage}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Create(identity.WithUser(ciHost(), f.u), f.u.ID, key, apikey.Hash(value)); err != nil {
		t.Fatal(err)
	}
	h := api.NewGateway(api.Dependencies{Health: admissionHealth{}, System: sys, Payments: funding, Keys: keys, CompanyInterface: f.r, CompanyCustomer: NewCompanyInterfaceAuthority(f.db), CompanyCentralOrigin: ciTestCentral})
	send := func(v *returnFixture, browser bool, method, path, secret string, alter func(*http.Request)) *httptest.ResponseRecorder {
		prefix := ci.CustomerSDKPrefix
		if browser {
			prefix = ci.CustomerBrowserPrefix
		}
		req := httptest.NewRequest(method, ciTestCentral+prefix+strings.TrimPrefix(path, "/"), nil).WithContext(ciHost())
		req.Header.Set(ci.InstallationHeader, v.tokens.InstallationCredential)
		req.Header.Set(ci.LocalOriginHeader, v.start.LocalOrigin)
		if browser {
			req.Header.Set(ci.SessionHeader, v.tokens.AccessToken)
			hash := ci.Digest(v.start.LocalSession)
			req.Header.Set(ci.LocalSessionHeader, hex.EncodeToString(hash[:]))
		}
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
		}
		if alter != nil {
			alter(req)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	path := "/v1/organizations/" + f.tokens.Company + "/payments/" + f.payment
	ciExec(t, f.db, `UPDATE payments SET expires_at=clock_timestamp()-INTERVAL '1 hour',checkout_url='https://checkout.stripe.com/c/pay/expired' WHERE id=$1`, f.payment)
	before := returnFinancialState(t, f.db)
	for _, browser := range []bool{true, false} {
		for _, method := range []string{"GET", "HEAD"} {
			secret := ""
			if !browser {
				secret = value
			}
			w := send(f, browser, method, path, secret, nil)
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("status", browser, method, w.Code, w.Body.String())
			}
			if method == "GET" {
				var p payments.Payment
				if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.ID != f.payment || p.Status != "expired" || p.CheckoutURL != "" {
					t.Fatal(p, err)
				}
			} else if w.Body.Len() != 0 {
				t.Fatal("HEAD body")
			}
		}
	}
	if after := returnFinancialState(t, f.db); after != before {
		t.Fatal("GET expired or mutated stored payment")
	}
	// Projection must match the existing maintenance predicate, without changing
	// a terminal cancellation/failure or applying that maintenance to the DB.
	for _, tc := range []struct {
		status        string
		settled, past bool
		want          string
	}{
		{"pending", false, true, "expired"}, {"confirming", false, true, "expired"},
		{"cancelled", false, true, "cancelled"}, {"failed", false, true, "failed"},
		{"expired", false, true, "expired"}, {"paid", true, true, "paid"},
		{"pending", true, true, "pending"}, {"pending", false, false, "pending"},
	} {
		deadline := time.Now().Add(time.Hour)
		if tc.past {
			deadline = time.Now().Add(-time.Hour)
		}
		ciExec(t, f.db, `UPDATE payments SET status=$2,settled=$3,expires_at=$4,checkout_url='https://checkout.stripe.com/c/pay/preserved' WHERE id=$1`, f.payment, tc.status, tc.settled, deadline)
		original := returnFinancialState(t, f.db)
		for _, browser := range []bool{true, false} {
			secret := ""
			if !browser {
				secret = value
			}
			w := send(f, browser, "GET", path, secret, nil)
			var got payments.Payment
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Status != tc.want {
				t.Fatalf("status projection %+v browser=%v: %d %s", tc, browser, w.Code, w.Body.String())
			}
			cleared := !tc.settled && tc.past && (tc.status == "pending" || tc.status == "confirming")
			if (got.CheckoutURL == "") != cleared {
				t.Fatal("terminal checkout projection changed", tc, got.CheckoutURL)
			}
		}
		if original != returnFinancialState(t, f.db) {
			t.Fatal("projection modified persisted state", tc)
		}
	}
	ciExec(t, f.db, `UPDATE payments SET status='pending',settled=FALSE,expires_at=clock_timestamp()-INTERVAL '1 hour',checkout_url='https://checkout.stripe.com/c/pay/expired' WHERE id=$1`, f.payment)
	before = returnFinancialState(t, f.db)
	member, _ := ciUser(t, f.db)
	ciExec(t, f.db, `INSERT INTO organization_members(organization_id,user_id,role)VALUES($1,$2,'member')`, f.tokens.Company, member.ID)
	login := ciStart(t)
	login.Mode = "login"
	login.InstallationProof = f.start.InstallationProof
	login.PreviousGrantID = f.tokens.GrantID
	login.GrantCredential = f.tokens.InstallationCredential
	tokens := ciIssue(t, f.r, member, login)
	tokens.InstallationCredential = f.tokens.InstallationCredential
	memberFixture := &returnFixture{f.db, f.r, member, login, tokens, f.payment}
	if w := send(memberFixture, true, "GET", path, value, nil); w.Code != 403 {
		t.Fatal("browser borrowed key recipient management", w.Code, w.Body.String())
	}
	if w := send(memberFixture, false, "GET", path, value, nil); w.Code != 200 {
		t.Fatal("SDK lost actual key recipient policy", w.Code, w.Body.String())
	}
	// QR reads are now admitted, but this Stripe payment has no QR data.
	if w := send(f, true, "GET", path+"/qr", "", nil); w.Code != 404 {
		t.Fatal("unavailable QR must remain a read-only not-found result", w.Code, w.Body.String())
	}
	for _, suffix := range []string{"/checkout", "/refresh", "/invoice"} {
		if w := send(f, true, "GET", path+suffix, "", nil); w.Code != 403 {
			t.Fatal("pending neighbor admitted", suffix, w.Code)
		}
	}
	for _, method := range []string{"POST", "DELETE", "PATCH"} {
		if w := send(f, true, method, path, "", nil); w.Code != 403 {
			t.Fatal("mutation route admitted", method, w.Code)
		}
	}
	for _, bad := range []string{strings.Replace(path, f.tokens.Company, uuid.NewString(), 1), path + "?success=1", path + "?return_url=https://evil.invalid"} {
		if w := send(f, true, "GET", bad, "", nil); w.Code != 403 {
			t.Fatal("substitution", w.Code, w.Body.String())
		}
	}
	if w := send(f, true, "GET", path, "", func(r *http.Request) { r.Header.Set(ci.LocalOriginHeader, "https://foreign.invalid") }); w.Code != 403 {
		t.Fatal("origin bypass")
	}
	if w := send(f, true, "GET", path, "invalid-explicit-key", nil); w.Code != 403 {
		t.Fatal("key fallback")
	}
	if err = f.r.Logout(ciHost(), ciCredentials(f.start, f.tokens)); err != nil {
		t.Fatal(err)
	}
	if w := send(f, true, "GET", path, value, nil); w.Code != 403 {
		t.Fatal("logged-out browser borrowed key")
	}
	if w := send(f, false, "GET", path, value, nil); w.Code != 200 {
		t.Fatal("browser logout revoked independent SDK key", w.Code, w.Body.String())
	}
	if probe.mutations != 0 || psp.checkout != 0 || psp.reconcile != 0 {
		t.Fatal("navigation entered financial workflow", probe.mutations, psp)
	}
	if after := returnFinancialState(t, f.db); after != before {
		t.Fatal("navigation changed payment/ledger/invoice")
	}
}

// The ordinary central route is the actual target of the existing portal's
// navigation reads. Observe persisted rows as well as adapter call counts.
func TestCompanyPaymentOrdinaryNavigationLeavesFinancialStateUnchanged(t *testing.T) {
	f := paymentReturnFixture(t)
	sys := NewSystemRepository(f.db)
	cipher, _ := providerconfig.NewCipher(strings.Repeat("n", 32))
	psp := &companyContinuePSP{}
	funding := payments.New(sys, cipher, ciTestCentral, map[string]payments.Adapter{"stripe": psp})
	keys := NewAPIKeyRepository(f.db)
	k, value, e := apikey.Generate(apikey.CreateInput{UserID: f.u.ID, OrganizationID: f.tokens.Company, Name: "Navigation fixture", ResourceMode: "shared", StorageMode: "shared", Scopes: []string{apikey.Generation, apikey.OrganizationsManage}}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if e = keys.Create(identity.WithUser(ciHost(), f.u), f.u.ID, k, apikey.Hash(value)); e != nil {
		t.Fatal(e)
	}
	h := api.NewGateway(api.Dependencies{Health: admissionHealth{}, System: sys, Payments: funding, Keys: keys})
	base := "/v1/organizations/" + f.tokens.Company
	for _, state := range []string{"pending", "confirming", "cancelled", "failed"} {
		ciExec(t, f.db, `UPDATE payments SET status=$2,expires_at=clock_timestamp()-INTERVAL '1 hour',checkout_url='https://checkout.stripe.com/c/pay/old' WHERE id=$1`, f.payment, state)
		before := returnFinancialState(t, f.db)
		for repeat := 0; repeat < 3; repeat++ {
			for _, suffix := range []string{"/payment-methods", "/payments?offset=0", "/payments/" + f.payment, "/balance-history?offset=0"} {
				for _, method := range []string{"GET", "HEAD"} {
					req := httptest.NewRequest(method, ciTestCentral+base+suffix, nil)
					req.Header.Set("Authorization", "Bearer "+value)
					w := httptest.NewRecorder()
					h.ServeHTTP(w, req)
					if w.Code != 200 {
						t.Fatal(state, method, suffix, w.Code, w.Body.String())
					}
				}
			}
		}
		if before != returnFinancialState(t, f.db) {
			t.Fatal("return/reload/poll changed persisted financial state", state)
		}
	}
	if psp.checkout != 0 || psp.reconcile != 0 {
		t.Fatal("navigation called payment adapter", psp)
	}
}

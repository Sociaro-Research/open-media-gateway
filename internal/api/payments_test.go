package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

const fixturePayment = "44444444-4444-4444-8444-444444444444"
const fixturePaymentProvider = "55555555-5555-4555-8555-555555555555"

type paymentAPIFixture struct {
	payments.Repository
	externalCalls int
	vatWrites     int
	invoiceCalls  int
	paymentState  *payments.Payment
}

func (f *paymentAPIFixture) PaymentProviders(context.Context) ([]payments.Provider, error) {
	return []payments.Provider{{ID: fixturePaymentProvider, Kind: "stripe", Name: "Stripe", FeeMode: "service", Live: true, Enabled: true}, {ID: fixtureOtherOrg, Kind: "stripe", Name: "Test Stripe", Live: false, Enabled: true}, {ID: fixturePayment, Kind: "stablecoin", Name: "Stablecoins", Live: true, Enabled: true, Stablecoin: &payments.StablecoinConfig{Wallets: []payments.StablecoinWallet{{Network: "ethereum", Address: "0x1111111111111111111111111111111111111111", Tokens: []string{"USDC"}}}}}}, nil
}
func (f *paymentAPIFixture) Payment(context.Context, string) (payments.Payment, error) {
	if f.paymentState != nil {
		return *f.paymentState, nil
	}
	return payments.Payment{ID: fixturePayment, OrganizationID: fixtureOrg, Live: true, Stablecoin: &payments.StablecoinPayment{Address: "0x1111111111111111111111111111111111111111"}}, nil
}
func (f *paymentAPIFixture) Payments(context.Context, string, int) ([]payments.Payment, error) {
	return []payments.Payment{}, nil
}
func (f *paymentAPIFixture) ExternalSettlement(context.Context, string, []byte, payments.ExternalInput) (payments.Payment, error) {
	f.externalCalls++
	return payments.Payment{}, payments.ErrForbidden
}

func (f *paymentAPIFixture) VATSettings(context.Context) (payments.VATSettings, error) {
	return payments.VATSettings{Version: 1, Rules: []payments.VATRule{}}, nil
}
func (f *paymentAPIFixture) SaveVATSettings(_ context.Context, _ string, s payments.VATSettings) (payments.VATSettings, error) {
	f.vatWrites++
	s.Version++
	return s, nil
}
func (f *paymentAPIFixture) VATVerification(context.Context, string) (payments.VATVerification, error) {
	return payments.VATVerification{}, nil
}
func (f *paymentAPIFixture) SaveVATVerification(_ context.Context, _, _ string, v payments.VATVerification) (payments.VATVerification, error) {
	f.vatWrites++
	v.Version++
	return v, nil
}

type paymentOrgFixture struct {
	*organizationFixture
	role string
}

func (f *paymentOrgFixture) OrganizationAccess(context.Context, string, string) (string, error) {
	return f.role, nil
}
func TestPaymentEndpointAuthorization(t *testing.T) {
	s, _ := newPersonalTestServer()
	orgs := &paymentOrgFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: "admin"}
	orgs.settings.SharedResources = true
	s.system = orgs
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	repo := &paymentAPIFixture{}
	s.payments = payments.New(repo, cipher, "https://gateway.test", nil)
	h := s.handler()
	for _, tc := range []struct {
		session string
		want    int
	}{{"", 401}, {"bob", 403}, {"alice", 200}} {
		w := sendTest(h, "GET", "/v1/system/payment-providers", "", tc.session, "")
		if w.Code != tc.want {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "token_hash") || strings.Contains(w.Body.String(), "secret_cipher") {
			t.Fatal("secret field exposed")
		}
	}
	for _, role := range []string{"member", "client_manager", "admin", "system_admin"} {
		orgs.role = role
		w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/payment-methods", "", "alice", "")
		want := 403
		if role == "admin" || role == "system_admin" {
			want = 200
		}
		if w.Code != want {
			t.Fatal(role, w.Code)
		}
		if role == "admin" && strings.Contains(w.Body.String(), "Test Stripe") {
			t.Fatal("test method exposed")
		}
		if role == "system_admin" && !strings.Contains(w.Body.String(), "Test Stripe") {
			t.Fatal("system admin cannot test")
		}
	}
	orgs.role = "admin"
	qr := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/payments/"+fixturePayment+"/qr", "", "alice", "")
	if qr.Code != 200 || qr.Header().Get("Content-Type") != "image/png" || qr.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(qr.Body.String(), "\x89PNG") {
		t.Fatal("private QR image", qr.Code, qr.Header())
	}

	for _, session := range []string{"bob", "alice"} {
		want := 403
		if session == "alice" {
			want = 200
		}
		for _, method := range []string{"GET", "PUT"} {
			body := ""
			if method == "PUT" {
				body = `{"version":1}`
			}
			w := sendTest(h, method, "/v1/system/billing", body, session, "")
			if w.Code != want {
				t.Fatal("seller settings authorization", session, method, w.Code, w.Body.String())
			}
		}
	}
	orgs.role = "admin"
	for _, session := range []string{"bob", "alice"} {
		want := 403
		if session == "alice" {
			want = 200
		}
		w := sendTest(h, "PUT", "/v1/organizations/"+fixtureOrg+"/vat-verification", `{"version":0,"verified":true}`, session, "")
		if w.Code != want {
			t.Fatal("buyer verified own VAT", session, w.Code, w.Body.String())
		}
	}
	if repo.vatWrites != 2 {
		t.Fatal("unauthorized VAT write reached repository", repo.vatWrites)
	}

	// A country/status rule table can legitimately exceed the default 8 KiB
	// settings limit. Other system endpoints retain their original body cap.
	large := payments.VATSettings{Version: 1, Seller: payments.SellerCompany{LegalName: "Seller Ltd", Address: payments.Address{Country: "GB", Line1: "1 Seller Road", City: "London"}, VATRegistered: true, VATNumber: "GB123456789"}}
	for a := 'A'; a <= 'Z'; a++ {
		for b := 'A'; b <= 'Z'; b++ {
			country := string([]rune{a, b})
			if payments.CountryCode(country) {
				large.Rules = append(large.Rules, payments.VATRule{Country: country, Status: "any", Treatment: "standard", Rate: 1900})
			}
		}
	}
	body, _ := json.Marshal(large)
	if len(body) <= 8192 {
		t.Fatal("large VAT fixture too small")
	}
	if w := sendTest(h, "PUT", "/v1/system/billing", string(body), "alice", ""); w.Code != 200 {
		t.Fatal("large VAT policy rejected", w.Code, w.Body.String())
	}
	orgs.role = "member"
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/vat-verification", "", "alice", ""); w.Code != 403 {
		t.Fatal("member read private verification", w.Code)
	}
	orgs.role = "member"
	if w := sendTest(h, "DELETE", "/v1/organizations/"+fixtureOrg+"/payments/"+fixturePayment, "", "alice", ""); w.Code != 403 {
		t.Fatal("member cancelled payment", w.Code)
	}
	orgs.role = "admin"
	if w := sendTest(h, "DELETE", "/v1/organizations/"+fixtureOtherOrg+"/payments/"+fixturePayment, "", "alice", ""); w.Code != 403 {
		t.Fatal("cross-tenant cancellation", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg+"/payments/"+fixturePayment, "", "alice", ""); w.Code != 403 {
		t.Fatal("cross-tenant read", w.Code)
	}
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generation","organization_id":"`+fixtureOrg+`"}`)
	_, manager := issueTestKey(t, h, "alice", `{"name":"Manager","organization_id":"`+fixtureOrg+`","scopes":["generation","organizations:manage"]}`)
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/payments", "", "", generation); w.Code != 403 {
		t.Fatal("generation scope accessed finance", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg+"/payments", "", "", manager); w.Code != 403 {
		t.Fatal("key crossed organization", w.Code)
	}
	if w := sendTest(h, "GET", "/v1/organizations/"+fixtureOrg+"/payments", "", "", manager); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{"", generation, manager, "service-secret"} {
		w := sendTest(h, "POST", "/v1/payment-integrations/"+fixturePaymentProvider+"/settlements", `{}`, "alice", secret)
		if w.Code != 403 {
			t.Fatal("browser/key settlement", w.Code)
		}
	}
	if repo.externalCalls != 0 {
		t.Fatal("untrusted credentials reached settlement")
	}
	// Browser mutations remain protected even for an authenticated system administrator.
	req := httptest.NewRequest("POST", "https://gateway.test/v1/system/payment-integrations", strings.NewReader(`{"name":"Attack","live":true}`))
	req.Header.Set("Origin", "https://evil.test")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.AddCookie(&http.Cookie{Name: "test_session", Value: "alice"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross-site payment administration", w.Code)
	}
}

func (f *paymentAPIFixture) BeginLocalCard(context.Context, string, string, string, int64, string, string, payments.BillingProfile, *payments.VATCalculator) (payments.Payment, error) {
	return payments.Payment{}, payments.ErrUnavailable
}
func (f *paymentAPIFixture) LocalInvoice(_ context.Context, _ string, _ func(payments.Payment, string, time.Time) ([]byte, error)) (payments.InvoiceDocument, error) {
	f.invoiceCalls++
	return payments.InvoiceDocument{Number: "INV-2026-000001", PDF: []byte("%PDF-fixture")}, nil
}
func TestInvoiceDownloadAuthorization(t *testing.T) {
	s, _ := newPersonalTestServer()
	orgs := &paymentOrgFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: "admin"}
	orgs.settings.SharedResources = true
	s.system = orgs
	repo := &paymentAPIFixture{}
	s.payments = payments.New(repo, nil, "https://gateway.test", nil)
	h := s.handler()
	endpoint := "/v1/organizations/" + fixtureOrg + "/payments/" + fixturePayment + "/invoice"
	w := sendTest(h, "GET", endpoint, "", "alice", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Disposition"), "INV-2026-000001.pdf") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	orgs.role = "member"
	if w = sendTest(h, "GET", endpoint, "", "bob", ""); w.Code != 403 {
		t.Fatal("member downloaded invoice", w.Code)
	}
	orgs.role = "admin"
	if w = sendTest(h, "GET", "/v1/organizations/"+fixtureOtherOrg+"/payments/"+fixturePayment+"/invoice", "", "alice", ""); w.Code != 403 {
		t.Fatal("cross-tenant invoice", w.Code)
	}
	if w = sendTest(h, "GET", endpoint, "", "", ""); w.Code != 401 {
		t.Fatal("anonymous invoice", w.Code)
	}
	for _, session := range []string{"", "bob"} {
		orgs.role = "member"
		w = sendTest(h, "POST", strings.TrimSuffix(endpoint, "/invoice")+"/checkout", "", session, "")
		if w.Code != 401 && w.Code != 403 {
			t.Fatal("unauthorized invoice checkout", w.Code)
		}
	}
	orgs.role = "admin"
	w = sendTest(h, "POST", "/v1/organizations/"+fixtureOtherOrg+"/payments/"+fixturePayment+"/checkout", "", "alice", "")
	if w.Code != 403 {
		t.Fatal("cross-tenant invoice checkout", w.Code)
	}
	past := time.Now().Add(-time.Minute)
	repo.paymentState = &payments.Payment{ID: fixturePayment, OrganizationID: fixtureOrg, ProviderID: fixturePaymentProvider, InvoiceMode: "local", ExpiresAt: &past, Status: "expired", Live: true}
	w = sendTest(h, "POST", strings.TrimSuffix(endpoint, "/invoice")+"/checkout", "", "alice", "")
	if w.Code != 410 || !strings.Contains(w.Body.String(), "payment_expired") {
		t.Fatal("expired invoice reopened", w.Code, w.Body.String())
	}
	if repo.invoiceCalls != 1 {
		t.Fatal("unauthorized invoice reached repository", repo.invoiceCalls)
	}
}

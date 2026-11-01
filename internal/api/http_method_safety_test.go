package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/auth"
	"github.com/Sociaro-Research/open-media-gateway/internal/branding"
	"github.com/Sociaro-Research/open-media-gateway/internal/core"
	"github.com/Sociaro-Research/open-media-gateway/internal/modelaccess"
	"github.com/Sociaro-Research/open-media-gateway/internal/money"
	org "github.com/Sociaro-Research/open-media-gateway/internal/organization"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type methodOrgFixture struct {
	*rolePolicyFixture
	name   string
	writes int
}

func (f *methodOrgFixture) Organizations(context.Context, string, bool) ([]org.Organization, error) {
	return []org.Organization{{ID: fixtureOrg, Name: f.name, Role: f.role}, {ID: fixtureOtherOrg, Name: "Other", Role: f.role}}, nil
}
func (f *methodOrgFixture) OrganizationSettings(_ context.Context, _, _, name string, _ money.USD, _ *money.USD, _ bool, _ ...*money.Percent) error {
	if !org.Manage(f.role) {
		return org.ErrForbidden
	}
	f.writes++
	f.name = name
	return nil
}
func (f *methodOrgFixture) CreateOrganization(context.Context, string, string, string, string) (org.Organization, error) {
	f.writes++
	return org.Organization{ID: fixtureOtherOrg}, nil
}
func (f *methodOrgFixture) OrganizationCredits(context.Context, string, string, int) ([]org.Credit, error) {
	return []org.Credit{}, nil
}
func (f *methodOrgFixture) OrganizationMembers(context.Context, string, string) ([]org.Member, error) {
	return []org.Member{}, nil
}
func (f *methodOrgFixture) OrganizationInvites(context.Context, string, string) ([]org.Invite, error) {
	return []org.Invite{}, nil
}

func methodOrg() *methodOrgFixture {
	f := &methodOrgFixture{rolePolicyFixture: &rolePolicyFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: org.Supermanager}, name: "Original"}
	f.settings.SharedResources = true
	return f
}

type methodModelFixture struct {
	*modelAccessMemory
	writes int
}

func (f *methodModelFixture) Organization(context.Context, string, string) (modelaccess.Access, error) {
	return modelaccess.Access{ModelIDs: []string{}, Version: 1}, nil
}
func (f *methodModelFixture) SaveOrganization(_ context.Context, _, _ string, in modelaccess.Access) (modelaccess.Access, error) {
	f.writes++
	in.Version++
	return in, nil
}

type methodPaymentFixture struct {
	*paymentAPIFixture
	provider                 payments.Provider
	saves, begins, checkouts int
	payment                  payments.Payment
}

func (f *methodPaymentFixture) SavePaymentProvider(_ context.Context, _ string, p payments.Provider) (payments.Provider, error) {
	f.saves++
	return p, nil
}
func (f *methodPaymentFixture) PaymentProvider(context.Context, string) (payments.Provider, error) {
	return f.provider, nil
}
func (f *methodPaymentFixture) BeginLocalCard(_ context.Context, actor, organization, provider string, cents int64, key, mode string, _ payments.BillingProfile, _ *payments.VATCalculator) (payments.Payment, error) {
	f.begins++
	f.payment = payments.Payment{ID: fixturePayment, OrganizationID: organization, ProviderID: provider, AmountCents: cents, ActorID: actor, IdempotencyKey: key, FeeMode: mode, CreatedAt: time.Now(), Status: "pending"}
	return f.payment, nil
}
func (f *methodPaymentFixture) WithPayment(ctx context.Context, _ string, fn func(payments.Payment) (payments.Snapshot, error)) (payments.Payment, error) {
	s, e := fn(f.payment)
	f.payment.SessionID = s.SessionID
	return f.payment, e
}

type methodPaymentAdapter struct {
	payments.Adapter
	repo *methodPaymentFixture
}

func (a methodPaymentAdapter) Checkout(context.Context, payments.Provider, payments.Credentials, payments.Payment, string) (payments.Snapshot, error) {
	a.repo.checkouts++
	return payments.Snapshot{SessionID: "fixture", Status: "pending"}, nil
}

// Covers every confirmed public-1.0.1 fallthrough path, plus the new development
// projects collection. Real provider/payment transports are never used.
func TestSafeMethodsNeverInvokeDomainWrites(t *testing.T) {
	paths := []string{
		"/v1/organizations", "/v1/organizations/" + fixtureOrg, "/v1/organizations/" + fixtureOrg + "/credits",
		"/v1/organizations/" + fixtureOrg + "/storage", "/v1/organizations/" + fixtureOrg + "/model-access",
		"/v1/system/branding", "/v1/system/catalog/settings", "/v1/system/email",
		"/v1/system/markups", "/v1/organizations/" + fixtureOrg + "/markups",
		"/v1/system/limits", "/v1/organizations/" + fixtureOrg + "/limits", "/v1/system/settings",
		"/v1/system/payment-providers", "/v1/organizations/" + fixtureOrg + "/payments", "/v1/projects",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			s, _ := newPersonalTestServer()
			f := methodOrg()
			s.system = f
			body, writeMethod := `{"name":"Changed","default_weekly_limit_usd":10}`, "PUT"
			snapshot := func() string { return fmt.Sprint(f.name, f.writes, f.credits) }
			switch {
			case path == "/v1/organizations":
				body = `{"name":"New","admin_user":"alice","domain":"fixture.test"}`
				writeMethod = "POST"
			case strings.HasSuffix(path, "/credits"):
				body = `{"amount_usd":10,"kind":"payment","external_id":"fixture"}`
				writeMethod = "POST"
			case strings.HasSuffix(path, "/storage"):
				a := &storageAdminStub{}
				s.storageAdmin = a
				body = `{"driver":"gateway"}`
				snapshot = func() string { return fmt.Sprintf("%+v/%d", a.update, a.checks) }
			case strings.HasSuffix(path, "/model-access"):
				a := &methodModelFixture{modelAccessMemory: &modelAccessMemory{org: map[string]map[string]bool{}}}
				s.modelAccess = a
				body = `{"model_ids":[],"version":1}`
				snapshot = func() string { return fmt.Sprint(a.writes) }
			case path == "/v1/system/branding":
				a := &brandingFixture{systemFixture: f.systemFixture, brand: branding.Defaults()}
				s.system = a
				next := a.brand
				next.Name = "Changed"
				b, _ := json.Marshal(next)
				body = string(b)
				snapshot = func() string { return fmt.Sprint(a.brand.Name, a.brand.Version) }
			case path == "/v1/system/catalog/settings":
				a := &catalogAPIFixture{}
				a.settings.Timezone = "UTC"
				a.settings.Version = 1
				s.catalogSync = a
				body = `{"timezone":"UTC","email":"fixture@example.test","version":1}`
				snapshot = func() string { return fmt.Sprint(a.settings) }
			case path == "/v1/system/email":
				a := &emailSettingsFixture{record: auth.DeliveryRecord{Version: 1}}
				cipher, _ := providerconfig.NewCipher(strings.Repeat("k", 32))
				s.emailDelivery = auth.NewDeliveryManager(a, cipher)
				body = `{"provider":"","from":"","version":1}`
				snapshot = func() string { return fmt.Sprint(a.record.Version) }
			case strings.HasSuffix(path, "/markups"):
				a := &markupFixture{organizationFixture: f.organizationFixture}
				_ = json.Unmarshal([]byte(`{"system":{"default_percent":0,"exceptions":[],"version":1},"company":{"default_percent":null,"exceptions":[],"version":1}}`), &a.policy)
				s.system = a
				body = `{"default_percent":0,"exceptions":[],"version":1}`
				snapshot = func() string { return fmt.Sprint(a.writes) }
			case strings.HasSuffix(path, "/limits"):
				a := newLimitsFixture()
				s.system = a
				b, _ := json.Marshal(a.policy.System)
				body = string(b)
				snapshot = func() string { return fmt.Sprint(a.writes) }
			case path == "/v1/system/settings":
				body = `{"generation_ui":false,"personal_providers":true,"version":1}`
				snapshot = func() string { return fmt.Sprint(f.settings) }
			case path == "/v1/system/payment-providers" || strings.HasSuffix(path, "/payments"):
				a := &methodPaymentFixture{paymentAPIFixture: &paymentAPIFixture{}}
				cipher, _ := providerconfig.NewCipher(strings.Repeat("p", 32))
				encrypted, _ := cipher.Encrypt([]byte(`{"secret_key":"sk_test_fixture"}`))
				a.provider = payments.Provider{ID: fixturePaymentProvider, Kind: "stripe", Enabled: true, SecretCipher: encrypted}
				s.payments = payments.New(a, cipher, "https://gateway.test", map[string]payments.Adapter{"stripe": methodPaymentAdapter{repo: a}})
				snapshot = func() string { return fmt.Sprint(a.saves, a.begins, a.checkouts) }
				writeMethod = "POST"
				body = `{"kind":"stablecoin","name":"Fixture","enabled":false,"stablecoin":{"wallets":[{"network":"ethereum","address":"0x1111111111111111111111111111111111111111","tokens":["USDC"]}]}}`
				if strings.HasSuffix(path, "/payments") {
					body = `{"provider_id":"` + fixturePaymentProvider + `","amount_usd":10,"fee_mode":"service"}`
				}
			case path == "/v1/projects":
				a := &methodProjectFixture{projectAPIService: &projectAPIService{openAIServiceFixture: openAIFixture()}}
				s.service = a
				body = `{"name":"Changed"}`
				writeMethod = "POST"
				snapshot = func() string { return fmt.Sprint(a.creates) }
			}
			h := s.handler()
			_, full := issueTestKey(t, h, "alice", `{"name":"Full fixture","scopes":["generation","organizations:manage","system:manage","keys:manage","providers:manage","storage:manage"]}`)
			before := snapshot()
			for _, method := range []string{"GET", "HEAD"} {
				for _, payload := range []string{body, ""} {
					w := methodSend(h, method, path, payload, "", full)
					if snapshot() != before {
						t.Fatalf("%s mutated state: %s -> %s (status %d)", method, before, snapshot(), w.Code)
					}
					if w.Code != 200 {
						t.Fatalf("%s %q: %d %s", method, payload, w.Code, w.Body)
					}
					if method == "HEAD" && w.Body.Len() != 0 {
						t.Fatal("HEAD returned a body")
					}
				}
			}
			for _, method := range []string{"OPTIONS", "TRACE", "CONNECT", "PATCH"} {
				w := methodSend(h, method, path, body, "", full)
				if w.Code != 405 {
					t.Fatalf("unsupported %s: %d", method, w.Code)
				}
				if w.Header().Get("Allow") == "" || snapshot() != before {
					t.Fatal("unsupported method changed state or omitted Allow")
				}
			}
			// Method override headers must never turn a safe request into a write.
			for _, method := range []string{"GET", "HEAD"} {
				r := httptest.NewRequest(method, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer "+full)
				r.Header.Set("X-HTTP-Method-Override", writeMethod)
				r.Header.Set("X-Method-Override", writeMethod)
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != 200 || snapshot() != before {
					t.Fatal("method override changed read semantics", w.Code)
				}
			}
			w := methodSend(h, writeMethod, path, body, "", full)
			if w.Code < 200 || w.Code >= 300 || snapshot() == before {
				t.Fatalf("legitimate %s failed: %d %s; %s -> %s", writeMethod, w.Code, w.Body, before, snapshot())
			}
		})
	}
}

type methodProjectFixture struct {
	*projectAPIService
	creates int
}

func (f *methodProjectFixture) CreateProject(ctx context.Context, name string) (core.Project, error) {
	f.creates++
	return f.projectAPIService.CreateProject(ctx, name)
}

func methodSend(h http.Handler, method, path, body, session, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Idempotency-Key", "method-fixture")
	if session != "" {
		r.AddCookie(&http.Cookie{Name: "test_session", Value: session})
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

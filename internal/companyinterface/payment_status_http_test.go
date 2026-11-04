package companyinterface_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sociaro-Research/open-media-gateway/internal/api"
	ci "github.com/Sociaro-Research/open-media-gateway/internal/companyinterface"
	"github.com/Sociaro-Research/open-media-gateway/internal/installation"
	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/postgres"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/google/uuid"
)

// Real local facade -> TLS transport -> real central handler/repository. This
// reserved-adapter fixture is not ordinary /v1 or full-portal activation proof.
type paymentStatusLogout struct {
	*postgres.CompanyInterfaceLifecycle
	after func()
}

func (r *paymentStatusLogout) CompanyPaymentStatus(ctx context.Context, id string) (payments.Payment, error) {
	p, e := r.CompanyInterfaceLifecycle.CompanyPaymentStatus(ctx, id)
	if e == nil && r.after != nil {
		r.after()
	}
	return p, e
}
func TestCompanyPaymentStatusTwoOriginFacadeRecoveryAndLogout(t *testing.T) {
	for _, boundary := range []string{"central_before_release", "local_during_transfer"} {
		t.Run(boundary, func(t *testing.T) {
			f, wire := facadeHTTP(t)
			v := f.current(t)
			provider, id := uuid.NewString(), uuid.NewString()
			if _, e := f.centralDB.Exec(`INSERT INTO payment_providers(id,kind,name,enabled,live,account_id,created_by) VALUES($1,'stripe','Fixture',TRUE,TRUE,'fixture',$2)`, provider, v.Tokens.Actor); e != nil {
				t.Fatal(e)
			}
			link := "https://checkout.stripe.com/c/pay/fixture?sig=a%2Fb&token=x%2By"
			if _, e := f.centralDB.Exec(`INSERT INTO payments(id,organization_id,provider_id,actor_id,idempotency_key,fee_mode,amount_cents,currency,live,checkout_url,expires_at) VALUES($1,$2,$3,$4,'fixture','service',1000,'usd',TRUE,$5,clock_timestamp()+INTERVAL '1 day')`, id, v.Tokens.Company, provider, v.Tokens.Actor, link); e != nil {
				t.Fatal(e)
			}
			cipher, e := providerconfig.NewCipher(strings.Repeat("r", 32))
			if e != nil {
				t.Fatal(e)
			}
			funding := payments.New(postgres.NewSystemRepository(f.centralDB), cipher, fixtureCentral, nil)
			f.central = mutationGateway(t, f, func(d *api.Dependencies) { d.Payments = funding })
			path := "/v1/organizations/" + v.Tokens.Company + "/payments/" + id
			snapshot := func() string {
				var s string
				if e := f.centralDB.QueryRow(`SELECT jsonb_build_object('payments',(SELECT jsonb_agg(to_jsonb(p))FROM payments p),'credits',(SELECT jsonb_agg(to_jsonb(c))FROM organization_credits c),'invoice',(SELECT jsonb_agg(to_jsonb(i))FROM payment_invoice_documents i))::text`).Scan(&s); e != nil {
					t.Fatal(e)
				}
				return s
			}
			before := snapshot()
			for _, method := range []string{"GET", "HEAD"} {
				w := facadeRequest(t, f, method, path, "", "", true, nil)
				expectFixture(t, w, 200)
				if w.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("cacheable private status")
				}
				if method == "GET" {
					var p payments.Payment
					if e = json.Unmarshal(w.Body.Bytes(), &p); e != nil || p.ID != id || p.CheckoutURL != link {
						t.Fatal("typed response changed signed link", p, e)
					}
				} else if w.Body.Len() != 0 {
					t.Fatal("HEAD body")
				}
			}
			wire.lose.Store(true)
			expectFixture(t, facadeRequest(t, f, "GET", path, "", "", true, nil), 503)
			expectFixture(t, facadeRequest(t, f, "GET", path, "", "", true, nil), 200)
			if snapshot() != before {
				t.Fatal("read retry mutated central payment state")
			}
			if countFixture(t, f.localDB, "payments") != 0 || countFixture(t, f.localDB, "organization_credits") != 0 {
				t.Fatal("local shadow financial data")
			}
			for _, p := range []string{path + "/refresh", "/v1/payments/webhooks/" + provider, "/v1/payment-integrations/" + provider + "/settlements"} {
				expectFixture(t, facadeRequest(t, f, "POST", p, "{}", "", true, nil), 403)
			}
			central, e := postgres.NewCompanyInterfaceLifecycle(f.centralDB, fixtureMaster, fixtureCentral)
			if e != nil {
				t.Fatal(e)
			}
			if boundary == "local_during_transfer" {
				wire.after = func() {
					wire.after = nil
					if _, e := f.repo.BeginLocalLogout(context.Background(), f.browser.Value, v.Epoch); e != nil {
						t.Fatal(e)
					}
				}
			} else {
				wrapped := &paymentStatusLogout{CompanyInterfaceLifecycle: central, after: func() {
					trusted := installation.WithContext(context.Background(), installation.Metadata{Role: "central", CentralOrigin: fixtureCentral})
					if e := central.Logout(trusted, ci.Credentials{Installation: v.Tokens.InstallationCredential, Session: v.Tokens.AccessToken, LocalSessionHash: v.Tokens.LocalSessionHash}); e != nil {
						t.Fatal(e)
					}
				}}
				f.central = mutationGateway(t, f, func(d *api.Dependencies) { d.Payments = funding; d.CompanyInterface = wrapped })
			}
			w := facadeRequest(t, f, "GET", path, "", "", true, nil)
			if w.Code != 409 && w.Code != 403 {
				t.Fatal("late logged-out viewer response", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), link) || strings.Contains(w.Body.String(), id) {
				t.Fatal("late private payment leaked")
			}
			if snapshot() != before {
				t.Fatal("logout/navigation altered financial state")
			}
		})
	}
}

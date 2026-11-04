package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
	"github.com/Sociaro-Research/open-media-gateway/internal/providerconfig"
	"github.com/Sociaro-Research/open-media-gateway/internal/system"
)

type navigationPayments struct {
	*paymentAPIFixture
	expiryCalls int
}

func (p *navigationPayments) ExpirePayments(context.Context) error {
	p.expiryCalls++
	return payments.ErrConflict
}
func (p *navigationPayments) Payments(context.Context, string, int) ([]payments.Payment, error) {
	return []payments.Payment{*p.paymentState}, nil
}

func TestPaymentNavigationReadsNeverMaintainPayments(t *testing.T) {
	s, _ := newPersonalTestServer()
	orgs := &paymentOrgFixture{organizationFixture: &organizationFixture{systemFixture: &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}}, role: "admin"}
	orgs.settings.SharedResources = true
	s.system = orgs
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	past := time.Now().Add(-time.Hour)
	repo := &navigationPayments{paymentAPIFixture: &paymentAPIFixture{paymentState: &payments.Payment{ID: fixturePayment, OrganizationID: fixtureOrg, Live: true, Status: "pending", ExpiresAt: &past, CheckoutURL: "https://checkout.stripe.com/example", Stablecoin: &payments.StablecoinPayment{Address: "0x1111111111111111111111111111111111111111"}}}}
	s.payments = payments.New(repo, cipher, "https://gateway.test", nil)
	h := s.handler()
	base := "/v1/organizations/" + fixtureOrg
	original, _ := json.Marshal(repo.paymentState)
	for _, suffix := range []string{"/payment-methods", "/payments", "/payments/" + fixturePayment, "/payments/" + fixturePayment + "/qr"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := sendTest(h, method, base+suffix, "", "alice", "")
			if w.Code != 200 {
				t.Fatalf("%s %s: %d %s", method, suffix, w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cacheable payment read")
			}
		}
	}
	if repo.expiryCalls != 0 {
		t.Fatal("navigation invoked maintenance", repo.expiryCalls)
	}
	after, _ := json.Marshal(repo.paymentState)
	if string(original) != string(after) {
		t.Fatal("read mutated payment")
	}
	for _, state := range []string{"pending", "confirming", "cancelled", "failed", "expired", "paid"} {
		repo.paymentState.Status = state
		repo.paymentState.Settled = state == "paid"
		want := state
		if state == "pending" || state == "confirming" {
			want = "expired"
		}
		for _, suffix := range []string{"/payments", "/payments/" + fixturePayment} {
			w := sendTest(h, "GET", base+suffix, "", "alice", "")
			var p payments.Payment
			if suffix == "/payments" {
				var list struct {
					Data []payments.Payment `json:"data"`
				}
				if json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Data) != 1 {
					t.Fatal(w.Body.String())
				}
				p = list.Data[0]
			} else if json.Unmarshal(w.Body.Bytes(), &p) != nil {
				t.Fatal(w.Body.String())
			}
			if w.Code != 200 || p.Status != want {
				t.Fatal(state, suffix, w.Code, w.Body.String())
			}
			if (p.CheckoutURL == "") != (state == "pending" || state == "confirming") {
				t.Fatal("checkout projection", state, p.CheckoutURL)
			}
		}
	}
	// Existing direct-central policy permits a manager to view another creator's
	// payment. Test payments remain system-administrator-only.
	repo.paymentState.ActorID = "another-manager"
	repo.paymentState.Live = false
	if w := sendTest(h, "GET", base+"/payments/"+fixturePayment, "", "alice", ""); w.Code != 403 {
		t.Fatal("test payment exposed", w.Code)
	}
	orgs.role = "system_admin"
	if w := sendTest(h, "GET", base+"/payments/"+fixturePayment, "", "alice", ""); w.Code != 200 {
		t.Fatal("administrator test payment", w.Code)
	}
	orgs.role = "member"
	if w := sendTest(h, "GET", base+"/payments/"+fixturePayment, "", "alice", ""); w.Code != 403 {
		t.Fatal("member payment access", w.Code)
	}
	orgs.role = "admin"
	repo.paymentState.Live = true
	repo.paymentState.OrganizationID = fixtureOtherOrg
	if w := sendTest(h, "GET", base+"/payments/"+fixturePayment, "", "alice", ""); w.Code != 403 {
		t.Fatal("foreign payment", w.Code)
	}
	if repo.expiryCalls != 0 {
		t.Fatal("denied navigation maintained payments")
	}
	// Maintenance still belongs to explicit mutation, not navigation.
	repo.paymentState.OrganizationID = fixtureOrg
	if w := sendTest(h, "POST", base+"/payments/"+fixturePayment+"/refresh", "", "alice", ""); w.Code != 409 || repo.expiryCalls != 1 {
		t.Fatal("explicit refresh bypassed maintenance", w.Code, repo.expiryCalls)
	}
}

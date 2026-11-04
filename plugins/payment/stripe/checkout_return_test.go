package stripe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/payments"
)

func TestCheckoutExplicitReturnTargetAndStandaloneParity(t *testing.T) {
	now := time.Now()
	p := payments.Payment{ID: "11111111-1111-4111-8111-111111111111", OrganizationID: "22222222-2222-4222-8222-222222222222", ProviderID: "33333333-3333-4333-8333-333333333333", AmountCents: 5000, Currency: "usd", Live: true, CreatedAt: now}
	var forms []url.Values
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/checkout/sessions" {
			t.Error("unexpected provider operation", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		forms = append(forms, r.PostForm)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		json.NewEncoder(w).Encode(session{ID: "cs_fixture", Mode: "payment", Amount: 5000, Currency: "usd", Live: true, Reference: p.ID, Metadata: map[string]string{"gateway_payment_id": p.ID, "organization_id": p.OrganizationID, "provider_id": p.ProviderID}, URL: "https://checkout.stripe.com/c/pay/fixture"})
	}))
	defer server.Close()
	client := New()
	client.base = server.URL
	var adapter payments.ReturnCheckoutAdapter = client
	if _, err := adapter.CheckoutWithReturn(context.Background(), payments.Provider{}, payments.Credentials{}, p, payments.CheckoutReturn{}); !errors.Is(err, payments.ErrInvalid) || len(forms) != 0 {
		t.Fatal("zero target reached PSP", err, len(forms))
	}
	state := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("n", 32)))
	target, err := payments.CompanyCheckoutReturn("https://console.sociaro.ai", state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.CheckoutWithReturn(context.Background(), payments.Provider{}, payments.Credentials{}, p, target); err != nil {
		t.Fatal(err)
	}
	callback := "https://console.sociaro.ai" + payments.CompanyPaymentReturnPath + "?state=" + state
	if forms[0].Get("success_url") != callback || forms[0].Get("cancel_url") != callback {
		t.Fatal("callback rewritten", forms[0])
	}
	if _, err = client.Checkout(context.Background(), payments.Provider{}, payments.Credentials{}, p, "https://standalone.fixture"); err != nil {
		t.Fatal(err)
	}
	standalone := "https://standalone.fixture/organizations?payment=" + p.ID + "&organization=" + p.OrganizationID
	if forms[1].Get("success_url") != standalone || forms[1].Get("cancel_url") != standalone+"&cancelled=1" {
		t.Fatal("standalone contract changed", forms[1])
	}
	// Only navigation differs; amount, provider metadata, expiry and immutable
	// provider idempotency key must remain identical to the direct adapter call.
	for _, f := range forms {
		f.Del("success_url")
		f.Del("cancel_url")
	}
	if forms[0].Encode() != forms[1].Encode() || keys[0] != p.CheckoutKey() || keys[1] != keys[0] {
		t.Fatal("payment semantics changed", forms, keys)
	}
}

package companyinterface

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"testing"
)

func TestCompanyPaymentStatusTypedLinksAndExactRoute(t *testing.T) {
	company := "11111111-1111-4111-8111-111111111111"
	id := "22222222-2222-4222-8222-222222222222"
	path := "/v1/organizations/" + company + "/payments/" + id
	c := Classify("GET", path)
	if c.ID != "company-payment-status" || !c.BrowserManagement || c.Scope != "organizations:manage" {
		t.Fatal(c)
	}
	p := Principal{Grant: Grant{Company: company}, Kind: CentralKey, Scopes: []string{"organizations:manage"}}
	if _, e := RequestContract("GET", path, "", nil, p); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{path + "/refresh", path + "/invoice", path + "/checkout", "/v1/payments/webhooks/" + id, "/v1/payment-integrations/" + id + "/settlements"} {
		if _, e := RequestContract("GET", bad, "", nil, p); e == nil {
			t.Fatal("neighbor", bad)
		}
	}
	row := map[string]any{"id": id, "organization_id": company, "checkout_url": "https://checkout.stripe.com/c/pay/a?token=a%2Fb&sig=x%2By", "receipt_url": "https://pay.stripe.com/receipts/test?signature=abc%2Fdef", "invoice": map[string]any{"url": "https://invoice.stripe.com/i/test?x=1", "pdf_url": "https://invoice.stripe.com/i/test/pdf?s=a%2Bb"}}
	raw, _ := json.Marshal(row)
	got, e := paymentJSONResponse(c, 200, "GET", path, company, raw)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("signed link altered", e)
	}
	for _, bad := range []string{"javascript:alert(1)", "http://checkout.stripe.com/a", "https://user:secret@checkout.stripe.com/a", "https://checkout.stripe.com/a#fragment", "//checkout.stripe.com/a", "https://checkout.stripe.com\\evil/a"} {
		row["checkout_url"] = bad
		raw, _ = json.Marshal(row)
		if _, e = paymentJSONResponse(c, 200, "GET", path, company, raw); e == nil {
			t.Fatal("unsafe typed link", bad)
		}
	}
	row["checkout_url"] = ""
	row["organization_id"] = id
	raw, _ = json.Marshal(row)
	if _, e = paymentJSONResponse(c, 200, "GET", path, company, raw); e == nil {
		t.Fatal("foreign payment")
	}
}

func TestCompanyPaymentReadSurfacesExactTypedContracts(t *testing.T) {
	company := "11111111-1111-4111-8111-111111111111"
	id := "22222222-2222-4222-8222-222222222222"
	base := "/v1/organizations/" + company
	p := Principal{Grant: Grant{Company: company}, Kind: CentralKey, Scopes: []string{"organizations:manage"}}
	for _, path := range []string{base + "/payment-methods", base + "/payments", base + "/payments/" + id + "/qr"} {
		for _, method := range []string{"GET", "HEAD"} {
			if _, e := RequestContract(method, path, "", nil, p); e != nil {
				t.Fatal(method, path, e)
			}
		}
		for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			if _, e := RequestContract(method, path, "", nil, p); e == nil {
				t.Fatal("mutation admitted", method, path)
			}
		}
		for _, query := range []string{"organization_id=" + id, "return_url=https://foreign.test", "offset=0&offset=1"} {
			if _, e := RequestContract("GET", path, query, nil, p); e == nil {
				t.Fatal("unexpected query", path, query)
			}
		}
	}
	if _, e := RequestContract("GET", base+"/payments", "offset=50", nil, p); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{base + "/payments/" + id + "/invoice", base + "/payments/" + id + "/qr/extra", base + "/payments/%32" + id[1:] + "/qr"} {
		if _, e := RequestContract("GET", path, "", nil, p); e == nil {
			t.Fatal("neighbor", path)
		}
	}
	row := map[string]any{"id": id, "organization_id": company, "checkout_url": "https://checkout.stripe.com/pay?sig=a%2Fb&x=1"}
	raw, _ := json.Marshal(map[string]any{"data": []any{row}, "has_more": false})
	c := Classify("GET", base+"/payments")
	if got, e := paymentJSONResponse(c, 200, "GET", base+"/payments", company, raw); e != nil || !bytes.Equal(got, raw) {
		t.Fatal("list changed", e)
	}
	row["organization_id"] = id
	raw, _ = json.Marshal(map[string]any{"data": []any{row}})
	if _, e := paymentJSONResponse(c, 200, "GET", base+"/payments", company, raw); e == nil {
		t.Fatal("foreign list row")
	}
	c = Classify("GET", base+"/payment-methods")
	for _, secret := range []bool{false, true} {
		m := map[string]any{"id": id, "kind": "stripe", "name": "Card", "live": true}
		if secret {
			m["secret_cipher"] = "must not escape"
		}
		raw, _ = json.Marshal(map[string]any{"data": []any{m}, "currency": "usd"})
		_, e := paymentJSONResponse(c, 200, "GET", base+"/payment-methods", company, raw)
		if (e != nil) != secret {
			t.Fatal("method fields", secret, e)
		}
	}
	c = Classify("GET", base+"/payments/"+id+"/qr")
	var buf bytes.Buffer
	png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 256, 256)))
	h := http.Header{"Content-Type": []string{"image/png"}}
	if e := paymentBinaryResponse(c, 200, "GET", h, buf.Bytes()); e != nil {
		t.Fatal(e)
	}
	for _, raw := range [][]byte{[]byte("<html>bad</html>"), buf.Bytes()[:33], bytes.Repeat([]byte("x"), 65537)} {
		if e := paymentBinaryResponse(c, 200, "GET", h, raw); e == nil {
			t.Fatal("bad PNG accepted")
		}
	}
	if e := paymentBinaryResponse(c, 200, "HEAD", h, nil); e != nil {
		t.Fatal(e)
	}
	if e := paymentBinaryResponse(c, 200, "GET", http.Header{"Content-Type": []string{"text/html"}}, buf.Bytes()); e == nil {
		t.Fatal("wrong media type")
	}
}

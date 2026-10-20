package payments

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func TestInvoicePDFAndLogo(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 120, 60))
	for y := 0; y < 60; y++ {
		for x := 0; x < 120; x++ {
			img.Set(x, y, color.RGBA{30, 75, 100, 255})
		}
	}
	var logo bytes.Buffer
	if e := png.Encode(&logo, img); e != nil {
		t.Fatal(e)
	}
	asset := &InvoiceLogo{MIME: "image/png", Data: append(logo.Bytes(), []byte("trailing metadata")...)}
	normalized, e := NormalizeInvoiceLogo(asset)
	if e != nil || bytes.Contains(normalized.Data, []byte("trailing metadata")) {
		t.Fatal("logo not re-encoded", e)
	}
	for _, bad := range []*InvoiceLogo{{MIME: "image/svg+xml", Data: []byte("<svg/>")}, {MIME: "image/jpeg", Data: logo.Bytes()}, {MIME: "image/png", Data: []byte("bad png")}, {MIME: "image/png", Data: make([]byte, MaxInvoiceLogoBytes+1)}} {
		if _, e := NormalizeInvoiceLogo(bad); e == nil {
			t.Fatal("accepted invalid logo")
		}
	}
	issued := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	p := Payment{ID: "11111111-1111-4111-8111-111111111111", AmountCents: 5000, Currency: "usd", InvoiceMode: "local", Live: true, CreatedAt: issued, Status: "pending", CheckoutURL: "https://checkout.stripe.com/c/pay/cs_example", InvoicePaymentURL: "https://gateway.test/organizations?payment=11111111-1111-4111-8111-111111111111&organization=22222222-2222-4222-8222-222222222222&pay_invoice=1", FeeCents: 213, FeeMode: "organization", VAT: &VATSnapshot{Seller: SellerCompany{LegalName: "Example Seller Ltd", RegistrationNumber: "REG-001", VATNumber: "GB123456789", Email: "billing@example.test", InvoiceLogo: normalized, Address: Address{Line1: "12 Example Road", City: "London", PostalCode: "SW1A 1AA", Country: "GB"}}, Rule: VATRule{Treatment: "standard", Rate: money.Percent(2000)}}, Billing: &Billing{BusinessName: "Atlas Labs Ltd", Email: "finance@atlas.example", Address: Address{Line1: "24 Sample Avenue", City: "Nicosia", PostalCode: "1010", Country: "CY"}, TaxIDs: []TaxID{{Type: "eu_vat", Value: "CY12345678X"}}, SubtotalCents: 5000, TaxCents: 1000, TotalCents: 6000}}
	for _, kind := range []string{"stripe", "stablecoin", "usdt", "paid-stablecoin", "long"} {
		copy := p
		if kind == "stablecoin" || kind == "usdt" || kind == "paid-stablecoin" {
			copy.Stablecoin = &StablecoinPayment{Network: "ethereum", Token: "USDC", Address: "0x1111111111111111111111111111111111111111", Amount: "60.001234", FeeMicros: 1234}
		}
		if kind == "usdt" {
			copy.Stablecoin.Token = "USDT"
		}
		if kind == "paid-stablecoin" {
			copy.Settled = true
			copy.Status = "paid"
		}
		if kind == "long" {
			vat := *copy.VAT
			seller := vat.Seller
			seller.LegalName = strings.Repeat("Νομικόν πρόσωπον ", 10)
			seller.Address.Line1 = strings.Repeat("Long legal address ", 16)
			seller.Address.Line2 = strings.Repeat("Additional address ", 16)
			seller.Address.City = strings.Repeat("City ", 60)
			seller.Address.State = strings.Repeat("Region ", 40)
			seller.Address.PostalCode = strings.Repeat("Postcode ", 30)
			vat.Seller = seller
			copy.VAT = &vat
		}
		data, e := RenderInvoicePDF(copy, "INV-2026-000001", issued)
		if e != nil || !bytes.HasPrefix(data, []byte("%PDF-")) || !bytes.Contains(data, []byte("/MediaBox [0 0 595.28 841.89]")) {
			t.Fatal(kind, "bad A4 PDF", e)
		}
		if dir := os.Getenv("GATEWAY_INVOICE_PREVIEW_DIR"); dir != "" {
			if e := os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(dir, kind+".pdf"), data, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	if invoiceMoney(1234, true) != "0.001234" || invoiceMoney(60001234, true) != "60.001234" || invoiceMoney(50000000, false) != "50.00" {
		t.Fatal("invoice precision lost")
	}
}

func TestInvoiceThirtyDayTermsAndCheckoutKeys(t *testing.T) {
	for _, issued := range []time.Time{time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), time.Date(2026, 1, 31, 23, 0, 0, 0, time.UTC), time.Date(2028, 2, 10, 0, 0, 0, 0, time.UTC)} {
		due := InvoiceDueDate(issued)
		if due.Sub(issued) != 30*24*time.Hour {
			t.Fatal(issued, due)
		}
	}
	p := Payment{ID: "fixture", CreatedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	if p.CheckoutKey() != "gateway-topup-fixture" || p.CheckoutStart() != p.CreatedAt {
		t.Fatal("legacy checkout changed")
	}
	start := p.CreatedAt.AddDate(0, 0, 20)
	p.CheckoutAttempt = 2
	p.CheckoutStartedAt = &start
	if p.RequestExpiry() != p.CreatedAt.AddDate(0, 0, 30) {
		t.Fatal("request deadline moved with invoice issue or checkout renewal")
	}
	if p.CheckoutKey() != "gateway-topup-fixture-attempt-2" || p.CheckoutStart() != start {
		t.Fatal("renewal key/start")
	}
}

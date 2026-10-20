package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sociaro-Research/open-media-gateway/internal/money"
)

func vatFixture() VATSettings {
	return VATSettings{Version: 2, Seller: SellerCompany{LegalName: "Example Seller Ltd", Address: Address{Country: "CY", Line1: "1 Seller Street", City: "Nicosia"}, VATRegistered: true, VATNumber: "CY12345678X", OSSRegistered: true}, Rules: []VATRule{{"domestic", "any", "standard", 1900}, {"EU", "registered", "reverse_charge", 0}, {"DE", "unregistered", "standard", 1900}, {"FR", "unregistered", "standard", 2000}, {"non_eu", "any", "outside_scope", 0}}}
}

func TestSellerVATNumberValidation(t *testing.T) {
	for _, number := range []string{"", "VAT", "GB12!34", "GB_1234", strings.Repeat("1", 41)} {
		s := vatFixture()
		s.Seller.VATNumber = number
		if !errors.Is(s.Validate(), ErrSellerVATNumber) {
			t.Fatalf("malformed seller VAT number accepted: %q", number)
		}
	}
	for _, number := range []string{"CY12345678X", " gb 123 456 789 ", "123456789"} {
		s := vatFixture()
		s.Seller.VATNumber = number
		if err := s.Validate(); err != nil {
			t.Fatalf("valid number rejected: %q: %v", number, err)
		}
	}
	s := vatFixture()
	s.Seller.VATRegistered, s.Seller.OSSRegistered, s.Seller.VATNumber = false, false, ""
	s.Rules = nil
	if err := s.Validate(); err != nil {
		t.Fatal("company without VAT rejected", err)
	}
}

type vatVerifierFixture struct {
	valid bool
	name  string
	err   error
	calls int
}

func (v *vatVerifierFixture) Verify(_ context.Context, country, number string) (VATVerification, error) {
	v.calls++
	if v.err != nil {
		return VATVerification{}, v.err
	}
	return VATVerification{Country: country, Number: number, Verified: v.valid, BusinessName: v.name, Source: "fixture", VerifiedAt: time.Now()}, nil
}
func TestLocalVATRulesByCountryAndRegistration(t *testing.T) {
	s := vatFixture()
	if err := s.Ready(); err != nil {
		t.Fatal(err)
	}
	verify := &vatVerifierFixture{valid: true, name: "Buyer GmbH"}
	calc := &VATCalculator{Verifier: verify, Now: time.Now}
	p := Payment{AmountCents: 5000}
	profile := BillingProfile{BusinessName: "Buyer GmbH", Address: Address{Country: "CY", Line1: "1 Buyer Street", City: "Nicosia"}}
	for _, test := range []struct {
		country, number, treatment string
		tax                        int64
	}{{"CY", "", "standard", 950}, {"DE", "DE123456789", "reverse_charge", 0}, {"DE", "", "standard", 950}, {"FR", "", "standard", 1000}, {"US", "", "outside_scope", 0}} {
		profile.Address.Country = test.country
		profile.TaxIDs = nil
		if test.number != "" {
			profile.TaxIDs = []TaxID{{Type: "eu_vat", Value: test.number}}
		}
		b, snapshot, err := calc.Quote(context.Background(), s, p, profile, VATVerification{})
		if err != nil || b.TaxCents != test.tax || b.TotalCents != 5000+test.tax || snapshot.Rule.Treatment != test.treatment || snapshot.Seller.LegalName != s.Seller.LegalName {
			t.Fatal(test, b, snapshot, err)
		}
	}
	// No implicit universal zero, no ambiguous duplicate configuration.
	profile.Address.Country = "ES"
	profile.TaxIDs = nil
	if _, _, err := calc.Quote(context.Background(), s, p, profile, VATVerification{}); !errors.Is(err, ErrVATRule) {
		t.Fatal("uncovered country silently zero-rated", err)
	}
	duplicate := s
	duplicate.Rules = append(append([]VATRule{}, s.Rules...), s.Rules[0])
	if duplicate.Validate() == nil {
		t.Fatal("duplicate rule allowed")
	}
	// A country rule overrides a region, while domestic VAT takes precedence
	// over the broad EU reverse-charge rule.
	profile.Address.Country = "CY"
	profile.TaxIDs = []TaxID{{Type: "eu_vat", Value: "CY12345678X"}}
	b, snapshot, err := calc.Quote(context.Background(), s, p, profile, VATVerification{})
	if err != nil || b.TaxCents != 950 || snapshot.Rule.Country != "domestic" {
		t.Fatal("domestic reverse charge", b, snapshot, err)
	}
	// User-entered registration alone cannot bypass tax or name verification.
	verify.valid = false
	profile.Address.Country = "DE"
	profile.TaxIDs = []TaxID{{Type: "eu_vat", Value: "DE123456789"}}
	b, _, err = calc.Quote(context.Background(), s, p, profile, VATVerification{})
	if err != nil || b.TaxCents != 950 {
		t.Fatal("invalid VAT obtained exemption", b, err)
	}
	verify.err = ErrVATUnavailable
	if _, _, err = calc.Quote(context.Background(), s, p, profile, VATVerification{}); !errors.Is(err, ErrVATUnavailable) {
		t.Fatal("outage treated as invalid/zero", err)
	}
	verify.err = nil
	verify.valid = true
	verify.name = "Someone Else Ltd"
	if _, _, err = calc.Quote(context.Background(), s, p, profile, VATVerification{}); !errors.Is(err, ErrVATVerification) {
		t.Fatal("another business VAT accepted", err)
	}
	// Explicit administrator verification supports countries outside VIES.
	verify.calls = 0
	s.Rules = append(s.Rules, VATRule{"GB", "registered", "reverse_charge", 0})
	profile.Address.Country = "GB"
	profile.TaxIDs = []TaxID{{Type: "gb_vat", Value: "GB123456789"}}
	now := time.Now()
	manual := VATVerification{Country: "GB", Number: "GB123456789", BusinessName: profile.BusinessName, Verified: true, Source: "manual", Reference: "Reviewed tax registration", VerifiedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}
	b, snapshot, err = calc.Quote(context.Background(), s, p, profile, manual)
	if err != nil || b.TaxCents != 0 || snapshot.BuyerStatus != "registered" || snapshot.Verification.Source != "manual" || verify.calls != 0 {
		t.Fatal("manual evidence ignored", b, snapshot, err)
	}
	manual.ExpiresAt = now.Add(-time.Hour)
	_, snapshot, err = calc.Quote(context.Background(), s, p, profile, manual)
	if err != nil || snapshot.BuyerStatus != "unverified" || snapshot.Rule.Treatment != "outside_scope" {
		t.Fatal("expired evidence reused", snapshot, err)
	}
	// No seller-specific default: the same installation can use another seller.
	s.Seller.Address.Country = "DE"
	profile.Address.Country = "DE"
	profile.TaxIDs = nil
	b, snapshot, err = calc.Quote(context.Background(), s, p, profile, VATVerification{})
	if err != nil || b.TaxCents != 950 || snapshot.Seller.Address.Country != "DE" {
		t.Fatal("seller country was hardcoded", b, snapshot, err)
	}
}
func TestVATCentRoundingAndConfiguration(t *testing.T) {
	s := vatFixture()
	s.Rules = []VATRule{{Country: "*", Status: "any", Treatment: "standard", Rate: money.Percent(1250)}}
	c := NewVATCalculator()
	p := Payment{AmountCents: 100}
	profile := BillingProfile{BusinessName: "Buyer", Address: Address{Country: "US", Line1: "1 Road", City: "Town"}}
	b, _, err := c.Quote(context.Background(), s, p, profile, VATVerification{})
	if err != nil || b.TaxCents != 13 {
		t.Fatal("half cent rounding", b, err)
	}
	for _, country := range []string{"EU", "ZZ", "AA", "cy", ""} {
		if CountryCode(country) {
			t.Fatal("invalid country", country)
		}
	}
	s.Rules = nil
	if _, _, err = c.Quote(context.Background(), s, p, profile, VATVerification{}); !errors.Is(err, ErrVATSettings) {
		t.Fatal("unconfigured policy accepted", err)
	}
}
func TestVIESResponseValidation(t *testing.T) {
	body := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/"><soap:Body><checkVatResponse xmlns="urn:ec.europa.eu:taxud:vies:services:checkVat:types"><countryCode>DE</countryCode><vatNumber>123456789</vatNumber><requestDate>2026-10-17</requestDate><valid>true</valid><name>Buyer GmbH</name></checkVatResponse></soap:Body></soap:Envelope>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || !strings.HasPrefix(r.Header.Get("User-Agent"), "Sociaro") {
			t.Error("unexpected validation request")
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	v := NewVIESVerifier()
	v.Endpoint = server.URL
	result, err := v.Verify(context.Background(), "DE", "DE123456789")
	if err != nil || !result.Verified || result.BusinessName != "Buyer GmbH" || result.Reference != "2026-10-17" {
		t.Fatal(result, err)
	}
	body = strings.Replace(body, "<valid>true</valid>", "", 1)
	if _, err = v.Verify(context.Background(), "DE", "DE123456789"); !errors.Is(err, ErrVATUnavailable) {
		t.Fatal("incomplete response accepted", err)
	}
}

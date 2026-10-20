package payments

import (
	"context"
	"errors"
	"golang.org/x/text/language"
	"strings"
	"time"
	"unicode"

	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
)

var ErrVATSettings = errors.New("configure the seller company and VAT rules before accepting payments with local VAT")
var ErrVATRule = errors.New("no VAT rule is configured for this buyer country and VAT status")
var ErrVATVerification = errors.New("VAT registration must be verified before using this VAT number; contact an administrator")
var ErrVATUnavailable = errors.New("VAT number verification is temporarily unavailable; retry without changing the payment")
var ErrSellerVATNumber = errors.New("Enter a VAT number using 3–40 letters and digits, including at least one digit.")

type SellerCompany struct {
	Email              string       `json:"email"`
	InvoiceLogo        *InvoiceLogo `json:"invoice_logo,omitempty"`
	LegalName          string       `json:"legal_name"`
	Address            Address      `json:"address"`
	RegistrationNumber string       `json:"registration_number"`
	VATRegistered      bool         `json:"vat_registered"`
	VATNumber          string       `json:"vat_number"`
	OSSRegistered      bool         `json:"oss_registered"`
}
type VATRule struct {
	Country   string        `json:"country"`
	Status    string        `json:"status"`
	Treatment string        `json:"treatment"`
	Rate      money.Percent `json:"rate_percent"`
}
type VATSettings struct {
	Version int64         `json:"version"`
	Seller  SellerCompany `json:"seller"`
	Rules   []VATRule     `json:"rules"`
}

func CountryCode(s string) bool {
	if len(s) != 2 || strings.ToUpper(s) != s {
		return false
	}
	r, err := language.ParseRegion(s)
	return err == nil && r.IsCountry() && r.String() == s
}
func EUCountry(s string) bool {
	return strings.Contains(" AT BE BG HR CY CZ DK EE FI FR DE GR HU IE IT LV LT LU MT NL PL PT RO SK SI ES SE ", " "+s+" ")
}
func (s VATSettings) Validate() error {
	if s.Seller.VATRegistered {
		number := NormalizeVATNumber(s.Seller.VATNumber)
		if number == "" || !strings.ContainsAny(number, "0123456789") {
			return ErrSellerVATNumber
		}
	}
	if err := s.Seller.ValidateInvoiceDetails(); err != nil {
		return err
	}
	if s.Version < 1 || strings.TrimSpace(s.Seller.LegalName) == "" || len(s.Seller.LegalName) > 200 || !CountryCode(s.Seller.Address.Country) || strings.TrimSpace(s.Seller.Address.Line1) == "" || strings.TrimSpace(s.Seller.Address.City) == "" || len(s.Rules) > 500 {
		return ErrVATSettings
	}
	for _, v := range []string{s.Seller.RegistrationNumber, s.Seller.VATNumber, s.Seller.Address.Line1, s.Seller.Address.Line2, s.Seller.Address.City, s.Seller.Address.State, s.Seller.Address.PostalCode} {
		if len(v) > 300 || strings.ContainsAny(v, "\r\n") {
			return ErrVATSettings
		}
	}
	if s.Seller.VATRegistered && strings.TrimSpace(s.Seller.VATNumber) == "" || s.Seller.OSSRegistered && !s.Seller.VATRegistered {
		return ErrVATSettings
	}
	seen := map[string]bool{}
	for _, r := range s.Rules {
		if !CountryCode(r.Country) && r.Country != "domestic" && r.Country != "EU" && r.Country != "non_eu" && r.Country != "*" {
			return ErrVATSettings
		}
		if r.Status != "any" && r.Status != "registered" && r.Status != "unregistered" {
			return ErrVATSettings
		}
		if r.Rate < 0 || r.Rate > 10000 {
			return ErrVATSettings
		}
		if r.Treatment != "standard" && r.Treatment != "reverse_charge" && r.Treatment != "exempt" && r.Treatment != "outside_scope" {
			return ErrVATSettings
		}
		if r.Treatment != "standard" && r.Rate != 0 || r.Treatment == "standard" && r.Rate > 0 && !s.Seller.VATRegistered || r.Treatment == "reverse_charge" && r.Status != "registered" {
			return ErrVATSettings
		}
		key := r.Country + ":" + r.Status
		if seen[key] {
			return ErrVATSettings
		}
		seen[key] = true
	}
	return nil
}
func (s VATSettings) Ready() error {
	if s.Validate() != nil || len(s.Rules) == 0 {
		return ErrVATSettings
	}
	return nil
}

type VATVerification struct {
	Version      int64     `json:"version"`
	Country      string    `json:"country"`
	Number       string    `json:"number"`
	BusinessName string    `json:"business_name"`
	Source       string    `json:"source"`
	Reference    string    `json:"reference"`
	VerifiedAt   time.Time `json:"verified_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	Verified     bool      `json:"verified"`
}
type VATSnapshot struct {
	SettingsVersion int64            `json:"settings_version"`
	Seller          SellerCompany    `json:"seller"`
	Rule            VATRule          `json:"rule"`
	BuyerCountry    string           `json:"buyer_country"`
	BuyerStatus     string           `json:"buyer_status"`
	Verification    *VATVerification `json:"verification,omitempty"`
}
type VATRepository interface {
	VATSettings(context.Context) (VATSettings, error)
	SaveVATSettings(context.Context, string, VATSettings) (VATSettings, error)
	VATVerification(context.Context, string) (VATVerification, error)
	SaveVATVerification(context.Context, string, string, VATVerification) (VATVerification, error)
}
type VATVerifier interface {
	Verify(context.Context, string, string) (VATVerification, error)
}
type VATCalculator struct {
	Verifier VATVerifier
	Now      func() time.Time
}

func NewVATCalculator() *VATCalculator {
	return &VATCalculator{Verifier: NewVIESVerifier(), Now: time.Now}
}
func VATNumber(b BillingProfile) (string, error) {
	number := ""
	for _, id := range b.TaxIDs {
		if id.Type != "eu_vat" && id.Type != "gb_vat" && id.Type != "vat" {
			continue
		}
		v := NormalizeVATNumber(id.Value)
		if v == "" || number != "" && number != v {
			return "", ErrInvalid
		}
		number = v
	}
	return number, nil
}
func NormalizeVATNumber(s string) string {
	s = strings.ToUpper(strings.Join(strings.Fields(s), ""))
	if len(s) < 3 || len(s) > 40 {
		return ""
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			if c < 'A' || c > 'Z' {
				return ""
			}
		}
	}
	return s
}
func VATCountryMatch(country, number string) bool {
	prefix := country
	if prefix == "GR" {
		prefix = "EL"
	}
	return strings.HasPrefix(number, prefix)
}
func ruleScore(r VATRule, country, seller string) int {
	switch {
	case r.Country == "*":
		return 1
	case r.Country == "domestic" && country == seller:
		return 3
	case r.Country == "EU" && EUCountry(country):
		return 2
	case r.Country == "non_eu" && !EUCountry(country):
		return 2
	case r.Country == country:
		return 4
	}
	return 0
}
func (c *VATCalculator) Quote(ctx context.Context, s VATSettings, p Payment, b BillingProfile, manual VATVerification) (*Billing, *VATSnapshot, error) {
	if s.Ready() != nil {
		return nil, nil, ErrVATSettings
	}
	if !validBilling(b) {
		return nil, nil, ErrInvalid
	}
	country := b.Address.Country
	number, err := VATNumber(b)
	if err != nil {
		return nil, nil, err
	}
	status := "unregistered"
	var verification *VATVerification
	if number != "" {
		if !VATCountryMatch(country, number) {
			return nil, nil, ErrVATVerification
		}
		now := time.Now()
		if c.Now != nil {
			now = c.Now()
		}
		if manual.Verified && manual.Country == country && manual.Number == number && manual.BusinessName == b.BusinessName && manual.VerifiedAt.Before(now.Add(time.Minute)) && manual.ExpiresAt.After(now) {
			verification = &manual
			status = "registered"
		} else if EUCountry(country) {
			if c.Verifier == nil {
				return nil, nil, ErrVATUnavailable
			}
			v, e := c.Verifier.Verify(ctx, country, number)
			if e != nil {
				return nil, nil, e
			}
			if v.Verified {
				if normalizeVATName(v.BusinessName) == "" || normalizeVATName(v.BusinessName) != normalizeVATName(b.BusinessName) {
					return nil, nil, ErrVATVerification
				}
				verification = &v
				status = "registered"
			}
		} else {
			// An unverified non-EU registration cannot obtain a registration-specific
			// exemption. Country-only rules can still explicitly cover that customer.
			status = "unverified"
		}
	}
	var selected *VATRule
	score := 0
	for _, r := range s.Rules {
		n := ruleScore(r, country, s.Seller.Address.Country)
		if n == 0 || r.Status != "any" && r.Status != status {
			continue
		}
		n *= 10
		if r.Status != "any" {
			n++
		}
		if n > score {
			v := r
			selected = &v
			score = n
		}
	}
	if selected == nil {
		if status == "unverified" {
			return nil, nil, ErrVATVerification
		}
		return nil, nil, ErrVATRule
	}
	if selected.Treatment == "reverse_charge" && status != "registered" {
		return nil, nil, ErrVATRule
	}
	cents, err := Cents(money.USD(p.AmountCents * 10000))
	if err != nil {
		return nil, nil, err
	}
	// Integer half-up cent rounding; no floating-point accumulation.
	tax := (cents*int64(selected.Rate) + 5000) / 10000
	bill := &Billing{SubtotalCents: cents, TaxCents: tax, TotalCents: cents + tax, BusinessName: b.BusinessName, Email: b.Email, Address: b.Address, TaxIDs: b.TaxIDs}
	snapshot := &VATSnapshot{SettingsVersion: s.Version, Seller: s.Seller, Rule: *selected, BuyerCountry: country, BuyerStatus: status, Verification: verification}
	return bill, snapshot, nil
}

func normalizeVATName(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToUpper(r)
		}
		return -1
	}, s)
}

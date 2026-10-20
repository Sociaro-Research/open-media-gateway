package payments

import (
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"time"
)

// VIES validates EU VAT registrations only. Tax amounts/rules remain local.
// The endpoint is fixed; the browser cannot select an outbound destination.
type VIESVerifier struct {
	Client   *http.Client
	Endpoint string
}

func NewVIESVerifier() *VIESVerifier {
	return &VIESVerifier{Client: &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, Endpoint: "https://ec.europa.eu/taxation_customs/vies/services/checkVatService"}
}
func (v *VIESVerifier) Verify(ctx context.Context, country, number string) (VATVerification, error) {
	if !EUCountry(country) || !VATCountryMatch(country, number) || NormalizeVATNumber(number) != number {
		return VATVerification{}, ErrVATVerification
	}
	prefix := country
	if country == "GR" {
		prefix = "EL"
	}
	local := strings.TrimPrefix(number, prefix)
	body := `<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:v="urn:ec.europa.eu:taxud:vies:services:checkVat:types"><soap:Body><v:checkVat><v:countryCode>` + prefix + `</v:countryCode><v:vatNumber>` + local + `</v:vatNumber></v:checkVat></soap:Body></soap:Envelope>`
	req, err := http.NewRequestWithContext(ctx, "POST", v.Endpoint, bytes.NewBufferString(body))
	if err != nil {
		return VATVerification{}, ErrVATUnavailable
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", "")
	req.Header.Set("User-Agent", "Sociaro-Gateway-VAT/1.0")
	response, err := v.Client.Do(req)
	if err != nil {
		return VATVerification{}, ErrVATUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return VATVerification{}, ErrVATUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return VATVerification{}, ErrVATUnavailable
	}
	var envelope struct {
		Body struct {
			Response struct {
				Country string `xml:"countryCode"`
				Number  string `xml:"vatNumber"`
				Date    string `xml:"requestDate"`
				Valid   *bool  `xml:"valid"`
				Name    string `xml:"name"`
			} `xml:"checkVatResponse"`
		} `xml:"Body"`
	}
	if xml.Unmarshal(data, &envelope) != nil {
		return VATVerification{}, ErrVATUnavailable
	}
	r := envelope.Body.Response
	if r.Valid == nil || r.Country != prefix || r.Number != local {
		return VATVerification{}, ErrVATUnavailable
	}
	now := time.Now().UTC()
	return VATVerification{Country: country, Number: number, BusinessName: r.Name, Source: "vies", Reference: r.Date, Verified: *r.Valid, VerifiedAt: now, ExpiresAt: now.Add(24 * time.Hour)}, nil
}

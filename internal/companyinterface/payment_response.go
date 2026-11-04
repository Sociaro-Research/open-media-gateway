package companyinterface

import (
	"bytes"
	"encoding/json"
	"image/png"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// Payment navigation URLs are typed external browser links, never API proxy
// destinations. Preserve signed queries byte-for-byte; do not fetch them or
// attach Gateway headers/cookies. Company-owned binary invoices/QR have separate
// route contracts and cannot be smuggled in as payment checkout links.
func paymentJSONResponse(c Contract, status int, method, path, company string, raw []byte) ([]byte, error) {
	if (c.ResponseURLs != "payment-status" && c.ResponseURLs != "payment-list" && c.ResponseURLs != "payment-methods") || status < 200 || status >= 300 || method == http.MethodHead {
		return raw, nil
	}
	var f mediaFields
	if json.Unmarshal(raw, &f) != nil || f == nil {
		return nil, ErrUnavailable
	}
	if c.ResponseURLs == "payment-methods" {
		for key := range f {
			switch key {
			case "data", "currency", "min_cents", "max_cents":
			default:
				return nil, ErrUnavailable
			}
		}
		var rows []mediaFields
		if json.Unmarshal(f["data"], &rows) != nil || rows == nil {
			return nil, ErrUnavailable
		}
		for _, row := range rows {
			id, e := fieldString(row, "id")
			kind, kindError := fieldString(row, "kind")
			if e != nil || !validID(id) || kindError != nil || (kind != "stripe" && kind != "stablecoin") {
				return nil, ErrUnavailable
			}
			// This route intentionally publishes only method configuration. An
			// accidental provider-secret projection must not reach the local UI.
			for key := range row {
				switch key {
				case "id", "name", "kind", "live", "fee_mode", "stablecoin", "stripe_invoices":
				default:
					return nil, ErrUnavailable
				}
			}
		}
		return raw, nil
	}
	if c.ResponseURLs == "payment-list" {
		var rows []mediaFields
		if json.Unmarshal(f["data"], &rows) != nil || rows == nil {
			return nil, ErrUnavailable
		}
		for _, row := range rows {
			if e := validatePaymentFields(row, company, ""); e != nil {
				return nil, e
			}
		}
		return raw, nil
	}
	if e := validatePaymentFields(f, company, path); e != nil {
		return nil, e
	}
	return raw, nil
}

func validatePaymentFields(f mediaFields, company, path string) error {
	id, e := fieldString(f, "id")
	if e != nil || !validID(id) || path != "" && !strings.HasSuffix(path, "/payments/"+id) {
		return ErrUnavailable
	}
	org, e := fieldString(f, "organization_id")
	if e != nil || org != company {
		return ErrUnavailable
	}
	link := func(row mediaFields, key string) error {
		value, ok := row[key]
		if !ok || string(value) == "null" {
			return nil
		}
		var s string
		if json.Unmarshal(value, &s) != nil {
			return ErrUnavailable
		}
		if s == "" {
			return nil
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.ContainsAny(s, "\\\r\n\x00") {
			return ErrUnavailable
		}
		return nil
	}
	for _, key := range []string{"checkout_url", "receipt_url"} {
		if e = link(f, key); e != nil {
			return e
		}
	}
	if doc, ok := f["invoice"]; ok && string(doc) != "null" {
		var invoice mediaFields
		if json.Unmarshal(doc, &invoice) != nil || invoice == nil {
			return ErrUnavailable
		}
		for _, key := range []string{"url", "pdf_url"} {
			if e = link(invoice, key); e != nil {
				return e
			}
		}
	}
	return nil
}

// QR is centrally generated, small PNG data, never a fetched URL or HTML page.
// Keep original bytes and HTTP range behavior; the existing central QR endpoint
// returns 200 for Range rather than manufacturing unsupported partial delivery.
func paymentBinaryResponse(c Contract, status int, method string, header http.Header, raw []byte) error {
	if c.ResponseURLs != "payment-qr" || status < 200 || status >= 300 {
		return nil
	}
	media, _, e := mime.ParseMediaType(header.Get("Content-Type"))
	if e != nil || media != "image/png" {
		return ErrUnavailable
	}
	if method == http.MethodHead {
		if len(raw) != 0 {
			return ErrUnavailable
		}
		return nil
	}
	if len(raw) > 64<<10 {
		return ErrUnavailable
	}
	config, e := png.DecodeConfig(bytes.NewReader(raw))
	if e != nil || config.Width != 256 || config.Height != 256 {
		return ErrUnavailable
	}
	if _, e = png.Decode(bytes.NewReader(raw)); e != nil {
		return ErrUnavailable
	}
	return nil
}

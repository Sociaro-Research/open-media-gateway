package stripe

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
)

func (c *Client) CreateCustomer(ctx context.Context, credentials payments.Credentials, org, key string) (string, error) {
	var customer struct {
		ID string `json:"id"`
	}
	err := c.request(ctx, credentials, "POST", "/v1/customers", url.Values{"metadata[organization_id]": {org}}, key, &customer)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(customer.ID, "cus_") {
		return "", payments.ErrConflict
	}
	return customer.ID, nil
}

// Reuse organization billing details, independently of the payer's Link wallet.
func (c *Client) checkoutBilling(ctx context.Context, credentials payments.Credentials, p payments.Payment, v url.Values, billing *payments.Billing) error {
	var customer struct {
		ID           string            `json:"id"`
		Deleted      bool              `json:"deleted"`
		Live         bool              `json:"livemode"`
		Name         string            `json:"name"`
		BusinessName string            `json:"business_name"`
		Address      payments.Address  `json:"address"`
		Metadata     map[string]string `json:"metadata"`
		TaxIDs       struct {
			Data []payments.TaxID `json:"data"`
		} `json:"tax_ids"`
	}
	if err := c.request(ctx, credentials, "GET", "/v1/customers/"+url.PathEscape(p.CustomerID), url.Values{"expand[]": {"tax_ids"}}, "", &customer); err != nil {
		return err
	}
	if customer.ID != p.CustomerID || customer.Deleted || customer.Live != p.Live || customer.Metadata["organization_id"] != p.OrganizationID {
		return payments.ErrConflict
	}
	// Older Checkout tax-ID collection stored the legal business name in name.
	knownName := strings.TrimSpace(customer.BusinessName) != "" || (strings.TrimSpace(customer.Name) != "" && len(customer.TaxIDs.Data) > 0)
	if knownName {
		billing.BusinessName = customer.BusinessName
		if strings.TrimSpace(billing.BusinessName) == "" {
			billing.BusinessName = customer.Name
		}
		billing.TaxIDs = customer.TaxIDs.Data
		v.Del("name_collection[business][enabled]")
		v.Del("name_collection[business][optional]")
		// Stripe requires name=auto whenever tax-ID collection is enabled.
		// Existing tax IDs already belong to this customer; do not collect again.
		if len(customer.TaxIDs.Data) > 0 {
			v.Set("tax_id_collection[enabled]", "false")
			v.Set("customer_update[name]", "never")
		}
	}
	if strings.TrimSpace(customer.Address.Line1) != "" && strings.TrimSpace(customer.Address.Country) != "" && (strings.TrimSpace(customer.Address.City) != "" || strings.TrimSpace(customer.Address.PostalCode) != "") {
		billing.Address = customer.Address
		v.Set("billing_address_collection", "auto")
		// Tax and invoices must keep the company's address, not the cardholder's.
		v.Set("customer_update[address]", "never")
	}
	if knownName {
		v.Set("custom_text[submit][message]", checkoutBillingSummary(billing))
	}
	return nil
}

// Keep the saved company identity visible even when Link hides billing fields.
// Bound each line so long customer records cannot prevent creating Checkout.
func checkoutBillingSummary(b *payments.Billing) string {
	clean := func(value string, limit int) string {
		value = strings.Join(strings.Fields(value), " ")
		r := []rune(value)
		if len(r) > limit {
			value = string(r[:limit-1]) + "…"
		}
		return value
	}
	lines := []string{"Billing to: " + clean(b.BusinessName, 150)}
	a := b.Address
	parts := []string{}
	for _, v := range []string{a.Line1, a.Line2, a.City, a.State, a.PostalCode, a.Country} {
		if strings.TrimSpace(v) != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) > 0 {
		lines = append(lines, "Address: "+clean(strings.Join(parts, ", "), 210))
	}
	ids := []string{}
	for _, id := range b.TaxIDs {
		if strings.TrimSpace(id.Value) != "" {
			ids = append(ids, id.Value)
		}
	}
	if len(ids) > 0 {
		lines = append(lines, "Tax ID: "+clean(strings.Join(ids, ", "), 100))
	}
	return strings.Join(lines, "\n")
}

// Reverse only the refunded/disputed principal, never tax. Rounding up avoids
// retaining spendable fractions; a full gross reversal removes all principal.
func principalReduction(gross, principal, total int64) int64 {
	if gross == 0 {
		return 0
	}
	return (gross*principal + total - 1) / total
}

func invoiceURL(raw string, pdf bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	if pdf {
		return u.Host == "pay.stripe.com" || u.Host == "invoice.stripe.com"
	}
	return u.Host == "invoice.stripe.com"
}

// Documents are eventually consistent. Unavailability never rolls back a valid
// funding settlement. The durable reconciliation queue retries pending invoices.
func (c *Client) FetchInvoice(ctx context.Context, credentials payments.Credentials, p payments.Payment) (*payments.Invoice, error) {
	if p.Billing == nil || p.Invoice == nil || !strings.HasPrefix(p.Invoice.ID, "in_") {
		return nil, payments.ErrUnavailable
	}
	s := session{InvoiceID: p.Invoice.ID, Amount: p.Billing.TotalCents}
	if p.Invoice.PDFURL != "" {
		return p.Invoice, nil
	}
	pending := &payments.Invoice{ID: s.InvoiceID, Status: "pending"}
	var doc struct {
		ID       string            `json:"id"`
		Customer string            `json:"customer"`
		Currency string            `json:"currency"`
		Total    int64             `json:"total"`
		Live     bool              `json:"livemode"`
		Status   string            `json:"status"`
		Number   string            `json:"number"`
		URL      string            `json:"hosted_invoice_url"`
		PDF      string            `json:"invoice_pdf"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := c.request(ctx, credentials, "GET", "/v1/invoices/"+url.PathEscape(s.InvoiceID), nil, "", &doc); err != nil {
		return pending, payments.ErrUnavailable
	}
	if doc.ID != s.InvoiceID || doc.Customer != p.CustomerID || doc.Currency != "usd" || doc.Total != s.Amount || doc.Live != p.Live || doc.Metadata["gateway_payment_id"] != p.ID || doc.Metadata["organization_id"] != p.OrganizationID {
		return pending, payments.ErrUnavailable
	}
	if doc.Status != "paid" || !invoiceURL(doc.PDF, true) || !invoiceURL(doc.URL, false) {
		return pending, payments.ErrUnavailable
	}
	return &payments.Invoice{ID: doc.ID, Number: doc.Number, Status: doc.Status, URL: doc.URL, PDFURL: doc.PDF}, nil
}

// Recover a durable attempt after a lost response, before the manager can
// reserve another one. Search only its bounded creation window and exact
// server-owned metadata; incomplete or inconsistent results fail closed.
func (c *Client) recoverCheckout(ctx context.Context, credentials payments.Credentials, p payments.Payment) (string, error) {
	values := url.Values{"limit": {"100"}, "created[gte]": {strconv.FormatInt(p.CheckoutStart().Add(-time.Minute).Unix(), 10)}, "created[lte]": {strconv.FormatInt(p.CheckoutStart().Add(24*time.Hour).Unix(), 10)}}
	found := ""
	for page := 0; page < 10; page++ {
		var list struct {
			Data []session `json:"data"`
			More bool      `json:"has_more"`
		}
		if err := c.request(ctx, credentials, "GET", "/v1/checkout/sessions", values, "", &list); err != nil {
			return "", err
		}
		for _, s := range list.Data {
			if s.Metadata["gateway_payment_id"] == p.ID && s.Metadata["gateway_checkout_attempt"] == strconv.FormatInt(p.CheckoutAttempt, 10) {
				if !validSession(s, p) || found != "" {
					return "", payments.ErrConflict
				}
				found = s.ID
			}
		}
		if !list.More {
			return found, nil
		}
		if len(list.Data) == 0 || list.Data[len(list.Data)-1].ID == values.Get("starting_after") {
			return "", payments.ErrConflict
		}
		values.Set("starting_after", list.Data[len(list.Data)-1].ID)
	}
	return "", payments.ErrUnavailable
}

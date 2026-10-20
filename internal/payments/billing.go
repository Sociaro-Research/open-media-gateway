package payments

import (
	"context"
	"strings"
	"time"
)

// Billing preserves the purchase basis separately from tax and processor fees.
// Refund and hold amounts here are gross; the ledger fields contain the
// corresponding principal reductions, rounded up so credits cannot be overdrawn.
type Billing struct {
	SubtotalCents int64   `json:"subtotal_cents"`
	TaxCents      int64   `json:"tax_cents"`
	TotalCents    int64   `json:"total_cents"`
	RefundedCents int64   `json:"refunded_cents"`
	HeldCents     int64   `json:"held_cents"`
	BusinessName  string  `json:"business_name"`
	Email         string  `json:"email"`
	Address       Address `json:"address"`
	TaxIDs        []TaxID `json:"tax_ids"`
}

// BillingProfile is provider-independent company identity, not payment amounts.
type BillingProfile struct {
	BusinessName string  `json:"business_name"`
	Email        string  `json:"email"`
	Address      Address `json:"address"`
	TaxIDs       []TaxID `json:"tax_ids"`
}

type Address struct {
	Line1      string `json:"line1"`
	Line2      string `json:"line2"`
	City       string `json:"city"`
	State      string `json:"state"`
	PostalCode string `json:"postal_code"`
	Country    string `json:"country"`
}
type TaxID struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}
type Invoice struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Status string `json:"status"`
	URL    string `json:"url,omitempty"`
	PDFURL string `json:"pdf_url,omitempty"`
}

// CustomerRepository serializes customer creation per organization/provider and
// persists its result independently of Checkout creation or payment settlement.
type CustomerRepository interface {
	PaymentCustomer(context.Context, string, string, func(string) (string, error)) (string, error)
}
type CustomerAdapter interface {
	CreateCustomer(context.Context, Credentials, string, string) (string, error)
}

func (m *Manager) customer(ctx context.Context, pr Provider, c Credentials, p Payment) (string, error) {
	if p.InvoiceMode == "local" || p.TaxMode != "exclusive" || p.CustomerID != "" {
		return p.CustomerID, nil
	}
	repo, ok := m.Repo.(CustomerRepository)
	if !ok {
		return "", ErrUnavailable
	}
	ca, ok := m.adapters[pr.Kind].(CustomerAdapter)
	if !ok {
		return "", ErrUnavailable
	}
	return repo.PaymentCustomer(ctx, p.OrganizationID, p.ProviderID, func(key string) (string, error) { return ca.CreateCustomer(ctx, c, p.OrganizationID, key) })
}
func (m *Manager) checkout(ctx context.Context, pr Provider, c Credentials, p Payment) (Snapshot, error) {
	if p.InvoiceMode != "local" && p.TaxMode == "exclusive" && p.CustomerID == "" {
		return Snapshot{}, ErrUnavailable
	}
	returnURL := strings.TrimRight(m.UIURL, "/")
	if returnURL == "" {
		returnURL = m.PublicURL
	}
	return m.adapters[pr.Kind].Checkout(ctx, pr, c, p, returnURL)
}

type InvoiceAdapter interface {
	FetchInvoice(context.Context, Credentials, Payment) (*Invoice, error)
}
type InvoiceRepository interface {
	SavePaymentInvoice(context.Context, Payment, *Invoice) error
}

// Funding has already committed before any document request is attempted.
func (m *Manager) enrichInvoice(ctx context.Context, pr Provider, c Credentials, p Payment) Payment {
	if p.InvoiceMode == "local" || !p.Settled || p.TaxMode != "exclusive" || (p.Invoice != nil && p.Invoice.PDFURL != "") {
		return p
	}
	a, ok := m.adapters[pr.Kind].(InvoiceAdapter)
	if !ok {
		return p
	}
	repo, ok := m.Repo.(InvoiceRepository)
	if !ok {
		return p
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	doc, err := a.FetchInvoice(ctx, c, p)
	if err == nil && doc != nil && repo.SavePaymentInvoice(ctx, p, doc) == nil {
		p.Invoice = doc
	}
	return p
}

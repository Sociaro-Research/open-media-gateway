package payments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"net/mail"
	"net/url"
	"strings"
	"time"
)

var ErrExpired = errors.New("This payment request has expired. Create a new payment.")
var ErrCheckoutWindow = errors.New("There is not enough time left to open a new Stripe checkout. Create a new payment.")

const InvoicePaymentDays = 30

func InvoiceDueDate(issued time.Time) time.Time {
	return issued.UTC().AddDate(0, 0, InvoicePaymentDays)
}
func (p Payment) CheckoutStart() time.Time {
	if p.CheckoutStartedAt != nil {
		return *p.CheckoutStartedAt
	}
	return p.CreatedAt
}
func (p Payment) CheckoutKey() string {
	key := "gateway-topup-" + p.ID
	if p.CheckoutAttempt > 0 {
		key += fmt.Sprintf("-attempt-%d", p.CheckoutAttempt)
	}
	return key
}

const MaxInvoiceLogoBytes = 512 * 1024
const MaxBillingRequestBytes = 1 << 20

type InvoiceLogo struct {
	MIME string `json:"mime"`
	Data []byte `json:"data"`
}

// Logos are embedded data only. No remote resource is fetched during rendering.
func (s SellerCompany) ValidateInvoiceDetails() error {
	if s.Email != "" {
		a, e := mail.ParseAddress(s.Email)
		if e != nil || a.Address != s.Email || len(s.Email) > 254 {
			return ErrVATSettings
		}
	}
	if s.InvoiceLogo == nil {
		return nil
	}
	_, err := NormalizeInvoiceLogo(s.InvoiceLogo)
	return err
}
func NormalizeInvoiceLogo(logo *InvoiceLogo) (*InvoiceLogo, error) {
	if logo == nil {
		return nil, nil
	}
	if len(logo.Data) == 0 || len(logo.Data) > MaxInvoiceLogoBytes || (logo.MIME != "image/png" && logo.MIME != "image/jpeg") {
		return nil, ErrInvalid
	}
	config, kind, e := image.DecodeConfig(bytes.NewReader(logo.Data))
	if e != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || config.Width*config.Height > 4000000 || "image/"+kind != logo.MIME {
		return nil, ErrInvalid
	}
	img, _, e := image.Decode(bytes.NewReader(logo.Data))
	if e != nil {
		return nil, ErrInvalid
	}
	// Re-encode to discard metadata, trailing content and non-raster payloads.
	var out bytes.Buffer
	if e = png.Encode(&out, img); e != nil || out.Len() > MaxInvoiceLogoBytes {
		return nil, ErrInvalid
	}
	return &InvoiceLogo{MIME: "image/png", Data: out.Bytes()}, nil
}

type InvoiceDocument struct {
	Number    string
	CreatedAt time.Time
	PDF       []byte
}
type LocalInvoiceRepository interface {
	BeginLocalCard(context.Context, string, string, string, int64, string, string, BillingProfile, *VATCalculator) (Payment, error)
	LocalInvoice(context.Context, string, func(Payment, string, time.Time) ([]byte, error)) (InvoiceDocument, error)
}

func (m *Manager) InvoicePDF(ctx context.Context, id string) (InvoiceDocument, error) {
	repo, ok := m.Repo.(LocalInvoiceRepository)
	if !ok {
		return InvoiceDocument{}, ErrUnavailable
	}
	return repo.LocalInvoice(ctx, id, func(p Payment, number string, issued time.Time) ([]byte, error) {
		base := strings.TrimRight(m.UIURL, "/")
		if base == "" {
			base = m.PublicURL
		}
		p.InvoicePaymentURL = base + "/organizations?" + url.Values{"payment": {p.ID}, "organization": {p.OrganizationID}, "pay_invoice": {"1"}}.Encode()
		return RenderInvoicePDF(p, number, issued)
	})
}

func InvoiceFileName(number string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, number) + ".pdf"
}

// ResumeInvoiceCheckout keeps the invoice and financial request stable while
// allowing a new Stripe session after the previous one is authoritatively expired.
type InvoiceCheckoutRepository interface {
	PrepareInvoiceCheckout(context.Context, string) (Payment, error)
}

func (m *Manager) ResumeInvoiceCheckout(ctx context.Context, id string) (Payment, error) {
	p, err := m.Repo.Payment(ctx, id)
	if err != nil {
		return p, err
	}
	if p.Stablecoin != nil || p.ExpiresAt == nil {
		return p, ErrInvalid
	}
	if p.Settled {
		return p, nil
	}
	if p.RequestExpired(time.Now()) {
		_ = m.ExpirePayments(ctx)
		return p, ErrExpired
	}
	pr, err := m.Repo.PaymentProvider(ctx, p.ProviderID)
	if err != nil {
		return p, err
	}
	if pr.Kind != "stripe" || !pr.Enabled {
		return p, ErrUnavailable
	}
	if !p.CheckoutExpired && (p.SessionID != "" || (p.CheckoutAttempt > 0 && time.Since(p.CheckoutStart()) > 23*time.Hour)) {
		p, err = m.Reconcile(ctx, id)
		if err != nil || p.Settled {
			return p, err
		}
	}
	repo, ok := m.Repo.(InvoiceCheckoutRepository)
	if !ok {
		return p, ErrUnavailable
	}
	p, err = repo.PrepareInvoiceCheckout(ctx, id)
	if err != nil {
		return p, err
	}
	c, err := m.credentials(pr)
	if err != nil {
		return p, err
	}
	customerID, err := m.customer(ctx, pr, c, p)
	if err != nil {
		return p, err
	}
	return m.Repo.WithPayment(ctx, id, func(p Payment) (Snapshot, error) {
		if p.RequestExpired(time.Now()) {
			return Snapshot{}, ErrExpired
		}
		if p.CustomerID == "" {
			p.CustomerID = customerID
		}
		if p.Settled || p.SessionID != "" {
			return existing(p), nil
		}
		if p.Status != "pending" || time.Since(p.CheckoutStart()) > 23*time.Hour {
			return Snapshot{}, ErrConflict
		}
		return m.checkout(ctx, pr, c, p)
	})
}

func (p Payment) RequestExpiry() time.Time {
	if p.ExpiresAt != nil {
		return p.ExpiresAt.UTC()
	}
	return InvoiceDueDate(p.CreatedAt)
}
func (p Payment) RequestExpired(now time.Time) bool {
	return !p.Settled && p.ExpiresAt != nil && !now.Before(*p.ExpiresAt)
}

type ExpiryRepository interface{ ExpirePayments(context.Context) error }

func (m *Manager) ExpirePayments(ctx context.Context) error {
	if repo, ok := m.Repo.(ExpiryRepository); ok {
		return repo.ExpirePayments(ctx)
	}
	return nil
}

// Package payments owns provider-neutral funding contracts. Only trusted server
// adapters may settle payments; browser and organization credentials cannot.
package payments

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/google/uuid"
)

var (
	ErrInvalid     = errors.New("invalid payment request")
	ErrConflict    = errors.New("payment or configuration conflicts with an existing operation")
	ErrForbidden   = errors.New("payment access denied")
	ErrUnavailable = errors.New("payment provider is unavailable")
	ErrNotFound    = errors.New("payment not found")
	ErrSignature   = errors.New("invalid payment webhook signature")
)

const MinCents int64 = 100
const MaxCents int64 = 1000000 // $10,000 per payment; integer cents throughout the adapter boundary.

type Provider struct {
	StripeInvoices bool              `json:"stripe_invoices"`
	Stablecoin     *StablecoinConfig `json:"stablecoin,omitempty"`
	FeeMode        string            `json:"fee_mode"`
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Live           bool              `json:"live"`
	AccountID      string            `json:"account_id"`
	Version        int64             `json:"version"`
	CreatedBy      string            `json:"-"`
	SecretCipher   []byte            `json:"-"`
	TokenHash      []byte            `json:"-"`
	LastWebhook    *time.Time        `json:"last_webhook_at,omitempty"`
}
type Credentials struct {
	SecretKey     string `json:"secret_key"`
	WebhookSecret string `json:"webhook_secret"`
}
type ProviderView struct {
	Provider
	WebhookConfigured     bool   `json:"webhook_configured"`
	CredentialsConfigured bool   `json:"credentials_configured"`
	WebhookURL            string `json:"webhook_url,omitempty"`
}
type ProviderUpdate struct {
	StripeInvoices *bool             `json:"stripe_invoices,omitempty"`
	Stablecoin     *StablecoinConfig `json:"stablecoin,omitempty"`
	FeeMode        string            `json:"fee_mode"`
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Enabled        bool              `json:"enabled"`
	Version        int64             `json:"version"`
	SecretKey      string            `json:"secret_key"`
	WebhookSecret  string            `json:"webhook_secret"`
}
type Payment struct {
	ExpiresAt         *time.Time         `json:"expires_at,omitempty"`
	CheckoutExpired   bool               `json:"-"`
	CheckoutAttempt   int64              `json:"-"`
	CheckoutStartedAt *time.Time         `json:"-"`
	InvoicePaymentURL string             `json:"-"`
	InvoiceMode       string             `json:"invoice_mode"`
	VAT               *VATSnapshot       `json:"vat,omitempty"`
	FeeMicros         int64              `json:"fee_micros"`
	Stablecoin        *StablecoinPayment `json:"stablecoin,omitempty"`
	TaxMode           string             `json:"tax_mode"`
	CustomerID        string             `json:"-"`
	Billing           *Billing           `json:"billing,omitempty"`
	Invoice           *Invoice           `json:"invoice,omitempty"`
	Settlement        *Settlement        `json:"settlement,omitempty"`
	FeeMode           string             `json:"fee_mode"`
	FeeCents          int64              `json:"fee_cents"`
	ID                string             `json:"id"`
	OrganizationID    string             `json:"organization_id"`
	ProviderID        string             `json:"provider_id"`
	ActorID           string             `json:"-"`
	IdempotencyKey    string             `json:"-"`
	AmountCents       int64              `json:"amount_cents"`
	Currency          string             `json:"currency"`
	Live              bool               `json:"live"`
	Status            string             `json:"status"`
	SessionID         string             `json:"-"`
	IntentID          string             `json:"-"`
	CheckoutURL       string             `json:"checkout_url,omitempty"`
	ReceiptURL        string             `json:"receipt_url,omitempty"`
	CreditedCents     int64              `json:"credited_cents"`
	RefundedCents     int64              `json:"refunded_cents"`
	HeldCents         int64              `json:"held_cents"`
	Settled           bool               `json:"settled"`
	Revision          int64              `json:"revision"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// Snapshot is the authoritative current state, not an incremental credit. The
// repository calculates and atomically appends only the difference to the ledger.
type Snapshot struct {
	CheckoutExpired                              bool
	CustomerID                                   string
	Billing                                      *Billing
	Invoice                                      *Invoice
	Settlement                                   *Settlement
	FeeCents                                     int64
	Status                                       string
	SessionID, IntentID, CheckoutURL, ReceiptURL string
	Paid                                         bool
	RefundedCents, HeldCents                     int64
	Revision                                     int64 // external modules use strictly increasing source revisions
}
type Account struct {
	ID                   string
	Live, ChargesEnabled bool
}
type Event struct{ PaymentID, SessionID, IntentID string }
type Adapter interface {
	Check(context.Context, Credentials) (Account, error)
	Checkout(context.Context, Provider, Credentials, Payment, string) (Snapshot, error)
	Reconcile(context.Context, Provider, Credentials, Payment) (Snapshot, error)
	Webhook(context.Context, Provider, Credentials, []byte, string) (Event, error)
}
type Repository interface {
	PaymentProviders(context.Context) ([]Provider, error)
	PaymentProvider(context.Context, string) (Provider, error)
	SavePaymentProvider(context.Context, string, Provider) (Provider, error)
	BeginPayment(context.Context, string, string, string, int64, string, string) (Payment, error)
	Payment(context.Context, string) (Payment, error)
	Payments(context.Context, string, int) ([]Payment, error)
	WithPayment(context.Context, string, func(Payment) (Snapshot, error)) (Payment, error)
	WebhookSeen(context.Context, string) error
	ExternalSettlement(context.Context, string, []byte, ExternalInput) (Payment, error)
}
type ExternalInput struct {
	FeeCents       int64  `json:"fee_cents"`
	OrganizationID string `json:"organization_id"`
	ExternalID     string `json:"external_id"`
	AmountCents    int64  `json:"amount_cents"`
	Currency       string `json:"currency"`
	RefundedCents  int64  `json:"refunded_cents"`
	HeldCents      int64  `json:"held_cents"`
	Revision       int64  `json:"revision"`
}

func (s ExternalInput) Validate() error {
	if _, err := uuid.Parse(s.OrganizationID); err != nil {
		return ErrInvalid
	}
	if !ValidReference(s.ExternalID) || s.Currency != "usd" || s.AmountCents < MinCents || s.AmountCents > MaxCents || s.Revision < 1 || s.FeeCents < 0 || s.FeeCents > s.AmountCents || s.RefundedCents < 0 || s.HeldCents < 0 || s.RefundedCents > s.AmountCents || s.HeldCents > s.AmountCents {
		return ErrInvalid
	}
	return nil
}
func ValidReference(s string) bool {
	return len(s) >= 1 && len(s) <= 128 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\r\n\t")
}
func Cents(u money.USD) (int64, error) {
	if u%10000 != 0 {
		return 0, ErrInvalid
	}
	v := int64(u) / 10000
	if v < MinCents || v > MaxCents {
		return 0, ErrInvalid
	}
	return v, nil
}
func HashToken(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }
func Net(p Payment, s Snapshot) (int64, error) {
	if s.Billing != nil && (s.Billing.SubtotalCents != p.AmountCents || s.Billing.TaxCents < 0 || s.Billing.TaxCents > MaxCents || s.Billing.TotalCents != p.AmountCents+s.Billing.TaxCents) {
		return 0, ErrConflict
	}
	if p.TaxMode == "exclusive" && s.Paid && s.Billing == nil {
		return 0, ErrConflict
	}
	if s.FeeCents < 0 || s.FeeCents > p.AmountCents || s.RefundedCents < 0 || s.HeldCents < 0 || s.RefundedCents > p.AmountCents || s.HeldCents > p.AmountCents {
		return 0, ErrInvalid
	}
	if !s.Paid {
		return 0, nil
	}
	net := p.AmountCents - s.RefundedCents - s.HeldCents
	if p.FeeMode == "organization" {
		net -= s.FeeCents
	}
	if net < 0 {
		net = 0
	}
	return net, nil
}

type Manager struct {
	VAT       *VATCalculator
	Chain     ChainReader
	Repo      Repository
	cipher    *providerconfig.Cipher
	adapters  map[string]Adapter
	PublicURL string
	UIURL     string
}

func New(repo Repository, cipher *providerconfig.Cipher, publicURL string, adapters map[string]Adapter) *Manager {
	return &Manager{VAT: NewVATCalculator(), Repo: repo, cipher: cipher, adapters: adapters, PublicURL: strings.TrimRight(publicURL, "/")}
}
func (m *Manager) view(p Provider) ProviderView {
	v := ProviderView{Provider: p, CredentialsConfigured: len(p.SecretCipher) > 0}
	if p.Kind == "stripe" {
		c, _ := m.credentials(p)
		v.WebhookConfigured = c.WebhookSecret != ""
	}
	if p.Kind == "stripe" {
		v.WebhookURL = m.PublicURL + "/v1/payments/webhooks/" + p.ID
	}
	return v
}
func (m *Manager) Providers(ctx context.Context) ([]ProviderView, error) {
	ps, e := m.Repo.PaymentProviders(ctx)
	out := []ProviderView{}
	for _, p := range ps {
		out = append(out, m.view(p))
	}
	return out, e
}
func (m *Manager) credentials(p Provider) (Credentials, error) {
	var c Credentials
	b, e := m.cipher.Decrypt(p.SecretCipher)
	if e != nil {
		return c, e
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
func (m *Manager) Save(ctx context.Context, actor, id string, in ProviderUpdate) (ProviderView, error) {
	if in.Kind == "stablecoin" {
		return m.saveStablecoin(ctx, actor, id, in)
	}
	if (in.FeeMode != "service" && in.FeeMode != "organization") || in.Kind != "stripe" || strings.TrimSpace(in.Name) == "" || len(in.Name) > 100 {
		return ProviderView{}, ErrInvalid
	}
	p := Provider{ID: uuid.NewString(), Kind: in.Kind, CreatedBy: actor}
	c := Credentials{}
	if id != "" {
		var e error
		p, e = m.Repo.PaymentProvider(ctx, id)
		if e != nil {
			return ProviderView{}, e
		}
		if p.Kind != in.Kind {
			return ProviderView{}, ErrConflict
		}
		c, e = m.credentials(p)
		if e != nil {
			return ProviderView{}, e
		}
	} else if in.Version != 0 {
		return ProviderView{}, ErrConflict
	}
	if in.SecretKey != "" {
		c.SecretKey = strings.TrimSpace(in.SecretKey)
	}
	if in.WebhookSecret != "" {
		c.WebhookSecret = strings.TrimSpace(in.WebhookSecret)
	}
	if len(c.SecretKey) > 512 || len(c.WebhookSecret) > 512 || (in.Enabled && c.WebhookSecret == "" || c.WebhookSecret != "" && !strings.HasPrefix(c.WebhookSecret, "whsec_")) || strings.ContainsAny(c.SecretKey+c.WebhookSecret, " \r\n\t") || !(strings.HasPrefix(c.SecretKey, "sk_") || strings.HasPrefix(c.SecretKey, "rk_")) {
		return ProviderView{}, ErrInvalid
	}
	u, e := url.Parse(m.PublicURL)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return ProviderView{}, errors.New("payment callbacks require AUTH_PUBLIC_URL to be an HTTPS origin")
	}
	a, e := m.adapters[p.Kind].Check(ctx, c)
	if e != nil {
		return ProviderView{}, e
	}
	if id != "" && (p.AccountID != a.ID || p.Live != a.Live) {
		return ProviderView{}, ErrConflict
	}
	if in.Enabled && a.Live && !a.ChargesEnabled {
		return ProviderView{}, ErrUnavailable
	}
	if in.StripeInvoices != nil {
		p.StripeInvoices = *in.StripeInvoices
	} else if id == "" {
		p.StripeInvoices = true
	}
	p.FeeMode = in.FeeMode
	p.Name = strings.TrimSpace(in.Name)
	p.Enabled = in.Enabled
	p.Version = in.Version
	p.AccountID = a.ID
	p.Live = a.Live
	b, _ := json.Marshal(c)
	p.SecretCipher, e = m.cipher.Encrypt(b)
	if e != nil {
		return ProviderView{}, e
	}
	p, e = m.Repo.SavePaymentProvider(ctx, actor, p)
	return m.view(p), e
}
func (m *Manager) Check(ctx context.Context, id string) (Account, error) {
	p, e := m.Repo.PaymentProvider(ctx, id)
	if e != nil {
		return Account{}, e
	}
	c, e := m.credentials(p)
	if e != nil {
		return Account{}, e
	}
	adapter, ok := m.adapters[p.Kind]
	if !ok {
		return Account{}, ErrInvalid
	}
	a, e := adapter.Check(ctx, c)
	if e == nil && (a.ID != p.AccountID || a.Live != p.Live) {
		e = ErrConflict
	}
	return a, e
}
func (m *Manager) Checkout(ctx context.Context, actor, org, provider string, amount money.USD, key, feeMode string) (Payment, error) {
	return m.CheckoutBilling(ctx, actor, org, provider, amount, key, feeMode, BillingProfile{})
}
func (m *Manager) CheckoutBilling(ctx context.Context, actor, org, provider string, amount money.USD, key, feeMode string, profile BillingProfile) (result Payment, resultErr error) {
	cents, e := Cents(amount)
	if e != nil || !ValidReference(key) || (feeMode != "service" && feeMode != "organization") {
		return Payment{}, ErrInvalid
	}
	var p Payment
	if repo, ok := m.Repo.(LocalInvoiceRepository); ok {
		p, e = repo.BeginLocalCard(ctx, actor, org, provider, cents, key, feeMode, profile, m.VAT)
	} else {
		p, e = m.Repo.BeginPayment(ctx, actor, org, provider, cents, key, feeMode)
	}
	if e != nil {
		return p, e
	}
	if p.RequestExpired(time.Now()) {
		_ = m.ExpirePayments(ctx)
		return m.Repo.Payment(ctx, p.ID)
	}
	defer func() { m.observe(ctx, "checkout:"+p.ID, p.ProviderID, p.ID, resultErr) }()
	pr, e := m.Repo.PaymentProvider(ctx, p.ProviderID)
	if e != nil {
		return p, e
	}
	c, e := m.credentials(pr)
	if e != nil {
		return p, e
	}
	customerID, e := m.customer(ctx, pr, c, p)
	if e != nil {
		return p, e
	}
	return m.Repo.WithPayment(ctx, p.ID, func(p Payment) (Snapshot, error) {
		if p.CustomerID == "" {
			p.CustomerID = customerID
		}
		if pr.Kind != "stripe" || !pr.Enabled {
			return Snapshot{}, ErrUnavailable
		}
		if p.SessionID != "" {
			return existing(p), nil
		}
		if time.Since(p.CheckoutStart()) > 23*time.Hour {
			return Snapshot{}, ErrConflict
		}
		return m.checkout(ctx, pr, c, p)
	})
}

func existing(p Payment) Snapshot {
	return Snapshot{CheckoutExpired: p.CheckoutExpired, CustomerID: p.CustomerID, Billing: p.Billing, Invoice: p.Invoice, Settlement: p.Settlement, FeeCents: p.FeeCents, Status: p.Status, SessionID: p.SessionID, IntentID: p.IntentID, CheckoutURL: p.CheckoutURL, ReceiptURL: p.ReceiptURL, Paid: p.Settled, RefundedCents: p.RefundedCents, HeldCents: p.HeldCents, Revision: p.Revision}
}
func (m *Manager) Reconcile(ctx context.Context, id string) (Payment, error) {
	return m.reconcile(ctx, id, Event{})
}
func (m *Manager) reconcile(ctx context.Context, id string, event Event) (result Payment, resultErr error) {
	p, e := m.Repo.Payment(ctx, id)
	if e != nil {
		return p, e
	}
	if p.RequestExpired(time.Now()) {
		if err := m.ExpirePayments(ctx); err != nil {
			return p, err
		}
		return m.Repo.Payment(ctx, id)
	}
	if p.CheckoutExpired && !p.Settled {
		return p, nil
	}
	defer func() {
		m.observe(ctx, "payment:"+p.ID, p.ProviderID, p.ID, resultErr)
		if resultErr == nil {
			m.observe(ctx, "checkout:"+p.ID, p.ProviderID, p.ID, nil)
		}
	}()
	pr, e := m.Repo.PaymentProvider(ctx, p.ProviderID)
	if e != nil {
		return p, e
	}
	if pr.Kind != "stripe" {
		return p, nil
	}
	c, e := m.credentials(pr)
	if e != nil {
		return p, e
	}
	customerID, e := m.customer(ctx, pr, c, p)
	if e != nil {
		return p, e
	}
	result, resultErr = m.Repo.WithPayment(ctx, id, func(p Payment) (Snapshot, error) {
		if p.CustomerID == "" {
			p.CustomerID = customerID
		}
		if event.SessionID != "" && p.SessionID != "" && event.SessionID != p.SessionID {
			if p.CheckoutAttempt > 0 {
				return existing(p), nil
			}
			return Snapshot{}, ErrConflict
		}
		if event.IntentID != "" && p.IntentID != "" && event.IntentID != p.IntentID {
			if p.CheckoutAttempt > 0 {
				return existing(p), nil
			}
			return Snapshot{}, ErrConflict
		}
		if p.CheckoutAttempt > 0 && p.SessionID == "" && (event.SessionID != "" || event.IntentID != "") {
			return existing(p), nil
		}
		if p.SessionID == "" {
			p.SessionID = event.SessionID
		}
		if p.IntentID == "" {
			p.IntentID = event.IntentID
		}
		if p.SessionID == "" && p.IntentID == "" {
			if time.Since(p.CheckoutStart()) > 23*time.Hour {
				if p.CheckoutAttempt > 0 {
					return m.adapters[pr.Kind].Reconcile(ctx, pr, c, p)
				}
				return existing(p), nil
			}
			return m.checkout(ctx, pr, c, p)
		}
		return m.adapters[pr.Kind].Reconcile(ctx, pr, c, p)
	})
	if resultErr == nil {
		result = m.enrichInvoice(ctx, pr, c, result)
	}
	return result, resultErr
}
func (m *Manager) Webhook(ctx context.Context, provider string, body []byte, signature string) error {
	pr, e := m.Repo.PaymentProvider(ctx, provider)
	if e != nil {
		return e
	}
	a, ok := m.adapters[pr.Kind]
	if !ok {
		return ErrNotFound
	}
	c, e := m.credentials(pr)
	if e != nil {
		return e
	}
	event, e := a.Webhook(ctx, pr, c, body, signature)
	if e != nil {
		m.observe(ctx, "webhook:"+provider, provider, "", e)
		return e
	}
	if event.PaymentID != "" {
		p, e := m.Repo.Payment(ctx, event.PaymentID)
		if e != nil {
			return e
		}
		if p.ProviderID != provider || p.Live != pr.Live {
			m.observe(ctx, "webhook:"+provider, provider, "", ErrConflict)
			return ErrConflict
		}
		if _, e = m.reconcile(ctx, event.PaymentID, event); e != nil {
			return e
		}
	}
	e = m.Repo.WebhookSeen(ctx, provider)
	m.observe(ctx, "webhook:"+provider, provider, "", e)
	return e
}

// NewIntegration returns its bearer credential once; only a digest is retained.
func (m *Manager) NewIntegration(ctx context.Context, actor, name string, live bool) (Provider, string, error) {
	if strings.TrimSpace(name) == "" || len(name) > 100 {
		return Provider{}, "", ErrInvalid
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return Provider{}, "", e
	}
	token := "pay_" + base64.RawURLEncoding.EncodeToString(b)
	p, e := m.Repo.SavePaymentProvider(ctx, actor, Provider{ID: uuid.NewString(), FeeMode: "service", Kind: "external", Name: strings.TrimSpace(name), Enabled: true, Live: live, CreatedBy: actor, TokenHash: HashToken(token)})
	if e != nil {
		return Provider{}, "", e
	}
	return p, token, nil
}
func (m *Manager) Disable(ctx context.Context, actor, id string, version int64) (ProviderView, error) {
	p, e := m.Repo.PaymentProvider(ctx, id)
	if e != nil {
		return ProviderView{}, e
	}
	p.Enabled = false
	p.Version = version
	p, e = m.Repo.SavePaymentProvider(ctx, actor, p)
	return m.view(p), e
}

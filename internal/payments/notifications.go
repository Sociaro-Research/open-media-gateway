package payments

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"net/url"
	"sort"
	"strings"
	"time"
)

var ErrNotificationSettings = errors.New("use email addresses of existing administrators (up to 20 per list)")

// Normal asynchronous capture is not a provider outage. It gets its own grace period.
var ErrSettlementPending = fmt.Errorf("settlement data is pending: %w", ErrUnavailable)

type NotificationSettings struct {
	CriticalEmails []string `json:"critical_emails"`
	SuccessEmails  []string `json:"success_emails"`
	Version        int64    `json:"version"`
}

func (s *NotificationSettings) Normalize() error {
	if s.Version < 1 || s.CriticalEmails == nil || s.SuccessEmails == nil {
		return ErrNotificationSettings
	}
	for _, list := range []*[]string{&s.CriticalEmails, &s.SuccessEmails} {
		if len(*list) > 20 {
			return ErrNotificationSettings
		}
		unique := map[string]bool{}
		for _, raw := range *list {
			email := strings.ToLower(strings.TrimSpace(raw))
			a, e := mail.ParseAddress(email)
			if e != nil || a.Address != email || len(email) > 254 || strings.ContainsAny(email, "\r\n") {
				return ErrNotificationSettings
			}
			unique[email] = true
		}
		*list = []string{}
		for email := range unique {
			*list = append(*list, email)
		}
		sort.Strings(*list)
	}
	return nil
}

type Notice struct {
	StablecoinFeeMicros int64     `json:"stablecoin_fee_micros,omitempty"`
	TaxCents            int64     `json:"tax_cents"`
	PaymentID           string    `json:"payment_id"`
	Organization        string    `json:"organization"`
	OrganizationID      string    `json:"organization_id"`
	Provider            string    `json:"provider"`
	AmountCents         int64     `json:"amount_cents"`
	FeeCents            int64     `json:"fee_cents"`
	CreditedCents       int64     `json:"credited_cents"`
	Code                string    `json:"code"`
	At                  time.Time `json:"at"`
}
type Notification struct {
	ID, Recipient, Kind string
	Notice              Notice
	Attempts            int
	Lease               string
}
type NotificationHealth struct {
	Pending  int        `json:"pending"`
	Retrying int        `json:"retrying"`
	LastSent *time.Time `json:"last_sent_at,omitempty"`
}
type Failure struct {
	Scope, ProviderID, PaymentID, Code string
	Delay                              time.Duration
}

type NotificationRepository interface {
	PaymentNotificationSettings(context.Context) (NotificationSettings, error)
	SavePaymentNotificationSettings(context.Context, string, NotificationSettings) (NotificationSettings, error)
	PaymentNotificationHealth(context.Context) (NotificationHealth, error)
	RecordPaymentFailure(context.Context, Failure) error
	ResolvePaymentFailure(context.Context, string) error
	QueuePaymentAlerts(context.Context) error
	ClaimPaymentNotification(context.Context) (Notification, bool, error)
	FinishPaymentNotification(context.Context, Notification, bool) error
}
type NotificationSender interface {
	SendNotification(context.Context, string, string, string, string) error
}

func FailurePolicy(err error) (string, time.Duration) {
	switch {
	case errors.Is(err, ErrSignature), errors.Is(err, ErrForbidden), errors.Is(err, ErrNotFound):
		return "", 0
	case errors.Is(err, ErrSettlementPending):
		return "settlement_delayed", 5 * time.Minute
	case errors.Is(err, ErrConflict), errors.Is(err, ErrInvalid):
		return "verification_mismatch", 0
	case errors.Is(err, ErrUnavailable), errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return "verification_unavailable", 2 * time.Minute
	default:
		return "processing_failed", 0
	}
}
func (m *Manager) observe(ctx context.Context, scope, provider, payment string, err error) {
	repo, ok := m.Repo.(NotificationRepository)
	if !ok {
		return
	}
	// The payment request may have timed out. A bounded independent context lets
	// us retain the incident without changing the payment result or logging secrets.
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	var e error
	if err == nil {
		e = repo.ResolvePaymentFailure(c, scope)
	} else {
		code, delay := FailurePolicy(err)
		if code == "" {
			return
		}
		e = repo.RecordPaymentFailure(c, Failure{Scope: scope, ProviderID: provider, PaymentID: payment, Code: code, Delay: delay})
	}
	if e != nil {
		slog.Error("payment incident could not be persisted", "scope", scope)
	}
}
func (m *Manager) ExternalSettlement(ctx context.Context, id string, hash []byte, in ExternalInput) (Payment, error) {
	p, e := m.Repo.ExternalSettlement(ctx, id, hash, in)
	// Invalid requests, credentials and duplicate caller revisions are not proof
	// of a lost payment; only trusted processing failures warrant an alert.
	if !errors.Is(e, ErrInvalid) && !errors.Is(e, ErrForbidden) && !errors.Is(e, ErrNotFound) {
		scope := fmt.Sprintf("external:%s:%x", id, sha256.Sum256([]byte(in.ExternalID)))
		m.observe(ctx, scope, id, p.ID, e)
	}
	return p, e
}

func (n Notification) Message(origin string) (string, string) {
	title := "Balance topped up"
	detail := "The payment was verified and the organization balance was credited."
	if n.Kind == "critical" {
		title = "Critical payment error"
		detail = map[string]string{
			"verification_mismatch":    "Payment verification failed because provider data did not match the stored payment. Investigate before making any manual credit.",
			"settlement_delayed":       "Payment succeeded at the provider, but settlement or fee data has remained unavailable for at least 5 minutes. Credit is not yet confirmed.",
			"verification_unavailable": "Payment verification has been unavailable for at least 2 minutes. Check provider access and connectivity; the payment state is uncertain.",
			"processing_failed":        "Payment processing failed internally. Check the application and database; credit is not confirmed.",
		}[n.Notice.Code]
		if detail == "" {
			detail = "Payment processing requires administrator attention."
		}
	}
	money := func(c int64) string { return fmt.Sprintf("USD %d.%02d", c/100, c%100) }
	fee, total := money(n.Notice.FeeCents), money(n.Notice.AmountCents+n.Notice.TaxCents)
	if n.Notice.StablecoinFeeMicros > 0 {
		fee = "USD " + StablecoinAmount(n.Notice.StablecoinFeeMicros)
		total = "USD " + StablecoinAmount((n.Notice.AmountCents+n.Notice.TaxCents)*10000+n.Notice.StablecoinFeeMicros)
	}
	body := fmt.Sprintf("%s\n\n%s\n\nProvider: %s\nOrganization: %s\nPayment ID: %s\nTop-up amount (before tax): %s\nTax: %s\nTotal payment: %s\nFee: %s\nCredited: %s\nDetected at: %s\n", title, detail, n.Notice.Provider, n.Notice.Organization, n.Notice.PaymentID, money(n.Notice.AmountCents), money(n.Notice.TaxCents), total, fee, money(n.Notice.CreditedCents), n.Notice.At.UTC().Format(time.RFC3339))
	if n.Kind == "critical" {
		body += "\nAmounts reflect the last committed Gateway state, not proof of the provider's current settlement. Do not ask the customer to pay again. For Stripe payments, automatic verification continues. External modules should retry failed settlement requests.\n"
	}
	if u, e := url.Parse(origin); e == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		u.Path = "/organizations"
		u.RawQuery = url.Values{"organization": {n.Notice.OrganizationID}}.Encode()
		u.Fragment = ""
		body += "\nOpen console: " + u.String() + "\n"
	}
	body += "\nYou receive this email because an administrator subscribed this address to payment notifications.\n"
	return title, body
}

// RunNotifications uses a durable outbox. Email failures never roll back credits.
// Multiple replicas coordinate via leases; each retry uses a stable provider key.
func (m *Manager) RunNotifications(ctx context.Context, sender NotificationSender) {
	repo, ok := m.Repo.(NotificationRepository)
	if !ok || sender == nil {
		return
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		m.deliverNotifications(ctx, repo, sender)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (m *Manager) deliverNotifications(ctx context.Context, repo NotificationRepository, sender NotificationSender) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := repo.QueuePaymentAlerts(c)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("payment alert queue unavailable")
		}
		return
	}
	for i := 0; i < 10 && ctx.Err() == nil; i++ {
		c, cancel = context.WithTimeout(ctx, 5*time.Second)
		n, found, e := repo.ClaimPaymentNotification(c)
		cancel()
		if e != nil {
			slog.Error("payment notification queue unavailable")
			return
		}
		if !found {
			return
		}
		subject, body := n.Message(m.PublicURL)
		c, cancel = context.WithTimeout(ctx, 15*time.Second)
		e = sender.SendNotification(c, n.Recipient, subject, body, "payment-notice-"+n.ID)
		cancel()
		c, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		finish := repo.FinishPaymentNotification(c, n, e == nil)
		cancel()
		if e != nil {
			slog.Warn("payment notification delivery will retry", "notification_id", n.ID)
		}
		if finish != nil {
			slog.Error("payment notification delivery state unavailable", "notification_id", n.ID)
		}
	}
}

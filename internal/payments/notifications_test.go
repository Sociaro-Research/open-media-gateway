package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNotificationSettings(t *testing.T) {
	s := NotificationSettings{CriticalEmails: []string{" Admin@example.com ", "admin@example.com"}, SuccessEmails: []string{}, Version: 1}
	if e := s.Normalize(); e != nil || len(s.CriticalEmails) != 1 || s.CriticalEmails[0] != "admin@example.com" {
		t.Fatal(s, e)
	}
	for _, email := range []string{"bad", "Name <admin@example.com>", "a@example.com\r\nBcc:b@example.com"} {
		s.CriticalEmails = []string{email}
		if s.Normalize() == nil {
			t.Fatal(email)
		}
	}
	s.CriticalEmails = make([]string, 21)
	if s.Normalize() == nil {
		t.Fatal("unbounded recipients")
	}
	s.CriticalEmails = nil
	if s.Normalize() == nil {
		t.Fatal("omitted list accepted")
	}
}
func TestPaymentFailurePolicy(t *testing.T) {
	for _, tc := range []struct {
		err   error
		code  string
		delay time.Duration
	}{
		{ErrConflict, "verification_mismatch", 0}, {ErrInvalid, "verification_mismatch", 0},
		{ErrUnavailable, "verification_unavailable", 2 * time.Minute}, {context.DeadlineExceeded, "verification_unavailable", 2 * time.Minute},
		{ErrSettlementPending, "settlement_delayed", 5 * time.Minute}, {errors.New("secret SQL detail"), "processing_failed", 0},
		{ErrSignature, "", 0}, {ErrForbidden, "", 0}, {ErrNotFound, "", 0},
	} {
		code, delay := FailurePolicy(tc.err)
		if code != tc.code || delay != tc.delay {
			t.Fatal(tc, code, delay)
		}
	}
}
func TestNoticeContent(t *testing.T) {
	n := Notification{Kind: "success", Notice: Notice{PaymentID: "payment", Organization: "Team", OrganizationID: "org", Provider: "Stripe", AmountCents: 500, TaxCents: 100, FeeCents: 46, CreditedCents: 454, At: time.Unix(100, 0)}}
	subject, body := n.Message("https://gateway.test")
	if !strings.Contains(subject, "topped up") || !strings.Contains(body, "USD 4.54") || !strings.Contains(body, "organization=org") || !strings.Contains(body, "Tax: USD 1.00") || !strings.Contains(body, "Total payment: USD 6.00") {
		t.Fatal(subject, body)
	}
	n.Notice.StablecoinFeeMicros = 1234
	_, stableBody := n.Message("https://gateway.test")
	if !strings.Contains(stableBody, "Fee: USD 0.001234") || !strings.Contains(stableBody, "Total payment: USD 6.001234") {
		t.Fatal(stableBody)
	}
	n.Kind = "critical"
	n.Notice.Code = "verification_mismatch"
	_, body = n.Message("javascript:bad")
	if !strings.Contains(body, "Do not ask the customer to pay again") || strings.Contains(body, "javascript:") {
		t.Fatal(body)
	}
}

type noticeRepo struct {
	NotificationRepository
	item  Notification
	sent  []bool
	claim int
}

func (r *noticeRepo) QueuePaymentAlerts(context.Context) error { return nil }
func (r *noticeRepo) ClaimPaymentNotification(context.Context) (Notification, bool, error) {
	r.claim++
	return r.item, r.claim == 1, nil
}
func (r *noticeRepo) FinishPaymentNotification(_ context.Context, _ Notification, ok bool) error {
	r.sent = append(r.sent, ok)
	return nil
}

type noticeSender struct {
	err          error
	keys, bodies []string
}

func (s *noticeSender) SendNotification(_ context.Context, _, _, body, key string) error {
	s.keys = append(s.keys, key)
	s.bodies = append(s.bodies, body)
	return s.err
}
func TestNotificationRetryKeepsPayloadAndKey(t *testing.T) {
	repo := &noticeRepo{item: Notification{ID: "notice-id", Recipient: "admin@example.test", Kind: "success", Notice: Notice{At: time.Unix(100, 0)}}}
	sender := &noticeSender{err: errors.New("mail unavailable")}
	m := &Manager{PublicURL: "https://gateway.test"}
	m.deliverNotifications(context.Background(), repo, sender)
	repo.claim = 0
	sender.err = nil
	m.deliverNotifications(context.Background(), repo, sender)
	if len(repo.sent) != 2 || repo.sent[0] || !repo.sent[1] || sender.keys[0] != sender.keys[1] || sender.bodies[0] != sender.bodies[1] {
		t.Fatal(repo.sent, sender.keys)
	}
}

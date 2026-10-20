package api

import (
	"context"
	"strings"
	"testing"

	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

type notificationAPIFixture struct {
	*paymentAPIFixture
	payments.NotificationRepository
	settings payments.NotificationSettings
}

func (f *notificationAPIFixture) PaymentNotificationSettings(context.Context) (payments.NotificationSettings, error) {
	return f.settings, nil
}
func (f *notificationAPIFixture) PaymentNotificationHealth(context.Context) (payments.NotificationHealth, error) {
	return payments.NotificationHealth{Pending: 2, Retrying: 1}, nil
}
func (f *notificationAPIFixture) SavePaymentNotificationSettings(_ context.Context, _ string, next payments.NotificationSettings) (payments.NotificationSettings, error) {
	if e := next.Normalize(); e != nil {
		return next, e
	}
	if next.Version != f.settings.Version {
		return next, system.ErrConflict
	}
	next.Version++
	f.settings = next
	return next, nil
}
func TestPaymentNotificationAdministration(t *testing.T) {
	s, _ := newPersonalTestServer()
	s.system = &systemFixture{settings: system.Defaults(), admins: map[string]bool{"alice": true}}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	repo := &notificationAPIFixture{paymentAPIFixture: &paymentAPIFixture{}}
	reset := func() {
		repo.settings = payments.NotificationSettings{CriticalEmails: []string{}, SuccessEmails: []string{}, Version: 1}
	}
	reset()
	s.payments = payments.New(repo, cipher, "https://gateway.test", nil)
	h := s.handler()
	_, generation := issueTestKey(t, h, "alice", `{"name":"Generation"}`)
	_, adminKey := issueTestKey(t, h, "alice", `{"name":"System","scopes":["generation","system:manage"]}`)
	body := `{"critical_emails":["admin@example.test"],"success_emails":[],"version":1}`
	for _, tc := range []struct {
		session, key string
		status       int
	}{{"", "", 401}, {"bob", "", 403}, {"", "service-secret", 403}, {"", generation, 403}, {"alice", "", 200}, {"", adminKey, 200}} {
		for _, method := range []string{"GET", "PUT"} {
			reset()
			requestBody := ""
			if method == "PUT" {
				requestBody = body
			}
			w := sendTest(h, method, "/v1/system/payment-notifications", requestBody, tc.session, tc.key)
			if w.Code != tc.status {
				t.Fatalf("%s %+v: %d %s", method, tc, w.Code, w.Body.String())
			}
		}
	}
	reset()
	for _, bad := range []string{`{"version":1}`, `{"critical_emails":["bad"],"success_emails":[],"version":1}`, `{"critical_emails":[],"success_emails":[],"version":1,"unknown":true}`} {
		if w := sendTest(h, "PUT", "/v1/system/payment-notifications", bad, "alice", ""); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := sendTest(h, "PUT", "/v1/system/payment-notifications", strings.Replace(body, `"version":1`, `"version":99`, 1), "alice", ""); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
}

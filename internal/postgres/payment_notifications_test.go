package postgres

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/money"
	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/Sociaro-Research/media-gateway-dev/internal/providerconfig"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
)

func TestPaymentNotificationsOutbox(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	settings := payments.NotificationSettings{CriticalEmails: []string{"admin@payment.test"}, SuccessEmails: []string{"admin@payment.test"}, Version: 1}
	if _, e := repo.SavePaymentNotificationSettings(ctx, owner, settings); !errors.Is(e, system.ErrForbidden) {
		t.Fatal("member edited subscriptions", e)
	}
	bad := settings
	bad.SuccessEmails = []string{"outside@example.test"}
	if _, e := repo.SavePaymentNotificationSettings(ctx, admin, bad); !errors.Is(e, payments.ErrNotificationSettings) {
		t.Fatal("nonadmin recipient accepted", e)
	}
	saved, e := repo.SavePaymentNotificationSettings(ctx, admin, settings)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = repo.SavePaymentNotificationSettings(ctx, admin, settings); !errors.Is(e, system.ErrConflict) {
		t.Fatal("stale save", e)
	}
	org, e := repo.CreateOrganization(ctx, admin, "Notification team", owner, uuid.NewString()+".fixture.test")
	if e != nil {
		t.Fatal(e)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	m := payments.New(repo, cipher, "https://gateway.test", nil)
	pr, token, e := m.NewIntegration(ctx, admin, "Bank transfer", true)
	if e != nil {
		t.Fatal(e)
	}
	in := payments.ExternalInput{OrganizationID: org.ID, ExternalID: "notification-fixture", AmountCents: 500, Currency: "usd", FeeCents: 46, Revision: 1}
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if e = db.QueryRow(`SELECT COUNT(*) FROM payment_notifications WHERE kind='success'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("duplicate success", count, e)
	}
	n, found, e := repo.ClaimPaymentNotification(ctx)
	if e != nil || !found || n.Kind != "success" || n.Notice.CreditedCents != 500 {
		t.Fatal(n, found, e)
	}
	if _, found, e = repo.ClaimPaymentNotification(ctx); e != nil || found {
		t.Fatal("lease ignored", found, e)
	}
	if e = repo.FinishPaymentNotification(ctx, n, false); e != nil {
		t.Fatal(e)
	}
	h, e := repo.PaymentNotificationHealth(ctx)
	if e != nil || h.Retrying != 1 {
		t.Fatal(h, e)
	}
	db.Exec(`UPDATE payment_notifications SET next_attempt_at=NOW()`)
	again, found, e := repo.ClaimPaymentNotification(ctx)
	if e != nil || !found || again.ID != n.ID || again.Notice != n.Notice || again.Lease == n.Lease {
		t.Fatal("retry changed payload", again, e)
	}
	// An expired lease holder must not acknowledge a newer claim.
	if e = repo.FinishPaymentNotification(ctx, n, true); e != nil {
		t.Fatal(e)
	}
	h, _ = repo.PaymentNotificationHealth(ctx)
	if h.Pending != 1 {
		t.Fatal("stale worker acknowledged newer lease")
	}
	if e = repo.FinishPaymentNotification(ctx, again, true); e != nil {
		t.Fatal(e)
	}
	h, _ = repo.PaymentNotificationHealth(ctx)
	if h.Pending != 0 || h.LastSent == nil {
		t.Fatal(h)
	}
	// A delayed failure must persist its initial time across repeated attempts.
	f := payments.Failure{Scope: "external:" + pr.ID, ProviderID: pr.ID, Code: "settlement_delayed", Delay: 5 * time.Minute}
	for i := 0; i < 3; i++ {
		if e = repo.RecordPaymentFailure(ctx, f); e != nil {
			t.Fatal(e)
		}
	}
	if e = repo.QueuePaymentAlerts(ctx); e != nil {
		t.Fatal(e)
	}
	h, _ = repo.PaymentNotificationHealth(ctx)
	if h.Pending != 0 {
		t.Fatal("alert before grace period")
	}
	db.Exec(`UPDATE payment_incidents SET notify_after=NOW()-INTERVAL '1 second'`)
	for i := 0; i < 3; i++ {
		if e = repo.QueuePaymentAlerts(ctx); e != nil {
			t.Fatal(e)
		}
	}
	h, _ = repo.PaymentNotificationHealth(ctx)
	if h.Pending != 1 {
		t.Fatal("incident duplicated", h)
	}
	n, found, e = repo.ClaimPaymentNotification(ctx)
	if e != nil || !found || n.Kind != "critical" {
		t.Fatal(n, found, e)
	}
	repo.FinishPaymentNotification(ctx, n, true)
	repo.ResolvePaymentFailure(ctx, f.Scope)
	f.Code = "verification_mismatch"
	f.Delay = 0
	if e = repo.RecordPaymentFailure(ctx, f); e != nil {
		t.Fatal(e)
	}
	h, _ = repo.PaymentNotificationHealth(ctx)
	if h.Pending != 1 {
		t.Fatal("new incident not alerted", h)
	}
	// Removing a recipient cancels outstanding mail, rather than leaking details.
	saved.CriticalEmails = []string{}
	if _, e = repo.SavePaymentNotificationSettings(ctx, admin, saved); e != nil {
		t.Fatal(e)
	}
	if _, found, e = repo.ClaimPaymentNotification(ctx); e != nil || found {
		t.Fatal("unsubscribed recipient still receives mail", e)
	}
	// Test payments never enqueue success mail.
	testProvider, testToken, e := m.NewIntegration(ctx, admin, "Test bank", false)
	if e != nil {
		t.Fatal(e)
	}
	in.ExternalID = "test"
	if _, e = m.ExternalSettlement(ctx, testProvider.ID, payments.HashToken(testToken), in); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT COUNT(*) FROM payment_notifications WHERE kind='success'`).Scan(&count); e != nil || count != 1 {
		t.Fatal("test success alerted", count, e)
	}
	// If the atomic outbox insert fails, neither a credit nor a success escapes.
	if _, e = db.Exec(`ALTER TABLE payment_notifications ADD CONSTRAINT reject_test_notice CHECK(kind <> 'success') NOT VALID`); e != nil {
		t.Fatal(e)
	}
	in.ExternalID = "rollback"
	if _, e = m.ExternalSettlement(ctx, pr.ID, payments.HashToken(token), in); e == nil {
		t.Fatal("failed outbox insert was ignored")
	}
	if e = db.QueryRow(`SELECT COUNT(*) FROM payments WHERE external_id='rollback'`).Scan(&count); e != nil || count != 0 {
		t.Fatal("partial payment commit", count, e)
	}
}

type failingNotificationAdapter struct {
	paymentAdapter
	failure error
}

func (a *failingNotificationAdapter) Reconcile(ctx context.Context, pr payments.Provider, c payments.Credentials, p payments.Payment) (payments.Snapshot, error) {
	if a.failure != nil {
		return payments.Snapshot{}, a.failure
	}
	return a.paymentAdapter.Reconcile(ctx, pr, c, p)
}
func TestPaymentNotificationProcessingFailureAndRecovery(t *testing.T) {
	db, repo, admin, owner := paymentDatabase(t)
	ctx := context.Background()
	if _, err := repo.SavePaymentNotificationSettings(ctx, admin, payments.NotificationSettings{CriticalEmails: []string{"admin@payment.test"}, SuccessEmails: []string{"admin@payment.test"}, Version: 1}); err != nil {
		t.Fatal(err)
	}
	cipher, _ := providerconfig.NewCipher(strings.Repeat("c", 32))
	adapter := &failingNotificationAdapter{failure: payments.ErrConflict}
	m := payments.New(repo, cipher, "https://gateway.test", map[string]payments.Adapter{"stripe": adapter})
	org, err := repo.CreateOrganization(ctx, admin, "Notification recovery", owner, uuid.NewString()+".fixture.test")
	if err != nil {
		t.Fatal(err)
	}
	pr, err := m.Save(ctx, admin, "", payments.ProviderUpdate{Kind: "stripe", Name: "Stripe", Enabled: true, SecretKey: "sk_live_fixture", WebhookSecret: "whsec_fixture", FeeMode: "organization"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.Checkout(ctx, owner, org.ID, pr.ID, money.USD(50*money.Scale), "notify-recovery", "organization")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err = m.Reconcile(ctx, p.ID); !errors.Is(err, payments.ErrConflict) {
			t.Fatal(err)
		}
	}
	n, found, err := repo.ClaimPaymentNotification(ctx)
	if err != nil || !found || n.Kind != "critical" || n.Notice.PaymentID != p.ID || n.Notice.Code != "verification_mismatch" {
		t.Fatal(n, found, err)
	}
	if err = repo.FinishPaymentNotification(ctx, n, true); err != nil {
		t.Fatal(err)
	}
	if _, found, err = repo.ClaimPaymentNotification(ctx); err != nil || found {
		t.Fatal("duplicate critical alert", err)
	}
	adapter.failure = nil
	for i := 0; i < 3; i++ {
		if _, err = m.Reconcile(ctx, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	n, found, err = repo.ClaimPaymentNotification(ctx)
	if err != nil || !found || n.Kind != "success" || n.Notice.CreditedCents != 4500 {
		t.Fatal(n, found, err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM payment_incidents WHERE resolved_at IS NULL`).Scan(&count); err != nil || count != 0 {
		t.Fatal("recovered incident stayed open", count, err)
	}
	// Revoking a role cancels queued notifications even when the address remains subscribed.
	if err = repo.FinishPaymentNotification(ctx, n, false); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE users SET is_system_admin=FALSE WHERE id=$1`, admin); err != nil {
		t.Fatal(err)
	}
	if _, found, err = repo.ClaimPaymentNotification(ctx); err != nil || found {
		t.Fatal("revoked admin received mail", err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM payment_notifications WHERE sent_at IS NULL AND cancelled_at IS NOT NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatal("revocation did not cancel retry", count, err)
	}
}

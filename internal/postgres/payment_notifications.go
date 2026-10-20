package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Sociaro-Research/media-gateway-dev/internal/payments"
	"github.com/Sociaro-Research/media-gateway-dev/internal/system"
	"github.com/google/uuid"
)

func readPaymentNotificationSettings(ctx context.Context, q rowQuerier) (payments.NotificationSettings, error) {
	var s payments.NotificationSettings
	var critical, success []byte
	err := q.QueryRowContext(ctx, `SELECT critical_emails,success_emails,version FROM payment_notification_settings WHERE id=TRUE`).Scan(&critical, &success, &s.Version)
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(critical, &s.CriticalEmails); err != nil {
		return s, err
	}
	err = json.Unmarshal(success, &s.SuccessEmails)
	return s, err
}
func (r *SystemRepository) PaymentNotificationSettings(ctx context.Context) (payments.NotificationSettings, error) {
	return readPaymentNotificationSettings(ctx, r.db)
}
func (r *SystemRepository) SavePaymentNotificationSettings(ctx context.Context, actor string, next payments.NotificationSettings) (payments.NotificationSettings, error) {
	if actor == "" {
		return next, system.ErrForbidden
	}
	if err := next.Normalize(); err != nil {
		return next, err
	}
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return next, err
	}
	defer tx.Rollback()
	if _, err = readSystemSettings(ctx, tx, true); err != nil {
		return next, err
	}
	_, err = systemActor(ctx, tx, actor)
	if err != nil {
		return next, err
	}
	before, err := readPaymentNotificationSettings(ctx, tx)
	if err != nil {
		return next, err
	}
	if before.Version != next.Version {
		return next, system.ErrConflict
	}
	for _, list := range [][]string{next.CriticalEmails, next.SuccessEmails} {
		for _, email := range list {
			var exists bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(email)=$1 AND is_system_admin)`, email).Scan(&exists); err != nil {
				return next, err
			}
			if !exists {
				return next, payments.ErrNotificationSettings
			}
		}
	}
	critical, _ := json.Marshal(next.CriticalEmails)
	success, _ := json.Marshal(next.SuccessEmails)
	next.Version++
	if _, err = tx.ExecContext(ctx, `UPDATE payment_notification_settings SET critical_emails=$1,success_emails=$2,version=$3 WHERE id=TRUE`, critical, success, next.Version); err != nil {
		return next, err
	}
	return next, tx.Commit()
}
func (r *SystemRepository) PaymentNotificationHealth(ctx context.Context) (payments.NotificationHealth, error) {
	var h payments.NotificationHealth
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER(WHERE sent_at IS NULL AND cancelled_at IS NULL),COUNT(*) FILTER(WHERE sent_at IS NULL AND cancelled_at IS NULL AND attempts>0),MAX(sent_at) FROM payment_notifications`).Scan(&h.Pending, &h.Retrying, &h.LastSent)
	return h, err
}

// Called inside the same transaction as ledger posting. The recipient list and
// notice are snapshots, so retries do not regenerate different email contents.
func queuePaymentNotice(ctx context.Context, tx *sql.Tx, key, kind string, n payments.Notice) error {
	data, err := json.Marshal(n)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO payment_notifications(id,event_key,kind,recipient,notice)
 SELECT gen_random_uuid(),$1,$2,e.email,$3 FROM payment_notification_settings s,
 LATERAL jsonb_array_elements_text(CASE WHEN $2='critical' THEN s.critical_emails ELSE s.success_emails END) e(email)
 WHERE EXISTS(SELECT 1 FROM users u WHERE LOWER(u.email)=e.email AND u.is_system_admin)
 ON CONFLICT(event_key,recipient) DO NOTHING`, key, kind, data)
	return err
}
func paymentNotice(ctx context.Context, tx *sql.Tx, provider, payment string) (payments.Notice, error) {
	var n payments.Notice
	err := tx.QueryRowContext(ctx, `SELECT pr.name,COALESCE(p.id::text,''),COALESCE(p.organization_id::text,''),COALESCE(o.name,'Not identified'),COALESCE(p.amount_cents,0),COALESCE(p.fee_cents,0),COALESCE(p.credited_cents,0),COALESCE((p.billing->>'tax_cents')::bigint,0),COALESCE((p.stablecoin->>'fee_micros')::bigint,0),NOW()
 FROM payment_providers pr LEFT JOIN payments p ON p.id=NULLIF($2,'')::uuid AND p.provider_id=pr.id LEFT JOIN organizations o ON o.id=p.organization_id WHERE pr.id=$1`, provider, payment).Scan(&n.Provider, &n.PaymentID, &n.OrganizationID, &n.Organization, &n.AmountCents, &n.FeeCents, &n.CreditedCents, &n.TaxCents, &n.StablecoinFeeMicros, &n.At)
	return n, err
}
func (r *SystemRepository) RecordPaymentFailure(ctx context.Context, f payments.Failure) error {
	// Only known live providers can create incidents; invalid signatures and
	// untrusted client input are filtered by the manager before reaching here.
	_, err := r.db.ExecContext(ctx, `INSERT INTO payment_incidents(id,scope,provider_id,payment_id,code,notify_after)
 SELECT $1,$2,pr.id,(SELECT id FROM payments WHERE id=NULLIF($4,'')::uuid AND provider_id=pr.id),$5,NOW()+($6*INTERVAL '1 second') FROM payment_providers pr WHERE pr.id=$3 AND pr.live
 ON CONFLICT(scope) WHERE resolved_at IS NULL DO UPDATE SET last_seen=NOW(),code=EXCLUDED.code,notify_after=LEAST(payment_incidents.notify_after,EXCLUDED.notify_after)`, uuid.NewString(), f.Scope, f.ProviderID, f.PaymentID, f.Code, int64(f.Delay/time.Second))
	if err == nil && f.Delay == 0 {
		return r.QueuePaymentAlerts(ctx)
	}
	return err
}
func (r *SystemRepository) ResolvePaymentFailure(ctx context.Context, scope string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE payment_incidents SET resolved_at=NOW() WHERE scope=$1 AND resolved_at IS NULL`, scope)
	return err
}
func (r *SystemRepository) QueuePaymentAlerts(ctx context.Context) error {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,provider_id,COALESCE(payment_id::text,''),code,first_seen FROM payment_incidents WHERE resolved_at IS NULL AND notified_at IS NULL AND notify_after<=NOW() ORDER BY notify_after LIMIT 100 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	type incident struct {
		id, provider, payment, code string
		at                          time.Time
	}
	items := []incident{}
	for rows.Next() {
		var i incident
		if err = rows.Scan(&i.id, &i.provider, &i.payment, &i.code, &i.at); err != nil {
			rows.Close()
			return err
		}
		items = append(items, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, i := range items {
		n, e := paymentNotice(ctx, tx, i.provider, i.payment)
		if e != nil {
			return e
		}
		n.Code = i.code
		n.At = i.at
		if e = queuePaymentNotice(ctx, tx, "incident:"+i.id, "critical", n); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `UPDATE payment_incidents SET notified_at=NOW() WHERE id=$1`, i.id); e != nil {
			return e
		}
	}
	return tx.Commit()
}
func (r *SystemRepository) ClaimPaymentNotification(ctx context.Context) (payments.Notification, bool, error) {
	tx, err := beginAuditTx(ctx, r.db)
	if err != nil {
		return payments.Notification{}, false, err
	}
	defer tx.Rollback()
	// Stop queued delivery after unsubscribe or administrator-role revocation.
	_, err = tx.ExecContext(ctx, `UPDATE payment_notifications n SET cancelled_at=NOW() WHERE n.sent_at IS NULL AND n.cancelled_at IS NULL AND NOT EXISTS(
 SELECT 1 FROM payment_notification_settings s,users u WHERE LOWER(u.email)=n.recipient AND u.is_system_admin AND (CASE WHEN n.kind='critical' THEN s.critical_emails ELSE s.success_emails END) ? n.recipient)`)
	if err != nil {
		return payments.Notification{}, false, err
	}
	var n payments.Notification
	var data []byte
	n.Lease = uuid.NewString()
	err = tx.QueryRowContext(ctx, `WITH due AS (SELECT id FROM payment_notifications WHERE sent_at IS NULL AND cancelled_at IS NULL AND next_attempt_at<=NOW() ORDER BY CASE WHEN kind='critical' THEN 0 ELSE 1 END,created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED)
 UPDATE payment_notifications n SET next_attempt_at=NOW()+INTERVAL '2 minutes',attempts=n.attempts+1,lease=$1 FROM due WHERE n.id=due.id RETURNING n.id,n.recipient,n.kind,n.notice,n.attempts`, n.Lease).Scan(&n.ID, &n.Recipient, &n.Kind, &data, &n.Attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return n, false, tx.Commit()
	}
	if err != nil {
		return n, false, err
	}
	if err = json.Unmarshal(data, &n.Notice); err != nil {
		return n, false, err
	}
	return n, true, tx.Commit()
}
func (r *SystemRepository) FinishPaymentNotification(ctx context.Context, n payments.Notification, sent bool) error {
	delay := time.Duration(1<<min(n.Attempts, 8)) * 15 * time.Second
	if delay > time.Hour {
		delay = time.Hour
	}
	_, err := r.db.ExecContext(ctx, `UPDATE payment_notifications SET sent_at=CASE WHEN $3 THEN NOW() ELSE NULL END,next_attempt_at=NOW()+($4*INTERVAL '1 second'),lease=NULL WHERE id=$1 AND lease=$2 AND sent_at IS NULL`, n.ID, n.Lease, sent, int64(delay/time.Second))
	return err
}

var _ payments.NotificationRepository = (*SystemRepository)(nil)

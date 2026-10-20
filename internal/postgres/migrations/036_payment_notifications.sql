-- +goose Up
CREATE TABLE payment_notification_settings (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id),
 critical_emails JSONB NOT NULL DEFAULT '[]', success_emails JSONB NOT NULL DEFAULT '[]',
 version BIGINT NOT NULL DEFAULT 1,
 CHECK(jsonb_typeof(critical_emails)='array' AND jsonb_typeof(success_emails)='array')
);
INSERT INTO payment_notification_settings(id) VALUES(TRUE);
CREATE TABLE payment_incidents (
 id UUID PRIMARY KEY, scope TEXT NOT NULL, provider_id UUID NOT NULL REFERENCES payment_providers(id),
 payment_id UUID REFERENCES payments(id), code TEXT NOT NULL,
 first_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(), last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 notify_after TIMESTAMPTZ NOT NULL, notified_at TIMESTAMPTZ, resolved_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX payment_incident_open ON payment_incidents(scope) WHERE resolved_at IS NULL;
CREATE TABLE payment_notifications (
 id UUID PRIMARY KEY, event_key TEXT NOT NULL, kind TEXT NOT NULL CHECK(kind IN ('critical','success')),
 recipient TEXT NOT NULL, notice JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 attempts INTEGER NOT NULL DEFAULT 0, lease UUID, sent_at TIMESTAMPTZ, cancelled_at TIMESTAMPTZ,
 UNIQUE(event_key,recipient)
);
CREATE INDEX payment_notification_due ON payment_notifications(next_attempt_at) WHERE sent_at IS NULL AND cancelled_at IS NULL;
-- +goose Down
DROP TABLE payment_notifications;
DROP TABLE payment_incidents;
DROP TABLE payment_notification_settings;

-- +goose Up
ALTER TABLE payments ADD COLUMN checkout_attempt BIGINT NOT NULL DEFAULT 0 CHECK(checkout_attempt>=0);
ALTER TABLE payments ADD COLUMN checkout_started_at TIMESTAMPTZ;
ALTER TABLE payments ADD COLUMN expires_at TIMESTAMPTZ;
ALTER TABLE payments ADD COLUMN checkout_expired BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE payments p SET expires_at=p.created_at+INTERVAL '720 hours'
 FROM payment_providers pr WHERE p.provider_id=pr.id AND pr.kind IN ('stripe','stablecoin');
UPDATE payments SET checkout_expired=TRUE,status='pending'
 WHERE status='expired' AND NOT settled AND expires_at>NOW() AND stablecoin IS NULL;
UPDATE payments SET status='expired',checkout_url=''
 WHERE NOT settled AND status IN ('pending','confirming') AND expires_at<=NOW();
CREATE INDEX payment_request_expiry ON payments(expires_at)
 WHERE NOT settled AND status IN ('pending','confirming');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back invoice checkout attempts'; END $$;
-- +goose StatementEnd

-- +goose Up
ALTER TABLE user_api_keys ADD COLUMN monthly_limit_micros BIGINT CHECK (monthly_limit_micros >= 0);
ALTER TABLE tasks ADD COLUMN cost JSONB;
ALTER TABLE tasks ADD COLUMN provider_identity TEXT NOT NULL DEFAULT '';
CREATE INDEX tasks_key_month_idx ON tasks(api_key_id,created_at) WHERE api_key_id IS NOT NULL;
CREATE TABLE provider_bill_records (
 owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,
 record_id TEXT NOT NULL,
 billing_month TEXT NOT NULL,
 payload JSONB NOT NULL,
 fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(owner_id,provider,record_id)
);
CREATE TABLE provider_billing_sync (
 owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 provider TEXT NOT NULL,
 checked_at TIMESTAMPTZ NOT NULL,
 status TEXT NOT NULL,
 message TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(owner_id,provider)
);
-- +goose Down
DROP TABLE provider_billing_sync;
DROP TABLE provider_bill_records;
DROP INDEX tasks_key_month_idx;
ALTER TABLE tasks DROP COLUMN provider_identity;
ALTER TABLE tasks DROP COLUMN cost;
ALTER TABLE user_api_keys DROP COLUMN monthly_limit_micros;

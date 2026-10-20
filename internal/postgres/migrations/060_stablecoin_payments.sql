-- +goose Up
ALTER TABLE payment_providers DROP CONSTRAINT payment_providers_kind_check;
ALTER TABLE payment_providers ADD CONSTRAINT payment_providers_kind_check CHECK(kind IN ('stripe','external','stablecoin'));
ALTER TABLE payment_providers ADD COLUMN stablecoin JSONB;
CREATE UNIQUE INDEX payment_stablecoin_provider ON payment_providers(kind) WHERE kind='stablecoin';
ALTER TABLE payments ADD COLUMN stablecoin JSONB;
ALTER TABLE payments ADD COLUMN fee_micros BIGINT GENERATED ALWAYS AS (COALESCE((stablecoin->>'fee_micros')::bigint,fee_cents*10000)) STORED;
-- Permanent reservations include cancelled/paid requests: late transfers must
-- never be assigned to another organization.
CREATE UNIQUE INDEX payment_stablecoin_amount ON payments ((stablecoin->>'network'),(stablecoin->>'contract'),(stablecoin->>'address'),(stablecoin->>'amount')) WHERE stablecoin IS NOT NULL;
CREATE TABLE stablecoin_watchers (
 network TEXT NOT NULL, address TEXT NOT NULL, next_block BIGINT NOT NULL CHECK(next_block>=0),
 checked_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch', PRIMARY KEY(network,address)
);
CREATE TABLE stablecoin_transfers (
 network TEXT NOT NULL, transaction_hash TEXT NOT NULL, log_index BIGINT NOT NULL,
 contract TEXT NOT NULL, address TEXT NOT NULL, amount TEXT NOT NULL,
 block_number BIGINT NOT NULL, block_hash TEXT NOT NULL, confirmed BOOLEAN NOT NULL,
 canonical BOOLEAN NOT NULL DEFAULT TRUE, payment_id UUID REFERENCES payments(id),
 received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(network,transaction_hash,log_index)
);
CREATE UNIQUE INDEX stablecoin_payment_transfer ON stablecoin_transfers(payment_id) WHERE payment_id IS NOT NULL;
DROP TRIGGER audit_payment_providers ON payment_providers;
CREATE TRIGGER audit_payment_providers AFTER INSERT OR UPDATE ON payment_providers FOR EACH ROW EXECUTE FUNCTION audit_management_change('payment_provider','payment_provider','kind,name,enabled,live,account_id,fee_mode,stablecoin','secret_cipher,token_hash');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back stablecoin financial history'; END $$;
-- +goose StatementEnd

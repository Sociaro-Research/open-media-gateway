-- +goose Up
CREATE TABLE payment_providers (
 id UUID PRIMARY KEY, kind TEXT NOT NULL CHECK(kind IN ('stripe','external')),
 fee_mode TEXT NOT NULL DEFAULT 'service' CHECK(fee_mode IN ('service','organization')),
 name TEXT NOT NULL, enabled BOOLEAN NOT NULL DEFAULT FALSE, live BOOLEAN NOT NULL,
 account_id TEXT NOT NULL DEFAULT '', version BIGINT NOT NULL DEFAULT 1,
 created_by UUID NOT NULL REFERENCES users(id), secret_cipher BYTEA, token_hash BYTEA,
 last_webhook_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX payment_stripe_account ON payment_providers(account_id,live) WHERE kind='stripe';
CREATE TABLE payments (
 id UUID PRIMARY KEY, organization_id UUID NOT NULL REFERENCES organizations(id),
 provider_id UUID NOT NULL REFERENCES payment_providers(id), actor_id UUID NOT NULL REFERENCES users(id),
 idempotency_key TEXT NOT NULL, external_id TEXT,
 fee_mode TEXT NOT NULL CHECK(fee_mode IN ('service','organization')), fee_cents BIGINT NOT NULL DEFAULT 0 CHECK(fee_cents>=0 AND fee_cents<=amount_cents),
 amount_cents BIGINT NOT NULL CHECK(amount_cents BETWEEN 100 AND 1000000), currency TEXT NOT NULL CHECK(currency='usd'),
 live BOOLEAN NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
 session_id TEXT NOT NULL DEFAULT '', intent_id TEXT NOT NULL DEFAULT '', checkout_url TEXT NOT NULL DEFAULT '', receipt_url TEXT NOT NULL DEFAULT '',
 credited_cents BIGINT NOT NULL DEFAULT 0 CHECK(credited_cents>=0 AND credited_cents<=amount_cents),
 refunded_cents BIGINT NOT NULL DEFAULT 0 CHECK(refunded_cents>=0 AND refunded_cents<=amount_cents),
 held_cents BIGINT NOT NULL DEFAULT 0 CHECK(held_cents>=0 AND held_cents<=amount_cents),
 settled BOOLEAN NOT NULL DEFAULT FALSE, revision BIGINT NOT NULL DEFAULT 0,
 next_reconcile_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(organization_id,actor_id,idempotency_key), UNIQUE(provider_id,external_id)
);
CREATE UNIQUE INDEX payment_session ON payments(provider_id,session_id) WHERE session_id<>'';
CREATE INDEX payment_due ON payments(next_reconcile_at);
CREATE INDEX payment_org_history ON payments(organization_id,created_at DESC,id);
ALTER TABLE organization_credits ADD COLUMN payment_id UUID REFERENCES payments(id);
ALTER TABLE organization_credits ADD COLUMN payment_revision BIGINT;
CREATE UNIQUE INDEX payment_credit_revision ON organization_credits(payment_id,payment_revision) WHERE payment_id IS NOT NULL;
-- +goose Down
-- Financial history must not be silently discarded.
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back the payments ledger migration'; END $$;
-- +goose StatementEnd

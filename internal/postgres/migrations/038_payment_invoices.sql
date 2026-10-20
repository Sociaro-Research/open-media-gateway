-- +goose Up
-- Existing purchases keep their original financial and Checkout contract.
ALTER TABLE payments ADD COLUMN tax_mode TEXT NOT NULL DEFAULT '' CHECK(tax_mode IN ('','exclusive'));
ALTER TABLE payments ADD COLUMN customer_id TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN billing JSONB;
ALTER TABLE payments ADD COLUMN invoice JSONB;
CREATE TABLE payment_customers (
 organization_id UUID NOT NULL REFERENCES organizations(id),
 provider_id UUID NOT NULL REFERENCES payment_providers(id),
 customer_id TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(organization_id,provider_id)
);
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back payment invoices'; END $$;
-- +goose StatementEnd

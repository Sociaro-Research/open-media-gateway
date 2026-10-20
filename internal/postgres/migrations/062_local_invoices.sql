-- +goose Up
ALTER TABLE payment_providers ADD COLUMN stripe_invoices BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE payments ADD COLUMN invoice_mode TEXT NOT NULL DEFAULT '' CHECK(invoice_mode IN ('','stripe','local'));
ALTER TABLE payments ADD COLUMN vat JSONB;
UPDATE payments SET invoice_mode=CASE WHEN stablecoin IS NOT NULL THEN 'local' WHEN tax_mode='exclusive' THEN 'stripe' ELSE '' END,
 vat=stablecoin->'vat';
CREATE SEQUENCE payment_invoice_numbers;
CREATE TABLE payment_invoice_documents (
 payment_id UUID PRIMARY KEY REFERENCES payments(id),
 number TEXT NOT NULL UNIQUE,
 created_at TIMESTAMPTZ NOT NULL,
 pdf BYTEA NOT NULL CHECK(octet_length(pdf)>0)
);
DROP TRIGGER audit_payment_providers ON payment_providers;
CREATE TRIGGER audit_payment_providers AFTER INSERT OR UPDATE ON payment_providers FOR EACH ROW EXECUTE FUNCTION audit_management_change('payment_provider','payment_provider','kind,name,enabled,live,account_id,fee_mode,stablecoin,stripe_invoices','secret_cipher,token_hash');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back issued invoice history'; END $$;
-- +goose StatementEnd

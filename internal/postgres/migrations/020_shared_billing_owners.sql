-- +goose Up
-- Billing connections can belong to the installation, not just a login account.
ALTER TABLE provider_bill_records DROP CONSTRAINT provider_bill_records_owner_id_fkey;
ALTER TABLE provider_billing_sync DROP CONSTRAINT provider_billing_sync_owner_id_fkey;

-- +goose Down
ALTER TABLE provider_bill_records ADD CONSTRAINT provider_bill_records_owner_id_fkey FOREIGN KEY(owner_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;
ALTER TABLE provider_billing_sync ADD CONSTRAINT provider_billing_sync_owner_id_fkey FOREIGN KEY(owner_id) REFERENCES users(id) ON DELETE CASCADE NOT VALID;

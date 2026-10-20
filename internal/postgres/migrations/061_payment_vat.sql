-- +goose Up
CREATE TABLE payment_vat_settings (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id), data JSONB NOT NULL DEFAULT '{}',
 version BIGINT NOT NULL DEFAULT 1, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO payment_vat_settings(id) VALUES(TRUE);
CREATE TABLE organization_vat_verifications (
 organization_id UUID PRIMARY KEY REFERENCES organizations(id),
 data JSONB NOT NULL, version BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TRIGGER audit_payment_vat_settings AFTER UPDATE ON payment_vat_settings FOR EACH ROW EXECUTE FUNCTION audit_management_change('payment_vat_settings','payment_vat_settings','data','');
CREATE TRIGGER audit_organization_vat_verifications AFTER INSERT OR UPDATE ON organization_vat_verifications FOR EACH ROW EXECUTE FUNCTION audit_management_change('vat_verification','vat_verification','organization_id,data','');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a backup to roll back VAT configuration and verification evidence'; END $$;
-- +goose StatementEnd

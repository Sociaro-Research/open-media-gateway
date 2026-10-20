-- +goose Up
ALTER TABLE organizations ADD COLUMN billing_profile JSONB;
ALTER TABLE organizations ADD COLUMN billing_payment_created_at TIMESTAMPTZ;
ALTER TABLE organizations ADD COLUMN billing_payment_id UUID;
-- Preserve the most recent known company details from existing successful payments.
WITH latest AS (
 SELECT DISTINCT ON (organization_id) organization_id,id,created_at,billing
 FROM payments WHERE live AND settled AND NULLIF(BTRIM(billing->>'business_name'),'') IS NOT NULL
 ORDER BY organization_id,created_at DESC,id DESC
)
UPDATE organizations o SET billing_profile=jsonb_build_object(
 'business_name',p.billing->'business_name','email',p.billing->'email',
 'address',p.billing->'address','tax_ids',p.billing->'tax_ids'),
 billing_payment_created_at=p.created_at,billing_payment_id=p.id
FROM latest p WHERE p.organization_id=o.id;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back organization billing profiles'; END $$;
-- +goose StatementEnd

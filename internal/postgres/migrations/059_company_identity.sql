-- +goose Up
ALTER TABLE organizations ADD COLUMN domain TEXT NOT NULL DEFAULT '';
UPDATE organizations o SET domain=c.domain FROM client_onboardings c WHERE c.organization_id=o.id AND c.domain<>'';
-- Existing duplicates are retained. All application creation/rename paths share
-- the settings transaction lock and check these keys before writing.
CREATE INDEX organizations_identity_name ON organizations(lower(regexp_replace(btrim(name),'\s+',' ','g')));
CREATE INDEX organizations_identity_domain ON organizations(domain) WHERE domain<>'';
DROP TRIGGER audit_organizations ON organizations;
CREATE TRIGGER audit_organizations AFTER INSERT OR UPDATE ON organizations FOR EACH ROW EXECUTE FUNCTION audit_management_change('settings','organization','name,domain,credit_limit_micros,default_weekly_limit_micros,minimize_request_logging,client_manager_id,billing_profile,markup_basis_points,markup_exceptions,request_limits','');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a backup to roll back company website identity'; END $$;
-- +goose StatementEnd

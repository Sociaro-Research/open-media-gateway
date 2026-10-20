-- +goose Up
ALTER TABLE organization_invites ADD COLUMN role TEXT NOT NULL DEFAULT 'member'
 CHECK (role IN ('supermanager','admin','content_officer','developer','member'));
UPDATE organization_invites SET role='supermanager' WHERE first_owner;
DROP TRIGGER audit_organization_invites ON organization_invites;
CREATE TRIGGER audit_organization_invites AFTER INSERT OR UPDATE ON organization_invites
 FOR EACH ROW EXECUTE FUNCTION audit_management_change('invitation','invitation','expires_at,accepted_by,revoked_at,role','');

-- +goose Down
DROP TRIGGER audit_organization_invites ON organization_invites;
CREATE TRIGGER audit_organization_invites AFTER INSERT OR UPDATE ON organization_invites
 FOR EACH ROW EXECUTE FUNCTION audit_management_change('invitation','invitation','expires_at,accepted_by,revoked_at','');
ALTER TABLE organization_invites DROP COLUMN role;

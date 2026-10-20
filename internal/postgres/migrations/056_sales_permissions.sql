-- +goose Up
CREATE TABLE sales_permission_grants (
 id UUID PRIMARY KEY, employee_id UUID NOT NULL REFERENCES users(id),
 permission TEXT NOT NULL CHECK(permission IN ('pricing','models','topup')),
 issuer_id UUID NOT NULL REFERENCES users(id), parent_id UUID REFERENCES sales_permission_grants(id),
 limit_micros BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), revoked_at TIMESTAMPTZ,
 CHECK(employee_id<>issuer_id),
 CHECK((permission='topup' AND limit_micros>0 AND limit_micros<=1000000000000) OR (permission<>'topup' AND limit_micros=0))
);
CREATE UNIQUE INDEX sales_permission_active ON sales_permission_grants(employee_id,permission) WHERE revoked_at IS NULL;
CREATE INDEX sales_permission_parent ON sales_permission_grants(parent_id);
CREATE TRIGGER audit_sales_permissions AFTER INSERT OR UPDATE ON sales_permission_grants FOR EACH ROW EXECUTE FUNCTION audit_management_change('sales_permission','sales_permission','employee_id,permission,issuer_id,parent_id,limit_micros,revoked_at','');
-- Effective grants must have an unbroken chain to a current administrator.
CREATE VIEW effective_sales_permissions AS
 WITH RECURSIVE grants AS (
  SELECT g.* FROM sales_permission_grants g JOIN users e ON e.id=g.employee_id JOIN users i ON i.id=g.issuer_id
  WHERE g.revoked_at IS NULL AND g.parent_id IS NULL AND e.is_client_manager AND i.is_system_admin
  UNION ALL
  SELECT g.* FROM sales_permission_grants g JOIN grants p ON p.id=g.parent_id
  JOIN users e ON e.id=g.employee_id JOIN users i ON i.id=g.issuer_id
  WHERE g.revoked_at IS NULL AND e.is_client_manager AND i.is_client_manager
  AND p.employee_id=g.issuer_id AND p.permission=g.permission AND g.limit_micros<=p.limit_micros
  AND i.id<>e.id AND sales_manages(i.id,e.id)
 ) SELECT * FROM grants;
-- Permanent invalidation prevents moving back or regranting from reviving old delegations.
-- +goose StatementBegin
CREATE FUNCTION invalidate_sales_permissions() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 UPDATE sales_permission_grants SET revoked_at=clock_timestamp()
 WHERE revoked_at IS NULL AND id NOT IN (SELECT id FROM effective_sales_permissions);
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER invalidate_sales_permissions AFTER UPDATE OF is_client_manager,sales_role,sales_supervisor_id,is_system_admin ON users FOR EACH STATEMENT EXECUTE FUNCTION invalidate_sales_permissions();
-- +goose StatementBegin
CREATE FUNCTION sales_permission_changed() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 UPDATE sales_structure SET version=version+1 WHERE id=TRUE;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER sales_permission_version AFTER INSERT OR UPDATE ON sales_permission_grants FOR EACH STATEMENT EXECUTE FUNCTION sales_permission_changed();
DROP TRIGGER audit_organization_credits ON organization_credits;
CREATE TRIGGER audit_organization_credits AFTER INSERT OR UPDATE ON organization_credits FOR EACH ROW EXECUTE FUNCTION audit_management_change('credit','credit','amount_micros,kind,reason,external_id,payment_id,payment_revision','');
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a backup to roll back sales permission history'; END $$;
-- +goose StatementEnd

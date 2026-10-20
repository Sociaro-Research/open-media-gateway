-- +goose Up
-- Keep legacy limit values as historical metadata; current grants are boolean.
-- +goose StatementBegin
DO $$ DECLARE constraint_name TEXT; BEGIN
 FOR constraint_name IN SELECT conname FROM pg_constraint
 WHERE conrelid='sales_permission_grants'::regclass AND contype='c'
 AND pg_get_constraintdef(oid) LIKE '%limit_micros%'
 LOOP EXECUTE format('ALTER TABLE sales_permission_grants DROP CONSTRAINT %I',constraint_name); END LOOP;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE VIEW effective_sales_permissions AS
 WITH RECURSIVE grants AS (
  SELECT g.* FROM sales_permission_grants g JOIN users e ON e.id=g.employee_id JOIN users i ON i.id=g.issuer_id
  WHERE g.revoked_at IS NULL AND g.parent_id IS NULL AND e.is_client_manager AND i.is_system_admin
  UNION ALL
  SELECT g.* FROM sales_permission_grants g JOIN grants p ON p.id=g.parent_id
  JOIN users e ON e.id=g.employee_id JOIN users i ON i.id=g.issuer_id
  WHERE g.revoked_at IS NULL AND e.is_client_manager AND i.is_client_manager
  AND p.employee_id=g.issuer_id AND p.permission=g.permission
  AND i.id<>e.id AND sales_manages(i.id,e.id)
 ) SELECT * FROM grants;
DROP TRIGGER audit_sales_permissions ON sales_permission_grants;
CREATE TRIGGER audit_sales_permissions AFTER INSERT OR UPDATE ON sales_permission_grants FOR EACH ROW EXECUTE FUNCTION audit_management_change('sales_permission','sales_permission','employee_id,permission,issuer_id,parent_id,revoked_at','');
UPDATE sales_structure SET version=version+1 WHERE id=TRUE;
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a backup to reinstate previous sales permission semantics'; END $$;
-- +goose StatementEnd

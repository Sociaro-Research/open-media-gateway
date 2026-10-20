-- +goose Up
-- The legacy flag remains the restrictive sales-account boundary for every rank.
ALTER TABLE users ADD COLUMN sales_role TEXT NOT NULL DEFAULT 'client_manager'
 CHECK (sales_role IN ('client_manager','lead_client_manager','regional_client_manager'));
ALTER TABLE users ADD COLUMN sales_supervisor_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE users ADD CONSTRAINT sales_parent_not_self CHECK (sales_supervisor_id IS DISTINCT FROM id);
ALTER TABLE users ADD CONSTRAINT sales_parent_requires_role CHECK (is_client_manager OR sales_supervisor_id IS NULL);
CREATE INDEX users_sales_parent ON users(sales_supervisor_id) WHERE is_client_manager;
CREATE TABLE sales_structure (id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id), version BIGINT NOT NULL DEFAULT 1);
INSERT INTO sales_structure(id) VALUES(TRUE);

-- +goose StatementBegin
CREATE FUNCTION sales_rank(role TEXT) RETURNS INTEGER LANGUAGE SQL IMMUTABLE AS $$
 SELECT CASE role WHEN 'regional_client_manager' THEN 3 WHEN 'lead_client_manager' THEN 2 WHEN 'client_manager' THEN 1 ELSE 0 END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION check_sales_hierarchy() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE parent_rank INTEGER;
BEGIN
 IF NEW.sales_supervisor_id IS NOT NULL THEN
  SELECT sales_rank(sales_role) INTO parent_rank FROM users WHERE id=NEW.sales_supervisor_id AND is_client_manager;
  IF parent_rank IS NULL OR parent_rank<=sales_rank(NEW.sales_role) THEN
   RAISE EXCEPTION 'sales supervisor must have a higher sales rank' USING ERRCODE='23514';
  END IF;
 END IF;
 IF EXISTS(SELECT 1 FROM users WHERE sales_supervisor_id=NEW.id AND (NOT NEW.is_client_manager OR sales_rank(sales_role)>=sales_rank(NEW.sales_role))) THEN
  RAISE EXCEPTION 'reassign incompatible direct reports before changing sales rank' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER check_sales_hierarchy BEFORE INSERT OR UPDATE OF is_client_manager,sales_role,sales_supervisor_id ON users FOR EACH ROW EXECUTE FUNCTION check_sales_hierarchy();

-- +goose StatementBegin
CREATE FUNCTION sales_structure_changed() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF (OLD.is_client_manager,OLD.sales_role,OLD.sales_supervisor_id) IS DISTINCT FROM (NEW.is_client_manager,NEW.sales_role,NEW.sales_supervisor_id) THEN
  UPDATE sales_structure SET version=version+1 WHERE id=TRUE;
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER sales_structure_changed AFTER UPDATE OF is_client_manager,sales_role,sales_supervisor_id ON users FOR EACH ROW EXECUTE FUNCTION sales_structure_changed();
CREATE TRIGGER audit_sales_hierarchy AFTER UPDATE OF is_client_manager,sales_role,sales_supervisor_id ON users FOR EACH ROW EXECUTE FUNCTION audit_management_change('sales_structure','sales_employee','is_client_manager,sales_role,sales_supervisor_id','');

-- +goose StatementBegin
CREATE FUNCTION sales_manages(actor UUID, employee UUID) RETURNS BOOLEAN LANGUAGE SQL STABLE AS $$
 WITH RECURSIVE chain AS (
  SELECT id,sales_supervisor_id FROM users WHERE id=employee AND is_client_manager
  UNION
  SELECT u.id,u.sales_supervisor_id FROM users u JOIN chain c ON u.id=c.sales_supervisor_id WHERE u.is_client_manager
 ) SELECT EXISTS(SELECT 1 FROM chain WHERE id=actor)
$$;
-- +goose StatementEnd

-- +goose Down
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back the sales hierarchy'; END $$;

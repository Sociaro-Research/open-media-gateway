-- +goose Up
-- Revisions belong to storage configuration, not to a caller-supplied connection
-- identifier. The sequence also prevents delete/reinsert from reusing an epoch.
CREATE SEQUENCE IF NOT EXISTS storage_configuration_revision_seq AS BIGINT;
ALTER TABLE storage_connections ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT nextval('storage_configuration_revision_seq');
ALTER TABLE storage_settings ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT nextval('storage_configuration_revision_seq');
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION storage_configuration_revision_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 NEW.revision := nextval('storage_configuration_revision_seq');
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS storage_connection_revision_guard ON storage_connections;
CREATE TRIGGER storage_connection_revision_guard BEFORE INSERT OR UPDATE ON storage_connections FOR EACH ROW EXECUTE FUNCTION storage_configuration_revision_guard();
DROP TRIGGER IF EXISTS storage_settings_revision_guard ON storage_settings;
CREATE TRIGGER storage_settings_revision_guard BEFORE INSERT OR UPDATE ON storage_settings FOR EACH ROW EXECUTE FUNCTION storage_configuration_revision_guard();
-- Old pending rows deliberately remain NULL: recovery must not bless a replacement.
ALTER TABLE reference_deletion_admissions ADD COLUMN IF NOT EXISTS physical_custody BYTEA;
-- +goose Down
-- Retain epochs across rollback/re-upgrade: issued custody must never mistake a
-- changed configuration for the one it captured, including writes by old code.
SELECT 1;

-- +goose Up
ALTER TABLE users ADD COLUMN IF NOT EXISTS table_columns JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(table_columns)='object');
ALTER TABLE users ADD COLUMN IF NOT EXISTS table_wide JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(table_wide)='object');
DROP TRIGGER audit_users ON users;
CREATE TRIGGER audit_users AFTER INSERT OR UPDATE ON users FOR EACH ROW
EXECUTE FUNCTION audit_management_change('profile','user','email,display_name,theme,report_columns,color_preset,table_columns,table_wide','');

-- +goose Down
-- Retain saved preferences on rollback; re-upgrade is additive and idempotent.
DROP TRIGGER audit_users ON users;
CREATE TRIGGER audit_users AFTER INSERT OR UPDATE ON users FOR EACH ROW
EXECUTE FUNCTION audit_management_change('profile','user','email,display_name,theme,report_columns,color_preset','');

-- +goose Up
ALTER TABLE users ADD COLUMN color_preset TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD CONSTRAINT users_color_preset_format CHECK (
    color_preset = '' OR color_preset ~ '^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$'
);
DROP TRIGGER audit_users ON users;
CREATE TRIGGER audit_users AFTER INSERT OR UPDATE ON users FOR EACH ROW
EXECUTE FUNCTION audit_management_change('profile','user','email,display_name,theme,report_columns,color_preset','');

-- +goose Down
DROP TRIGGER audit_users ON users;
CREATE TRIGGER audit_users AFTER INSERT OR UPDATE ON users FOR EACH ROW
EXECUTE FUNCTION audit_management_change('profile','user','email,display_name,theme,report_columns','');
ALTER TABLE users DROP COLUMN color_preset;

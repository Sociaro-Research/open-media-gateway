-- +goose Up
ALTER TABLE users ADD COLUMN is_client_manager BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD CONSTRAINT exclusive_system_roles CHECK (NOT (is_system_admin AND is_client_manager));
ALTER TABLE organizations ADD COLUMN client_manager_id UUID REFERENCES users(id) ON DELETE SET NULL;
CREATE INDEX organizations_client_manager ON organizations(client_manager_id) WHERE client_manager_id IS NOT NULL;

-- +goose Down
ALTER TABLE organizations DROP COLUMN client_manager_id;
ALTER TABLE users DROP CONSTRAINT exclusive_system_roles;
ALTER TABLE users DROP COLUMN is_client_manager;

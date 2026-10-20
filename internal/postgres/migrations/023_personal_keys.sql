-- +goose Up
-- Personal credentials no longer require membership. Historical organization data stays put.
UPDATE user_api_keys SET organization_id=NULL, storage_mode='personal' WHERE resource_mode='personal';

-- +goose Down
-- Reattaching personal credentials to an organization would grant different access.
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Restore a database backup to undo personal key separation';
END $$;
-- +goose StatementEnd

-- +goose Up
ALTER TABLE system_settings ADD COLUMN shared_resources BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN shared_monthly_limit_micros BIGINT NOT NULL DEFAULT 0 CHECK(shared_monthly_limit_micros >= 0);
ALTER TABLE user_api_keys ADD COLUMN resource_mode TEXT NOT NULL DEFAULT 'personal' CHECK(resource_mode IN ('personal','shared'));
ALTER TABLE user_api_keys ADD COLUMN storage_mode TEXT NOT NULL DEFAULT 'personal' CHECK(storage_mode IN ('personal','shared'));
ALTER TABLE tasks ADD COLUMN provider_owner_id UUID;
ALTER TABLE tasks ADD COLUMN storage_owner_id UUID;
UPDATE tasks SET provider_owner_id=owner_id, storage_owner_id=owner_id;
ALTER TABLE tasks ALTER COLUMN provider_owner_id SET NOT NULL;
ALTER TABLE tasks ALTER COLUMN storage_owner_id SET NOT NULL;
CREATE INDEX tasks_provider_owner ON tasks(provider_owner_id,provider,created_at);
CREATE INDEX tasks_shared_budget ON tasks(owner_id,created_at) WHERE provider_owner_id='00000000-0000-0000-0000-000000000001';
-- +goose Down
DROP INDEX tasks_shared_budget;
DROP INDEX tasks_provider_owner;
ALTER TABLE tasks DROP COLUMN storage_owner_id, DROP COLUMN provider_owner_id;
ALTER TABLE user_api_keys DROP COLUMN storage_mode, DROP COLUMN resource_mode;
ALTER TABLE users DROP COLUMN shared_monthly_limit_micros;
ALTER TABLE system_settings DROP COLUMN shared_resources;

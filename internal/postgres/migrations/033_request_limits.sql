-- +goose Up
ALTER TABLE system_settings ADD COLUMN request_limits JSONB NOT NULL DEFAULT '{"key_generations_minute":100,"user_generations_minute":300,"key_generations_second":20,"user_generations_second":50,"key_reads_minute":1200,"user_reads_minute":3600,"user_key_creations_minute":5,"user_active_keys":20}', ADD COLUMN request_limits_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE organizations ADD COLUMN request_limits JSONB NOT NULL DEFAULT '{}', ADD COLUMN request_limits_version BIGINT NOT NULL DEFAULT 1;
CREATE INDEX user_api_keys_active_owner_idx ON user_api_keys(user_id,organization_id,expires_at) WHERE revoked_at IS NULL;
-- +goose Down
DROP INDEX user_api_keys_active_owner_idx;
ALTER TABLE organizations DROP COLUMN request_limits, DROP COLUMN request_limits_version;
ALTER TABLE system_settings DROP COLUMN request_limits, DROP COLUMN request_limits_version;

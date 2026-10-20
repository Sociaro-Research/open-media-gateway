-- +goose Up
-- Availability is controlled by each provider connection and its model settings.
ALTER TABLE system_settings DROP COLUMN allowed_providers;

-- +goose Down
ALTER TABLE system_settings ADD COLUMN allowed_providers JSONB NOT NULL DEFAULT '["mock","bytedance","alibaba","openai"]'::jsonb;

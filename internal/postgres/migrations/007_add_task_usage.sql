-- +goose Up
ALTER TABLE tasks ADD COLUMN provider_usage JSONB;

-- +goose Down
ALTER TABLE tasks DROP COLUMN provider_usage;

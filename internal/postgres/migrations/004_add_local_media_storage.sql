-- +goose Up
ALTER TABLE tasks
    ADD COLUMN provider_result_url TEXT,
    ADD COLUMN storage_key TEXT,
    ADD COLUMN result_content_type TEXT,
    ADD COLUMN result_size_bytes BIGINT CHECK (result_size_bytes >= 0),
    ADD COLUMN result_sha256 TEXT,
    ADD COLUMN stored_at TIMESTAMPTZ;

UPDATE tasks
SET provider_result_url = result_url
WHERE result_url IS NOT NULL;

-- +goose Down
ALTER TABLE tasks
    DROP COLUMN stored_at,
    DROP COLUMN result_sha256,
    DROP COLUMN result_size_bytes,
    DROP COLUMN result_content_type,
    DROP COLUMN storage_key,
    DROP COLUMN provider_result_url;

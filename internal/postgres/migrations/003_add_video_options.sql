-- +goose Up
ALTER TABLE tasks
    ADD COLUMN request_options JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN last_frame_url TEXT;

-- +goose Down
ALTER TABLE tasks
    DROP COLUMN last_frame_url,
    DROP COLUMN request_options;

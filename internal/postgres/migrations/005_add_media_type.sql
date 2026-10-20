-- +goose Up
ALTER TABLE tasks
    ADD COLUMN media_type TEXT NOT NULL DEFAULT 'video'
    CHECK (media_type IN ('image', 'video', 'speech', 'music', 'audio'));

-- +goose Down
ALTER TABLE tasks DROP COLUMN media_type;

-- +goose Up
ALTER TABLE users ALTER COLUMN theme SET DEFAULT 'system';

-- +goose Down
ALTER TABLE users ALTER COLUMN theme SET DEFAULT 'dark';

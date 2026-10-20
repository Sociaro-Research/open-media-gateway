-- +goose Up
ALTER TABLE users ADD COLUMN custom_display_name BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN theme TEXT NOT NULL DEFAULT 'dark' CHECK (theme IN ('dark', 'light', 'system'));

-- +goose Down
ALTER TABLE users DROP COLUMN theme;
ALTER TABLE users DROP COLUMN custom_display_name;

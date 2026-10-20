-- +goose Up
ALTER TABLE system_settings ADD COLUMN allow_inline_references BOOLEAN NOT NULL DEFAULT TRUE;

-- +goose Down
ALTER TABLE system_settings DROP COLUMN allow_inline_references;

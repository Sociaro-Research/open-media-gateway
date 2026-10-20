-- +goose Up
-- Existing default and organization rates retain their meaning and value.
ALTER TABLE system_settings ADD COLUMN markup_exceptions JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(markup_exceptions) = 'array');
ALTER TABLE organizations ADD COLUMN markup_exceptions JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(markup_exceptions) = 'array');
ALTER TABLE organizations ADD COLUMN markup_version BIGINT NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE organizations DROP COLUMN markup_version;
ALTER TABLE organizations DROP COLUMN markup_exceptions;
ALTER TABLE system_settings DROP COLUMN markup_exceptions;

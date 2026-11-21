-- +goose Up
-- Marks a pending connect request that a second start reused, so the start that
-- created it cannot discard it when it fails while the other start still uses it.
ALTER TABLE remote_workspace_authorizations ADD COLUMN IF NOT EXISTS shared BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose Down
ALTER TABLE remote_workspace_authorizations DROP COLUMN IF EXISTS shared;

-- +goose Up
-- Marks a pending connect request the central server accepted, so a failed start that
-- created it cannot discard it after a concurrent start returned it.
ALTER TABLE remote_workspace_authorizations ADD COLUMN IF NOT EXISTS accepted BOOLEAN NOT NULL DEFAULT FALSE;
-- +goose Down
ALTER TABLE remote_workspace_authorizations DROP COLUMN IF EXISTS accepted;

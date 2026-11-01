-- +goose Up
-- Existing installations retain their settings and do not reopen onboarding.
ALTER TABLE system_settings ADD COLUMN setup_pending BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE remote_workspaces (
 owner_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 version BIGINT NOT NULL CHECK(version>0),
 active BOOLEAN NOT NULL DEFAULT FALSE,
 origin TEXT NOT NULL DEFAULT '',
 organization_id TEXT NOT NULL DEFAULT '',
 organization_name TEXT NOT NULL DEFAULT '',
 remote_user_id TEXT NOT NULL DEFAULT '',
 remote_key_id TEXT NOT NULL DEFAULT '',
 token_hash BYTEA,
 token_ciphertext BYTEA,
 verified_at TIMESTAMPTZ
);
-- +goose Down
DROP TABLE remote_workspaces;
ALTER TABLE system_settings DROP COLUMN setup_pending;

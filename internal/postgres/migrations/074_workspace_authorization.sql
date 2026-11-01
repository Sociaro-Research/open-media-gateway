-- +goose Up
-- Existing deployments never enable public customer provisioning implicitly.
ALTER TABLE system_settings ADD COLUMN customer_workspaces BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE workspace_authorizations (
 device_hash BYTEA PRIMARY KEY,
 user_code TEXT NOT NULL UNIQUE,
 client_name TEXT NOT NULL,
 client_origin TEXT NOT NULL,
 replace_key_id UUID,
 approval_hash BYTEA,
 replace_approved BOOLEAN NOT NULL DEFAULT FALSE,
 status TEXT NOT NULL CHECK(status IN ('pending','approved','issued','denied','cancelled')),
 expires_at TIMESTAMPTZ NOT NULL,
 next_poll_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 actor_id UUID REFERENCES users(id) ON DELETE CASCADE,
 organization_id UUID REFERENCES organizations(id),
 key_id UUID REFERENCES user_api_keys(id),
 token_ciphertext BYTEA
);
CREATE INDEX workspace_authorizations_expiry ON workspace_authorizations(expires_at);
CREATE TABLE customer_workspace_owners (
 user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 organization_id UUID NOT NULL UNIQUE REFERENCES organizations(id)
);
CREATE TABLE remote_workspace_authorizations (
 owner_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 id UUID NOT NULL UNIQUE,
 origin TEXT NOT NULL,
 client_name TEXT NOT NULL,
 client_origin TEXT NOT NULL,
 device_ciphertext BYTEA NOT NULL,
 base_version BIGINT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL
);
-- +goose Down
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back customer workspace authorization'; END $$;

-- +goose Up
-- Existing grants, companies, credits and local connections retain their identity.
CREATE TABLE IF NOT EXISTS workspace_installations (
 installation_hash BYTEA PRIMARY KEY CHECK(octet_length(installation_hash)=32),
 user_id UUID NOT NULL REFERENCES users(id),
 organization_id UUID NOT NULL UNIQUE REFERENCES organizations(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE workspace_authorizations ADD COLUMN IF NOT EXISTS installation_hash BYTEA CHECK(installation_hash IS NULL OR octet_length(installation_hash)=32);
CREATE INDEX IF NOT EXISTS workspace_authorizations_installation ON workspace_authorizations(installation_hash) WHERE installation_hash IS NOT NULL;
CREATE TABLE IF NOT EXISTS remote_installation_identities (
 created_by UUID REFERENCES users(id) ON DELETE SET NULL,
 origin TEXT PRIMARY KEY,
 proof_ciphertext BYTEA NOT NULL
);
ALTER TABLE remote_workspaces ADD COLUMN IF NOT EXISTS provisioning TEXT NOT NULL DEFAULT 'legacy' CHECK(provisioning IN ('legacy','dedicated'));
-- Preserve customer_workspace_owners as historical data; new provisioning is per installation.
-- +goose Down
-- Application rollback keeps all additive structures and admitted identities.
-- Older binaries ignore them. A subsequent Up must reuse the same proof/company.
-- Do not use a schema rollback to delete installation grants or reassign billing.
SELECT 1;

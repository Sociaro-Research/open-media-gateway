-- +goose Up
-- Existing/pending consents retain their generation-only scope and lifetime.
ALTER TABLE workspace_authorizations ADD COLUMN IF NOT EXISTS access_intent TEXT NOT NULL DEFAULT 'generation-v1' CHECK(access_intent IN ('generation-v1','company-api-v1'));
ALTER TABLE remote_workspace_authorizations ADD COLUMN IF NOT EXISTS access_intent TEXT NOT NULL DEFAULT 'generation-v1' CHECK(access_intent IN ('generation-v1','company-api-v1'));
ALTER TABLE remote_workspaces ADD COLUMN IF NOT EXISTS access_intent TEXT NOT NULL DEFAULT 'generation-v1' CHECK(access_intent IN ('generation-v1','company-api-v1'));
ALTER TABLE remote_workspace_recovery ADD COLUMN IF NOT EXISTS access_intent TEXT NOT NULL DEFAULT 'generation-v1' CHECK(access_intent IN ('generation-v1','company-api-v1'));
-- +goose Down
-- Keep consent evidence and all post-upgrade writes on application rollback.
SELECT 1;

-- +goose Up
CREATE TABLE model_settings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    name TEXT NOT NULL,
    public_id TEXT NOT NULL UNIQUE,
    enabled_by_default BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1,
    UNIQUE (provider, model)
);
ALTER TABLE organizations ADD COLUMN model_access_version BIGINT NOT NULL DEFAULT 1;
CREATE TABLE organization_model_grants (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    model_id UUID NOT NULL REFERENCES model_settings(id) ON DELETE CASCADE,
    PRIMARY KEY (organization_id, model_id)
);
-- Freeze the public identifier used by each accepted generation.
ALTER TABLE tasks ADD COLUMN public_model TEXT NOT NULL DEFAULT '';
UPDATE tasks SET public_model=provider || '/' || model;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN RAISE EXCEPTION 'Restore a database backup to roll back model settings'; END $$;
-- +goose StatementEnd

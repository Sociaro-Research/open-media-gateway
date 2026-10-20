-- +goose Up
CREATE TABLE provider_asset_groups (
 owner_id uuid NOT NULL, provider_owner_id uuid NOT NULL, provider text NOT NULL,
 library_scope text NOT NULL, id text NOT NULL, name text NOT NULL,
 PRIMARY KEY(owner_id,provider_owner_id,provider,library_scope,id)
);
CREATE TABLE provider_assets (
 id uuid PRIMARY KEY, owner_id uuid NOT NULL, actor_id text NOT NULL,
 provider_owner_id uuid NOT NULL, provider text NOT NULL, library_scope text NOT NULL,
 source_id text NOT NULL DEFAULT '', asset_id text NOT NULL DEFAULT '',
 status text NOT NULL, data jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX provider_assets_source ON provider_assets(owner_id,provider_owner_id,provider,library_scope,source_id) WHERE source_id<>'' AND status NOT IN ('Deleted','Detached');
CREATE UNIQUE INDEX provider_assets_remote ON provider_assets(owner_id,provider_owner_id,provider,library_scope,asset_id) WHERE asset_id<>'' AND status NOT IN ('Deleted','Detached');
CREATE INDEX provider_assets_pending ON provider_assets(updated_at) WHERE status IN ('Creating','Unknown','Processing');
-- +goose Down
DROP TABLE provider_assets;
DROP TABLE provider_asset_groups;

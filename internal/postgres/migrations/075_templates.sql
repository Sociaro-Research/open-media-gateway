-- +goose Up
-- Template media is a deliberate durable copy, independent of reference-library
-- deletion, task retention and soft removal of a gallery entry.
CREATE TABLE IF NOT EXISTS generation_templates (
 id uuid PRIMARY KEY,
 version bigint NOT NULL DEFAULT 1,
 revision bigint NOT NULL DEFAULT 0,
 draft jsonb NOT NULL,
 published jsonb,
 updated_at timestamptz NOT NULL DEFAULT now(),
 removed_at timestamptz
);
CREATE TABLE IF NOT EXISTS template_media (
 id uuid PRIMARY KEY,
 template_id uuid NOT NULL REFERENCES generation_templates(id),
 content_type text NOT NULL,
 size_bytes bigint NOT NULL CHECK(size_bytes>0),
 storage_key text NOT NULL UNIQUE,
 sha256 text NOT NULL,
 published boolean NOT NULL DEFAULT false
);
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS template_snapshot jsonb;

-- +goose Down
-- Deliberately retain published media and generation history on rollback.
SELECT 1;

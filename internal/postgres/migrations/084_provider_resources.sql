-- +goose Up
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_media_type_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_media_type_check CHECK (media_type IN ('image','video','speech','music','audio','text','json','resource'));
CREATE TABLE IF NOT EXISTS provider_resources (
 id uuid PRIMARY KEY, owner_id uuid NOT NULL, actor_id text NOT NULL,
 provider_id text NOT NULL, connection_id text NOT NULL, provider_owner_id uuid NOT NULL,
 revision text NOT NULL CHECK(revision<>''), remote_id text NOT NULL CHECK(remote_id<>''),
 kind text NOT NULL CHECK(kind IN ('element','voice','face_session','editing_session')),
 subtype text NOT NULL DEFAULT '', name text NOT NULL, description text NOT NULL DEFAULT '',
 status text NOT NULL CHECK(status IN ('queued','running','succeeded','failed','deleting','deleted')),
 is_preset boolean NOT NULL DEFAULT false,
 sources jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(sources)='array' AND jsonb_array_length(sources)<=64),
 session jsonb CHECK(session IS NULL OR jsonb_typeof(session)='object'),
 bound_voice_binding_id uuid REFERENCES provider_resources(id),
 origin_task_id uuid REFERENCES tasks(id),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), deleted_at timestamptz
);
CREATE UNIQUE INDEX IF NOT EXISTS provider_resources_remote ON provider_resources(owner_id,provider_owner_id,provider_id,connection_id,revision,kind,remote_id) WHERE status<>'deleted';
CREATE INDEX IF NOT EXISTS provider_resources_owner ON provider_resources(owner_id,provider_owner_id,provider_id,updated_at DESC,id);
CREATE INDEX IF NOT EXISTS provider_resources_bound_voice ON provider_resources(bound_voice_binding_id) WHERE status NOT IN ('deleted','failed');
CREATE TABLE IF NOT EXISTS provider_resource_jobs (
 id uuid PRIMARY KEY, binding_id uuid NOT NULL, owner_id uuid NOT NULL, actor_id text NOT NULL,
 provider_id text NOT NULL, connection_id text NOT NULL, provider_owner_id uuid NOT NULL,
 revision text NOT NULL CHECK(revision<>''), kind text NOT NULL CHECK(kind IN ('element','voice','face_session','editing_session')),
 operation text NOT NULL CHECK(operation IN ('create','delete')),
 status text NOT NULL CHECK(status IN ('queued','running','succeeded','failed')),
 intent jsonb NOT NULL CHECK(jsonb_typeof(intent)='object'),
 handle text NOT NULL DEFAULT '', safe_error text NOT NULL DEFAULT '', warning text NOT NULL DEFAULT '',
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 1 AND 200),
 intent_hash text NOT NULL CHECK(length(intent_hash)=64),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), started_at timestamptz, completed_at timestamptz,
 UNIQUE(owner_id,provider_owner_id,provider_id,idempotency_key)
);
CREATE INDEX IF NOT EXISTS provider_resource_jobs_pending ON provider_resource_jobs(updated_at,id) WHERE status IN ('queued','running');
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS result_resource_bindings jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_resource_bindings_array;
ALTER TABLE tasks ADD CONSTRAINT tasks_resource_bindings_array CHECK(jsonb_typeof(result_resource_bindings)='array' AND jsonb_array_length(result_resource_bindings)<=64);
-- +goose Down
-- Preserve bindings, private handles, immutable intent and deletion evidence
-- through an application rollback. Older binaries ignore these additive tables;
-- re-upgrade reuses them and must never resubmit an uncertain remote creation.
SELECT 1;

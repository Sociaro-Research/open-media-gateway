-- +goose Up
CREATE TABLE catalog_sync_settings (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id), timezone TEXT NOT NULL DEFAULT 'UTC',
 email TEXT NOT NULL DEFAULT '', version BIGINT NOT NULL DEFAULT 1
);
INSERT INTO catalog_sync_settings(id) VALUES(TRUE);
CREATE TABLE catalog_sync_schedules (
 provider TEXT PRIMARY KEY, enabled BOOLEAN NOT NULL DEFAULT FALSE,
 clock TEXT NOT NULL, sources JSONB NOT NULL DEFAULT '[]', version BIGINT NOT NULL DEFAULT 1,
 next_check TIMESTAMPTZ, last_check TIMESTAMPTZ, requested BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE TABLE catalog_sync_state (
 provider TEXT PRIMARY KEY, payload JSONB NOT NULL
);
CREATE TABLE catalog_sync_versions (
 provider TEXT NOT NULL, version BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 payload JSONB NOT NULL, PRIMARY KEY(provider,version)
);
CREATE TABLE catalog_sync_documents (
 hash TEXT PRIMARY KEY, content TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE catalog_sync_runs (
 id UUID PRIMARY KEY, provider TEXT NOT NULL, completed_at TIMESTAMPTZ NOT NULL,
 payload JSONB NOT NULL, email_status TEXT NOT NULL, email_attempts INTEGER NOT NULL DEFAULT 0,
 email_retry_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX catalog_sync_runs_provider_time ON catalog_sync_runs(provider,completed_at DESC,id DESC);
CREATE INDEX catalog_sync_email_pending ON catalog_sync_runs(email_retry_at) WHERE email_status IN ('pending','retry');

-- +goose Down
DROP TABLE catalog_sync_runs,catalog_sync_documents,catalog_sync_versions,catalog_sync_state,catalog_sync_schedules,catalog_sync_settings;

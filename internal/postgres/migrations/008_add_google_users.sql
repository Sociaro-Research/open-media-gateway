-- +goose Up
CREATE TABLE users (
    id UUID PRIMARY KEY,
    google_subject TEXT NOT NULL UNIQUE,
    email TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    picture_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX users_email_idx ON users (LOWER(email));

CREATE TABLE user_sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX user_sessions_expiry_idx ON user_sessions (expires_at);

ALTER TABLE tasks
    ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';

DROP INDEX tasks_idempotency_key_idx;
CREATE UNIQUE INDEX tasks_owner_idempotency_key_idx
    ON tasks (owner_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX tasks_owner_recent_idx ON tasks (owner_id, created_at DESC);

ALTER TABLE provider_settings
    DROP CONSTRAINT provider_settings_pkey,
    ADD COLUMN owner_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000';

ALTER TABLE provider_settings
    ADD PRIMARY KEY (owner_id, provider_id);

-- +goose Down
ALTER TABLE provider_settings DROP CONSTRAINT provider_settings_pkey;
ALTER TABLE provider_settings DROP COLUMN owner_id;
ALTER TABLE provider_settings ADD PRIMARY KEY (provider_id);

DROP INDEX tasks_owner_recent_idx;
DROP INDEX tasks_owner_idempotency_key_idx;
CREATE UNIQUE INDEX tasks_idempotency_key_idx
    ON tasks (idempotency_key)
    WHERE idempotency_key IS NOT NULL;
ALTER TABLE tasks DROP COLUMN owner_id;

DROP TABLE user_sessions;
DROP INDEX users_email_idx;
DROP TABLE users;

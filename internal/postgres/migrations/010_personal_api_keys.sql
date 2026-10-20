-- +goose Up
CREATE TABLE user_api_keys (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    prefix TEXT NOT NULL,
    token_hash BYTEA NOT NULL UNIQUE,
    scopes JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);
CREATE INDEX user_api_keys_owner_idx ON user_api_keys(user_id, created_at DESC);
ALTER TABLE tasks ADD COLUMN api_key_id UUID REFERENCES user_api_keys(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE tasks DROP COLUMN api_key_id;
DROP TABLE user_api_keys;

-- +goose Up
CREATE TABLE provider_settings (
    provider_id TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    credentials_ciphertext BYTEA,
    credential_source TEXT NOT NULL DEFAULT 'none'
        CHECK (credential_source IN ('none', 'environment', 'console')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE provider_settings;

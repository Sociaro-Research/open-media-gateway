-- +goose Up
CREATE TABLE login_providers (
 provider TEXT PRIMARY KEY,
 enabled BOOLEAN NOT NULL DEFAULT FALSE,
 connected BOOLEAN NOT NULL DEFAULT FALSE,
 client_id TEXT NOT NULL DEFAULT '',
 secret_ciphertext BYTEA,
 version BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO login_providers(provider,enabled,connected)
 SELECT 'google',google_enabled,FALSE FROM login_method_settings
 UNION ALL SELECT 'email',email_enabled,TRUE FROM login_method_settings;
DROP TABLE login_method_settings;

-- +goose Down
CREATE TABLE login_method_settings (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
 google_enabled BOOLEAN NOT NULL DEFAULT TRUE,
 email_enabled BOOLEAN NOT NULL DEFAULT TRUE,
 version BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO login_method_settings(id,google_enabled,email_enabled)
 VALUES(TRUE,(SELECT enabled FROM login_providers WHERE provider='google'),(SELECT enabled FROM login_providers WHERE provider='email'));
DROP TABLE login_providers;

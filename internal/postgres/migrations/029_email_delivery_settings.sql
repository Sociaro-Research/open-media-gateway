-- +goose Up
CREATE TABLE email_delivery_settings (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    provider TEXT NOT NULL DEFAULT '' CHECK (provider IN ('', 'resend')),
    sender TEXT NOT NULL DEFAULT '',
    key_ciphertext BYTEA,
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (provider = '' OR (sender <> '' AND key_ciphertext IS NOT NULL))
);
INSERT INTO email_delivery_settings(id) VALUES(TRUE);

-- +goose Down
DROP TABLE email_delivery_settings;

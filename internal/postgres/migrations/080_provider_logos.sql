-- +goose Up
CREATE TABLE IF NOT EXISTS provider_logos (
    owner_id uuid NOT NULL,
    provider_id text NOT NULL,
    data bytea NOT NULL CHECK (octet_length(data) <= 1048576),
    PRIMARY KEY (owner_id, provider_id),
    FOREIGN KEY (owner_id, provider_id) REFERENCES provider_settings(owner_id, provider_id)
);

-- +goose Down
-- Keep uploaded logos when rolling application code back. Older code ignores
-- this table; re-upgrade is idempotent and must not erase administrator uploads.
SELECT 1;

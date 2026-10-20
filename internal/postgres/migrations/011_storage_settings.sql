-- +goose Up
CREATE TABLE storage_connections (
    owner_id UUID NOT NULL,
    id UUID NOT NULL,
    config_ciphertext BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, id)
);
CREATE TABLE storage_settings (
    owner_id UUID PRIMARY KEY,
    driver TEXT NOT NULL CHECK (driver IN ('local', 's3')),
    connection_id UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    FOREIGN KEY (owner_id, connection_id) REFERENCES storage_connections(owner_id, id),
    CHECK (driver <> 's3' OR connection_id IS NOT NULL)
);
ALTER TABLE tasks ADD COLUMN last_frame_storage_key TEXT;

-- +goose Down
ALTER TABLE tasks DROP COLUMN last_frame_storage_key;
DROP TABLE storage_settings;
DROP TABLE storage_connections;

-- +goose Up
CREATE TABLE reference_images (
    owner_id UUID NOT NULL,
    id UUID NOT NULL,
    name TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    storage_key TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_id, id)
);
CREATE INDEX reference_images_owner_created ON reference_images (owner_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE reference_images;

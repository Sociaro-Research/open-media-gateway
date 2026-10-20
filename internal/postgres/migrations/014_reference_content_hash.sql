-- +goose Up
ALTER TABLE reference_images ADD COLUMN content_sha256 TEXT;
CREATE UNIQUE INDEX reference_images_owner_hash ON reference_images(owner_id, content_sha256) WHERE content_sha256 IS NOT NULL;
CREATE INDEX reference_images_unhashed_size ON reference_images(owner_id, size_bytes) WHERE content_sha256 IS NULL;

-- +goose Down
DROP INDEX reference_images_unhashed_size;
DROP INDEX reference_images_owner_hash;
ALTER TABLE reference_images DROP COLUMN content_sha256;

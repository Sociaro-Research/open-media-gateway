-- +goose Up
-- Preserve every legacy identity and object in place. Shared assets may have
-- identical bytes under distinct retained IDs; private deduplication is unchanged.
DROP INDEX IF EXISTS reference_images_owner_hash;
CREATE UNIQUE INDEX IF NOT EXISTS reference_images_owner_hash ON reference_images(owner_id,content_sha256)
 WHERE content_sha256 IS NOT NULL AND owner_id <> '00000000-0000-0000-0000-000000000002';
ALTER TABLE template_media DROP CONSTRAINT IF EXISTS template_media_library_source;
CREATE TABLE IF NOT EXISTS deleted_template_assets(id uuid PRIMARY KEY);
ALTER TABLE reference_images ADD COLUMN IF NOT EXISTS storage_owner_id uuid;
ALTER TABLE reference_images ADD COLUMN IF NOT EXISTS template_published boolean NOT NULL DEFAULT false;
ALTER TABLE reference_images ADD COLUMN IF NOT EXISTS deleting boolean NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS reference_images_shared_hash ON reference_images(content_sha256) WHERE owner_id='00000000-0000-0000-0000-000000000002';
INSERT INTO reference_images(owner_id,id,name,content_type,size_bytes,storage_key,content_sha256,storage_owner_id)
 SELECT '00000000-0000-0000-0000-000000000002',id,
 CASE WHEN name='' THEN 'Template media ' || id::text ELSE name END,
 content_type,size_bytes,storage_key,sha256,'00000000-0000-0000-0000-000000000001' FROM template_media m
 WHERE NOT EXISTS(SELECT 1 FROM deleted_template_assets d WHERE d.id=m.id)
 ON CONFLICT(owner_id,id) DO NOTHING;
-- Legacy copies have no uploader field. Preserve their distinct identities;
-- enforce cross-process deduplication for newly uploaded shared content.
CREATE UNIQUE INDEX IF NOT EXISTS reference_images_shared_upload_hash ON reference_images(content_sha256)
 WHERE owner_id='00000000-0000-0000-0000-000000000002' AND actor_id IS NOT NULL AND content_sha256 IS NOT NULL AND NOT deleting;
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM template_media m JOIN reference_images r ON r.owner_id='00000000-0000-0000-0000-000000000002' AND r.id=m.id WHERE r.storage_key<>m.storage_key OR r.content_type<>m.content_type OR r.size_bytes<>m.size_bytes OR r.content_sha256 IS DISTINCT FROM m.sha256) THEN
 RAISE EXCEPTION 'Legacy template asset identity conflicts with shared library metadata';
 END IF;
END $$;
-- +goose StatementEnd
-- +goose Down
-- Preserve canonical identities/bytes and restore the prior migration's
-- constraint shape so a deliberate further schema rollback remains executable.
-- NOT VALID preserves obsolete historical provenance after authorized deletions.
ALTER TABLE template_media ADD CONSTRAINT template_media_library_source
 FOREIGN KEY(library_owner_id,library_reference_id) REFERENCES reference_images(owner_id,id) ON DELETE RESTRICT NOT VALID;

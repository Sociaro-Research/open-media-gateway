-- +goose Up
ALTER TABLE template_media ADD COLUMN name text NOT NULL DEFAULT '';
ALTER TABLE template_media ADD COLUMN library_reference_id uuid;
ALTER TABLE template_media ADD COLUMN library_owner_id uuid GENERATED ALWAYS AS
 (CASE WHEN library_reference_id IS NULL THEN NULL::uuid ELSE '00000000-0000-0000-0000-000000000002'::uuid END) STORED;
ALTER TABLE template_media ADD CONSTRAINT template_media_library_source
 FOREIGN KEY (library_owner_id,library_reference_id) REFERENCES reference_images(owner_id,id) ON DELETE RESTRICT;
CREATE INDEX template_media_library_reference ON template_media(library_reference_id) WHERE library_reference_id IS NOT NULL;

-- +goose Down
DROP INDEX template_media_library_reference;
ALTER TABLE template_media DROP CONSTRAINT template_media_library_source;
ALTER TABLE template_media DROP COLUMN library_owner_id;
ALTER TABLE template_media DROP COLUMN library_reference_id;
ALTER TABLE template_media DROP COLUMN name;

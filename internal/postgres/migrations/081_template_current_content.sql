-- +goose Up
-- One active definition and a visibility flag. Retain the complete pre-transition
-- state as inert recovery data; no runtime query or API exposes/reactivates it.
ALTER TABLE generation_templates ADD COLUMN IF NOT EXISTS legacy_state jsonb;
UPDATE generation_templates SET legacy_state=jsonb_build_object(
 'draft',draft,'published',published,'version',version,'revision',revision)
 WHERE legacy_state IS NULL;
ALTER TABLE generation_templates RENAME COLUMN draft TO content;
ALTER TABLE generation_templates ADD COLUMN is_published boolean NOT NULL DEFAULT false;
-- Keep the previously visible, validated definition. Do not silently publish a
-- divergent legacy draft; invalidate its old editor token instead.
UPDATE generation_templates SET
 version=version+CASE WHEN content IS DISTINCT FROM published THEN 1 ELSE 0 END,
 content=published,is_published=true
 WHERE published IS NOT NULL AND published <> 'null'::jsonb;
-- The old column is redundant: all original values remain in legacy_state.
ALTER TABLE generation_templates DROP COLUMN published;

-- +goose Down
-- Restore the old schema with BOTH aliases containing the CURRENT definition.
-- Keep the inert pre-transition backup; do not restore an obsolete publication.
ALTER TABLE generation_templates ADD COLUMN published jsonb;
UPDATE generation_templates SET published=CASE WHEN is_published THEN content ELSE NULL END;
ALTER TABLE generation_templates RENAME COLUMN content TO draft;
ALTER TABLE generation_templates DROP COLUMN is_published;

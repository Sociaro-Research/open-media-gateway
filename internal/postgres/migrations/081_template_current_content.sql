-- +goose Up
-- One active definition and a visibility flag. Retain the complete pre-transition
-- state as inert recovery data; no runtime query or API exposes/reactivates it.
ALTER TABLE generation_templates ADD COLUMN IF NOT EXISTS legacy_state jsonb;
ALTER TABLE generation_templates ADD COLUMN IF NOT EXISTS recovery_states jsonb NOT NULL DEFAULT '[]';
UPDATE generation_templates SET legacy_state=jsonb_build_object(
 'draft',draft,'published',published,'version',version,'revision',revision)
 WHERE legacy_state IS NULL;
-- Old applications can write new drafts during a schema rollback. Preserve
-- every distinct pre-Up state before choosing the visible current definition.
-- Keep the original legacy_state intact and deduplicate by exact JSON equality.
WITH snapshots AS (
 SELECT id,jsonb_build_object('draft',draft,'published',published,
  'version',version,'revision',revision) AS state FROM generation_templates
)
UPDATE generation_templates t SET recovery_states=t.recovery_states || jsonb_build_array(s.state)
 FROM snapshots s WHERE t.id=s.id AND t.legacy_state IS DISTINCT FROM s.state
 AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(t.recovery_states) saved(state)
  WHERE saved.state=s.state);
ALTER TABLE generation_templates RENAME COLUMN draft TO content;
ALTER TABLE generation_templates ADD COLUMN is_published boolean NOT NULL DEFAULT false;
-- Keep the previously visible, validated definition. Do not silently publish a
-- divergent legacy draft; invalidate its old editor token instead.
UPDATE generation_templates SET
 version=version+CASE WHEN content IS DISTINCT FROM published THEN 1 ELSE 0 END,
 content=published,is_published=true
 WHERE published IS NOT NULL AND published <> 'null'::jsonb;
-- Original and rollback-window values remain in inert recovery columns.
ALTER TABLE generation_templates DROP COLUMN published;

-- +goose Down
-- Restore the old schema with BOTH aliases containing the CURRENT definition.
-- Keep the inert pre-transition backup; do not restore an obsolete publication.
-- Capture the current state before old writers can replace it. This also
-- supports Down for an earlier installation of 081 without recovery_states.
ALTER TABLE generation_templates ADD COLUMN IF NOT EXISTS recovery_states jsonb NOT NULL DEFAULT '[]';
WITH snapshots AS (
 SELECT id,jsonb_build_object('draft',content,
  'published',CASE WHEN is_published THEN content ELSE NULL END,
  'version',version,'revision',revision) AS state FROM generation_templates
)
UPDATE generation_templates t SET recovery_states=t.recovery_states || jsonb_build_array(s.state)
 FROM snapshots s WHERE t.id=s.id AND t.legacy_state IS DISTINCT FROM s.state
 AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(t.recovery_states) saved(state)
  WHERE saved.state=s.state);
ALTER TABLE generation_templates ADD COLUMN published jsonb;
UPDATE generation_templates SET published=CASE WHEN is_published THEN content ELSE NULL END;
ALTER TABLE generation_templates RENAME COLUMN content TO draft;
ALTER TABLE generation_templates DROP COLUMN is_published;

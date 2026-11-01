-- +goose Up
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS journal_revision bigint NOT NULL DEFAULT 0;
-- Revisions observe old and new application writers, including same-timestamp
-- status/project/content changes. They are synchronization metadata only.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_task_journal_revision() RETURNS trigger AS $$
BEGIN
 NEW.journal_revision := OLD.journal_revision + 1;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS tasks_journal_revision ON tasks;
CREATE TRIGGER tasks_journal_revision BEFORE UPDATE ON tasks
 FOR EACH ROW EXECUTE FUNCTION bump_task_journal_revision();
-- Result availability can also change through the storage quota index.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION bump_stored_task_journal_revision() RETURNS trigger AS $$
BEGIN
 IF TG_OP = 'DELETE' THEN
  UPDATE tasks SET journal_revision=journal_revision WHERE id=OLD.task_id AND owner_id=OLD.owner_id;
 ELSE
  UPDATE tasks SET journal_revision=journal_revision WHERE id=NEW.task_id AND owner_id=NEW.owner_id;
  IF TG_OP = 'UPDATE' THEN
   IF OLD.task_id IS DISTINCT FROM NEW.task_id OR OLD.owner_id IS DISTINCT FROM NEW.owner_id THEN
    UPDATE tasks SET journal_revision=journal_revision WHERE id=OLD.task_id AND owner_id=OLD.owner_id;
   END IF;
  END IF;
 END IF;
 RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS stored_objects_task_journal_revision ON stored_objects;
CREATE TRIGGER stored_objects_task_journal_revision AFTER INSERT OR UPDATE OR DELETE ON stored_objects
 FOR EACH ROW EXECUTE FUNCTION bump_stored_task_journal_revision();
-- Complete timestamp/ID ordering without deep OFFSET scans. Reuse the existing
-- assigned-project indexes; the partial indexes cover No Projects, including
-- author-restricted company roles. Counts can use these same narrow indexes.
CREATE INDEX tasks_owner_cursor_history ON tasks(owner_id,created_at DESC,id DESC);
CREATE INDEX tasks_cursor_history ON tasks(created_at DESC,id DESC);
CREATE INDEX tasks_author_cursor_history ON tasks(owner_id,actor_id,created_at DESC,id DESC);
CREATE INDEX tasks_unassigned_cursor_history ON tasks(owner_id,created_at DESC,id DESC) WHERE project_id IS NULL;
CREATE INDEX tasks_author_unassigned_cursor_history ON tasks(owner_id,actor_id,created_at DESC,id DESC) WHERE project_id IS NULL;

-- +goose Down
-- Retain the additive counter and backward-compatible bump triggers through
-- supported older-schema rollbacks. Old application writes must still advance
-- revisions so a browser spanning a rollback/re-upgrade cannot retain stale
-- content. Re-upgrade replaces the same functions/triggers without row rewrites.
DROP INDEX tasks_author_unassigned_cursor_history;
DROP INDEX tasks_unassigned_cursor_history;
DROP INDEX tasks_author_cursor_history;
DROP INDEX tasks_owner_cursor_history;
DROP INDEX tasks_cursor_history;

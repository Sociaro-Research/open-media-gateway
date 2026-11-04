-- +goose Up
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS admission_intent TEXT;
-- The caller intent survives task redaction, project deletion and catalogue edits.
-- Existing rows remain NULL; they keep their verifiable legacy request-hash path.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION task_admission_intent_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.admission_intent IS DISTINCT FROM OLD.admission_intent THEN
  RAISE EXCEPTION 'accepted generation intent is immutable';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS task_admission_intent_guard ON tasks;
CREATE TRIGGER task_admission_intent_guard BEFORE UPDATE ON tasks FOR EACH ROW EXECUTE FUNCTION task_admission_intent_immutable();
-- +goose Down
-- Retain acceptance identity for rollback/re-upgrade recovery.
SELECT 1;

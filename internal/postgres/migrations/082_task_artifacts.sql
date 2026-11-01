-- +goose Up
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS result_artifacts jsonb NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE tasks DROP CONSTRAINT IF EXISTS tasks_result_artifacts_array;
ALTER TABLE tasks ADD CONSTRAINT tasks_result_artifacts_array CHECK (jsonb_typeof(result_artifacts)='array' AND jsonb_array_length(result_artifacts)<=64);

-- +goose Down
-- Preserve private artifact identity, ownership and deletion metadata through a
-- supported application rollback. Schema-81 code ignores this additive column;
-- re-upgrade reuses it without losing non-primary archived results.
SELECT 1;

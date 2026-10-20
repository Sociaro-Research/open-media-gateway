-- +goose Up
ALTER TABLE tasks ADD COLUMN worker_next_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ADD COLUMN worker_started_at TIMESTAMPTZ,
  ADD COLUMN worker_poll_errors INTEGER NOT NULL DEFAULT 0;
UPDATE tasks SET worker_started_at=COALESCE((cost->>'submitted_at')::timestamptz,created_at) WHERE status='running';
CREATE INDEX tasks_due_work_idx ON tasks(worker_next_at,id) WHERE status IN ('queued','running');

-- +goose Down
DROP INDEX tasks_due_work_idx;
ALTER TABLE tasks DROP COLUMN worker_next_at, DROP COLUMN worker_started_at, DROP COLUMN worker_poll_errors;

-- +goose Up
ALTER TABLE tasks ADD COLUMN worker_token UUID, ADD COLUMN worker_lease_until TIMESTAMPTZ;
CREATE INDEX tasks_worker_recovery_idx ON tasks(id) WHERE status IN ('queued','running');

-- +goose Down
DROP INDEX tasks_worker_recovery_idx;
ALTER TABLE tasks DROP COLUMN worker_lease_until, DROP COLUMN worker_token;

-- +goose Up
ALTER TABLE tasks
    ADD COLUMN webhook_url TEXT,
    ADD COLUMN idempotency_key TEXT,
    ADD COLUMN request_hash TEXT;

CREATE UNIQUE INDEX tasks_idempotency_key_idx
    ON tasks (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE task_queue_outbox (
    task_id UUID PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO task_queue_outbox (task_id)
SELECT id FROM tasks WHERE status IN ('queued', 'running')
ON CONFLICT DO NOTHING;

CREATE TABLE webhook_deliveries (
    id UUID PRIMARY KEY,
    task_id UUID NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('task.created', 'task.started', 'task.succeeded', 'task.failed')),
    webhook_url TEXT NOT NULL,
    payload JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    delivered_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX webhook_deliveries_due_idx
    ON webhook_deliveries (next_attempt_at, created_at)
    WHERE status = 'pending';

-- +goose Down
DROP TABLE webhook_deliveries;
DROP TABLE task_queue_outbox;
DROP INDEX tasks_idempotency_key_idx;
ALTER TABLE tasks
    DROP COLUMN request_hash,
    DROP COLUMN idempotency_key,
    DROP COLUMN webhook_url;

-- +goose Up
CREATE TABLE tasks (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    prompt TEXT NOT NULL,
    provider_task_id TEXT,
    result_url TEXT,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX tasks_recoverable_idx
    ON tasks (created_at)
    WHERE status IN ('queued', 'running');

-- +goose Down
DROP TABLE tasks;

-- +goose Up
-- Personal generation labels, scoped to both the data owner and authenticated actor.
CREATE TABLE generation_projects (
 id UUID PRIMARY KEY,
 owner_id UUID NOT NULL,
 actor_id UUID NOT NULL,
 name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX generation_projects_name ON generation_projects(owner_id, actor_id, lower(name));
ALTER TABLE tasks ADD COLUMN project_id UUID REFERENCES generation_projects(id);
CREATE INDEX tasks_project_history ON tasks(owner_id, actor_id, project_id, created_at DESC, id DESC) WHERE project_id IS NOT NULL;

-- +goose Down
DROP INDEX tasks_project_history;
ALTER TABLE tasks DROP COLUMN project_id;
DROP TABLE generation_projects;

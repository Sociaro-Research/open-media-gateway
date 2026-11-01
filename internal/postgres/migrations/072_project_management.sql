-- +goose Up
ALTER TABLE generation_projects ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);
ALTER TABLE tasks ADD COLUMN project_version BIGINT NOT NULL DEFAULT 0 CHECK (project_version >= 0);
-- Deleting a label can never remove generation or media records.
ALTER TABLE tasks DROP CONSTRAINT tasks_project_id_fkey;
ALTER TABLE tasks ADD CONSTRAINT tasks_project_id_fkey FOREIGN KEY(project_id) REFERENCES generation_projects(id) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE tasks DROP CONSTRAINT tasks_project_id_fkey;
ALTER TABLE tasks ADD CONSTRAINT tasks_project_id_fkey FOREIGN KEY(project_id) REFERENCES generation_projects(id);
ALTER TABLE tasks DROP COLUMN project_version;
ALTER TABLE generation_projects DROP COLUMN version;

-- +goose Up
-- Existing owner_id already identifies the company. Keep every project ID/name,
-- actor_id (creator provenance), version and task association unchanged.
-- Retain the legacy uniqueness index for application rollback; new writers
-- serialize company name creation/rename with PostgreSQL transaction locks.
CREATE INDEX IF NOT EXISTS tasks_company_project_history ON tasks(owner_id,project_id,created_at DESC,id DESC) WHERE project_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS generation_projects_company_name ON generation_projects(owner_id,lower(name),created_at,id);
-- +goose Down
-- Roll back the application without rewriting shared grouping or user data.
SELECT 1;

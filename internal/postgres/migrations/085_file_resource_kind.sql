-- +goose Up
-- File uploads are owner- and actual-connection-bound resources too. Extend the
-- constraints additively; preserve the already-applied resource migration.
ALTER TABLE provider_resources DROP CONSTRAINT IF EXISTS provider_resources_kind_check;
ALTER TABLE provider_resources ADD CONSTRAINT provider_resources_kind_check CHECK (kind IN ('element','voice','file_resource','face_session','editing_session'));
ALTER TABLE provider_resource_jobs DROP CONSTRAINT IF EXISTS provider_resource_jobs_kind_check;
ALTER TABLE provider_resource_jobs ADD CONSTRAINT provider_resource_jobs_kind_check CHECK (kind IN ('element','voice','file_resource','face_session','editing_session'));

-- +goose Down
-- Retain the additive kind through rollback: older code ignores these bindings,
-- and narrowing the constraint would reject or discard existing file evidence.
SELECT 1;

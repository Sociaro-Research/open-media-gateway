-- +goose Up
-- A committed group intent admits one provider call; uncertain outcomes remain
-- durable and are never converted into an automatic provider retry.
CREATE TABLE IF NOT EXISTS provider_asset_group_admissions (
 id UUID PRIMARY KEY, owner_id UUID NOT NULL, actor_id UUID NOT NULL,
 provider_owner_id UUID NOT NULL, provider TEXT NOT NULL, library_scope TEXT NOT NULL,
 name TEXT NOT NULL, grant_id UUID NOT NULL, session_id UUID,
 state TEXT NOT NULL DEFAULT 'admitted' CHECK(state IN ('admitted','completed')),
 result JSONB, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), completed_at TIMESTAMPTZ,
 CHECK((state='completed')=(result IS NOT NULL AND completed_at IS NOT NULL))
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_group_admission_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (to_jsonb(NEW)-'state'-'result'-'completed_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'result'-'completed_at')
 OR OLD.state='completed' AND NEW IS DISTINCT FROM OLD
 THEN RAISE EXCEPTION 'group admission is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_group_admission_guard ON provider_asset_group_admissions;
CREATE TRIGGER company_group_admission_guard BEFORE UPDATE ON provider_asset_group_admissions FOR EACH ROW EXECUTE FUNCTION company_group_admission_immutable();
-- Cleanup custody fixes the historical physical destination at admission.
CREATE TABLE IF NOT EXISTS reference_deletion_admissions (
 owner_id UUID NOT NULL, reference_id UUID NOT NULL, storage_owner_id UUID NOT NULL,
 storage_key TEXT NOT NULL, content_sha256 TEXT NOT NULL, size_bytes BIGINT NOT NULL,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','completed')),
 lease_token UUID, lease_until TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), last_attempt TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(), completed_at TIMESTAMPTZ,
 PRIMARY KEY(owner_id,reference_id), CHECK((state='completed')=(completed_at IS NOT NULL))
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reference_deletion_custody_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (to_jsonb(NEW)-'state'-'lease_token'-'lease_until'-'last_attempt'-'completed_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'lease_token'-'lease_until'-'last_attempt'-'completed_at')
 OR OLD.state='completed' AND NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'reference deletion custody is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS reference_deletion_custody_guard ON reference_deletion_admissions;
CREATE TRIGGER reference_deletion_custody_guard BEFORE UPDATE ON reference_deletion_admissions FOR EACH ROW EXECUTE FUNCTION reference_deletion_custody_guard();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION reference_deleting_binding_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  IF EXISTS(SELECT 1 FROM reference_deletion_admissions WHERE owner_id=NEW.owner_id AND reference_id=NEW.id) THEN RAISE EXCEPTION 'deleted reference identity cannot be reused'; END IF;
 ELSIF OLD.deleting AND (NOT NEW.deleting OR NEW.owner_id IS DISTINCT FROM OLD.owner_id OR NEW.id IS DISTINCT FROM OLD.id OR NEW.storage_owner_id IS DISTINCT FROM OLD.storage_owner_id OR NEW.storage_key IS DISTINCT FROM OLD.storage_key OR NEW.content_sha256 IS DISTINCT FROM OLD.content_sha256 OR NEW.size_bytes IS DISTINCT FROM OLD.size_bytes) THEN
  RAISE EXCEPTION 'deleting reference binding is immutable';
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS reference_deleting_binding_guard ON reference_images;
CREATE TRIGGER reference_deleting_binding_guard BEFORE INSERT OR UPDATE ON reference_images FOR EACH ROW EXECUTE FUNCTION reference_deleting_binding_guard();
-- +goose Down
-- Retain uncertain provider-operation custody across rollback/re-upgrade.
SELECT 1;

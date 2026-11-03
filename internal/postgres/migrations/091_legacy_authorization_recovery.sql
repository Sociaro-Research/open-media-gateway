-- +goose Up
-- Preserve unresolved legacy proof custody independently of company sessions.
ALTER TABLE remote_workspace_authorizations ADD COLUMN IF NOT EXISTS connection_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE remote_workspaces ADD COLUMN IF NOT EXISTS connection_revision BIGINT NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS remote_workspace_recovery (
 operation_id UUID PRIMARY KEY,
 owner_id UUID NOT NULL,
 attempt_id UUID NOT NULL,
 origin TEXT NOT NULL,
 client_name TEXT NOT NULL,
 client_origin TEXT NOT NULL,
 device_ciphertext BYTEA NOT NULL,
 identity_ciphertext BYTEA NOT NULL,
 base_version BIGINT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 attempt_revision BIGINT NOT NULL,
 expected_version BIGINT NOT NULL,
 expected_revision BIGINT NOT NULL,
 destination TEXT NOT NULL,
 result_version BIGINT NOT NULL,
 result_revision BIGINT NOT NULL,
 saved_present BOOLEAN NOT NULL,
 saved_snapshot JSONB NOT NULL,
 saved_identity_ciphertext BYTEA,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','cancelled')),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 confirmed_at TIMESTAMPTZ,
 UNIQUE(owner_id,attempt_id),
 CHECK(result_version=expected_version+1),
 CHECK(result_revision=expected_revision+1),
 CHECK((state='cancelled')=(confirmed_at IS NOT NULL))
);
CREATE TABLE IF NOT EXISTS remote_workspace_authorization_receipts (
 attempt_id UUID PRIMARY KEY,
 owner_id UUID NOT NULL,
 origin TEXT NOT NULL,
 base_version BIGINT NOT NULL,
 connection_revision BIGINT NOT NULL,
 saved_version BIGINT NOT NULL,
 token_hash BYTEA NOT NULL CHECK(octet_length(token_hash)=32),
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION legacy_recovery_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (to_jsonb(NEW)-'state'-'confirmed_at') IS DISTINCT FROM (to_jsonb(OLD)-'state'-'confirmed_at')
 OR OLD.state='cancelled' AND NEW IS DISTINCT FROM OLD
 OR OLD.state='pending' AND NEW.state='pending' AND NEW.confirmed_at IS DISTINCT FROM OLD.confirmed_at
 THEN RAISE EXCEPTION 'legacy recovery custody is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS legacy_recovery_guard ON remote_workspace_recovery;
CREATE TRIGGER legacy_recovery_guard BEFORE UPDATE ON remote_workspace_recovery FOR EACH ROW EXECUTE FUNCTION legacy_recovery_immutable();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION legacy_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW IS DISTINCT FROM OLD THEN RAISE EXCEPTION 'legacy receipt is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS legacy_receipt_guard ON remote_workspace_authorization_receipts;
CREATE TRIGGER legacy_receipt_guard BEFORE UPDATE ON remote_workspace_authorization_receipts FOR EACH ROW EXECUTE FUNCTION legacy_receipt_immutable();
-- +goose Down
-- Keep unresolved encrypted custody, exact-save receipts and revision fences.
SELECT 1;

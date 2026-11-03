-- +goose Up
-- A new purpose, never an upgrade of generation-only workspace/API credentials.
-- Empty on migration: no company, membership, money, key or active mode is created.
CREATE TABLE IF NOT EXISTS company_interface_installations (
 installation_hash BYTEA PRIMARY KEY CHECK(octet_length(installation_hash)=32),
 organization_id UUID NOT NULL UNIQUE REFERENCES organizations(id),
 central_origin TEXT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(installation_hash,organization_id,central_origin)
);

CREATE TABLE IF NOT EXISTS company_interface_grants (
 id UUID PRIMARY KEY,
 installation_hash BYTEA NOT NULL CHECK(octet_length(installation_hash)=32),
 organization_id UUID NOT NULL REFERENCES organizations(id),
 central_origin TEXT NOT NULL,
 approved_local_origin TEXT NOT NULL,
 purpose TEXT NOT NULL CHECK(purpose='company-interface'),
 protocol_version INTEGER NOT NULL CHECK(protocol_version=1),
 credential_hash BYTEA NOT NULL UNIQUE CHECK(octet_length(credential_hash)=32),
 revocation_epoch BIGINT NOT NULL DEFAULT 1 CHECK(revocation_epoch>0),
 expires_at TIMESTAMPTZ,
 revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(id, organization_id),
 FOREIGN KEY(installation_hash,organization_id,central_origin)
  REFERENCES company_interface_installations(installation_hash,organization_id,central_origin)
);
-- Explicit reapproval creates a fresh grant ID after revocation, preserving the
-- same installation/company and all terminal old grants/session evidence.
CREATE UNIQUE INDEX IF NOT EXISTS company_interface_one_active_grant
 ON company_interface_grants(installation_hash) WHERE revoked_at IS NULL;

-- Session identity is always a verified central user, distinct from the grant.
-- Hashes authenticate. Durable recovery ciphertext and issuance/rotation/approval
-- transactions require a later additive migration; this schema cannot activate.
CREATE TABLE IF NOT EXISTS company_interface_sessions (
 id UUID PRIMARY KEY,
 grant_id UUID NOT NULL,
 organization_id UUID NOT NULL,
 actor_id UUID NOT NULL REFERENCES users(id),
 local_session_hash BYTEA NOT NULL CHECK(octet_length(local_session_hash)=32),
 grant_epoch BIGINT NOT NULL CHECK(grant_epoch>0),
 session_epoch BIGINT NOT NULL DEFAULT 1 CHECK(session_epoch>0),
 token_version BIGINT NOT NULL DEFAULT 1 CHECK(token_version>0),
 access_hash BYTEA NOT NULL UNIQUE CHECK(octet_length(access_hash)=32),
 access_expires_at TIMESTAMPTZ NOT NULL,
 refresh_hash BYTEA NOT NULL UNIQUE CHECK(octet_length(refresh_hash)=32),
 refresh_expires_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 FOREIGN KEY(grant_id,organization_id) REFERENCES company_interface_grants(id,organization_id),
 CHECK(access_expires_at<=refresh_expires_at)
);
CREATE INDEX IF NOT EXISTS company_interface_sessions_grant ON company_interface_sessions(grant_id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_guard_installation() RETURNS trigger AS $$
BEGIN
 IF NEW IS DISTINCT FROM OLD THEN
  RAISE EXCEPTION 'company interface installation binding is immutable';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_installation_guard ON company_interface_installations;
CREATE TRIGGER company_interface_installation_guard BEFORE UPDATE ON company_interface_installations
 FOR EACH ROW EXECUTE FUNCTION company_interface_guard_installation();

-- Bindings cannot be reassigned when an initiating user is demoted or removed.
-- Revocation is terminal and advances the epoch; old binaries cannot resurrect it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_guard_grant() RETURNS trigger AS $$
BEGIN
 IF ROW(NEW.id,NEW.installation_hash,NEW.organization_id,NEW.central_origin,NEW.approved_local_origin,NEW.purpose,NEW.protocol_version,NEW.credential_hash,NEW.expires_at,NEW.created_at)
    IS DISTINCT FROM ROW(OLD.id,OLD.installation_hash,OLD.organization_id,OLD.central_origin,OLD.approved_local_origin,OLD.purpose,OLD.protocol_version,OLD.credential_hash,OLD.expires_at,OLD.created_at)
    OR NEW.revocation_epoch<OLD.revocation_epoch
    OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at)
    OR (OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL AND NEW.revocation_epoch<=OLD.revocation_epoch)
 THEN RAISE EXCEPTION 'company interface grant binding is immutable'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_grant_guard ON company_interface_grants;
CREATE TRIGGER company_interface_grant_guard BEFORE UPDATE ON company_interface_grants
 FOR EACH ROW EXECUTE FUNCTION company_interface_guard_grant();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_guard_session() RETURNS trigger AS $$
BEGIN
 IF ROW(NEW.id,NEW.grant_id,NEW.organization_id,NEW.actor_id,NEW.local_session_hash,NEW.grant_epoch,NEW.created_at)
    IS DISTINCT FROM ROW(OLD.id,OLD.grant_id,OLD.organization_id,OLD.actor_id,OLD.local_session_hash,OLD.grant_epoch,OLD.created_at)
    OR NEW.session_epoch<OLD.session_epoch OR NEW.token_version<OLD.token_version
    OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at)
    OR (OLD.revoked_at IS NULL AND NEW.revoked_at IS NOT NULL AND NEW.session_epoch<=OLD.session_epoch)
 THEN RAISE EXCEPTION 'company interface session binding is immutable'; END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_session_guard ON company_interface_sessions;
CREATE TRIGGER company_interface_session_guard BEFORE UPDATE ON company_interface_sessions
 FOR EACH ROW EXECUTE FUNCTION company_interface_guard_session();

-- +goose Down
-- Keep new identities and revocation evidence on application rollback. Old code
-- ignores these separate tables. Re-upgrade must never reprovision a company.
SELECT 1;

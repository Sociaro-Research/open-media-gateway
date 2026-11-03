-- +goose Up
-- Additive only. Stable private source-session identities survive cookie hash
-- rotation and preserve revoked delegation evidence after central logout/reset.
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS session_id UUID NOT NULL DEFAULT gen_random_uuid();
CREATE UNIQUE INDEX IF NOT EXISTS user_sessions_private_id ON user_sessions(session_id);
CREATE TABLE IF NOT EXISTS company_interface_source_sessions (
 id UUID PRIMARY KEY,
 actor_id UUID NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 revoked_at TIMESTAMPTZ
);
INSERT INTO company_interface_source_sessions(id,actor_id,expires_at)
 SELECT session_id,user_id,expires_at FROM user_sessions ON CONFLICT(id) DO NOTHING;
ALTER TABLE company_interface_sessions ADD COLUMN IF NOT EXISTS source_session_id UUID REFERENCES company_interface_source_sessions(id);
-- NULL is retained only for pre-protocol fixture/history rows; readers deny it.
CREATE INDEX IF NOT EXISTS company_interface_session_source ON company_interface_sessions(source_session_id);

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_source_lifecycle() RETURNS trigger AS $$
BEGIN
 IF TG_OP='INSERT' THEN
  INSERT INTO company_interface_source_sessions(id,actor_id,expires_at) VALUES(NEW.session_id,NEW.user_id,NEW.expires_at);
  RETURN NEW;
 ELSIF TG_OP='DELETE' THEN
  UPDATE company_interface_source_sessions SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=OLD.session_id;
  UPDATE company_interface_sessions SET revoked_at=clock_timestamp(),session_epoch=session_epoch+1 WHERE source_session_id=OLD.session_id AND revoked_at IS NULL;
  RETURN OLD;
 ELSE
  IF ROW(NEW.session_id,NEW.user_id,NEW.token_hash) IS DISTINCT FROM ROW(OLD.session_id,OLD.user_id,OLD.token_hash) THEN
   RAISE EXCEPTION 'central session identity is immutable';
  END IF;
  UPDATE company_interface_source_sessions SET expires_at=LEAST(expires_at,NEW.expires_at) WHERE id=NEW.session_id;
  RETURN NEW;
 END IF;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_source_lifecycle ON user_sessions;
CREATE TRIGGER company_interface_source_lifecycle AFTER INSERT OR UPDATE OR DELETE ON user_sessions FOR EACH ROW EXECUTE FUNCTION company_interface_source_lifecycle();

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_source_guard() RETURNS trigger AS $$
BEGIN
 IF ROW(NEW.id,NEW.actor_id) IS DISTINCT FROM ROW(OLD.id,OLD.actor_id) OR NEW.expires_at>OLD.expires_at
 OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN
  RAISE EXCEPTION 'central source session cannot be rebound or resurrected';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_source_guard ON company_interface_source_sessions;
CREATE TRIGGER company_interface_source_guard BEFORE UPDATE ON company_interface_source_sessions FOR EACH ROW EXECUTE FUNCTION company_interface_source_guard();

-- Source binding is immutable even when a session is rotated or revoked.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_source_binding_guard() RETURNS trigger AS $$
BEGIN
 IF NEW.source_session_id IS DISTINCT FROM OLD.source_session_id THEN
  RAISE EXCEPTION 'delegated source session is immutable';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_source_binding_guard ON company_interface_sessions;
CREATE TRIGGER company_interface_source_binding_guard BEFORE UPDATE ON company_interface_sessions FOR EACH ROW EXECUTE FUNCTION company_interface_source_binding_guard();

CREATE TABLE IF NOT EXISTS company_interface_attempts (
 id UUID PRIMARY KEY,
 installation_hash BYTEA NOT NULL CHECK(octet_length(installation_hash)=32),
 request_hash BYTEA NOT NULL CHECK(octet_length(request_hash)=32),
 binding JSONB NOT NULL,
 user_code TEXT NOT NULL UNIQUE,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','approved','cancelled','denied')),
 expires_at TIMESTAMPTZ NOT NULL,
 recovery_expires_at TIMESTAMPTZ NOT NULL,
 actor_id UUID REFERENCES users(id),
 source_session_id UUID REFERENCES company_interface_source_sessions(id),
 approval_operation UUID,
 approval_hash BYTEA CHECK(approval_hash IS NULL OR octet_length(approval_hash)=32),
 redeem_operation UUID,
 grant_id UUID REFERENCES company_interface_grants(id),
 session_id UUID REFERENCES company_interface_sessions(id),
 receipt BYTEA,
 CHECK(expires_at<recovery_expires_at),
 CHECK((state='approved')=(receipt IS NOT NULL)),
 CHECK(state<>'approved' OR (actor_id IS NOT NULL AND source_session_id IS NOT NULL AND approval_operation IS NOT NULL AND grant_id IS NOT NULL AND session_id IS NOT NULL))
);
CREATE TABLE IF NOT EXISTS company_interface_rotations (
 session_id UUID NOT NULL REFERENCES company_interface_sessions(id),
 operation_id UUID NOT NULL,
 request_hash BYTEA NOT NULL CHECK(octet_length(request_hash)=32),
 from_version BIGINT NOT NULL CHECK(from_version>0),
 to_version BIGINT NOT NULL CHECK(to_version=from_version+1),
 receipt BYTEA NOT NULL,
 recovery_expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(session_id,operation_id),
 UNIQUE(session_id,from_version)
);
-- Local state is separate control data, never a shadow company/business database.
CREATE TABLE IF NOT EXISTS company_interface_local_contexts (
 local_session_hash BYTEA PRIMARY KEY CHECK(octet_length(local_session_hash)=32),
 context_epoch BIGINT NOT NULL DEFAULT 1 CHECK(context_epoch>0),
 connection_revision BIGINT NOT NULL CHECK(connection_revision>=0),
 grant_id UUID,
 grant_epoch BIGINT,
 pending_attempt UUID,
 pending_ciphertext BYTEA,
 actor_id UUID,
 session_id UUID,
 token_version BIGINT,
 ciphertext BYTEA,
 CHECK((ciphertext IS NULL)=(session_id IS NULL)),
 CHECK((pending_ciphertext IS NULL)=(pending_attempt IS NULL))
);
CREATE TABLE IF NOT EXISTS company_interface_local_control (
 singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(singleton),
 revision BIGINT NOT NULL DEFAULT 0 CHECK(revision>=0),
 installation_hash BYTEA CHECK(installation_hash IS NULL OR octet_length(installation_hash)=32),
 grant_id UUID,
 organization_id UUID,
 central_origin TEXT,
 approved_local_origin TEXT,
 grant_epoch BIGINT CHECK(grant_epoch IS NULL OR grant_epoch>0),
 grant_ciphertext BYTEA,
 CHECK((grant_id IS NULL)=(grant_ciphertext IS NULL)),
 CHECK(grant_id IS NULL OR (installation_hash IS NOT NULL AND organization_id IS NOT NULL AND central_origin IS NOT NULL AND approved_local_origin IS NOT NULL AND grant_epoch IS NOT NULL))
);
INSERT INTO company_interface_local_control(singleton) VALUES(TRUE) ON CONFLICT DO NOTHING;
-- Uncertain pending proofs outlive logout/epoch changes in separate custody.
-- Only positively reconciled terminal outcomes or committed activation clear
-- ciphertext. IDs, destination and original cipher binding remain historical.
CREATE TABLE IF NOT EXISTS company_interface_local_recovery (
 id UUID PRIMARY KEY,
 local_session_hash BYTEA NOT NULL CHECK(octet_length(local_session_hash)=32),
 context_epoch BIGINT NOT NULL CHECK(context_epoch>0),
 connection_revision BIGINT NOT NULL CHECK(connection_revision>=0),
 central_origin TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('pending','quarantined','settled')),
 ciphertext BYTEA,
 confirmed_outcome TEXT CHECK(confirmed_outcome IN ('activated','cancelled','denied','revoked')),
 CHECK((state='settled')=(ciphertext IS NULL)),
 CHECK((state='settled')=(confirmed_outcome IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS company_interface_local_recovery_session ON company_interface_local_recovery(local_session_hash,context_epoch);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_local_recovery_guard() RETURNS trigger AS $$
BEGIN
 IF ROW(NEW.id,NEW.local_session_hash,NEW.context_epoch,NEW.connection_revision,NEW.central_origin)
 IS DISTINCT FROM ROW(OLD.id,OLD.local_session_hash,OLD.context_epoch,OLD.connection_revision,OLD.central_origin)
 OR (OLD.state='settled' AND NEW IS DISTINCT FROM OLD)
 OR (NEW.state<>'settled' AND NEW.ciphertext IS DISTINCT FROM OLD.ciphertext)
 OR (OLD.state='quarantined' AND NEW.state='pending') THEN
  RAISE EXCEPTION 'local recovery custody cannot be rebound or resurrected';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_local_recovery_guard ON company_interface_local_recovery;
CREATE TRIGGER company_interface_local_recovery_guard BEFORE UPDATE ON company_interface_local_recovery FOR EACH ROW EXECUTE FUNCTION company_interface_local_recovery_guard();
-- +goose Down
-- Retain bindings, encrypted uncertain receipts and terminal revocations. Old
-- binaries ignore the separate protocol; rollback cannot resurrect authority.
SELECT 1;

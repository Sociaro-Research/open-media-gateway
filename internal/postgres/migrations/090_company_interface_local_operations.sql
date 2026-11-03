-- +goose Up
-- Per-browser encrypted control custody only; never a local business authority.
CREATE TABLE IF NOT EXISTS company_interface_local_operations (
 id UUID PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('rotation','logout')),
 local_session_hash BYTEA NOT NULL CHECK(octet_length(local_session_hash)=32),
 context_epoch BIGINT NOT NULL CHECK(context_epoch>0),
 connection_revision BIGINT NOT NULL CHECK(connection_revision>=0),
 grant_id UUID NOT NULL,
 grant_epoch BIGINT NOT NULL CHECK(grant_epoch>0),
 actor_id UUID NOT NULL,
 session_id UUID NOT NULL,
 session_epoch BIGINT NOT NULL CHECK(session_epoch>0),
 token_version BIGINT NOT NULL CHECK(token_version>0),
 central_origin TEXT NOT NULL,
 request_hash BYTEA NOT NULL CHECK(octet_length(request_hash)=32),
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','settled')),
 ciphertext BYTEA,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 settled_at TIMESTAMPTZ,
 UNIQUE(local_session_hash,context_epoch,session_id,token_version,kind),
 CHECK((state='pending')=(ciphertext IS NOT NULL)),
 CHECK((state='settled')=(settled_at IS NOT NULL))
);
-- No FK to live context/user/session rows: logout/rebind must retain original
-- uncertain cleanup custody, and a removed source user cannot resurrect it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_local_operation_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.id,NEW.kind,NEW.local_session_hash,NEW.context_epoch,NEW.connection_revision,NEW.grant_id,NEW.grant_epoch,NEW.actor_id,NEW.session_id,NEW.session_epoch,NEW.token_version,NEW.central_origin,NEW.request_hash,NEW.created_at)
 IS DISTINCT FROM
 (OLD.id,OLD.kind,OLD.local_session_hash,OLD.context_epoch,OLD.connection_revision,OLD.grant_id,OLD.grant_epoch,OLD.actor_id,OLD.session_id,OLD.session_epoch,OLD.token_version,OLD.central_origin,OLD.request_hash,OLD.created_at)
 OR OLD.state='settled' AND NEW IS DISTINCT FROM OLD
 OR OLD.state='pending' AND NEW.state='pending' AND NEW.ciphertext IS DISTINCT FROM OLD.ciphertext
 THEN RAISE EXCEPTION 'company operation custody is immutable'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_local_operation_immutable ON company_interface_local_operations;
CREATE TRIGGER company_interface_local_operation_immutable BEFORE UPDATE ON company_interface_local_operations FOR EACH ROW EXECUTE FUNCTION company_interface_local_operation_guard();

-- +goose Down
-- Retain encrypted pending intents and terminal tombstones across rollback.
SELECT 1;

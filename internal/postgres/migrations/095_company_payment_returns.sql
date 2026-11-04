-- +goose Up
-- Empty additive navigation custody; no payment, credit or grant is created.
CREATE TABLE IF NOT EXISTS company_interface_payment_returns (
 id UUID PRIMARY KEY,
 grant_id UUID NOT NULL REFERENCES company_interface_grants(id),
 session_id UUID NOT NULL REFERENCES company_interface_sessions(id),
 operation_id UUID NOT NULL,
 -- Deliberately no payment FK: checking it would acquire a payment KEY SHARE
 -- lock after the company authority locks, inverting existing settlement's
 -- payment -> company order. Issuance verifies retained financial history by
 -- MVCC in the same transaction; no supported API deletes financial records.
 payment_id UUID NOT NULL,
 binding JSONB NOT NULL,
 token_hash BYTEA NOT NULL UNIQUE CHECK(octet_length(token_hash)=32),
 receipt BYTEA NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(grant_id,session_id,operation_id),
 CHECK(expires_at>created_at)
);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION company_interface_payment_return_guard() RETURNS trigger AS $$
BEGIN
 IF ROW(NEW.id,NEW.grant_id,NEW.session_id,NEW.operation_id,NEW.payment_id,NEW.binding,NEW.token_hash,NEW.receipt,NEW.expires_at,NEW.created_at)
 IS DISTINCT FROM ROW(OLD.id,OLD.grant_id,OLD.session_id,OLD.operation_id,OLD.payment_id,OLD.binding,OLD.token_hash,OLD.receipt,OLD.expires_at,OLD.created_at)
 OR (OLD.consumed_at IS NOT NULL AND NEW.consumed_at IS DISTINCT FROM OLD.consumed_at) THEN
  RAISE EXCEPTION 'payment return identity is immutable';
 END IF;
 RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TRIGGER IF EXISTS company_interface_payment_return_guard ON company_interface_payment_returns;
CREATE TRIGGER company_interface_payment_return_guard BEFORE UPDATE ON company_interface_payment_returns
 FOR EACH ROW EXECUTE FUNCTION company_interface_payment_return_guard();

-- +goose Down
-- Keep immutable navigation/replay history across binary rollback. Old code
-- cannot activate this new contract or use these records as payment authority.
SELECT 1;

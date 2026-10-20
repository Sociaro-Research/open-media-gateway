-- +goose Up
ALTER TABLE organization_invites ADD COLUMN recipient_email TEXT NOT NULL DEFAULT '';
ALTER TABLE organization_invites ADD COLUMN first_owner BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE organization_invites ADD COLUMN previous_token_hash BYTEA;
ALTER TABLE organization_invites ADD COLUMN previous_token_expires_at TIMESTAMPTZ;
CREATE TABLE client_onboardings (
 organization_id UUID PRIMARY KEY REFERENCES organizations(id),
 manager_id UUID REFERENCES users(id) ON DELETE SET NULL,
 country TEXT NOT NULL, domain TEXT NOT NULL DEFAULT '', contact_name TEXT NOT NULL,
 email TEXT NOT NULL, comment TEXT NOT NULL DEFAULT '',
 invite_id UUID NOT NULL UNIQUE REFERENCES organization_invites(id),
 version BIGINT NOT NULL DEFAULT 1, activated_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX client_onboardings_manager ON client_onboardings(manager_id);
CREATE INDEX client_onboardings_domain ON client_onboardings(domain) WHERE domain<>'';
CREATE INDEX client_onboardings_email ON client_onboardings(email);
CREATE TABLE client_onboarding_operations (
 actor_id UUID NOT NULL REFERENCES users(id), operation_id UUID NOT NULL,
 fingerprint BYTEA NOT NULL, organization_id UUID NOT NULL REFERENCES organizations(id),
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY(actor_id,operation_id)
);
CREATE INDEX client_onboarding_hour ON client_onboarding_operations(actor_id,created_at,organization_id);
CREATE TABLE owner_invitation_deliveries (
 id UUID PRIMARY KEY, invite_id UUID NOT NULL REFERENCES organization_invites(id),
 organization_id UUID NOT NULL REFERENCES organizations(id),
 token_hash BYTEA NOT NULL, recipient TEXT NOT NULL, company_name TEXT NOT NULL,
 origin TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
 attempts INTEGER NOT NULL DEFAULT 0, lease UUID,
 status TEXT NOT NULL DEFAULT 'sending' CHECK(status IN ('sending','sent','send_failed','cancelled')),
 next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), sent_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX owner_invitation_due ON owner_invitation_deliveries(next_attempt_at) WHERE status IN ('sending','send_failed');
-- This table's key is organization_id, whereas the shared generic trigger
-- expects id. Keep a correct organization object and an explicit safe projection.
-- +goose StatementBegin
CREATE FUNCTION audit_client_onboarding_change() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE previous JSONB='{}'; following JSONB='{}'; oldrow JSONB='{}'; newrow JSONB=to_jsonb(NEW); field TEXT;
BEGIN
 IF TG_OP='UPDATE' THEN oldrow=to_jsonb(OLD); END IF;
 FOREACH field IN ARRAY ARRAY['manager_id','country','domain','contact_name','email','comment','activated_at'] LOOP
  IF oldrow->field IS DISTINCT FROM newrow->field THEN
   previous=previous || jsonb_build_object(field,oldrow->field);
   following=following || jsonb_build_object(field,newrow->field);
  END IF;
 END LOOP;
 IF previous<>following THEN
  PERFORM audit_record(NEW.organization_id::text,'','client_onboarding','organization',NEW.organization_id::text,'',jsonb_build_object('operation',lower(TG_OP),'before',previous,'after',following));
 END IF;
 RETURN NULL;
END $$;
-- +goose StatementEnd
CREATE TRIGGER audit_client_onboardings AFTER INSERT OR UPDATE ON client_onboardings FOR EACH ROW EXECUTE FUNCTION audit_client_onboarding_change();
CREATE TRIGGER audit_owner_deliveries AFTER INSERT OR UPDATE ON owner_invitation_deliveries FOR EACH ROW EXECUTE FUNCTION audit_management_change('owner_invitation_delivery','invitation','invite_id,status,sent_at','');
-- +goose Down
DROP TABLE owner_invitation_deliveries;
DROP TABLE client_onboarding_operations;
DROP TABLE client_onboardings;
DROP FUNCTION audit_client_onboarding_change();
ALTER TABLE organization_invites DROP COLUMN first_owner;
ALTER TABLE organization_invites DROP COLUMN recipient_email;
ALTER TABLE organization_invites DROP COLUMN previous_token_hash;
ALTER TABLE organization_invites DROP COLUMN previous_token_expires_at;

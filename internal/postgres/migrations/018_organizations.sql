-- +goose Up
CREATE TABLE organizations (
 id UUID PRIMARY KEY, name TEXT NOT NULL, credit_limit_micros BIGINT NOT NULL DEFAULT 0 CHECK(credit_limit_micros>=0),
 default_weekly_limit_micros BIGINT DEFAULT 0 CHECK(default_weekly_limit_micros>=0), created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE organization_members (
 organization_id UUID NOT NULL REFERENCES organizations(id), user_id UUID NOT NULL REFERENCES users(id),
 role TEXT NOT NULL CHECK(role IN ('admin','member')), weekly_limit_micros BIGINT DEFAULT 0 CHECK(weekly_limit_micros>=0),
 removed_at TIMESTAMPTZ, PRIMARY KEY(organization_id,user_id)
);
CREATE TABLE organization_credits (
 id UUID PRIMARY KEY, organization_id UUID NOT NULL REFERENCES organizations(id), actor_id UUID NOT NULL REFERENCES users(id),
 amount_micros BIGINT NOT NULL CHECK(amount_micros<>0), kind TEXT NOT NULL CHECK(kind IN ('payment','promo','adjustment')),
 reason TEXT NOT NULL DEFAULT '', external_id TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), UNIQUE(organization_id,external_id)
);
CREATE TABLE organization_invites (
 id UUID PRIMARY KEY, organization_id UUID NOT NULL REFERENCES organizations(id), token_hash BYTEA NOT NULL UNIQUE,
 created_by UUID NOT NULL REFERENCES users(id), expires_at TIMESTAMPTZ NOT NULL, accepted_by UUID REFERENCES users(id), revoked_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE organization_events (
 id BIGSERIAL PRIMARY KEY, organization_id UUID NOT NULL REFERENCES organizations(id), actor_id UUID NOT NULL,
 action TEXT NOT NULL, details JSONB NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE user_api_keys ADD COLUMN organization_id UUID REFERENCES organizations(id);
ALTER TABLE tasks ADD COLUMN actor_id UUID;
ALTER TABLE reference_images ADD COLUMN actor_id UUID;
-- Preserve each existing account's isolation and object paths. No paid credits are inferred.
INSERT INTO organizations(id,name) SELECT id,COALESCE(NULLIF(display_name,''),email)||' workspace' FROM users;
INSERT INTO organization_members(organization_id,user_id,role) SELECT id,id,'admin' FROM users;
UPDATE user_api_keys SET organization_id=user_id;
UPDATE tasks SET actor_id=owner_id;
UPDATE reference_images SET actor_id=owner_id;
CREATE INDEX tasks_org_actor ON tasks(owner_id,actor_id,created_at);
CREATE INDEX keys_org ON user_api_keys(organization_id,user_id);
-- +goose Down
-- Organization ownership cannot be safely mapped back after new memberships or credits.
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Organization migration requires restoring a pre-migration database backup';
END $$;
-- +goose StatementEnd

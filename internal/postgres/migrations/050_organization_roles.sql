-- +goose Up
ALTER TABLE organization_members DROP CONSTRAINT organization_members_role_check;
-- Preserve established administrative access and existing consumer key access.
UPDATE organization_members SET role=CASE role WHEN 'admin' THEN 'supermanager' WHEN 'member' THEN 'developer' ELSE role END;
ALTER TABLE organization_members ADD CONSTRAINT organization_members_role_check CHECK(role IN ('supermanager','admin','content_officer','developer','member'));
ALTER TABLE user_api_keys ADD COLUMN created_by UUID REFERENCES users(id);
UPDATE user_api_keys SET created_by=user_id;
ALTER TABLE user_api_keys ALTER COLUMN created_by SET NOT NULL;
ALTER TABLE reference_uploads ADD COLUMN actor_id UUID;
-- Existing unfinished uploads are visible only to full-content roles.
CREATE INDEX reference_images_org_actor ON reference_images(owner_id,actor_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'Organization roles migration requires restoring a pre-migration database backup';
END $$;
-- +goose StatementEnd

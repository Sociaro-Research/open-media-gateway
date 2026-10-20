-- +goose Up
ALTER TABLE users ADD COLUMN is_system_admin BOOLEAN NOT NULL DEFAULT FALSE;
CREATE TABLE system_settings (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK(id),
 generation_ui BOOLEAN NOT NULL DEFAULT TRUE,
 personal_providers BOOLEAN NOT NULL DEFAULT TRUE,
 allowed_providers JSONB NOT NULL DEFAULT '["mock","bytedance","alibaba"]'::jsonb,
 version BIGINT NOT NULL DEFAULT 1
);
INSERT INTO system_settings(id) VALUES(TRUE);
CREATE TABLE system_audit_log (
 id BIGSERIAL PRIMARY KEY,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 actor TEXT NOT NULL,
 action TEXT NOT NULL,
 target TEXT NOT NULL,
 details JSONB NOT NULL
);
CREATE INDEX system_audit_time ON system_audit_log(created_at DESC,id DESC);

-- +goose Down
DROP TABLE system_audit_log;
DROP TABLE system_settings;
ALTER TABLE users DROP COLUMN is_system_admin;

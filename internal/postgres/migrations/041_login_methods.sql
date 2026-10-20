-- +goose Up
-- Keep existing configured authentication methods available after migration.
CREATE TABLE login_method_settings (
 id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
 google_enabled BOOLEAN NOT NULL DEFAULT TRUE,
 email_enabled BOOLEAN NOT NULL DEFAULT TRUE,
 version BIGINT NOT NULL DEFAULT 1,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO login_method_settings(id) VALUES(TRUE);

-- +goose Down
DROP TABLE login_method_settings;

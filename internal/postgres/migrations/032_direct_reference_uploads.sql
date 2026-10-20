-- +goose Up
CREATE TABLE reference_uploads (
 owner_id UUID NOT NULL,
 id UUID NOT NULL,
 state JSONB NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY (owner_id,id)
);
CREATE INDEX reference_uploads_expiry ON reference_uploads(expires_at);
-- +goose Down
DROP TABLE reference_uploads;

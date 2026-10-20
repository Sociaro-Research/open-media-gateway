-- +goose Up
CREATE TABLE site_branding (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    configuration JSONB NOT NULL DEFAULT '{}',
    version BIGINT NOT NULL DEFAULT 1,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO site_branding(id) VALUES(TRUE);

-- +goose Down
DROP TABLE site_branding;

-- +goose Up
CREATE TABLE installation_setup (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    code_hash BYTEA CHECK (code_hash IS NULL OR octet_length(code_hash)=32),
    code_ciphertext BYTEA,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Upgrades must never expose an existing installation to an ownership claim.
INSERT INTO installation_setup(id,completed_at)
VALUES(TRUE, CASE WHEN EXISTS(SELECT 1 FROM users) THEN NOW() END);

-- +goose Down
DROP TABLE installation_setup;

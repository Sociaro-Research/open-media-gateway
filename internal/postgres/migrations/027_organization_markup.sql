-- +goose Up
ALTER TABLE system_settings ADD COLUMN markup_basis_points BIGINT NOT NULL DEFAULT 0 CHECK (markup_basis_points BETWEEN 0 AND 100000);
ALTER TABLE organizations ADD COLUMN markup_basis_points BIGINT CHECK (markup_basis_points BETWEEN 0 AND 100000);

-- +goose Down
ALTER TABLE organizations DROP COLUMN markup_basis_points;
ALTER TABLE system_settings DROP COLUMN markup_basis_points;

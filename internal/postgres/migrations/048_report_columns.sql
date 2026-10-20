-- +goose Up
ALTER TABLE users ADD COLUMN report_columns JSONB CHECK (report_columns IS NULL OR jsonb_typeof(report_columns) = 'array');

-- +goose Down
ALTER TABLE users DROP COLUMN report_columns;

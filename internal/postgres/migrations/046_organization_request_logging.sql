-- +goose Up
ALTER TABLE organizations ADD COLUMN minimize_request_logging BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE tasks ADD COLUMN minimize_request_logging BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE tasks ADD COLUMN had_image_input BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE tasks DROP COLUMN had_image_input;
ALTER TABLE tasks DROP COLUMN minimize_request_logging;
ALTER TABLE organizations DROP COLUMN minimize_request_logging;

-- +goose Up
ALTER TABLE users ADD COLUMN contacts JSONB NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(contacts) = 'array' AND jsonb_array_length(contacts) <= 20);

-- +goose Down
ALTER TABLE users DROP COLUMN contacts;

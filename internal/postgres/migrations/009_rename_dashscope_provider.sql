-- +goose Up
UPDATE provider_settings SET provider_id = 'alibaba' WHERE provider_id = 'dashscope';
UPDATE tasks SET provider = 'alibaba' WHERE provider = 'dashscope';

-- +goose Down
UPDATE tasks SET provider = 'dashscope' WHERE provider = 'alibaba';
UPDATE provider_settings SET provider_id = 'dashscope' WHERE provider_id = 'alibaba';

-- +goose Up
UPDATE tasks SET cost = jsonb_set(cost, '{actual_source}', '"free_provider"'::jsonb) WHERE cost->>'actual_source'='free_mock';
UPDATE tasks SET cost = jsonb_build_object('expected_usd',0,'actual_usd',0,'actual_source','free_provider','status','confirmed','quoted_at',created_at,'pricing',jsonb_build_object('currency','USD','unit','request','per',1,'source','Gateway free provider','version','1','checked_at',created_at)) WHERE provider='mock' AND cost IS NULL;
-- +goose Down
UPDATE tasks SET cost = jsonb_set(cost, '{actual_source}', '"free_mock"'::jsonb) WHERE provider='mock' AND cost->>'actual_source'='free_provider';

-- +goose Up
ALTER TABLE user_api_keys DROP COLUMN monthly_limit_micros;
ALTER TABLE users DROP COLUMN shared_monthly_limit_micros;

-- +goose Down
ALTER TABLE user_api_keys ADD COLUMN monthly_limit_micros BIGINT CHECK(monthly_limit_micros>=0);
ALTER TABLE users ADD COLUMN shared_monthly_limit_micros BIGINT NOT NULL DEFAULT 0 CHECK(shared_monthly_limit_micros>=0);

-- +goose Up
ALTER TABLE storage_settings DROP CONSTRAINT storage_settings_driver_check;
ALTER TABLE storage_settings ADD CONSTRAINT storage_settings_driver_check CHECK (driver IN ('local', 's3', 'gateway'));

-- +goose Down
UPDATE storage_settings SET driver='local' WHERE driver='gateway';
ALTER TABLE storage_settings DROP CONSTRAINT storage_settings_driver_check;
ALTER TABLE storage_settings ADD CONSTRAINT storage_settings_driver_check CHECK (driver IN ('local', 's3'));

-- +goose Up
ALTER TABLE migration_retry_probe ADD COLUMN id BIGINT;

-- +goose Down
SELECT 1;

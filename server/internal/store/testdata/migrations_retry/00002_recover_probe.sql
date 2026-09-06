-- +goose Up
ALTER TABLE migration_retry_probe ADD COLUMN recovered BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE migration_retry_probe DROP COLUMN recovered;

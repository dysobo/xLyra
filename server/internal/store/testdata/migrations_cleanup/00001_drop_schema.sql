-- +goose Up
DROP SCHEMA IF EXISTS xlyra_migration_test CASCADE;

-- +goose Down
CREATE SCHEMA xlyra_migration_test;

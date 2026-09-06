-- +goose Up
DROP SCHEMA IF EXISTS xlyra_migration_test CASCADE;
CREATE SCHEMA xlyra_migration_test;

-- +goose Down
DROP SCHEMA IF EXISTS xlyra_migration_test CASCADE;

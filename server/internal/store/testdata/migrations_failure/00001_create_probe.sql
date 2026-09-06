-- +goose Up
CREATE TABLE migration_retry_probe (
  id BIGINT PRIMARY KEY
);

-- +goose Down
DROP TABLE migration_retry_probe;

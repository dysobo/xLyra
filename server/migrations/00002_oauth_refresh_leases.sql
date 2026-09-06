-- +goose Up
ALTER TABLE oauth_connections
  ADD COLUMN refresh_lease_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN refresh_lease_until TIMESTAMPTZ;

-- +goose Down
ALTER TABLE oauth_connections
  DROP COLUMN refresh_lease_until,
  DROP COLUMN refresh_lease_id;

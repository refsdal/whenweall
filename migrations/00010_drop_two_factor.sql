-- +goose Up

-- Limen's two-factor plugin was unmounted in 72a8306 (never wired to any UI). Its schema — the
-- users.two_factor_enabled column and the two_factors table, both declared by that plugin, not by
-- Limen core — was left behind because migrations/00002_auth.sql had been generated with the
-- plugin mounted and was never regenerated. Nothing reads either object: Limen inserts users with
-- explicit columns, and the only references were sqlc's generated User model (regenerated after
-- this migration) and admin.DeleteUser's now-removed `DELETE FROM two_factors`.
ALTER TABLE users DROP COLUMN two_factor_enabled;
DROP TABLE two_factors;

-- +goose Down
CREATE TABLE two_factors (
  id BIGSERIAL,
  user_id BIGINT NOT NULL,
  secret VARCHAR(255),
  backup_codes TEXT,
  PRIMARY KEY (id),
  CONSTRAINT fk_two_factors_users_user_id FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE RESTRICT ON UPDATE CASCADE
);
CREATE UNIQUE INDEX idx_two_factors_user_id ON two_factors (user_id);
ALTER TABLE users ADD COLUMN two_factor_enabled BOOLEAN NOT NULL DEFAULT false;

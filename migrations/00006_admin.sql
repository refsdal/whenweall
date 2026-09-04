-- +goose Up

-- admin_audit_log, transcribed from drizzle/0000_orange_zzzax.sql with the spec §6 re-cut
-- applied: created_at becomes timestamptz, metadata (JSON-encoded text in the drizzle/SQLite
-- source) becomes jsonb. id stays a text nanoid, matching every other domain table.
--
-- Ruling (same as migrations/00003_polls.sql and 00004_bookings.sql): actor_user_id is bigint,
-- referencing Limen's users table (migrations/00002_auth.sql) rather than staying text — its FK
-- ON DELETE SET NULL is carried over unchanged from drizzle's own ALTER TABLE ... ADD CONSTRAINT
-- (a deleted user's past actions must stay in the trail; only the link to their row goes away).
--
-- This table has no down-facing mutator by design — see internal/admin/audit.go's doc comment.
-- The three indexes below match drizzle's admin_audit_log_created_idx/_actor_idx/_target_idx
-- one-for-one: List (audit.go) filters on actor_email/target_type/target_id and always orders by
-- created_at, so all three earn their keep.
CREATE TABLE admin_audit_log (
  id text PRIMARY KEY NOT NULL,
  actor_user_id bigint,
  actor_email text NOT NULL,
  action text NOT NULL,
  target_type text NOT NULL,
  target_id text,
  reason text,
  metadata jsonb,
  created_at timestamptz NOT NULL,
  CONSTRAINT fk_admin_audit_log_actor_user_id FOREIGN KEY (actor_user_id) REFERENCES users (id) ON DELETE SET NULL ON UPDATE NO ACTION
);
CREATE INDEX admin_audit_log_created_idx ON admin_audit_log (created_at);
CREATE INDEX admin_audit_log_actor_idx ON admin_audit_log (actor_user_id);
CREATE INDEX admin_audit_log_target_idx ON admin_audit_log (target_type, target_id);

-- +goose Down
DROP TABLE admin_audit_log;

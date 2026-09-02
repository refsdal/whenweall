-- +goose Up

-- List/CountAuditLog (internal/admin/audit.go) filter on actor_email — never actor_user_id — but
-- migrations/00006_admin.sql only ever indexed the latter (admin_audit_log_actor_idx). Every
-- ActorEmail-filtered call (the support console's own "actor" query param, handlers.go) has been
-- a sequential scan since that table shipped; this adds the index its actual access pattern
-- needs.
CREATE INDEX admin_audit_log_actor_email_idx ON admin_audit_log (actor_email);

-- admin_audit_log_actor_idx (on actor_user_id, from migrations/00006_admin.sql) is deliberately
-- kept, not dropped, even though no application query filters on that column directly — grep
-- confirms it's written (INSERT) and selected, but nothing anywhere issues a WHERE
-- actor_user_id = $1. actor_user_id is a FK to users(id) ON DELETE SET NULL, though, and Postgres
-- needs an index on the REFERENCING column to efficiently find the rows it must null out when a
-- parent users row is deleted (internal/admin's DeleteUser) — without it, every single user
-- deletion would force a sequential scan of the whole audit log table looking for rows to update.
-- That's a real use of the index even though no SELECT names it; it stays.

-- +goose Down
DROP INDEX admin_audit_log_actor_email_idx;

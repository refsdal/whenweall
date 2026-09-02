-- +goose Up

-- Ours, not Limen's: migrations/00002_auth.sql's `users` table is Limen-generated and carries no
-- ban/lock concept at all (unlike Better-Auth's `banned`/`ban_reason` columns the TS source read
-- from — see src/server/admin/users.ts's SUMMARY_COLUMNS). Same shape as `staff_users` just above
-- it in 00002: a flag table keyed on Limen's own bigint user id, rather than an extension of
-- Limen's schema.
--
-- Enforcement lives at the auth seam, not here: internal/auth's resolveSession (session.go)
-- checks this table on every request (same per-request-EXISTS-check pattern as its staff_users
-- lookup) and treats a locked user's otherwise-valid session as anonymous. internal/admin's
-- LockUser also revokes the user's existing Limen sessions outright (see auth.Service's
-- RevokeUserSessions) — belt and suspenders: the resolveSession check is what stops a *new*
-- sign-in from working, and the revoke is what stops an *already-issued* session immediately
-- rather than waiting for its own next validation to hit the check above (both, in practice, land
-- on the same request).
CREATE TABLE locked_users (
  user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  reason text,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS locked_users;

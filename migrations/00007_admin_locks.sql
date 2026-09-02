-- +goose Up

-- Ours, not Limen's: migrations/00002_auth.sql's `users` table is Limen-generated and carries no
-- ban/lock concept at all (unlike Better-Auth's `banned`/`ban_reason` columns the TS source read
-- from — see src/server/admin/users.ts's SUMMARY_COLUMNS). Same shape as `staff_users` just above
-- it in 00002: a flag table keyed on Limen's own bigint user id, rather than an extension of
-- Limen's schema.
--
-- Enforcement lives at the auth seam, not here, in two layers:
--
--   1. internal/auth's resolveSession (session.go) checks this table on every request (same
--      per-request-EXISTS-check pattern as its staff_users lookup) and treats a locked user's
--      otherwise-valid session as anonymous. This is what every OTHER package in the application
--      (internal/polls, internal/bookings, internal/admin, ...) sees, via auth.FromContext — but
--      it does nothing at all for Limen's own mounted routes (organization's invitations, Limen's
--      own /me, ...), which authenticate against Limen's own session validation and never call
--      FromContext.
--   2. internal/auth's LockedSessionMiddleware (session.go), wrapped by internal/httpserver
--      around the whole /api/v1/auth/ mount, closes that gap: it blocks a locked user's session
--      from reaching any Limen route except signout. A locked user can still complete a fresh
--      credential sign-in or an OAuth callback — none of Limen's plugins know this table exists —
--      which mints a brand new, perfectly valid Limen session; layer 2 is what makes that session
--      useless anyway.
--
-- internal/admin's LockUser also revokes the user's existing Limen sessions outright (see
-- auth.Service's RevokeUserSessions) — belt and suspenders on top of both layers above: revoking
-- stops an *already-issued* session immediately rather than waiting for its own next validation to
-- hit either check, and layers 1+2 are what stop a *new* sign-in (which a revoke can't touch, since
-- it doesn't exist yet) from working for as long as the lock stands.
CREATE TABLE locked_users (
  user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  reason text,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS locked_users;

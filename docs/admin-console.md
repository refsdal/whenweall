# Admin console runbook

A staff-only support console at `/admin`. This is how it is granted, used and revoked.

## Granting a staff account

There is deliberately no self-serve path, and no in-console way to promote someone to
staff either — every grant is the CLI subcommand, run by whoever operates the instance:

```bash
docker compose exec app /whenweall create-staff-user --email you@example.com
```

The e-mail must already belong to a real, signed-up user — the command errors with "no
user with email ..." otherwise. It's idempotent: re-running it against an already-staffed
address succeeds silently, so it's also the right command to re-run if you're not sure
whether an account already has it.

**This is unaudited by design.** Unlike everything staff do from the console itself,
granting staff never writes an `admin_audit_log` row — it's a direct database write by
whoever already has shell access to the container, before any staff session exists to
attribute the action to. Treat access to run this command as equivalent to staff access
itself, and keep a separate record (outside the app) of who has it and when it was used.

## Revoking

There is no CLI subcommand or console action for this — remove the row directly:

```bash
docker compose exec db psql -U whenweall -d whenweall -c \
  "delete from staff_users where user_id = (select id from users where email = 'them@example.com')"
```

Staff status is re-checked on every request (never cached in the session itself), so this
takes effect on their very next request — no session revocation needed for the role change
alone. If the revocation is urgent — a compromised or departing admin — also **lock** their
account from the console (or the API directly), which additionally revokes every one of
their existing sessions immediately:

```bash
curl -X POST http://localhost:3000/api/v1/admin/users/<id>/lock \
  -H 'content-type: application/json' --cookie <your-own-staff-session-cookie> \
  -d '{"reason":"departing admin"}'
```

## What staff can do

List and search users; view an account with its organisations and content; **lock**
(revokes every session, blocks sign-in) and **unlock**; **delete**; view platform stats;
and view/retry failed background jobs (digests, reminders, Google Calendar sync).

**There is no impersonation and no direct password reset from the console.** Both were
deliberate cuts versus the pre-rewrite TypeScript admin console — see
[`docs/superpowers/specs/2026-08-28-admin-console-design.md`](./superpowers/specs/2026-08-28-admin-console-design.md)
if you need the history.

**Staff cannot lock or delete their own account.** Every self-target request is rejected
with 400 before anything else runs — a staff member acting on themselves would revoke
their own session mid-request with no recovery path short of another staff member's help.

## Reading the audit log

Every mutating action (lock, unlock, delete, job retry) is recorded, whether it came from
the console or a direct API call. Read it in the console at `/admin/audit`, or directly:

```bash
docker compose exec db psql -U whenweall -d whenweall -c \
  "select created_at, actor_email, action, target_id, reason
     from admin_audit_log order by created_at desc limit 50"
```

Notes on reading it:

- **`reason` is required for lock/unlock/delete** — there is no reasonless path for those
  three, unlike the old TS console. A blank `reason` is rejected as a validation error
  before the action runs at all. A job retry carries no reason (there's no one's rights
  to weigh against resuming work the system itself already scheduled).
- **`actor_user_id` is null for a deleted admin, but `actor_email` survives.** The trail is
  designed to outlive the person it describes.
- **`metadata` lists which fields an action touched, never their values.** An audit row
  must not become a second copy of the data it describes.
- Read-only endpoints (search, detail, stats) are not audited, on purpose — auditing list
  views buries the entries that matter.
- **Granting staff (`create-staff-user`) is the one exception — it is never in this log.**
  See [Granting a staff account](#granting-a-staff-account) above.

The log has no mutator by design — nothing in the application can edit or delete a row.

## If an admin account is compromised

1. Lock their account (above) — this both blocks sign-in and revokes every existing
   session in one call.
2. Revoke staff separately if you also want the account fully demoted (above).
3. Read the audit log for everything they did:
   `select * from admin_audit_log where actor_email = '...'`.
4. Every listed action there is something they actually did — there's no impersonation
   feature to additionally account for in this backend, unlike the pre-rewrite console.

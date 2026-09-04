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
account — open them at `/admin/users`, press **Lock account**, type a
reason — which additionally revokes every one of their existing sessions immediately. The
same thing from the API directly:

```bash
curl -X POST http://localhost:3000/api/v1/admin/users/<id>/lock \
  -H 'content-type: application/json' --cookie <your-own-staff-session-cookie> \
  -d '{"reason":"departing admin"}'
```

## What staff can do

List and search users; view an account with its organisations and content; **lock**
(revokes every session, blocks sign-in) and **unlock**; **delete**; view platform stats;
and view/retry failed background jobs. Lock, unlock and delete are buttons on the user's
page (`/admin/users/<id>`) — each opens a dialog that requires a reason, which is what ends
up in the audit log. Failed jobs have their own tab, `/admin/jobs` — see
[Failed jobs](#failed-jobs) below.

**There is no impersonation and no direct password reset from the console.** Both were
deliberate cuts versus the pre-rewrite TypeScript admin console — see
[`docs/superpowers/specs/2026-08-28-admin-console-design.md`](./superpowers/specs/2026-08-28-admin-console-design.md)
if you need the history.

**Staff cannot lock or delete their own account.** Every self-target request is rejected
with 400 before anything else runs — a staff member acting on themselves would revoke
their own session mid-request with no recovery path short of another staff member's help.

### A destructive route this console does not offer

`DELETE /api/v1/auth/organizations/<id>` (from the underlying auth library, not this
console) has no last-owner guard — unlike leaving an organization or removing a member,
deleting one is not blocked just because the caller is its only owner. Any owner of an
organization can call it directly, as an ordinary signed-in user, no staff session
required. For an organization with more than one member, that one request deletes every
poll and every booking page the organization owns (both cascade from `organizations` in
the schema), with no confirmation prompt and no audit row anywhere — this console's own
audit log only covers actions taken *through the console*.

This is a known gap, inherited from the auth library, not something this admin console
adds a guard for — whether to add one is a product decision, not an operational one, and
is out of scope for this document. If you are investigating unexplained data loss for an
organization, this route is one thing to rule out; there is nothing in the audit log that
will show it happened.

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
  before the action runs at all. A job retry (`job.retry`) carries no reason (there's no one's rights to weigh against
  resuming work the system itself already scheduled). A *refused* retry — the job isn't
  actually dead-lettered, or its payload has expired (below) — leaves no row at all.
- **`actor_user_id` is null for a deleted admin, but `actor_email` survives.** The trail is
  designed to outlive the person it describes.
- **`metadata` lists which fields an action touched, never their values.** An audit row
  must not become a second copy of the data it describes.
- Read-only endpoints (search, detail, stats) are not audited, on purpose — auditing list
  views buries the entries that matter.
- **Granting staff (`create-staff-user`) is the one exception — it is never in this log.**
  See [Granting a staff account](#granting-a-staff-account) above.

The log has no mutator by design — nothing in the application can edit or delete a row.

## Failed jobs

Every background job — mail delivery (`mail:send` for sign-in/verification/reset/invite
mail, `mail:poll` and `mail:booking` for notifications), poll deadlines and digests,
booking reminders — is a row in `scheduled_jobs`, retried with
backoff until its attempt budget is spent (10 for mail, 5 otherwise; the three
`deadletter:sweep`/`rooms:prune`/`presence:sweep`/`ratelimit:sweep` housekeeping chains
carry a far larger budget by design — see [Checking housekeeping is alive](#checking-housekeeping-is-alive)
below — so they never dead-letter and never appear on this screen). A job that still
fails then **parks as dead-lettered**: it stops retrying and appears at `/admin/jobs` with
its kind, attempt count, last error and last run time. The dashboard's *Failed jobs* count
is the size of that list; *Mail queue depth* is every `mail:*` job still waiting or
retrying. (Google Calendar sync is disabled product-wide — `googleSyncActive` in
`internal/bookings/google.go` is hard-coded `false` — so no `google:sync` job is ever
enqueued, and none can appear here.)

**Retry** re-queues the job to run immediately (the worker picks it up within its poll
interval) and writes a `job.retry` audit row. A retry is refused with `409 conflict` for a
job that isn't dead-lettered — a stale tab, or the worker already has it — and with
`409 payload_expired` in the case below.

### Dead-letter retention

`mail:send`'s payload is the rendered message: the recipient address and, for
verification and password-reset mail, the raw token in the link. A dead-lettered row would
otherwise keep that readable to anyone with database access for as long as it sat there.
`mail:poll` and `mail:booking` (the other two mail kinds, used for poll and booking
notifications) carry no such thing — ids only. `mail:poll`'s payload is a poll id plus a
participant or user id (internal/polls' `mailPollPayload`); `mail:booking`'s is a booking
id plus which side to notify, `visitor` or `organiser` (internal/bookings'
`mailBookingPayload`). Neither ever carries an address or a token, so there is nothing in
them this retention exists to protect. So a housekeeping job (`deadletter:sweep`, hourly)
does two things:

- **24 hours** after a `mail:send` job dead-letters, its **payload is nulled**. Kind,
  attempt count and last error are kept, so the row still tells you what failed and why —
  but it can no longer be retried (there is nothing left to send), and the console shows
  *Payload purged — can't be retried* in place of the button; the API answers
  `409 payload_expired`. If a batch of mail bounced and you want it resent, the console
  gives you a day. After that, the user re-requests (a new verification or reset mail) —
  which is also the only safe answer, since the original token has expired anyway.
  Every other kind — `mail:poll`, `mail:booking`, and every non-mail kind — keeps its
  payload indefinitely (until the 30-day row delete below) and stays retryable, precisely
  because an SMTP outage dead-lettering a batch of booking confirmations or poll digests
  must not make them permanently unresendable: unlike a verification or reset mail, a
  visitor cannot simply re-request a booking confirmation.
- **30 days** after any job dead-letters, the **row is deleted**. The failed-jobs screen
  is a to-do list, not an archive; the audit log keeps the `job.retry` entries.

Age is measured on the job's last scheduled run, so a job you retried that dies again gets
a fresh 24 hours.

### Checking housekeeping is alive

`deadletter:sweep` is the only thing enforcing the retention above, and because it shares
the housekeeping chains' deliberately huge attempt budget (so a transient blip can never
permanently kill a self-perpetuating chain — see `housekeepingMaxAttempts` in
`internal/jobs/housekeeping.go`), it can never dead-letter and so never shows up on
`/admin/jobs` even if it has stalled. There is no console signal for this — check it
directly:

```bash
docker compose exec db psql -U whenweall -d whenweall -c \
  "select kind, run_at, attempts from scheduled_jobs where kind = 'deadletter:sweep'"
```

One row, `run_at` no more than an hour or so in the future (it reschedules itself hourly on
every successful run) and `attempts` at 0 — a healthy chain always completes and reschedules
before it would ever need a retry. A `run_at` stuck in the past, or `attempts` climbing,
means the sweep itself is failing and needs investigating like any other stuck job — just
without a console screen to notice it for you. The same query works for
`rooms:prune`/`presence:sweep`/`ratelimit:sweep` by swapping the `kind`.

## If an admin account is compromised

1. Lock their account (above) — this both blocks sign-in and revokes every existing
   session in one call.
2. Revoke staff separately if you also want the account fully demoted (above).
3. Read the audit log for everything they did:
   `select * from admin_audit_log where actor_email = '...'`.
4. Every listed action there is something they actually did — there's no impersonation
   feature to additionally account for in this backend, unlike the pre-rewrite console.

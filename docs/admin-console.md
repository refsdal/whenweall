# Admin console runbook

A staff-only support console at `/admin`. This is how it is granted, used and revoked.

## Granting the first staff account

There is deliberately no self-serve path. The first one is a statement against production:

```bash
bunx wrangler d1 execute whenweall-eu --remote \
  --command "update user set role = 'staff' where email = 'you@example.com'"
```

Verify it took — Better-Auth lower-cases email addresses on write, so a mixed-case address in the
`where` clause silently matches nothing and reports `changes: 0`:

```bash
bunx wrangler d1 execute whenweall-eu --remote \
  --command "select email, role from user where role = 'staff'"
```

After the first, use **set role** in the console itself, which is audited.

## Revoking

```bash
bunx wrangler d1 execute whenweall-eu --remote \
  --command "update user set role = 'user' where email = 'them@example.com'"
```

A role change does not end their existing sessions. If the revocation is urgent — a compromised
or departing admin — also revoke their sessions from the console, or:

```bash
bunx wrangler d1 execute whenweall-eu --remote \
  --command "delete from session where user_id = (select id from user where email = 'them@example.com')"
```

## What staff can do

List and search users; view an account with its organisations and content; edit profile fields;
set a password directly; ban and unban; revoke sessions; delete; and impersonate.

**Staff cannot impersonate other staff.** The role maps to Better-Auth's `adminAc`, which omits
the `impersonate-admins` permission.

**An impersonated session is not a staff session.** `isStaff` returns false while
`session.impersonatedBy` is set, so admin powers do not travel into an impersonation. Ending it
returns the original session.

## Reading the audit log

Every mutating action is recorded, whether it came from the console or a direct call to
`/api/auth/admin/*`. Read it in the console at `/admin/audit`, or directly:

```bash
bunx wrangler d1 execute whenweall-eu --remote --command \
  "select created_at, actor_email, action, target_id, reason
     from admin_audit_log order by created_at desc limit 50"
```

Notes on reading it:

- **`reason` is null for anything not done through the console.** The UI always sends
  `x-admin-reason`; a raw API call need not. A cluster of reasonless entries is worth asking about.
- **`actor_user_id` is null for a deleted admin, but `actor_email` survives.** The trail is
  designed to outlive the person it describes.
- **`metadata` lists which fields an update touched, never their values.** An audit row must not
  become a second copy of the data it describes.
- Read-only endpoints are not audited, on purpose — auditing list views buries the entries that
  matter.

The log is append-only. Nothing in the application can edit or delete a row.

## If an admin account is compromised

1. Revoke the role and delete their sessions (above).
2. Read the audit log for everything they did: `select * from admin_audit_log where actor_email = '...'`.
3. Impersonations show as `action = 'impersonate-user'` — treat every account they impersonated as
   having been read.
4. Password changes show as `set-user-password`; those accounts should be reset again.

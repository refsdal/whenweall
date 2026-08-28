# Admin console — Phase 1 design

**Goal:** A staff-only support console: find a user, see their account, and act on it — reset or
set a password, edit profile fields, ban, revoke sessions, delete, impersonate — with every action
recorded. Plus a dashboard of growth and revenue numbers.

**Status:** Phase 1 of three. Phases 2 and 3 are scoped at the end and are _not_ built here.

---

## Scope

**In:** platform role on `user`; `admin_audit_log`; an `/admin` route area; user list, search and
detail; the support actions above; growth and revenue statistics.

**Out, deliberately:**

- **Content moderation** (`suspendedAt` on polls and booking pages) — Phase 2. It changes the
  public render path for every visitor, which is a much larger test surface than an admin-only
  screen, and it should not hold up the support console.
- **Operational health panel** (mail failures, dead letters) — Phase 2, because it depends on the
  `mail_failures` table and DLQ consumer that are still unbuilt.
- **Org console** (list orgs, members, plan, seats, suspend) — Phase 3, because org suspension has
  an unanswered billing question: an org with an active Premium subscription that gets suspended
  is either still being charged for a service it cannot use, cancelled in Stripe (destructive and
  awkward to reverse), or left billing normally while access is blocked (a support burden the
  moment they notice). That needs a product decision, not a technical one.

---

## Decisions

### Use Better-Auth's `admin` plugin, not a hand-rolled flag

The installed `better-auth` already ships `better-auth/plugins`' `admin`, providing `role`,
`banned`, `banReason`, `banExpires` on `user`, `impersonatedBy` on `session`, and fifteen
endpoints — `set-user-password`, `remove-user`, `update-user`, `list-users`, `get-user`,
`ban-user`, `unban-user`, `impersonate-user`, `stop-impersonating`, `revoke-user-session(s)`,
`set-role`, `create-user`, `list-user-sessions`, `has-permission`.

These are the operations we would otherwise write by hand against the session and account tables,
in the security-sensitive part of the system, without the upstream project's tests. Impersonation
in particular is not a flag — it mints a real session carrying `impersonatedBy`, and getting that
subtly wrong is an authentication bug.

### The platform role is `'user' | 'staff'`, never `'admin'`

This codebase **already** has an `admin`: `OrgRole` is `'owner' | 'admin' | 'member'` on the
`member` table, meaning "can manage this organisation's content". A global `user.role` of
`'admin'` sitting beside it invites exactly the confusion that produces authorization bugs —
someone reads `role === 'admin'` and does not notice which role they are holding.

The plugin's vocabulary is configurable, so:

```ts
admin({ defaultRole: 'user', adminRoles: ['staff'] })
```

The type is named `PlatformRole` to keep the distinction visible at every call site. `OrgRole` is
untouched.

### Powers: the full set, including impersonation

Chosen deliberately. An admin can set a password directly and view the product as any user, which
means they can read participants' names, emails, comments and bookings. Two consequences that are
not optional:

1. **The audit log is load-bearing**, not decoration. It is the only thing separating legitimate
   support from misuse — including by a compromised admin account.
2. **The privacy policy must say so.** `privacy_data_body` / `privacy_purposes_body` currently
   describe no such access. This is a policy change, in both locales, and it is part of the work.

### The audit log is append-only, and outlives its actor

No update or delete path is written for `admin_audit_log`. D1 has no table-level permissions, so
this is a code-level discipline: the guarantee is that no code path exists, and a test asserts the
module exports no mutating function.

`actorUserId` references `user` with `onDelete: 'set null'` rather than `cascade` — deleting an
admin must not erase what they did. Because the id alone becomes useless at that point, the row
also denormalises `actorEmail` at write time, so the trail stays readable afterwards.

### Auditing happens in a Better-Auth `after` hook, not in the UI's server functions

The admin endpoints live under `/api/auth/admin/*` and any staff user can call them directly with
`curl`. If auditing lived only in the server functions our UI calls, the trail would be trivially
bypassable, which defeats the point.

So the choke point is a `hooks.after` middleware matching `ctx.path.startsWith('/admin/')`, in the
same place as the existing `hooks.before` for `invite-member` and `subscription/upgrade`. Anything
that reaches the endpoint is recorded, regardless of caller.

The reason string is supplied by our UI as an `x-admin-reason` request header and recorded as null
when absent; the UI lists reasonless actions distinctly, because in normal operation there should
be none.

**To verify during implementation:** that `hooks.after` can read `ctx.path`, the request body and
the outcome, and that throwing from it does not roll back an action that already happened. If the
`after` hook cannot see enough, the fallback is a `before` hook that records intent — worse, since
it logs attempts rather than results, but still unbypassable.

### The first admin is granted by hand

There is no safe self-serve path to the first staff account. Bootstrap is one statement against
production, documented in the runbook:

```bash
bunx wrangler d1 execute whenweall-eu --remote \
  --command "update user set role = 'staff' where email = '...'"
```

Thereafter `set-role` in the UI, which is itself audited.

---

## Data model

Plugin-owned fields, added to `user` and `session` via `auth:generate` and a Drizzle migration:

| Table     | Field            | Notes                                       |
| --------- | ---------------- | ------------------------------------------- |
| `user`    | `role`           | `'user'` (default) or `'staff'`             |
| `user`    | `banned`         | boolean, default false                      |
| `user`    | `banReason`      | text, nullable                              |
| `user`    | `banExpires`     | date, nullable                              |
| `session` | `impersonatedBy` | user id, nullable — set while impersonating |

New table:

```ts
export const adminAuditLog = sqliteTable(
  'admin_audit_log',
  {
    id: text('id').primaryKey(),
    actorUserId: text('actor_user_id').references(() => user.id, { onDelete: 'set null' }),
    // Denormalised so the trail survives the actor's own deletion.
    actorEmail: text('actor_email').notNull(),
    action: text('action').notNull(), // 'impersonate-user', 'set-user-password', ...
    targetType: text('target_type').notNull(), // 'user' now; 'org' | 'poll' in later phases
    targetId: text('target_id'),
    reason: text('reason'),
    metadata: text('metadata'), // JSON; never credentials or password material
    createdAt: text('created_at').notNull(),
  },
  (t) => [
    index('admin_audit_log_created_idx').on(t.createdAt),
    index('admin_audit_log_actor_idx').on(t.actorUserId),
    index('admin_audit_log_target_idx').on(t.targetType, t.targetId),
  ],
)
```

`metadata` records _which_ fields changed on an update, never their values — an audit row must
never itself become a place personal data or password material accumulates.

---

## Authorization

A new `src/server/auth/platform-roles.ts` holds the pure predicate, and
`src/server/auth/staff.ts` holds the middleware — the same split as `org-roles.ts` / `org.ts`, and
for the same reason documented there: the domain layer and the Durable Objects must be able to
import a predicate without dragging TanStack Start, Better-Auth and the Stripe SDK into every DO
bundle and test isolate.

```ts
export type PlatformRole = 'user' | 'staff'
export function isStaff(role: string | null | undefined): boolean
export function requireStaff(role: string | null | undefined): void // throws AppError('FORBIDDEN')
```

Every admin server function takes `requireStaffMiddleware`. The route-level guard in `/admin`'s
`beforeLoad` is for _navigation only_ — it must never be the sole gate, and every server function
re-checks. This mirrors how `requireOrgMiddleware` is already used.

**Impersonation and staff powers:** while `session.impersonatedBy` is set, the session must not be
treated as staff, or an admin could impersonate a user and retain admin powers as them.
`requireStaff` therefore checks the impersonation marker as well as the role. This is the single
most important test in the phase.

---

## Routes and UI

| Route              | Purpose                                                                     |
| ------------------ | --------------------------------------------------------------------------- |
| `/admin`           | Dashboard: growth and revenue panels, recent admin activity                 |
| `/admin/users`     | List with search by email or name, paginated                                |
| `/admin/users/$id` | Detail: profile, orgs, content counts, actions, audit history for this user |
| `/admin/audit`     | Full audit log, newest first, filterable by actor and action                |

Not linked from the main navigation for non-staff. This is not a security measure — the gates
above are — but there is no reason to advertise it.

Every destructive action is a confirmation dialog that requires the reason string before the
confirm button enables. Following existing conventions: `m.*` messages in both locales, shadcn
dialogs, `sonner` for toasts.

---

## Statistics

Direct `COUNT` queries against D1, in `src/server/admin/stats.ts`. At current scale (roughly one
hundred rows) this is free, and an aggregation layer would be inventing a problem. If the
dashboard ever gets slow the fix is caching that module's output, not restructuring it.

**Growth:** users, orgs, polls (total and finalised), sign-up sheets, booking pages, bookings —
each as a total plus the count created in the last 7 and 30 days.

**Revenue:** free vs Premium orgs, active subscriptions, MRR, and subscriptions cancelling at
period end. Read from the local `subscription` table, never a live Stripe call.

**`isActivePremium` from `entitlements.ts` is reused, not re-derived.** That module is documented
as the one place plan rules live, and a dashboard that disagreed with the gate about who is
Premium would be worse than no dashboard.

---

## Testing

- `platform-roles.ts` predicates, including the impersonation case, as unit tests.
- Workers tests that each admin server function rejects a non-staff session — the gate, not the
  happy path, is what matters.
- **A test that an impersonated session is not treated as staff.**
- A test that the audit helper writes a row for an action invoked through the raw
  `/api/auth/admin/*` endpoint, not only through our server functions — this is what proves the
  choke point is real.
- A test that `admin_audit_log` has no exported mutating path.
- Stats tests against seeded fixtures, asserting the Premium count matches `isActivePremium`.
- e2e: a staff user reaches `/admin`; a non-staff user gets a 404/redirect.

---

## Risks

| Risk                                      | Mitigation                                                                                                     |
| ----------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| Impersonated session retains staff powers | `requireStaff` checks `impersonatedBy`; dedicated test                                                         |
| `user.role` confused with `OrgRole`       | Vocabulary is `'staff'`, never `'admin'`; type named `PlatformRole`                                            |
| Audit bypassed via raw endpoints          | Auditing in the Better-Auth `after` hook, not in server functions                                              |
| Audit trail lost when an admin is deleted | `onDelete: 'set null'` plus denormalised `actorEmail`                                                          |
| Admin account compromise                  | Every action audited; ban and session revocation available; future phase can add re-auth for sensitive actions |
| Privacy policy silent on staff access     | Policy update in both locales is part of this phase, not a follow-up                                           |

---

## Phases 2 and 3

**Phase 2 — Content moderation and operational health.** `suspendedAt` on polls and booking pages
with the public render path handling it, closing #48's takedown gap; plus the `mail_failures`
table and DLQ consumer, surfaced as the dashboard's health panel.

**Phase 3 — Org console.** Org list and detail, members, plan, seats, org-level actions — once the
billing question above is answered.

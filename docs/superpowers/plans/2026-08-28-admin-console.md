# Admin console (phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A staff-only support console at `/admin`: find a user, act on their account (reset/set password, edit, ban, revoke sessions, delete, impersonate), with every action recorded in an append-only audit log — plus growth and revenue statistics.

**Architecture:** Better-Auth's `admin` plugin supplies the `role`/`banned`/`impersonatedBy` fields and the fifteen `/api/auth/admin/*` endpoints. Our code adds three things the plugin does not: a **platform-role gate** that refuses an impersonated session, an **audit choke point** in a Better-Auth `after` hook (so raw `curl` against the endpoints is recorded too), and the `/admin` UI. Statistics are direct `COUNT` queries against D1.

**Tech Stack:** unchanged — TanStack Start, D1 + Drizzle, Better-Auth 1.7 (`admin` plugin), Paraglide, Tailwind/shadcn, Vitest unit + workers, Playwright.

**Spec:** `docs/superpowers/specs/2026-08-28-admin-console-design.md`

## Global Constraints

- bun only. `m.*` messages must exist in **both** `messages/en.json` and `messages/nb.json` — `messages/__tests__/messages.test.ts` asserts identical key sets and matching placeholders.
- The platform role vocabulary is **`'user' | 'staff'`**. Never `'admin'` — that is already an `OrgRole` (`'owner' | 'admin' | 'member'`) on `member`, and conflating them is an authorization bug.
- Pure predicates go in `platform-roles.ts`; middleware goes in `staff.ts`. Same split, and same reason, as `org-roles.ts` / `org.ts`: the domain layer and Durable Objects import predicates, and must not drag TanStack Start / Better-Auth / Stripe into every DO bundle and test isolate.
- Every admin server function gates itself with `requireStaffMiddleware`. A route `beforeLoad` guard is for navigation only and is never the sole gate.
- `import * as z from 'zod'`.
- Audit metadata records **which** fields changed, never their values, and never password material.
- TDD. Conventional Commits. `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` trailer.
- Green before each commit: `bun run typecheck && bun run lint && bun run format:check && bun run test`.

---

### Task 1: Platform role plumbing — plugin, generated schema, audit table, migration

**Files:**

- Modify: `src/server/auth/auth.ts` (add `admin` plugin to `plugins: [...]`)
- Modify: `src/server/auth/auth.cli.ts` (mirror it, for generator parity)
- Regenerate: `src/server/db/auth-schema.ts` (via `bun run auth:generate`)
- Modify: `src/server/db/schema.ts` (add `adminAuditLog`)
- Create: `drizzle/0004_*.sql` (via `bun run db:generate`)
- Test: `src/server/db/__tests__/admin-schema.workers.test.ts`

**Interfaces (Produces):**

```ts
// src/server/db/schema.ts
export const adminAuditLog: SQLiteTable // columns below
export type AdminAuditLogRow = typeof adminAuditLog.$inferSelect
// src/server/db/auth-schema.ts (generated)
//   user.role: text | null            'user' | 'staff'
//   user.banned: integer(boolean) | null
//   user.banReason: text | null
//   user.banExpires: integer(timestamp_ms) | null
//   session.impersonatedBy: text | null
```

- [ ] **Step 1: Add the plugin to both auth configs**

In `src/server/auth/auth.ts`, import `admin` from `better-auth/plugins` (alongside the existing `captcha, organization`) and add to `plugins`:

```ts
// The platform role is deliberately 'staff', not the plugin's default 'admin': `OrgRole` already
// uses 'admin' for a per-org role on `member`, and two different `role` values with the same name
// is how authorization bugs get written.
admin({ defaultRole: 'user', adminRoles: ['staff'] }),
```

Add the identical call to `src/server/auth/auth.cli.ts`'s `plugins` array — that file exists purely to shape the generated schema, and drifting from the runtime config produces a wrong migration.

**Correction found during execution:** the plugin validates `adminRoles` against its `roles` map
and throws `Invalid admin roles: staff` if the role is not defined there. So a custom role name
requires an explicit mapping:

```ts
import { adminAc, userAc } from 'better-auth/plugins/admin/access'

admin({
  defaultRole: 'user',
  adminRoles: ['staff'],
  roles: { user: userAc, staff: adminAc },
}),
```

Do **not** also pass `ac: defaultAc` — its concrete generic type is not assignable to the plugin's
own `AccessControl` interface and `tsc` rejects it. Passing `roles` alone satisfies the validation.
A welcome side effect of reusing `adminAc`: it omits `impersonate-admins`, so one staff user
cannot impersonate another.

- [ ] **Step 2: Regenerate the auth schema**

Run: `bun run auth:generate`
Expected: `src/server/db/auth-schema.ts` gains `role`, `banned`, `banReason`, `banExpires` on `user` and `impersonatedBy` on `session`.

**Then re-add the index the generator drops.** `auth:generate` rewrites the whole file from the
Better-Auth config and silently loses `subscription_referenceId_idx`, which `getEntitlements`
relies on for a lookup performed on every `getSession`. Restore it, with the warning comment,
before generating the migration — otherwise the migration will contain a `DROP INDEX`. Confirm
the diff against the previous version is additive only.

- [ ] **Step 3: Add the audit table**

In `src/server/db/schema.ts`, after the notification tables:

```ts
/**
 * Append-only record of every staff action. There is deliberately no update or delete path — D1
 * has no table-level permissions, so the guarantee is that no such code exists.
 *
 * `actorUserId` is `set null` rather than `cascade`: deleting an admin must not erase what they
 * did. Because the id alone is then useless, `actorEmail` is denormalised at write time so the
 * trail stays readable.
 */
export const adminAuditLog = sqliteTable(
  'admin_audit_log',
  {
    id: text('id').primaryKey(),
    actorUserId: text('actor_user_id').references(() => user.id, { onDelete: 'set null' }),
    actorEmail: text('actor_email').notNull(),
    action: text('action').notNull(),
    targetType: text('target_type').notNull(),
    targetId: text('target_id'),
    reason: text('reason'),
    /** JSON. Which fields changed — never their values, never password material. */
    metadata: text('metadata'),
    createdAt: text('created_at').notNull(),
  },
  (t) => [
    index('admin_audit_log_created_idx').on(t.createdAt),
    index('admin_audit_log_actor_idx').on(t.actorUserId),
    index('admin_audit_log_target_idx').on(t.targetType, t.targetId),
  ],
)

export type AdminAuditLogRow = typeof adminAuditLog.$inferSelect
```

- [ ] **Step 4: Write the failing schema test**

Create `src/server/db/__tests__/admin-schema.workers.test.ts`:

```ts
import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { eq } from 'drizzle-orm'
import { createDb } from '#/server/db/client'
import { adminAuditLog } from '#/server/db/schema'
import { newId } from '#/lib/ids'
import { makeUser } from '../../../../test/helpers'

describe('admin_audit_log', () => {
  it('keeps the row, and the actor email, after the actor is deleted', async () => {
    const db = createDb(env.DB)
    const { id: actorId, email } = await makeUser(db)
    const rowId = newId()

    await db.insert(adminAuditLog).values({
      id: rowId,
      actorUserId: actorId,
      actorEmail: email,
      action: 'impersonate-user',
      targetType: 'user',
      targetId: 'target-1',
      reason: 'support ticket 12',
      metadata: null,
      createdAt: new Date().toISOString(),
    })

    await db.delete(user).where(eq(user.id, actorId))

    const row = await db.query.adminAuditLog.findFirst({ where: eq(adminAuditLog.id, rowId) })
    expect(row).toBeDefined()
    expect(row!.actorUserId).toBeNull()
    expect(row!.actorEmail).toBe(email)
  })
})
```

(Import `user` from `#/server/db/schema` too.)

- [ ] **Step 5: Run it and watch it fail**

Run: `bun run test:workers -- --run src/server/db/__tests__/admin-schema`
Expected: FAIL — `no such table: admin_audit_log`.

- [ ] **Step 6: Generate and apply the migration**

Run: `bun run db:generate` then `bun run db:migrate:local`
Inspect the generated SQL before running it. It must be **additive only** — new nullable columns and one new table. The deploy applies migrations _before_ the new code goes live, so anything destructive would break the running worker.

- [ ] **Step 7: Run the test again**

Run: `bun run test:workers -- --run src/server/db/__tests__/admin-schema`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add src/server/auth/auth.ts src/server/auth/auth.cli.ts src/server/db/auth-schema.ts \
        src/server/db/schema.ts drizzle src/server/db/__tests__/admin-schema.workers.test.ts
git commit -m "feat(admin): platform role fields and an append-only audit table"
```

---

### Task 2: The staff gate, and why an impersonated session is not staff

**Files:**

- Create: `src/server/auth/platform-roles.ts`
- Create: `src/server/auth/staff.ts`
- Test: `src/server/auth/__tests__/platform-roles.test.ts`

**Interfaces (Produces):**

```ts
// platform-roles.ts
export type PlatformRole = 'user' | 'staff'
export function isStaff(input: { role?: string | null; impersonatedBy?: string | null }): boolean
export function requireStaff(input: { role?: string | null; impersonatedBy?: string | null }): void
// staff.ts
export const requireStaffMiddleware // adds context.staff = { userId, email }
```

- [ ] **Step 1: Write the failing tests**

Create `src/server/auth/__tests__/platform-roles.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import { isStaff, requireStaff } from '#/server/auth/platform-roles'
import { errorCode } from '#/lib/errors'

describe('isStaff', () => {
  it('accepts the staff role', () => {
    expect(isStaff({ role: 'staff' })).toBe(true)
  })

  it('rejects an ordinary user, a missing role and a null role', () => {
    expect(isStaff({ role: 'user' })).toBe(false)
    expect(isStaff({})).toBe(false)
    expect(isStaff({ role: null })).toBe(false)
  })

  // `admin` is an OrgRole on `member`, not a platform role. If it ever satisfied this predicate,
  // every org admin would become a system administrator.
  it('rejects the org role "admin"', () => {
    expect(isStaff({ role: 'admin' })).toBe(false)
  })

  // The most important test in this phase. Without it an admin could impersonate a user and keep
  // their admin powers while acting as that user.
  it('rejects a staff session that is currently impersonating someone', () => {
    expect(isStaff({ role: 'staff', impersonatedBy: 'user_123' })).toBe(false)
  })
})

describe('requireStaff', () => {
  it('throws FORBIDDEN for a non-staff session', () => {
    let caught: unknown
    try {
      requireStaff({ role: 'user' })
    } catch (err) {
      caught = err
    }
    expect(errorCode(caught)).toBe('FORBIDDEN')
  })

  it('returns quietly for staff', () => {
    expect(() => requireStaff({ role: 'staff' })).not.toThrow()
  })
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `bun run test:unit -- --run src/server/auth/__tests__/platform-roles`
Expected: FAIL — cannot resolve `#/server/auth/platform-roles`.

- [ ] **Step 3: Implement the predicates**

Create `src/server/auth/platform-roles.ts`:

```ts
import { AppError } from '#/lib/errors'

/**
 * A user's role across the whole platform, as opposed to `OrgRole`, which is their role *within
 * one organisation*. The two are unrelated and live on different tables (`user` vs `member`) —
 * hence 'staff' rather than the Better-Auth default of 'admin', which `OrgRole` already uses.
 */
export type PlatformRole = 'user' | 'staff'

const STAFF: PlatformRole = 'staff'

/**
 * Whether this session may use the admin console.
 *
 * `impersonatedBy` is checked, not just the role: while impersonating, the session belongs to the
 * person being impersonated for every purpose except ending the impersonation. Treating it as
 * staff would let an admin act with admin powers *as another user*, which is both a privilege
 * escalation and an audit-trail hole.
 */
export function isStaff(input: { role?: string | null; impersonatedBy?: string | null }): boolean {
  if (input.impersonatedBy) return false
  return input.role === STAFF
}

export function requireStaff(input: {
  role?: string | null
  impersonatedBy?: string | null
}): void {
  if (!isStaff(input)) throw new AppError('FORBIDDEN')
}
```

- [ ] **Step 4: Run and watch it pass**

Run: `bun run test:unit -- --run src/server/auth/__tests__/platform-roles`
Expected: PASS (6 tests).

- [ ] **Step 5: Add the middleware**

Create `src/server/auth/staff.ts`:

```ts
import { createMiddleware } from '@tanstack/react-start'
import { requireSessionMiddleware } from './middleware'
import { requireStaff } from './platform-roles'

/**
 * Gate for every admin server function. Middleware lives here rather than in
 * `platform-roles.ts` for the reason documented in `org-roles.ts`: this module pulls in TanStack
 * Start and Better-Auth, and the predicates must stay importable without them.
 */
export const requireStaffMiddleware = createMiddleware({ type: 'function' })
  .middleware([requireSessionMiddleware])
  .server(async ({ next, context }) => {
    const user = context.session.user as { id: string; email: string; role?: string | null }
    const session = context.session.session as { impersonatedBy?: string | null }

    requireStaff({ role: user.role, impersonatedBy: session.impersonatedBy })

    return next({ context: { staff: { userId: user.id, email: user.email } } })
  })
```

- [ ] **Step 6: Verify the whole suite is still green, then commit**

Run: `bun run typecheck && bun run lint && bun run test`

```bash
git add src/server/auth/platform-roles.ts src/server/auth/staff.ts \
        src/server/auth/__tests__/platform-roles.test.ts
git commit -m "feat(admin): staff gate that refuses an impersonated session"
```

---

### Task 3: The audit choke point

**Files:**

- Create: `src/server/admin/audit.ts`
- Modify: `src/server/auth/auth.ts` (add `hooks.after`)
- Test: `src/server/admin/__tests__/audit.workers.test.ts`

**Interfaces (Produces):**

```ts
// audit.ts
export const AUDITED_ADMIN_ACTIONS: readonly string[]
export async function recordAdminAction(
  db: Db,
  entry: {
    actorUserId: string
    actorEmail: string
    action: string
    targetType: 'user' | 'org' | 'poll'
    targetId: string | null
    reason: string | null
    metadata?: Record<string, unknown> | null
  },
): Promise<void>
```

- [ ] **Step 1: Write the failing tests**

Create `src/server/admin/__tests__/audit.workers.test.ts` with three cases:

```ts
import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { adminAuditLog } from '#/server/db/schema'
import { recordAdminAction } from '#/server/admin/audit'
import * as auditModule from '#/server/admin/audit'
import { makeUser } from '../../../../test/helpers'

describe('recordAdminAction', () => {
  it('writes an auditable row', async () => {
    const db = createDb(env.DB)
    const { id, email } = await makeUser(db)

    await recordAdminAction(db, {
      actorUserId: id,
      actorEmail: email,
      action: 'set-user-password',
      targetType: 'user',
      targetId: 'target-9',
      reason: 'ticket 12',
      metadata: { fields: ['password'] },
    })

    const rows = await db.query.adminAuditLog.findMany()
    const row = rows.find((r) => r.targetId === 'target-9')!
    expect(row.action).toBe('set-user-password')
    expect(row.actorEmail).toBe(email)
    expect(row.reason).toBe('ticket 12')
    expect(JSON.parse(row.metadata!)).toEqual({ fields: ['password'] })
  })

  // The trail is the only thing separating support from misuse, so it must not be erasable from
  // inside the app.
  it('exports no way to change or remove a recorded action', () => {
    const mutators = Object.keys(auditModule).filter((k) => /update|delete|remove|clear/i.test(k))
    expect(mutators).toEqual([])
  })
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `bun run test:workers -- --run src/server/admin/__tests__/audit`
Expected: FAIL — cannot resolve `#/server/admin/audit`.

- [ ] **Step 3: Implement the writer**

Create `src/server/admin/audit.ts`:

```ts
import type { Db } from '#/server/db/client'
import { adminAuditLog } from '#/server/db/schema'
import { newId } from '#/lib/ids'

/** Endpoint suffixes under `/admin/` that change something and must leave a trail. Read-only
 * endpoints (`list-users`, `get-user`, `has-permission`) are deliberately absent — auditing a
 * list view produces noise that buries the actions that matter. */
export const AUDITED_ADMIN_ACTIONS = [
  'set-role',
  'create-user',
  'update-user',
  'set-user-password',
  'remove-user',
  'ban-user',
  'unban-user',
  'impersonate-user',
  'stop-impersonating',
  'revoke-user-session',
  'revoke-user-sessions',
] as const

/**
 * Appends one row. There is intentionally no counterpart that edits or deletes: see the table's
 * own doc comment in `schema.ts`.
 */
export async function recordAdminAction(
  db: Db,
  entry: {
    actorUserId: string
    actorEmail: string
    action: string
    targetType: 'user' | 'org' | 'poll'
    targetId: string | null
    reason: string | null
    metadata?: Record<string, unknown> | null
  },
): Promise<void> {
  await db.insert(adminAuditLog).values({
    id: newId(),
    actorUserId: entry.actorUserId,
    actorEmail: entry.actorEmail,
    action: entry.action,
    targetType: entry.targetType,
    targetId: entry.targetId,
    reason: entry.reason,
    metadata: entry.metadata ? JSON.stringify(entry.metadata) : null,
    createdAt: new Date().toISOString(),
  })
}
```

- [ ] **Step 4: Run and watch it pass**

Run: `bun run test:workers -- --run src/server/admin/__tests__/audit`
Expected: PASS.

- [ ] **Step 5: Wire the choke point into Better-Auth**

`/api/auth/admin/*` is reachable by any staff user with `curl`, so auditing inside our own server functions would be bypassable. Add an `after` hook beside the existing `before` hook in `src/server/auth/auth.ts`:

```ts
hooks: {
  before: createAuthMiddleware(async (ctx) => { /* ...existing... */ }),
  // Audits every mutating admin endpoint, wherever the call came from. Deliberately here and not
  // in the admin server functions: the raw endpoints are reachable directly, and a trail that
  // only records the calls made through our own UI is not a trail.
  after: createAuthMiddleware(async (ctx) => {
    if (!ctx.path.startsWith('/admin/')) return
    const action = ctx.path.slice('/admin/'.length)
    if (!(AUDITED_ADMIN_ACTIONS as readonly string[]).includes(action)) return

    const session = await getSessionFromCtx(ctx)
    if (!session) return

    const body = (ctx.body ?? {}) as { userId?: string; sessionToken?: string; data?: object }
    await recordAdminAction(createDb(d1), {
      actorUserId: session.user.id,
      actorEmail: session.user.email,
      action,
      targetType: 'user',
      targetId: body.userId ?? null,
      reason: ctx.headers?.get('x-admin-reason') ?? null,
      // Field NAMES only. An audit row must never become another copy of the data it describes.
      metadata: body.data ? { fields: Object.keys(body.data) } : null,
    })
  }),
},
```

**Verify while implementing:** that `ctx.path`, `ctx.body` and `ctx.headers` are populated in an `after` hook, and that this runs _after_ the action succeeded. If `after` cannot see enough, fall back to `before` and note in the code comment that it records intent rather than outcome.

- [ ] **Step 6: Prove the choke point with an end-to-end test**

Add to the same test file — this is the test that proves auditing cannot be bypassed:

```ts
it('records an action invoked through the raw endpoint, not just through our UI', async () => {
  const db = createDb(env.DB)
  const { id: staffId } = await makeUser(db, { email: `staff-${newId()}@example.com` })
  await db.update(user).set({ role: 'staff' }).where(eq(user.id, staffId))
  const { id: targetId } = await makeUser(db)

  // Drive the Better-Auth handler directly, as `curl` would.
  await getAuth().api.banUser({
    body: { userId: targetId },
    headers: await signedInHeadersFor(staffId),
  })

  const rows = await db.query.adminAuditLog.findMany()
  expect(rows.some((r) => r.action === 'ban-user' && r.targetId === targetId)).toBe(true)
})
```

Build `signedInHeadersFor` from the pattern already used in `test/server-functions.workers.test.ts`.

- [ ] **Step 7: Green, then commit**

```bash
git add src/server/admin src/server/auth/auth.ts
git commit -m "feat(admin): audit every mutating admin endpoint at the choke point"
```

---

### Task 4: Statistics

**Files:**

- Create: `src/server/admin/stats.ts`
- Create: `src/server/admin/admin.functions.ts`
- Test: `src/server/admin/__tests__/stats.workers.test.ts`

**Interfaces (Produces):**

```ts
// stats.ts
export type AdminStats = {
  growth: {
    users: Count
    orgs: Count
    polls: Count
    pollsFinalized: number
    signupSheets: Count
    bookingPages: Count
    bookings: Count
  }
  revenue: {
    premiumOrgs: number
    totalOrgs: number
    activeSubscriptions: number
    mrrMinor: number
    cancellingAtPeriodEnd: number
  }
}
type Count = { total: number; last7: number; last30: number }
export async function getAdminStats(db: Db, now?: Date): Promise<AdminStats>
// admin.functions.ts
export const fetchAdminStats // createServerFn, requireStaffMiddleware
```

- [ ] **Step 1: Write the failing test**

Assert three things against seeded fixtures: totals are right; `last7` excludes a row created 10 days ago; and the Premium count agrees with `isActivePremium`.

```ts
it('counts premium orgs using the same rule as the entitlement gate', async () => {
  const db = createDb(env.DB)
  const { orgId } = await makeUserWithOrg(db)
  await makeSubscription(db, { referenceId: orgId, plan: PREMIUM_PLAN_NAME, status: 'trialing' })

  const stats = await getAdminStats(db)

  // 'trialing' counts as active in entitlements.ts. A dashboard that disagreed with the gate
  // about who is Premium would be worse than no dashboard.
  expect(stats.revenue.premiumOrgs).toBe(1)
})
```

- [ ] **Step 2: Run and watch it fail**

Run: `bun run test:workers -- --run src/server/admin/__tests__/stats`

- [ ] **Step 3: Implement**

`getAdminStats` runs `count()` queries via `drizzle-orm`'s `count`, with `gte(table.createdAt, isoDaysAgo(7))` for the windowed figures. **Import `isActivePremium` from `#/server/billing/entitlements`** and filter subscription rows through it rather than re-deriving the status set. Polls, sign-up sheets and booking pages all filter `isNull(deletedAt)`.

- [ ] **Step 4: Run and watch it pass**

- [ ] **Step 5: Expose it, gated**

```ts
export const fetchAdminStats = createServerFn({ method: 'GET' })
  .middleware([requireStaffMiddleware])
  .handler(async (): Promise<AdminStats> => getAdminStats(getDb()))
```

- [ ] **Step 6: Green, then commit**

```bash
git commit -m "feat(admin): growth and revenue statistics"
```

---

### Task 5: User list, search and detail

**Files:**

- Create: `src/server/admin/users.ts`
- Modify: `src/server/admin/admin.functions.ts`
- Test: `src/server/admin/__tests__/users.workers.test.ts`

**Interfaces (Produces):**

```ts
// users.ts
export type AdminUserSummary = {
  id: string
  name: string
  email: string
  role: string | null
  banned: boolean
  emailVerified: boolean
  createdAt: string
}
export type AdminUserDetail = AdminUserSummary & {
  orgs: { id: string; name: string; role: string }[]
  counts: { polls: number; bookingPages: number; bookings: number }
  recentActions: AdminAuditLogRow[]
}
export async function listAdminUsers(
  db: Db,
  q: { search?: string; limit: number; offset: number },
): Promise<{ users: AdminUserSummary[]; total: number }>
export async function getAdminUserDetail(db: Db, userId: string): Promise<AdminUserDetail | null>
// admin.functions.ts
export const fetchAdminUsers, fetchAdminUserDetail // both requireStaffMiddleware
```

- [ ] **Step 1: Write the failing tests**

Cover: search matches on email and on name, case-insensitively; pagination; detail returns the user's orgs and content counts; **detail returns `null` for an unknown id rather than throwing**; and — the one that matters — `fetchAdminUsers` rejects a non-staff session:

```ts
it('refuses a signed-in non-staff user', async () => {
  await expect(
    fetchAdminUsers({
      data: { limit: 20, offset: 0 },
      headers: await signedInHeadersFor(plainUserId),
    }),
  ).rejects.toMatchObject({ code: 'FORBIDDEN' })
})
```

- [ ] **Step 2: Run and watch them fail**

- [ ] **Step 3: Implement `users.ts`, then the two server functions**

Search uses `like(lower(user.email), '%' + q + '%')` OR the same on `name`. Never return password hashes, session tokens or `manageTokenHash` from any of these.

- [ ] **Step 4: Run and watch them pass**

- [ ] **Step 5: Green, then commit**

```bash
git commit -m "feat(admin): user list, search and detail queries"
```

---

### Task 6: The `/admin` UI

**Files:**

- Create: `src/routes/admin/index.tsx`, `src/routes/admin/users.tsx`, `src/routes/admin/users.$id.tsx`, `src/routes/admin/audit.tsx`
- Create: `src/components/admin/StatCard.tsx`, `UserTable.tsx`, `UserActions.tsx`, `AuditTable.tsx`, `ReasonDialog.tsx`
- Modify: `messages/en.json`, `messages/nb.json`
- Test: component tests for `ReasonDialog` and `UserActions`

- [ ] **Step 1: Route guard**

Every `/admin` route's `beforeLoad` checks the session's role and throws `notFound()` for non-staff — a 404, not a 403, so the console's existence is not confirmed to a stranger. **This is navigation only; the server functions from Tasks 4 and 5 are the real gate.**

- [ ] **Step 2: `ReasonDialog`, test-first**

The reusable confirmation. Test that the confirm button stays disabled until a non-empty reason is typed, and that the reason reaches `onConfirm`. Every destructive action goes through it, and it is what populates `x-admin-reason`.

- [ ] **Step 3: The four screens**

Dashboard (stat cards + recent activity), user list (search box, table, pagination), user detail (profile, orgs, counts, actions, this user's audit history), audit log (filterable by actor and action). Follow existing conventions: shadcn primitives, `sonner` toasts, `m.*` for every string.

- [ ] **Step 4: Set the reason header on every admin call**

Configure the Better-Auth client so admin calls send `x-admin-reason`. The UI must never call an admin endpoint without one.

- [ ] **Step 5: Add messages to both locales**

Run: `bun run test -- --run messages` — the parity test fails if `nb.json` is missing a key.

- [ ] **Step 6: Green, then commit**

```bash
git commit -m "feat(admin): admin console screens"
```

---

### Task 7: Disclose staff access in the privacy policy

**Files:**

- Modify: `messages/en.json`, `messages/nb.json`

Staff can read any user's data and act as them. The policy currently does not say so, and publishing the capability without the disclosure is the compliance failure — not the capability itself.

- [ ] **Step 1: Extend `privacy_data_body` / `privacy_purposes_body`** in both locales: that a small number of staff can access account and content data to provide support and investigate abuse, that such access is logged, and that the lawful basis is legitimate interest.

- [ ] **Step 2: Bump `legal_updated`** in both locales to today.

- [ ] **Step 3: Run `bun run test -- --run messages`, then commit**

```bash
git commit -m "docs(privacy): disclose staff access to account data"
```

---

### Task 8: Bootstrap runbook and e2e

**Files:**

- Create: `docs/admin-console.md`
- Create: `e2e/admin.spec.ts`
- Modify: `src/routes/api/test/seed.ts` (accept `role: 'staff'`)

- [ ] **Step 1: Runbook** — how the first staff account is granted:

```bash
bunx wrangler d1 execute whenweall-eu --remote \
  --command "update user set role = 'staff' where email = '...'"
```

…plus how to revoke it, how to read the audit log, and a note that impersonation is visible in `session.impersonatedBy`.

- [ ] **Step 2: Extend the seed route** so e2e can create a staff user. It is already double-gated (`APP_ENV !== 'production'` **and** `ENABLE_TEST_ROUTES === 'true'`) — keep both.

- [ ] **Step 3: e2e** — a staff user reaches `/admin` and sees the dashboard; a plain user gets a 404. Follow `e2e/billing.spec.ts`'s seeding pattern.

- [ ] **Step 4: Full green, then commit**

```bash
bun run typecheck && bun run lint && bun run format:check && bun run test && bun run test:e2e
git commit -m "docs(admin): bootstrap runbook and admin e2e coverage"
```

---

## Deployment note

The deploy applies migrations **before** the new code is live, so Task 1's migration must be additive — new nullable columns and one new table, exactly as generated. Nothing here is destructive, so no coordination is needed beyond a normal merge.

After deploying, grant the first staff account with the command in Task 8's runbook. Until then the console exists and nobody can reach it, which is the correct resting state.

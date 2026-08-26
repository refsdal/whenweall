# whenweall v4 Phase 1 — Organization Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all content ownership from users to Better-Auth organizations, with silently auto-created personal orgs, so the app behaves exactly as before for solo users.

**Architecture:** Better-Auth `organization` plugin (orgs/members/invitations + `activeOrganizationId` on session); database hooks auto-create a personal org per user; `polls`/`bookingPages` gain `organizationId` + `createdBy` (+ `memberUserId` for booking pages); the user `handle` is replaced by the organization `slug`. One authorization path: a `requireOrgMiddleware` + `canManageContent` role check.

**Tech Stack:** TanStack Start, Cloudflare Workers, D1 + Drizzle, Better-Auth 1.5 (`organization` plugin), vitest (+@cloudflare/vitest-plugin workers project), Playwright.

**Spec:** `docs/superpowers/specs/2026-08-25-whenweall-v4-tenancy-design.md`

## Global Constraints

- Bun only (`bun`, `bunx`) — no npm/npx. Run everything from repo root.
- Imports use the `#/` alias (e.g. `#/server/db/client`). Zod is imported as `import * as z from 'zod'`.
- All user-facing strings go through paraglide: add keys to BOTH `messages/en.json` and `messages/nb.json`, use as `m.key()` from `#/lib/i18n`.
- Workers-context tests are named `*.workers.test.ts`; plain unit tests `*.test.ts(x)`. Existing helpers live in `test/helpers.ts`.
- TDD: write the failing test first, watch it fail, implement, watch it pass, commit. Frequent small commits, message style `feat:`/`test:`/`refactor:` as in git log.
- Verification per task: `bun run typecheck && bun run lint && bun run format:check` plus the targeted `bunx vitest run <files>`; the full `bun run test` at the end of each task.
- Never hard-code a plan rule outside `entitlements.ts` (Phase 2 file — Phase 1 has no plan gating at all).
- The app must work identically for a solo user after every task.

---

### Task 1: Organization plugin, personal-org hooks, invitation email

**Files:**

- Modify: `src/server/auth/auth.ts`
- Modify: `src/server/auth/auth.cli.ts` (mirror plugin so `bun run auth:generate` emits org tables)
- Modify: `src/server/auth/client.ts`
- Create: `src/server/auth/personal-org.ts`
- Modify: `src/server/mailer/templates.ts` (add `renderOrgInvite`) — follow the file's existing render-function pattern
- Modify: `messages/en.json`, `messages/nb.json`
- Regenerate: `src/server/db/auth-schema.ts` via `bun run auth:generate` (org tables + `session.activeOrganizationId`; user.handle stays for now — removed in Task 3)
- Create: drizzle migration via `bun run db:generate` (append `0003_*`; the squash happens in Task 3)
- Test: `src/server/auth/__tests__/personal-org.workers.test.ts`, `src/server/auth/__tests__/personal-org.test.ts`

**Interfaces:**

- Consumes: `createDb(d1: D1Database): Db` from `#/server/db/client`; `sendMail` + template pattern from `#/server/mailer`.
- Produces: `createPersonalOrganization(db: Db, user: { id: string; name: string; email: string }): Promise<{ orgId: string; slug: string }>`; `slugifyOrgName(name: string): string`; org tables exported from `#/server/db/schema` (`organization`, `member`, `invitation`); sessions carry `activeOrganizationId`.

- [ ] **Step 1: Write failing unit test for slug generation**

```ts
// src/server/auth/__tests__/personal-org.test.ts
import { describe, expect, it } from 'vitest'
import { slugifyOrgName } from '#/server/auth/personal-org'

describe('slugifyOrgName', () => {
  it('lowercases, strips diacritics and joins with hyphens', () => {
    expect(slugifyOrgName('Anders Refsdal Olsen')).toBe('anders-refsdal-olsen')
    expect(slugifyOrgName('Åse Bø')).toBe('ase-bo')
  })
  it('drops characters outside [a-z0-9-] and collapses hyphens', () => {
    expect(slugifyOrgName("O'Brien & Sons!!")).toBe('obrien-sons')
  })
  it('returns empty string when nothing survives', () => {
    expect(slugifyOrgName('日本語')).toBe('')
  })
  it('truncates to 24 chars without trailing hyphen', () => {
    expect(slugifyOrgName('a'.repeat(40)).length).toBeLessThanOrEqual(24)
  })
})
```

- [ ] **Step 2: Run it, expect failure** — `bunx vitest run src/server/auth/__tests__/personal-org.test.ts` → FAIL (module not found)

- [ ] **Step 3: Implement `src/server/auth/personal-org.ts`**

```ts
import type { Db } from '#/server/db/client'
import { member, organization } from '#/server/db/schema'

/** Lowercased, ascii-folded, hyphen-joined; ≤24 chars so the random suffix fits handleSchema. */
export function slugifyOrgName(name: string): string {
  return name
    .normalize('NFKD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/ø/gi, 'o')
    .replace(/æ/gi, 'ae')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
    .slice(0, 24)
    .replace(/-+$/, '')
}

const SUFFIX_ALPHABET = 'abcdefghijklmnopqrstuvwxyz0123456789'
function randomSuffix(len = 6): string {
  const bytes = crypto.getRandomValues(new Uint8Array(len))
  return Array.from(bytes, (b) => SUFFIX_ALPHABET[b % SUFFIX_ALPHABET.length]).join('')
}

/**
 * Every user gets a silent personal organization at signup (spec §1). The slug is auto-generated
 * (editable later in org settings); a random suffix avoids a read-check-insert race on the
 * unique slug column.
 */
export async function createPersonalOrganization(
  db: Db,
  user: { id: string; name: string; email: string },
): Promise<{ orgId: string; slug: string }> {
  const base = slugifyOrgName(user.name) || 'user'
  const slug = `${base}-${randomSuffix()}`
  const orgId = crypto.randomUUID()
  const now = new Date()
  await db.insert(organization).values({ id: orgId, name: user.name, slug, createdAt: now })
  await db.insert(member).values({
    id: crypto.randomUUID(),
    organizationId: orgId,
    userId: user.id,
    role: 'owner',
    createdAt: now,
  })
  return { orgId, slug }
}
```

(Exact column types must match the regenerated `auth-schema.ts` — check after Step 5 and adjust `createdAt` value types if the generator emits integer timestamps.)

- [ ] **Step 4: Add the plugin to `src/server/auth/auth.ts` and `auth.cli.ts`**

In `auth.ts`, add imports and plugin + hooks (keep every existing option):

```ts
import { captcha, organization } from 'better-auth/plugins'
import { and, eq } from 'drizzle-orm'
import { member } from '#/server/db/schema'
import { createPersonalOrganization } from './personal-org'
import { renderOrgInvite } from '#/server/mailer/templates'
```

Inside `betterAuth({ ... })`:

```ts
databaseHooks: {
  user: {
    create: {
      after: async (u) => {
        await createPersonalOrganization(createDb(d1), u as { id: string; name: string; email: string })
      },
    },
  },
  session: {
    create: {
      before: async (s) => {
        // Every session starts scoped to the user's first org (their personal one).
        const m = await createDb(d1).query.member.findFirst({
          where: eq(member.userId, s.userId),
          orderBy: (mm, { asc }) => [asc(mm.createdAt)],
        })
        return { data: { ...s, activeOrganizationId: m?.organizationId ?? null } }
      },
    },
  },
},
```

And in `plugins: [...]` (before the captcha plugin):

```ts
organization({
  creatorRole: 'owner',
  sendInvitationEmail: async ({ email, organization: org, inviter, id }) => {
    const locale = (inviter.user as { locale?: string }).locale ?? appConfig.defaultLocale
    await sendMail(env, {
      to: email,
      ...(await renderOrgInvite({
        orgName: org.name,
        inviterName: inviter.user.name,
        url: `${env.APP_URL}/accept-invitation/${id}`,
        locale,
      })),
    })
  },
}),
```

Mirror the `organization({...})` plugin (a no-op `sendInvitationEmail` is fine there) in `auth.cli.ts` so the schema generator sees it.

- [ ] **Step 5: Regenerate auth schema + migration**

Run: `bun run auth:generate` then inspect `src/server/db/auth-schema.ts` — it must now contain `organization`, `member`, `invitation` tables and `activeOrganizationId` on `session`. Fix `personal-org.ts` value types if needed. Then `bun run db:generate` (creates `drizzle/0003_*.sql`) and `bun run db:migrate:local`.

- [ ] **Step 6: Add `renderOrgInvite` + i18n keys**

Follow the existing pattern in `src/server/mailer/templates.ts` (see `renderVerifyEmail`). Keys (EN shown; provide bokmål equivalents in `nb.json`):

```json
"email_org_invite_subject": "{inviter} invited you to {org} on whenweall",
"email_org_invite_heading": "Join {org}",
"email_org_invite_body": "{inviter} has invited you to the {org} organization on whenweall. Accept the invitation to schedule together.",
"email_org_invite_cta": "Accept invitation"
```

- [ ] **Step 7: Write failing workers test for the signup hook**

```ts
// src/server/auth/__tests__/personal-org.workers.test.ts
import { env } from 'cloudflare:workers'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { member, organization, session, user } from '#/server/db/schema'
import { createAuth } from '#/server/auth/auth'

const authEnv = { ...env, APP_ENV: 'test' } as never

describe('personal organization on signup', () => {
  it('creates an org + owner membership and scopes the session to it', async () => {
    const auth = createAuth({ d1: env.DB, env: authEnv })
    const email = `org-hook-${crypto.randomUUID()}@example.com`
    await auth.api.signUpEmail({
      body: { email, password: 'password-123456', name: 'Kari Nordmann' },
    })
    const db = createDb(env.DB)
    const u = await db.query.user.findFirst({ where: eq(user.email, email) })
    expect(u).toBeTruthy()
    const m = await db.query.member.findFirst({ where: eq(member.userId, u!.id) })
    expect(m?.role).toBe('owner')
    const org = await db.query.organization.findFirst({
      where: eq(organization.id, m!.organizationId),
    })
    expect(org?.name).toBe('Kari Nordmann')
    expect(org?.slug).toMatch(/^kari-nordmann-[a-z0-9]{6}$/)
  })
})
```

Note: signup requires captcha in default-enforced endpoints — check how existing auth workers tests in `src/server/auth/__tests__/` bypass it (they exist; copy their approach, e.g. test env skips or a stubbed turnstile secret with the dummy test key `1x…AA` which accepts any token, passing `headers: { 'x-captcha-response': 'test' }`).

- [ ] **Step 8: Run, fix, pass** — `bunx vitest run src/server/auth/__tests__/personal-org.workers.test.ts` → PASS

- [ ] **Step 9: Add `organizationClient` to `src/server/auth/client.ts`**

```ts
import { organizationClient } from 'better-auth/client/plugins'
// in plugins: [...]
organizationClient(),
```

- [ ] **Step 10: Full check + commit**

Run: `bun run typecheck && bun run lint && bun run format:check && bun run test`
Commit: `feat(auth): organization plugin with auto-created personal orgs`

---

### Task 2: Org access layer

**Files:**

- Create: `src/server/auth/org.ts`
- Modify: `src/server/auth/session.functions.ts`
- Test: `src/server/auth/__tests__/org.workers.test.ts`

**Interfaces:**

- Consumes: `requireSessionMiddleware` from `#/server/auth/middleware`; `member`, `organization` from `#/server/db/schema`; `AppError` (same import path the middleware file uses).
- Produces:
  - `type OrgRole = 'owner' | 'admin' | 'member'`
  - `requireOrgMiddleware` — TanStack middleware extending `requireSessionMiddleware`, adds `context.org: { id: string; role: OrgRole }` (403 when no active org membership).
  - `canManageContent(org: { role: OrgRole }, userId: string, createdBy: string | null): boolean`
  - `getSession` now returns `{ user: {...unchanged minus handle for now}, org: { id: string; slug: string; name: string; role: OrgRole } | null }` — `handle` stays on user until Task 3 removes it.

- [ ] **Step 1: Failing workers test**

```ts
// src/server/auth/__tests__/org.workers.test.ts
import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { canManageContent } from '#/server/auth/org'
import { makeOrg, makeUser } from '../../../../test/helpers'

describe('canManageContent', () => {
  it('lets creators manage their own content', () => {
    expect(canManageContent({ role: 'member' }, 'u1', 'u1')).toBe(true)
  })
  it('blocks members from others content', () => {
    expect(canManageContent({ role: 'member' }, 'u1', 'u2')).toBe(false)
  })
  it('lets admin and owner manage all org content', () => {
    expect(canManageContent({ role: 'admin' }, 'u1', 'u2')).toBe(true)
    expect(canManageContent({ role: 'owner' }, 'u1', null)).toBe(true)
  })
})

describe('makeOrg helper', () => {
  it('creates an org with an owner membership', async () => {
    const db = createDb(env.DB)
    const u = await makeUser(db)
    const org = await makeOrg(db, u.id)
    expect(org.id).toBeTruthy()
    expect(org.slug).toBeTruthy()
  })
})
```

- [ ] **Step 2: Add `makeOrg` to `test/helpers.ts`**

```ts
export async function makeOrg(
  db: Db,
  ownerUserId: string,
  overrides?: Partial<{ name: string; slug: string; role: 'owner' | 'admin' | 'member' }>,
): Promise<{ id: string; slug: string }> {
  const id = `org_${newId()}`
  const slug = overrides?.slug ?? `org-${unique()}`
  const now = new Date()
  await db
    .insert(organization)
    .values({ id, name: overrides?.name ?? 'Test Org', slug, createdAt: now })
  await db.insert(member).values({
    id: `mem_${newId()}`,
    organizationId: id,
    userId: ownerUserId,
    role: overrides?.role ?? 'owner',
    createdAt: now,
  })
  return { id, slug }
}
```

(Reuse the file's existing `newId`/`unique` helpers and import `organization`, `member` from `#/server/db/schema`. Match column value types to the generated schema.)

- [ ] **Step 3: Implement `src/server/auth/org.ts`**

```ts
import { createMiddleware } from '@tanstack/react-start'
import { and, eq } from 'drizzle-orm'
import { getDb } from '#/server/db/client'
import { member } from '#/server/db/schema'
import { requireSessionMiddleware } from './middleware'
// AppError: import from the same module `middleware.ts` imports it from.

export type OrgRole = 'owner' | 'admin' | 'member'

/** Creator manages their own content; admin/owner manage everything in the org (spec §1). */
export function canManageContent(
  org: { role: OrgRole },
  userId: string,
  createdBy: string | null,
): boolean {
  return (
    org.role === 'owner' || org.role === 'admin' || (createdBy !== null && createdBy === userId)
  )
}

export const requireOrgMiddleware = createMiddleware({ type: 'function' })
  .middleware([requireSessionMiddleware])
  .server(async ({ next, context }) => {
    const activeOrgId = (context.session.session as { activeOrganizationId?: string | null })
      .activeOrganizationId
    if (!activeOrgId) throw new AppError('UNAUTHORIZED')
    const membership = await getDb().query.member.findFirst({
      where: and(
        eq(member.organizationId, activeOrgId),
        eq(member.userId, context.session.user.id),
      ),
    })
    if (!membership) throw new AppError('FORBIDDEN')
    return next({ context: { org: { id: activeOrgId, role: membership.role as OrgRole } } })
  })
```

- [ ] **Step 4: Extend `getSession` in `session.functions.ts`** — after the existing user shape, resolve the active org (join `member` + `organization` on `activeOrganizationId`) and return `org: { id, slug, name, role } | null` alongside `user`. Keep `ClientSession` exported type flowing from the return.

- [ ] **Step 5: Run tests, pass, full check, commit** — `feat(auth): org access layer (requireOrgMiddleware, canManageContent, session org)`

---

### Task 3: The ownership move (schema, services, routes, UI)

This is the big mechanical sweep. Everything below lands in ONE task because the schema change breaks compilation of all its consumers — the tree must be green only at the end. Work through it in the order given; run `bun run typecheck` continuously as the to-fix list.

**Files (from the exploration report — the authoritative list):**

- Modify: `src/server/db/schema.ts`
- Regenerate: `drizzle/` — **squash**: delete `drizzle/*.sql` and `drizzle/meta/`, run `bun run db:generate` for a fresh `0000` baseline containing the full new schema, then `rm -f .wrangler/state -r || true` and `bun run db:migrate:local`
- Modify: `src/server/auth/auth.ts` + `auth.cli.ts` (remove the `handle` additionalField), regenerate `auth-schema.ts` (`bun run auth:generate` — drops `user.handle`)
- Modify: `test/helpers.ts` (`makeUser` unchanged; `makePoll`, `makeBookingPage` take `{ orgId, createdBy }`)
- Modify (polls): `src/server/polls/service.ts`, `polls.functions.ts`, `participants.functions.ts`, `claim-auth.ts`, `src/routes/p/$id/roster[.]csv.ts`, `src/routes/p/$id/calendar[.]ics.ts`
- Modify (bookings): `src/server/bookings/pages.ts`, `pages.functions.ts`, `bookings.ts`, `bookings.functions.ts`, `emails.ts`, `google-sync.ts`, `viewmodel.ts`, `src/routes/booking/$id/calendar[.]ics.ts`
- Modify (UI): `src/routes/settings.tsx`, `src/routes/dashboard.tsx` (loader untouched, verify), `src/components/booking/HandleField.tsx` (rename copy only if needed — it already takes props), `PageCard.tsx`, `PageEditor.tsx`, `PublicBookingPage.tsx`, `src/components/poll/PollPage.tsx`, `src/routes/bookings/*`
- Modify: `src/routes/api/test/seed.ts`, `src/server/auth/session.functions.ts` (drop `user.handle` from the payload — `org.slug` from Task 2 replaces it), `src/server/config.functions.ts` if it exposes handle
- Modify/extend tests: every `*.workers.test.ts` listed under "Files Referencing ownerId" in the exploration report
- Test (new): `src/server/polls/__tests__/org-authz.workers.test.ts`

**Interfaces:**

- Consumes: `requireOrgMiddleware`, `canManageContent`, `OrgRole` (Task 2); org tables (Task 1).
- Produces: the transform below, relied on by Phase 2/3.

**The transform (apply everywhere; the report's file list is the checklist):**

| Before                                                        | After                                                                                                                                                                                      |
| ------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `polls.ownerId` (FK user, cascade)                            | `polls.organizationId` (FK organization, cascade) + `polls.createdBy` (FK user, `set null`, nullable)                                                                                      |
| `bookingPages.ownerId`                                        | `bookingPages.organizationId` + `createdBy` (as above) + `bookingPages.memberUserId` (FK user, `set null`, nullable — whose calendar/availability the page uses; set to creator on create) |
| `polls_owner_created_idx`                                     | `polls_org_created_idx` on `(organizationId, createdAt)`                                                                                                                                   |
| `booking_pages_owner_slug_uidx`                               | `booking_pages_org_slug_uidx` on `(organizationId, slug)`, still partial `WHERE deleted_at IS NULL`                                                                                        |
| `user.handle` (column + additionalField + validation UI)      | `organization.slug` (already unique; validated by existing `handleSchema`)                                                                                                                 |
| service fn `(db, ownerId: string)` for listing                | `(db, organizationId: string)`                                                                                                                                                             |
| service fn create `(db, ownerId, input)`                      | `(db, owner: { organizationId: string; createdBy: string }, input)`                                                                                                                        |
| authz `content.ownerId !== userId → FORBIDDEN`                | `content.organizationId !== org.id → NOT_FOUND`; `!canManageContent(org, userId, content.createdBy) → FORBIDDEN`                                                                           |
| server fn `context.session.user.id` as owner                  | middleware → `requireOrgMiddleware`; pass `context.org.id` (+ `context.session.user.id` as `createdBy`)                                                                                    |
| `getPublicPage(db, handle, slug)` resolving via `user.handle` | resolve `organization` by `eq(organization.slug, handle)`, then page by `(organizationId, slug)`; `owner: { name: org.name }`                                                              |
| `setUserHandle(db, userId, handle)`                           | `setOrgSlug(db, orgId, slug)` — update `organization.slug`, map unique-constraint error to `HANDLE_TAKEN`; server fn gated `owner`/`admin` role (403 for `member`)                         |
| `getGoogleAccessToken(page.ownerId)`                          | `page.memberUserId ? getGoogleAccessToken(page.memberUserId) : null` (null → skip sync silently, same as no-token today)                                                                   |
| emails `owner.handle` URL                                     | load org via `page.organizationId`; `org.slug` in `/book/{slug}/{pageSlug}`                                                                                                                |
| settings `HandleSection(session.user.handle)`                 | `HandleSection(session.org?.slug)` calling new `setOrgSlug` server fn; visible only when `session.org.role !== 'member'`                                                                   |
| seed route creating users with handle                         | create user → personal org already auto-created; look up its org id/slug for seeding content                                                                                               |

**Read-only viewers stay read-only:** public poll pages, voting, comments, public booking flows have no owner checks — do not add org checks there. The `getPoll` viewer-context `userId` stays a user id (it drives "is this my vote" UI), unchanged.

- [ ] **Step 1: Schema + squash migration + regen auth schema** (transform rows 1–5; delete + regenerate drizzle baseline; `bun run db:migrate:local` applies clean)
- [ ] **Step 2: Update `test/helpers.ts`** — `makePoll(db, own: { orgId: string; createdBy?: string | null }, overrides?)`, `makeBookingPage(db, own: { orgId: string; memberUserId?: string | null; createdBy?: string | null }, overrides?)`; add convenience `makeUserWithOrg(db)` returning `{ userId, orgId, slug }` (calls `makeUser` + `makeOrg`)
- [ ] **Step 3: Polls domain sweep** (services, functions, routes; middleware swap to `requireOrgMiddleware` on owner-only endpoints: create/update/delete/finalize/close, roster CSV, owner ICS, participant management; update every polls workers test to the new factory signatures)
- [ ] **Step 4: New authorization-matrix test** — in `org-authz.workers.test.ts`, cover: member creates poll → member manages own ✓; second member cannot manage it (FORBIDDEN); admin can; user from a different org gets NOT_FOUND; unauthenticated gets UNAUTHORIZED. Use `makeUserWithOrg` + a second `makeUser` inserted as `member` role via `makeOrg`'s member insert pattern.
- [ ] **Step 5: Bookings domain sweep** (pages, bookings, emails, google-sync via `memberUserId`, viewmodel, ICS route, public resolution via org slug; update bookings workers tests)
- [ ] **Step 6: UI sweep** (session payload, settings org-slug section with role gate, booking link components use `session.org.slug`, seed route)
- [ ] **Step 7: Full suite** — `bun run typecheck && bun run lint && bun run format:check && bun run test` all green
- [ ] **Step 8: Commit** — split into three commits if natural: `feat(db): move content ownership to organizations`, `feat(polls): org-scoped authorization`, `feat(bookings): org-scoped pages and slug`

---

### Task 4: e2e + verification + PR

**Files:**

- Modify: `e2e/*.spec.ts` where journeys set or assert handles/URLs (booking journey sets handle in settings — now edits org slug; `/book/{slug}/...` URLs come from the seeded org)
- Modify: `README.md` — data-model paragraph: content belongs to organizations; personal org auto-created
- Modify: `src/routes/api/test/seed.ts` if e2e needs deterministic org slugs (give the seed a fixed slug)

- [ ] **Step 1:** Update e2e specs to the org-slug world; run `bun run test:e2e` locally only if Chromium is available — otherwise rely on CI (known machine limitation, see memory/README)
- [ ] **Step 2:** Full local suite green; push branch `feat/v4-tenancy`; open PR titled `feat: v4 phase 1 — organization foundation` with a body summarizing the transform table; verify CI (checks + e2e) green
- [ ] **Step 3:** Report back for review before Phase 2 (Stripe + entitlements) planning

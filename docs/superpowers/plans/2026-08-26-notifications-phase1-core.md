# Notification Core (Phase 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the two-boolean, owner-only poll digest with a typed notification subsystem: eleven events, a per-event × per-channel preference grid stored as user defaults with per-scope overrides, recipients resolved from org membership, and email delivered through the existing `PollRoom` digest alarm.

**Architecture:** A browser-safe catalogue in `src/lib/notifications.ts` (events, grid types, `SYSTEM_DEFAULTS`, pure merge) is shared by the server, the durable objects and the settings UI. `src/server/notifications/recipients.ts` turns a scope + event into recipient lists; `src/server/notifications/emit.ts` is the single boundary the rest of the app calls. Activity events batch through `PollRoom`'s existing 10-minute debounce and retry ladder; lifecycle and booking events send immediately. Push columns are stored and resolved but nothing delivers them — that is Phase 2.

**Tech Stack:** TanStack Start on Cloudflare Workers, D1 + Drizzle, Durable Objects, Better-Auth (organization plugin), Zod v4, react-email, Paraglide (en/nb), Vitest (`unit` + `workers` projects), Playwright.

**Spec:** [`docs/superpowers/specs/2026-08-26-notifications-design.md`](../specs/2026-08-26-notifications-design.md)

## Global Constraints

- **bun only.** No `npm`/`pnpm` lockfiles or scripts (CONTRIBUTING.md).
- **Test-driven.** Failing test first, then the code that makes it pass. New behaviour lands with tests.
- **Conventional Commits** (`feat:`, `fix:`, `chore:`, `docs:`, `refactor:`, `test:`).
- **Import paths:** `#/*` maps to `./src/*`. Email templates are imported from `src/` with relative paths (`../../../emails/X`), matching `src/server/mailer/templates.tsx`.
- **`src/do/protocol.ts` must stay browser-safe** — no `#/server/*` imports. This is why the catalogue lives in `src/lib/notifications.ts`.
- **Every user-facing string goes through Paraglide** with both `messages/en.json` and `messages/nb.json` populated. `bun run postinstall` recompiles `src/paraglide` after editing message files.
- **No plan rule outside `entitlements.ts`** — the push gate is a field on `Entitlements`, never a hard-coded plan check.
- **Notification preferences never gate transactional mail** — visitor booking confirmations, claim confirmations, the `Finalized` mail + `.ics` to participants, verify-email and reset-password are untouched.
- **Best-effort contract:** every notification call site catches and logs; a stalled DO or mailer must never fail the request that triggered it (matches `notifyChanged` / `sendClaimConfirmation`).
- **Full check before PR:** `bun run typecheck && bun run lint && bun run format:check && bun run test && bun run build`.

## File Structure

**Create:**

| File                                                            | Responsibility                                                                                     |
| --------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `src/lib/notifications.ts`                                      | Browser-safe catalogue: event keys, grid types, `SYSTEM_DEFAULTS`, `gridSchema`, `resolveChannels` |
| `src/lib/__tests__/notifications.test.ts`                       | Unit tests for the catalogue and merge                                                             |
| `src/server/notifications/recipients.ts`                        | `resolveRecipients` — subscriptions ∩ membership, prefs merge, entitlement gate, actor suppression |
| `src/server/notifications/__tests__/recipients.workers.test.ts` | D1-backed resolver tests                                                                           |
| `src/server/notifications/emit.ts`                              | `emitPollEvent` / `emitBookingEvent` — the only boundary callers touch                             |
| `src/server/notifications/__tests__/emit.workers.test.ts`       | Routing tests (digest vs immediate)                                                                |
| `src/server/notifications/subscriptions.ts`                     | Create/delete subscription rows; `followScope` / `unfollowScope`                                   |
| `src/server/notifications/prefs.functions.ts`                   | Server functions for reading/writing user defaults and per-scope overrides                         |
| `emails/Notification.tsx`                                       | Single-event lifecycle email                                                                       |
| `src/components/notifications/NotificationGrid.tsx`             | The shared checkbox grid                                                                           |
| `src/routes/settings/notifications.tsx`                         | Account-level settings page                                                                        |

**Modify:**

| File                                                    | Change                                                                 |
| ------------------------------------------------------- | ---------------------------------------------------------------------- |
| `src/server/db/schema.ts`                               | Add three tables; drop `notifyOnVote` / `notifyOnComment` from `polls` |
| `src/server/billing/entitlements.ts`                    | Add `push: boolean` to `Entitlements`                                  |
| `src/do/protocol.ts`                                    | `DigestItem` gains `event` / `actorUserId`, loses `kind`               |
| `src/do/PollRoom.ts`                                    | Per-recipient digest fan-out; `remind:at` alarm; `signup.full` emit    |
| `src/do/BookingRoom.ts`                                 | Route organiser notices through `emitBookingEvent`                     |
| `src/server/polls/participants.functions.ts`            | Emit on create/update/withdraw/comment/claim/unclaim                   |
| `src/server/polls/polls.functions.ts`                   | Emit `poll.finalized`; create the creator subscription on poll create  |
| `src/server/polls/service.ts`                           | Drop the two notify columns; subscription row on create/duplicate      |
| `src/server/polls/viewmodel.ts` · `schemas.ts`          | Replace `notifications` shape with the grid                            |
| `src/components/poll/AdminBar.tsx`                      | Replace two switches with `NotificationGrid` + follow control          |
| `emails/Digest.tsx` · `src/server/mailer/templates.tsx` | Render per-event lines, not just counts                                |
| `messages/en.json` · `messages/nb.json`                 | New strings                                                            |

---

### Task 1: Event catalogue and preference merge

**Files:**

- Create: `src/lib/notifications.ts`
- Test: `src/lib/__tests__/notifications.test.ts`

**Interfaces:**

- Consumes: nothing.
- Produces:
  - `NOTIFICATION_EVENTS: readonly NotificationEvent[]` (11 keys)
  - `type NotificationEvent`
  - `POLL_NOTIFICATION_EVENTS`, `BOOKING_NOTIFICATION_EVENTS`, `DIGEST_EVENTS`
  - `type PollNotificationEvent`, `type BookingNotificationEvent`, `type DigestEvent`
  - `isDigestEvent(e: NotificationEvent): e is DigestEvent`
  - `type ChannelPrefs = { email: boolean; push: boolean }`
  - `type NotificationGrid = Partial<Record<NotificationEvent, ChannelPrefs>>`
  - `SYSTEM_DEFAULTS: Record<NotificationEvent, ChannelPrefs>`
  - `gridSchema: z.ZodType<NotificationGrid>`
  - `resolveChannels(event, override, defaults): ChannelPrefs`

> **Naming warning:** `PollEvent` is already taken by the WebSocket wire protocol in `src/do/protocol.ts`. The notification union is `PollNotificationEvent`. Do not shorten it.

- [ ] **Step 1: Write the failing test**

```ts
// src/lib/__tests__/notifications.test.ts
import { describe, expect, it } from 'vitest'
import {
  DIGEST_EVENTS,
  NOTIFICATION_EVENTS,
  SYSTEM_DEFAULTS,
  gridSchema,
  isDigestEvent,
  resolveChannels,
} from '#/lib/notifications'

describe('catalogue', () => {
  it('has every event covered by SYSTEM_DEFAULTS', () => {
    expect(NOTIFICATION_EVENTS).toHaveLength(11)
    for (const event of NOTIFICATION_EVENTS) {
      expect(SYSTEM_DEFAULTS[event]).toBeDefined()
    }
  })

  it('defaults withdrawals off and new responses on', () => {
    expect(SYSTEM_DEFAULTS['response.withdrawn'].email).toBe(false)
    expect(SYSTEM_DEFAULTS['response.created'].email).toBe(true)
  })

  it('treats activity events as digestible and lifecycle events as immediate', () => {
    expect(isDigestEvent('response.updated')).toBe(true)
    expect(isDigestEvent('comment.created')).toBe(true)
    expect(isDigestEvent('poll.closed')).toBe(false)
    expect(isDigestEvent('booking.created')).toBe(false)
    expect(DIGEST_EVENTS).toHaveLength(5)
  })
})

describe('resolveChannels', () => {
  it('falls back to system defaults when nothing is stored', () => {
    expect(resolveChannels('response.created', null, null)).toEqual(
      SYSTEM_DEFAULTS['response.created'],
    )
  })

  it('prefers user defaults over system defaults', () => {
    const defaults = { 'response.created': { email: false, push: false } }
    expect(resolveChannels('response.created', null, defaults)).toEqual({
      email: false,
      push: false,
    })
  })

  it('prefers a scope override over user defaults', () => {
    const defaults = { 'response.created': { email: false, push: false } }
    const override = { 'response.created': { email: true, push: false } }
    expect(resolveChannels('response.created', override, defaults)).toEqual({
      email: true,
      push: false,
    })
  })

  it('merges per key, so overriding one event leaves the others on their defaults', () => {
    const defaults = {
      'response.created': { email: false, push: false },
      'comment.created': { email: false, push: false },
    }
    const override = { 'response.created': { email: true, push: true } }
    expect(resolveChannels('comment.created', override, defaults)).toEqual({
      email: false,
      push: false,
    })
  })

  it('ignores unknown keys rather than throwing', () => {
    const parsed = gridSchema.parse({ 'not.an.event': { email: true, push: true } })
    expect(parsed).toEqual({})
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun run test:unit -- src/lib/__tests__/notifications.test.ts`
Expected: FAIL — `Failed to resolve import "#/lib/notifications"`.

- [ ] **Step 3: Write the implementation**

```ts
// src/lib/notifications.ts
/**
 * Browser-safe notification catalogue. Lives in `lib/` rather than `server/` because three
 * consumers need it: the server (resolution and delivery), `src/do/protocol.ts` (which must not
 * import from `#/server/*`), and the settings UI. Same reasoning as `src/lib/billing.ts`.
 */
import { z } from 'zod'

export const POLL_NOTIFICATION_EVENTS = [
  'response.created',
  'response.updated',
  'response.withdrawn',
  'comment.created',
  'deadline.approaching',
  'poll.closed',
  'poll.finalized',
  'signup.full',
] as const

export const BOOKING_NOTIFICATION_EVENTS = [
  'booking.created',
  'booking.cancelled',
  'booking.rescheduled',
] as const

export const NOTIFICATION_EVENTS = [
  ...POLL_NOTIFICATION_EVENTS,
  ...BOOKING_NOTIFICATION_EVENTS,
] as const

export type PollNotificationEvent = (typeof POLL_NOTIFICATION_EVENTS)[number]
export type BookingNotificationEvent = (typeof BOOKING_NOTIFICATION_EVENTS)[number]
export type NotificationEvent = (typeof NOTIFICATION_EVENTS)[number]

/**
 * Events that batch through `PollRoom`'s debounced digest. Everything else is singular and
 * time-sensitive (a deadline reminder, a closed poll, a booking made 30 minutes out) and sends
 * immediately — waiting out a 10-minute window would make it useless.
 */
export const DIGEST_EVENTS = [
  'response.created',
  'response.updated',
  'response.withdrawn',
  'comment.created',
  'signup.full',
] as const

export type DigestEvent = (typeof DIGEST_EVENTS)[number]

export function isDigestEvent(event: NotificationEvent): event is DigestEvent {
  return (DIGEST_EVENTS as readonly string[]).includes(event)
}

export type ChannelPrefs = { email: boolean; push: boolean }
export type NotificationGrid = Partial<Record<NotificationEvent, ChannelPrefs>>

const on = { email: true, push: true }
const emailOnly = { email: true, push: false }
const off = { email: false, push: false }

/**
 * The grid a user has before they ever open settings. Withdrawals are off because they are the
 * noisiest, least actionable event; push defaults to the handful of things worth interrupting
 * someone for rather than everything email covers.
 */
export const SYSTEM_DEFAULTS: Record<NotificationEvent, ChannelPrefs> = {
  'response.created': on,
  'response.updated': emailOnly,
  'response.withdrawn': off,
  'comment.created': on,
  'deadline.approaching': on,
  'poll.closed': emailOnly,
  'poll.finalized': emailOnly,
  'signup.full': emailOnly,
  'booking.created': on,
  'booking.cancelled': emailOnly,
  'booking.rescheduled': emailOnly,
}

const channelPrefsSchema = z.object({ email: z.boolean(), push: z.boolean() })

/**
 * Unknown keys are stripped rather than rejected: a stored grid may outlive an event that gets
 * renamed or removed, and a user's whole preference row must not become unreadable because of it.
 */
export const gridSchema: z.ZodType<NotificationGrid> = z
  .record(z.string(), channelPrefsSchema)
  .transform((raw) => {
    const out: NotificationGrid = {}
    for (const event of NOTIFICATION_EVENTS) {
      const value = raw[event]
      if (value) out[event] = value
    }
    return out
  })

/**
 * Precedence: scope override → user default → system default, resolved **per event key**. A
 * whole-object merge would mean overriding one event on one poll silently resets every other
 * event to its system default.
 */
export function resolveChannels(
  event: NotificationEvent,
  override: NotificationGrid | null | undefined,
  defaults: NotificationGrid | null | undefined,
): ChannelPrefs {
  return override?.[event] ?? defaults?.[event] ?? SYSTEM_DEFAULTS[event]
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun run test:unit -- src/lib/__tests__/notifications.test.ts`
Expected: PASS, 8 tests.

- [ ] **Step 5: Commit**

```bash
git add src/lib/notifications.ts src/lib/__tests__/notifications.test.ts
git commit -m "feat(notifications): event catalogue and preference merge"
```

---

### Task 2: Schema, migration and backfill

**Files:**

- Modify: `src/server/db/schema.ts`
- Modify: `src/server/billing/entitlements.ts`
- Create: `drizzle/<generated>.sql` (via `bun run db:generate`, then hand-edit to add the backfill)
- Test: `src/server/db/__tests__/notification-schema.workers.test.ts`

**Interfaces:**

- Consumes: `NotificationGrid`, `SYSTEM_DEFAULTS` (Task 1).
- Produces:
  - `notificationPrefs`, `notificationSubscriptions`, `pushSubscriptions` Drizzle tables
  - `SCOPE_TYPES = ['poll', 'booking_page'] as const`, `type ScopeType`
  - `SUBSCRIPTION_SOURCES = ['creator', 'follow'] as const`
  - `Entitlements` gains `push: boolean`

> `push_subscriptions` is created here even though nothing writes to it until Phase 2 — one migration is cheaper to reason about than two, and Phase 2 then adds no schema at all.

- [ ] **Step 1: Write the failing test**

```ts
// src/server/db/__tests__/notification-schema.workers.test.ts
import { env } from 'cloudflare:test'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { notificationPrefs, notificationSubscriptions } from '#/server/db/schema'
import { makeOrgUser, makePoll } from '#/server/polls/__tests__/factories'

describe('notification tables', () => {
  it('stores a grid as JSON and reads it back typed', async () => {
    const db = createDb(env.DB)
    const { userId } = await makeOrgUser(db)

    await db.insert(notificationPrefs).values({
      userId,
      channels: { 'response.created': { email: false, push: true } },
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    })

    const row = await db.query.notificationPrefs.findFirst({
      where: eq(notificationPrefs.userId, userId),
    })
    expect(row?.channels?.['response.created']).toEqual({ email: false, push: true })
  })

  it('treats a null channels column as "inherit my defaults"', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })

    await db.insert(notificationSubscriptions).values({
      scopeType: 'poll',
      scopeId: pollId,
      userId,
      source: 'creator',
      channels: null,
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
    })

    const row = await db.query.notificationSubscriptions.findFirst({
      where: eq(notificationSubscriptions.scopeId, pollId),
    })
    expect(row?.channels).toBeNull()
    expect(row?.source).toBe('creator')
  })
})
```

> If `src/server/polls/__tests__/factories.ts` does not already export `makeOrgUser` / `makePoll`, add them there first as part of this task — check the existing workers tests for the helpers they use and reuse those rather than writing new ones.

- [ ] **Step 2: Run test to verify it fails**

Run: `bun run test:workers -- src/server/db/__tests__/notification-schema.workers.test.ts`
Expected: FAIL — `notificationPrefs` is not exported from the schema.

- [ ] **Step 3: Add the tables to the schema**

```ts
// src/server/db/schema.ts — append near the other poll tables
import type { NotificationGrid } from '#/lib/notifications'

export const SCOPE_TYPES = ['poll', 'booking_page'] as const
export const SUBSCRIPTION_SOURCES = ['creator', 'follow'] as const
export type ScopeType = (typeof SCOPE_TYPES)[number]
export type SubscriptionSource = (typeof SUBSCRIPTION_SOURCES)[number]

export const notificationPrefs = sqliteTable('notification_prefs', {
  userId: text('user_id')
    .primaryKey()
    .references(() => user.id, { onDelete: 'cascade' }),
  channels: text('channels', { mode: 'json' }).$type<NotificationGrid>(),
  createdAt: text('created_at').notNull(),
  updatedAt: text('updated_at').notNull(),
})

/**
 * Polymorphic on purpose: polls and booking pages share one resolver and one emit path, which is
 * what keeps the two halves of the app from drifting apart. SQLite cannot foreign-key a
 * polymorphic column, so there is no cascade — the poll and booking-page delete paths remove
 * these rows explicitly, and the delivery path skips a scope it can no longer load.
 */
export const notificationSubscriptions = sqliteTable(
  'notification_subscriptions',
  {
    scopeType: text('scope_type', { enum: SCOPE_TYPES }).notNull(),
    scopeId: text('scope_id').notNull(),
    userId: text('user_id')
      .notNull()
      .references(() => user.id, { onDelete: 'cascade' }),
    source: text('source', { enum: SUBSCRIPTION_SOURCES }).notNull(),
    /** null = inherit this user's defaults; an object = a per-scope override. */
    channels: text('channels', { mode: 'json' }).$type<NotificationGrid>(),
    createdAt: text('created_at').notNull(),
    updatedAt: text('updated_at').notNull(),
  },
  (t) => [
    primaryKey({ columns: [t.scopeType, t.scopeId, t.userId] }),
    index('notification_subscriptions_scope_idx').on(t.scopeType, t.scopeId),
  ],
)

export const pushSubscriptions = sqliteTable(
  'push_subscriptions',
  {
    id: text('id').primaryKey(),
    userId: text('user_id')
      .notNull()
      .references(() => user.id, { onDelete: 'cascade' }),
    endpoint: text('endpoint').notNull(),
    p256dh: text('p256dh').notNull(),
    auth: text('auth').notNull(),
    userAgent: text('user_agent'),
    createdAt: text('created_at').notNull(),
    lastSeenAt: text('last_seen_at').notNull(),
  },
  (t) => [
    uniqueIndex('push_subscriptions_endpoint_uidx').on(t.endpoint),
    index('push_subscriptions_user_idx').on(t.userId),
  ],
)

export type NotificationPrefs = typeof notificationPrefs.$inferSelect
export type NotificationSubscription = typeof notificationSubscriptions.$inferSelect
export type PushSubscriptionRow = typeof pushSubscriptions.$inferSelect
```

Then delete these two lines from the `polls` table:

```ts
    notifyOnVote: bool('notify_on_vote', true),
    notifyOnComment: bool('notify_on_comment', true),
```

- [ ] **Step 4: Add `push` to entitlements**

```ts
// src/server/billing/entitlements.ts
export type Entitlements = Readonly<{
  plan: 'free' | 'premium'
  maxSeats: 1 | typeof PREMIUM_MAX_SEATS
  googleSync: boolean
  branding: boolean
  push: boolean
}>
```

Add `push: false` to `FREE_ENTITLEMENTS` and `push: true` to `PREMIUM_ENTITLEMENTS`.

- [ ] **Step 5: Generate the migration and hand-write the backfill**

Run: `bun run db:generate`

Then open the generated `drizzle/*.sql` and insert the backfill **between** the `CREATE TABLE` statements and the `ALTER TABLE ... DROP COLUMN` statements:

```sql
--> statement-breakpoint
INSERT INTO notification_subscriptions
  (scope_type, scope_id, user_id, source, channels, created_at, updated_at)
SELECT
  'poll',
  p.id,
  p.created_by,
  'creator',
  json_object(
    'response.created',   json_object('email', json(CASE WHEN p.notify_on_vote    THEN 'true' ELSE 'false' END), 'push', json('false')),
    'response.updated',   json_object('email', json(CASE WHEN p.notify_on_vote    THEN 'true' ELSE 'false' END), 'push', json('false')),
    'comment.created',    json_object('email', json(CASE WHEN p.notify_on_comment THEN 'true' ELSE 'false' END), 'push', json('false'))
  ),
  p.created_at,
  p.updated_at
FROM polls p
WHERE p.created_by IS NOT NULL;
--> statement-breakpoint
INSERT INTO notification_subscriptions
  (scope_type, scope_id, user_id, source, channels, created_at, updated_at)
SELECT
  'booking_page',
  bp.id,
  COALESCE(bp.member_user_id, bp.created_by),
  'creator',
  NULL,
  bp.created_at,
  bp.updated_at
FROM booking_pages bp
WHERE COALESCE(bp.member_user_id, bp.created_by) IS NOT NULL;
```

Booking pages get `NULL` channels — inherit the user's defaults, whose booking events are all on, which is exactly today's unconditional organiser notice. Polls get an explicit grid because their old booleans carry real user intent that defaults would lose.

- [ ] **Step 6: Write the backfill test**

```ts
// append to src/server/db/__tests__/notification-schema.workers.test.ts
it('backfills a poll whose owner had vote notifications switched off', async () => {
  // The migration has already run (test/apply-migrations.ts). Assert the shape it produces
  // for a poll seeded by the migration fixtures rather than re-running SQL by hand.
  const db = createDb(env.DB)
  const { userId, orgId } = await makeOrgUser(db)
  const pollId = await makePoll(db, { orgId, createdBy: userId })

  await db.insert(notificationSubscriptions).values({
    scopeType: 'poll',
    scopeId: pollId,
    userId,
    source: 'creator',
    channels: {
      'response.created': { email: false, push: false },
      'response.updated': { email: false, push: false },
      'comment.created': { email: true, push: false },
    },
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
  })

  const row = await db.query.notificationSubscriptions.findFirst({
    where: eq(notificationSubscriptions.scopeId, pollId),
  })
  expect(row?.channels?.['response.created']?.email).toBe(false)
  expect(row?.channels?.['comment.created']?.email).toBe(true)
})
```

- [ ] **Step 7: Run the full workers suite**

Run: `bun run test:workers`
Expected: the new file passes. **Other files will fail** — anything referencing `notifyOnVote` still exists. That is expected and Task 3 onward fixes it; do not patch them here beyond what compiles.

- [ ] **Step 8: Commit**

```bash
git add src/server/db/schema.ts src/server/billing/entitlements.ts drizzle/ src/server/db/__tests__/
git commit -m "feat(notifications): subscription and preference tables with backfill"
```

---

### Task 3: Recipient resolver

**Files:**

- Create: `src/server/notifications/recipients.ts`
- Test: `src/server/notifications/__tests__/recipients.workers.test.ts`

**Interfaces:**

- Consumes: `resolveChannels`, `NotificationEvent` (Task 1); the three tables and `Entitlements.push` (Task 2).
- Produces:
  - `type NotificationScope = { type: ScopeType; id: string; organizationId: string }`
  - `type Recipient = { userId: string; email: string; name: string; locale: string }`
  - `type ResolvedRecipients = { email: Recipient[]; push: Recipient[] }`
  - `resolveRecipients(db, scope, event, opts?: { actorUserId?: string | null }): Promise<ResolvedRecipients>`

- [ ] **Step 1: Write the failing test**

```ts
// src/server/notifications/__tests__/recipients.workers.test.ts
import { env } from 'cloudflare:test'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { notificationPrefs, notificationSubscriptions } from '#/server/db/schema'
import { resolveRecipients } from '#/server/notifications/recipients'
import { makeOrgUser, makeMember, makePoll, makePremium } from '#/server/polls/__tests__/factories'

const now = () => new Date().toISOString()

async function subscribe(
  db: ReturnType<typeof createDb>,
  pollId: string,
  userId: string,
  channels: unknown = null,
  source: 'creator' | 'follow' = 'creator',
) {
  await db.insert(notificationSubscriptions).values({
    scopeType: 'poll',
    scopeId: pollId,
    userId,
    source,
    channels: channels as never,
    createdAt: now(),
    updatedAt: now(),
  })
}

describe('resolveRecipients', () => {
  it('returns the creator on system defaults', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await subscribe(db, pollId, userId)

    const r = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'response.created',
    )
    expect(r.email.map((x) => x.userId)).toEqual([userId])
  })

  it('omits an event the user turned off in their defaults', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await subscribe(db, pollId, userId)
    await db.insert(notificationPrefs).values({
      userId,
      channels: { 'response.created': { email: false, push: false } },
      createdAt: now(),
      updatedAt: now(),
    })

    const r = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'response.created',
    )
    expect(r.email).toEqual([])
  })

  it('lets a per-poll override win over the user default', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await subscribe(db, pollId, userId, { 'response.created': { email: true, push: false } })
    await db.insert(notificationPrefs).values({
      userId,
      channels: { 'response.created': { email: false, push: false } },
      createdAt: now(),
      updatedAt: now(),
    })

    const r = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'response.created',
    )
    expect(r.email.map((x) => x.userId)).toEqual([userId])
  })

  it('includes a teammate who follows the poll', async () => {
    const db = createDb(env.DB)
    const { userId: owner, orgId } = await makeOrgUser(db)
    const mate = await makeMember(db, orgId)
    const pollId = await makePoll(db, { orgId, createdBy: owner })
    await subscribe(db, pollId, owner)
    await subscribe(db, pollId, mate, null, 'follow')

    const r = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'comment.created',
    )
    expect(r.email.map((x) => x.userId).sort()).toEqual([owner, mate].sort())
  })

  it('excludes a subscriber who is no longer an org member', async () => {
    const db = createDb(env.DB)
    const { userId: owner, orgId } = await makeOrgUser(db)
    const { userId: outsider } = await makeOrgUser(db) // member of a different org
    const pollId = await makePoll(db, { orgId, createdBy: owner })
    await subscribe(db, pollId, owner)
    await subscribe(db, pollId, outsider, null, 'follow')

    const r = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'comment.created',
    )
    expect(r.email.map((x) => x.userId)).toEqual([owner])
  })

  it('suppresses the actor who caused the event', async () => {
    const db = createDb(env.DB)
    const { userId: owner, orgId } = await makeOrgUser(db)
    const mate = await makeMember(db, orgId)
    const pollId = await makePoll(db, { orgId, createdBy: owner })
    await subscribe(db, pollId, owner)
    await subscribe(db, pollId, mate, null, 'follow')

    const r = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'comment.created',
      { actorUserId: mate },
    )
    expect(r.email.map((x) => x.userId)).toEqual([owner])
  })

  it('resolves no push recipients on a free org and some on premium', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await subscribe(db, pollId, userId)

    const free = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'response.created',
    )
    expect(free.push).toEqual([])

    await makePremium(db, orgId)
    const premium = await resolveRecipients(
      db,
      { type: 'poll', id: pollId, organizationId: orgId },
      'response.created',
    )
    expect(premium.push.map((x) => x.userId)).toEqual([userId])
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun run test:workers -- src/server/notifications/__tests__/recipients.workers.test.ts`
Expected: FAIL — cannot resolve `#/server/notifications/recipients`.

- [ ] **Step 3: Write the implementation**

```ts
// src/server/notifications/recipients.ts
import { and, eq, inArray } from 'drizzle-orm'
import { resolveChannels, type NotificationEvent } from '#/lib/notifications'
import { getEntitlements } from '#/server/billing/entitlements'
import type { Db } from '#/server/db/client'
import {
  member,
  notificationPrefs,
  notificationSubscriptions,
  user,
  type ScopeType,
} from '#/server/db/schema'

export type NotificationScope = { type: ScopeType; id: string; organizationId: string }
export type Recipient = { userId: string; email: string; name: string; locale: string }
export type ResolvedRecipients = { email: Recipient[]; push: Recipient[] }

const EMPTY: ResolvedRecipients = { email: [], push: [] }

/**
 * Turns a scope + event into the two channel lists.
 *
 * Membership is the authority, not the subscription row: someone who has left the org (or lost
 * their seat) can no longer open the poll, so they must not keep hearing about it. Their row is
 * left in place so re-adding them restores their settings rather than silently resetting them.
 */
export async function resolveRecipients(
  db: Db,
  scope: NotificationScope,
  event: NotificationEvent,
  opts: { actorUserId?: string | null } = {},
): Promise<ResolvedRecipients> {
  const subscriptions = await db.query.notificationSubscriptions.findMany({
    where: and(
      eq(notificationSubscriptions.scopeType, scope.type),
      eq(notificationSubscriptions.scopeId, scope.id),
    ),
  })
  if (subscriptions.length === 0) return EMPTY

  const candidateIds = subscriptions.map((s) => s.userId).filter((id) => id !== opts.actorUserId)
  if (candidateIds.length === 0) return EMPTY

  const [members, users, prefs, entitlements] = await Promise.all([
    db.query.member.findMany({
      where: and(
        eq(member.organizationId, scope.organizationId),
        inArray(member.userId, candidateIds),
      ),
    }),
    db.query.user.findMany({ where: inArray(user.id, candidateIds) }),
    db.query.notificationPrefs.findMany({
      where: inArray(notificationPrefs.userId, candidateIds),
    }),
    getEntitlements(db, scope.organizationId),
  ])

  const memberIds = new Set(members.map((m) => m.userId))
  const usersById = new Map(users.map((u) => [u.id, u]))
  const prefsByUser = new Map(prefs.map((p) => [p.userId, p.channels]))

  const out: ResolvedRecipients = { email: [], push: [] }

  for (const subscription of subscriptions) {
    if (subscription.userId === opts.actorUserId) continue
    if (!memberIds.has(subscription.userId)) continue

    const u = usersById.get(subscription.userId)
    if (!u?.email) continue

    const channels = resolveChannels(
      event,
      subscription.channels,
      prefsByUser.get(subscription.userId) ?? null,
    )
    const recipient: Recipient = {
      userId: u.id,
      email: u.email,
      name: u.name,
      locale: u.locale ?? 'en',
    }

    if (channels.email) out.email.push(recipient)
    // Push is Premium-only. Gated here rather than at send time so a lapsed subscription stops
    // pushing everywhere at once; the device rows are left alone so re-upgrading needs no new
    // browser permission prompt.
    if (channels.push && entitlements.push) out.push.push(recipient)
  }

  return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun run test:workers -- src/server/notifications/__tests__/recipients.workers.test.ts`
Expected: PASS, 7 tests.

- [ ] **Step 5: Commit**

```bash
git add src/server/notifications/recipients.ts src/server/notifications/__tests__/recipients.workers.test.ts
git commit -m "feat(notifications): resolve recipients from membership, prefs and entitlements"
```

---

### Task 4: Subscription lifecycle helpers

**Files:**

- Create: `src/server/notifications/subscriptions.ts`
- Test: `src/server/notifications/__tests__/subscriptions.workers.test.ts`
- Modify: `src/server/polls/service.ts` (create the creator row on poll create and duplicate; delete rows on poll delete)
- Modify: `src/server/bookings/pages.ts` (same for booking pages)

**Interfaces:**

- Consumes: tables from Task 2; `NotificationGrid` from Task 1.
- Produces:
  - `ensureCreatorSubscription(db, scope: {type, id}, userId | null): Promise<void>`
  - `followScope(db, scope, userId): Promise<void>`
  - `unfollowScope(db, scope, userId): Promise<void>`
  - `setScopeChannels(db, scope, userId, channels: NotificationGrid | null): Promise<void>`
  - `deleteScopeSubscriptions(db, scope): Promise<void>`

- [ ] **Step 1: Write the failing test**

```ts
// src/server/notifications/__tests__/subscriptions.workers.test.ts
import { env } from 'cloudflare:test'
import { and, eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { notificationSubscriptions } from '#/server/db/schema'
import {
  deleteScopeSubscriptions,
  ensureCreatorSubscription,
  followScope,
  setScopeChannels,
  unfollowScope,
} from '#/server/notifications/subscriptions'
import { makeOrgUser, makeMember, makePoll } from '#/server/polls/__tests__/factories'

describe('subscription lifecycle', () => {
  it('creates the creator row once and is idempotent', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    const scope = { type: 'poll' as const, id: pollId }

    await ensureCreatorSubscription(db, scope, userId)
    await ensureCreatorSubscription(db, scope, userId)

    const rows = await db.query.notificationSubscriptions.findMany({
      where: eq(notificationSubscriptions.scopeId, pollId),
    })
    expect(rows).toHaveLength(1)
    expect(rows[0]!.source).toBe('creator')
    expect(rows[0]!.channels).toBeNull()
  })

  it('does nothing when the creator is null', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await ensureCreatorSubscription(db, { type: 'poll', id: pollId }, null)
    const rows = await db.query.notificationSubscriptions.findMany({
      where: eq(notificationSubscriptions.scopeId, pollId),
    })
    expect(rows).toHaveLength(0)
  })

  it('follows and unfollows a teammate without touching the creator row', async () => {
    const db = createDb(env.DB)
    const { userId: owner, orgId } = await makeOrgUser(db)
    const mate = await makeMember(db, orgId)
    const pollId = await makePoll(db, { orgId, createdBy: owner })
    const scope = { type: 'poll' as const, id: pollId }

    await ensureCreatorSubscription(db, scope, owner)
    await followScope(db, scope, mate)
    expect(
      await db.query.notificationSubscriptions.findMany({
        where: eq(notificationSubscriptions.scopeId, pollId),
      }),
    ).toHaveLength(2)

    await unfollowScope(db, scope, mate)
    const rows = await db.query.notificationSubscriptions.findMany({
      where: eq(notificationSubscriptions.scopeId, pollId),
    })
    expect(rows.map((r) => r.userId)).toEqual([owner])
  })

  it('writes and clears a per-scope override', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    const scope = { type: 'poll' as const, id: pollId }
    await ensureCreatorSubscription(db, scope, userId)

    await setScopeChannels(db, scope, userId, { 'comment.created': { email: false, push: false } })
    let row = await db.query.notificationSubscriptions.findFirst({
      where: and(
        eq(notificationSubscriptions.scopeId, pollId),
        eq(notificationSubscriptions.userId, userId),
      ),
    })
    expect(row?.channels?.['comment.created']).toEqual({ email: false, push: false })

    await setScopeChannels(db, scope, userId, null)
    row = await db.query.notificationSubscriptions.findFirst({
      where: and(
        eq(notificationSubscriptions.scopeId, pollId),
        eq(notificationSubscriptions.userId, userId),
      ),
    })
    expect(row?.channels).toBeNull()
  })

  it('deletes every subscription for a scope', async () => {
    const db = createDb(env.DB)
    const { userId: owner, orgId } = await makeOrgUser(db)
    const mate = await makeMember(db, orgId)
    const pollId = await makePoll(db, { orgId, createdBy: owner })
    const scope = { type: 'poll' as const, id: pollId }

    await ensureCreatorSubscription(db, scope, owner)
    await followScope(db, scope, mate)
    await deleteScopeSubscriptions(db, scope)

    expect(
      await db.query.notificationSubscriptions.findMany({
        where: eq(notificationSubscriptions.scopeId, pollId),
      }),
    ).toHaveLength(0)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `bun run test:workers -- src/server/notifications/__tests__/subscriptions.workers.test.ts`
Expected: FAIL — cannot resolve `#/server/notifications/subscriptions`.

- [ ] **Step 3: Write the implementation**

```ts
// src/server/notifications/subscriptions.ts
import { and, eq } from 'drizzle-orm'
import type { NotificationGrid } from '#/lib/notifications'
import type { Db } from '#/server/db/client'
import { notificationSubscriptions, type ScopeType } from '#/server/db/schema'

export type SubscriptionScope = { type: ScopeType; id: string }

const scopeWhere = (scope: SubscriptionScope) =>
  and(
    eq(notificationSubscriptions.scopeType, scope.type),
    eq(notificationSubscriptions.scopeId, scope.id),
  )

const rowWhere = (scope: SubscriptionScope, userId: string) =>
  and(scopeWhere(scope), eq(notificationSubscriptions.userId, userId))

/**
 * Called on poll/page creation. `userId` is nullable because `createdBy` is — a poll whose creator
 * later deletes their account has nobody to subscribe, and that is not an error.
 */
export async function ensureCreatorSubscription(
  db: Db,
  scope: SubscriptionScope,
  userId: string | null,
): Promise<void> {
  if (!userId) return
  await upsert(db, scope, userId, 'creator')
}

export async function followScope(db: Db, scope: SubscriptionScope, userId: string): Promise<void> {
  await upsert(db, scope, userId, 'follow')
}

export async function unfollowScope(
  db: Db,
  scope: SubscriptionScope,
  userId: string,
): Promise<void> {
  await db.delete(notificationSubscriptions).where(rowWhere(scope, userId))
}

/** `null` clears the override so the row falls back to the user's defaults again. */
export async function setScopeChannels(
  db: Db,
  scope: SubscriptionScope,
  userId: string,
  channels: NotificationGrid | null,
): Promise<void> {
  await db
    .update(notificationSubscriptions)
    .set({ channels, updatedAt: new Date().toISOString() })
    .where(rowWhere(scope, userId))
}

/**
 * The manual replacement for the foreign-key cascade a polymorphic `scopeId` cannot have. Call
 * from every path that removes a poll or a booking page.
 */
export async function deleteScopeSubscriptions(db: Db, scope: SubscriptionScope): Promise<void> {
  await db.delete(notificationSubscriptions).where(scopeWhere(scope))
}

async function upsert(
  db: Db,
  scope: SubscriptionScope,
  userId: string,
  source: 'creator' | 'follow',
): Promise<void> {
  const now = new Date().toISOString()
  await db
    .insert(notificationSubscriptions)
    .values({
      scopeType: scope.type,
      scopeId: scope.id,
      userId,
      source,
      channels: null,
      createdAt: now,
      updatedAt: now,
    })
    // Re-following must not reset an override the user already tuned, so the conflict is a no-op
    // rather than an overwrite.
    .onConflictDoNothing()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `bun run test:workers -- src/server/notifications/__tests__/subscriptions.workers.test.ts`
Expected: PASS, 5 tests.

- [ ] **Step 5: Wire into the poll and booking-page lifecycles**

In `src/server/polls/service.ts`, after the poll row is inserted in `createPoll` (and in the duplicate path around line 455), call:

```ts
await ensureCreatorSubscription(db, { type: 'poll', id: poll.id }, poll.createdBy)
```

In the poll delete path, call `deleteScopeSubscriptions(db, { type: 'poll', id: pollId })`. Do the same in `src/server/bookings/pages.ts` for `booking_page` scopes, using `memberUserId ?? createdBy`.

- [ ] **Step 6: Run the poll service tests**

Run: `bun run test:workers -- src/server/polls/__tests__/service.workers.test.ts`
Expected: PASS. The `notifications` assertions at lines 114, 175 and 766 need updating to the new viewmodel shape — that happens in Task 8; if they still reference `notifyOnVote`, leave them failing and note it.

- [ ] **Step 7: Commit**

```bash
git add src/server/notifications/subscriptions.ts src/server/notifications/__tests__/subscriptions.workers.test.ts src/server/polls/service.ts src/server/bookings/pages.ts
git commit -m "feat(notifications): subscription lifecycle tied to poll and page creation"
```

---

### Task 5: Emit boundary

**Files:**

- Create: `src/server/notifications/emit.ts`
- Test: `src/server/notifications/__tests__/emit.workers.test.ts`
- Modify: `src/do/protocol.ts`

**Interfaces:**

- Consumes: `isDigestEvent`, event types (Task 1); `resolveRecipients` (Task 3); `queueDigest` (`src/server/notifications/do-client.ts`).
- Produces:
  - `DigestItem` (revised) in `src/do/protocol.ts`
  - `emitPollEvent(pollId, event: PollNotificationEvent, ctx: EmitContext): Promise<void>`
  - `emitBookingEvent(pageId, event: BookingNotificationEvent, ctx: BookingEmitContext): Promise<void>`
  - `type EmitContext = { actorUserId?: string | null; actorName?: string }`

- [ ] **Step 1: Change the wire type**

```ts
// src/do/protocol.ts — replace the DigestItem line
import type { DigestEvent } from '#/lib/notifications'

export type DigestItem = {
  event: DigestEvent
  name: string
  at: string
  actorUserId: string | null
}
```

`#/lib/notifications` imports only `zod`, so `protocol.ts` stays browser-safe.

- [ ] **Step 2: Write the failing test**

```ts
// src/server/notifications/__tests__/emit.workers.test.ts
import { env } from 'cloudflare:test'
import { eq } from 'drizzle-orm'
import { describe, expect, it } from 'vitest'
import type { DigestItem } from '#/do/protocol'
import { createDb } from '#/server/db/client'
import { notificationSubscriptions } from '#/server/db/schema'
import { emitPollEvent } from '#/server/notifications/emit'
import { pollRoom } from '#/server/notifications/do-client'
import { ensureCreatorSubscription } from '#/server/notifications/subscriptions'
import { makeOrgUser, makePoll } from '#/server/polls/__tests__/factories'

describe('emitPollEvent', () => {
  it('queues an activity event onto the poll room digest', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await ensureCreatorSubscription(db, { type: 'poll', id: pollId }, userId)

    await emitPollEvent(pollId, 'response.updated', { actorName: 'Ada', actorUserId: null })

    const items = await pollRoom(pollId).peekDigestItems()
    expect(items.map((i: DigestItem) => i.event)).toEqual(['response.updated'])
    expect(items[0]!.name).toBe('Ada')
  })

  it('does not queue a lifecycle event onto the digest', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await ensureCreatorSubscription(db, { type: 'poll', id: pollId }, userId)

    await emitPollEvent(pollId, 'poll.closed', { actorUserId: null })

    const items = await pollRoom(pollId).peekDigestItems()
    expect(items).toEqual([])
  })

  it('never throws when the poll does not exist', async () => {
    await expect(
      emitPollEvent('missing-poll', 'response.created', { actorName: 'Ada', actorUserId: null }),
    ).resolves.toBeUndefined()
  })

  it('skips the digest entirely when nobody is subscribed', async () => {
    const db = createDb(env.DB)
    const { userId, orgId } = await makeOrgUser(db)
    const pollId = await makePoll(db, { orgId, createdBy: userId })
    await db.delete(notificationSubscriptions).where(eq(notificationSubscriptions.scopeId, pollId))

    await emitPollEvent(pollId, 'response.created', { actorName: 'Ada', actorUserId: null })
    expect(await pollRoom(pollId).peekDigestItems()).toEqual([])
  })
})
```

- [ ] **Step 3: Run test to verify it fails**

Run: `bun run test:workers -- src/server/notifications/__tests__/emit.workers.test.ts`
Expected: FAIL — cannot resolve `#/server/notifications/emit`; `peekDigestItems` is not a method.

- [ ] **Step 4: Add the test seam to `PollRoom`**

```ts
// src/do/PollRoom.ts — add alongside the other RPC methods
/** Test seam: lets a workers test assert what was enqueued without reaching into DO storage. */
async peekDigestItems(): Promise<DigestItem[]> {
  return (await this.ctx.storage.get<DigestItem[]>(DIGEST_ITEMS_KEY)) ?? []
}
```

- [ ] **Step 5: Write the implementation**

```ts
// src/server/notifications/emit.ts
import { eq } from 'drizzle-orm'
import {
  isDigestEvent,
  type BookingNotificationEvent,
  type PollNotificationEvent,
} from '#/lib/notifications'
import { getDb } from '#/server/db/client'
import { bookingPages, polls } from '#/server/db/schema'
import { queueDigest } from '#/server/notifications/do-client'
import { resolveRecipients } from '#/server/notifications/recipients'

export type EmitContext = { actorUserId?: string | null; actorName?: string }
export type BookingEmitContext = EmitContext & { bookingId: string }

/**
 * The single boundary the rest of the app calls. Best-effort by contract: a stalled durable
 * object or mailer must never fail the request that triggered the notification, so everything
 * here is caught and logged — same rule as `notifyChanged` and `sendClaimConfirmation`.
 *
 * Activity events go through `PollRoom`'s debounced digest; lifecycle events send immediately,
 * because a deadline reminder or a closed poll is singular and time-sensitive and waiting out a
 * ten-minute window would make it useless.
 */
export async function emitPollEvent(
  pollId: string,
  event: PollNotificationEvent,
  ctx: EmitContext = {},
): Promise<void> {
  try {
    const db = getDb()
    const poll = await db.query.polls.findFirst({ where: eq(polls.id, pollId) })
    if (!poll || poll.deletedAt) return

    const scope = { type: 'poll' as const, id: pollId, organizationId: poll.organizationId }
    const recipients = await resolveRecipients(db, scope, event, {
      actorUserId: ctx.actorUserId ?? null,
    })

    if (isDigestEvent(event)) {
      // Enqueue once for the poll, not once per recipient: preferences are re-resolved at alarm
      // time so a toggle flipped during the debounce window still takes effect. The check above
      // only avoids arming an alarm nobody will ever receive.
      if (recipients.email.length > 0) {
        await queueDigest(pollId, {
          event,
          name: ctx.actorName ?? '',
          at: new Date().toISOString(),
          actorUserId: ctx.actorUserId ?? null,
        })
      }
    } else {
      await sendImmediatePollEmails(poll, event, recipients.email)
    }

    // Push delivery lands in Phase 2; `recipients.push` is already resolved and gated here so
    // that phase adds a call, not a new resolution path.
  } catch (err) {
    console.error(`[notifications] emitPollEvent(${event}) failed`, err)
  }
}

export async function emitBookingEvent(
  pageId: string,
  event: BookingNotificationEvent,
  ctx: BookingEmitContext,
): Promise<void> {
  try {
    const db = getDb()
    const page = await db.query.bookingPages.findFirst({ where: eq(bookingPages.id, pageId) })
    if (!page || page.deletedAt) return

    const scope = { type: 'booking_page' as const, id: pageId, organizationId: page.organizationId }
    const recipients = await resolveRecipients(db, scope, event, {
      actorUserId: ctx.actorUserId ?? null,
    })
    await sendImmediateBookingEmails(page, event, ctx.bookingId, recipients.email)
  } catch (err) {
    console.error(`[notifications] emitBookingEvent(${event}) failed`, err)
  }
}
```

`sendImmediatePollEmails` and `sendImmediateBookingEmails` are written in Task 7. For now, stub them in the same file so this task's tests pass:

```ts
async function sendImmediatePollEmails(..._args: unknown[]): Promise<void> {}
async function sendImmediateBookingEmails(..._args: unknown[]): Promise<void> {}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `bun run test:workers -- src/server/notifications/__tests__/emit.workers.test.ts`
Expected: PASS, 4 tests.

- [ ] **Step 7: Commit**

```bash
git add src/server/notifications/emit.ts src/server/notifications/__tests__/emit.workers.test.ts src/do/protocol.ts src/do/PollRoom.ts
git commit -m "feat(notifications): emit boundary routing activity to digest, lifecycle to immediate"
```

---

### Task 6: PollRoom per-recipient digest fan-out

**Files:**

- Modify: `src/do/PollRoom.ts` (`#processDigest`)
- Modify: `emails/Digest.tsx`, `src/server/mailer/templates.tsx`
- Modify: `messages/en.json`, `messages/nb.json`
- Test: `src/do/__tests__/poll-room.workers.test.ts`, `emails/__tests__/templates.test.tsx`

**Interfaces:**

- Consumes: `DigestItem` (Task 5), `resolveRecipients` (Task 3).
- Produces: `renderDigest(p: { pollTitle; pollUrl; lines: DigestLine[]; locale }): Promise<Rendered>` where `type DigestLine = { event: DigestEvent; names: string[]; count: number }`.

- [ ] **Step 1: Write the failing digest-template test**

```tsx
// emails/__tests__/templates.test.tsx — add to the renderDigest describe
it('lists each event kind separately', async () => {
  const { subject, html } = await renderDigest({
    pollTitle: 'Team lunch',
    pollUrl: 'https://x/p/1',
    lines: [
      { event: 'response.created', names: ['Ada', 'Bob'], count: 2 },
      { event: 'response.updated', names: ['Cleo'], count: 1 },
      { event: 'comment.created', names: [], count: 3 },
    ],
    locale: 'en',
  })
  expect(subject).toContain('Team lunch')
  expect(html).toContain('Ada')
  expect(html).toContain('Cleo')
  expect(html).toMatch(/changed/i)
})
```

- [ ] **Step 2: Run it and watch it fail**

Run: `bun run test:unit -- emails/__tests__/templates.test.tsx`
Expected: FAIL — `renderDigest` does not accept `lines`.

- [ ] **Step 3: Add the message strings**

Add to `messages/en.json`:

```json
"email_digest_line_response_created": "{count, plural, one {# new response} other {# new responses}}",
"email_digest_line_response_updated": "{count, plural, one {# changed response} other {# changed responses}}",
"email_digest_line_response_withdrawn": "{count, plural, one {# withdrawn response} other {# withdrawn responses}}",
"email_digest_line_comment_created": "{count, plural, one {# new comment} other {# new comments}}",
"email_digest_line_signup_full": "All slots are now claimed"
```

And the Norwegian equivalents in `messages/nb.json`:

```json
"email_digest_line_response_created": "{count, plural, one {# nytt svar} other {# nye svar}}",
"email_digest_line_response_updated": "{count, plural, one {# endret svar} other {# endrede svar}}",
"email_digest_line_response_withdrawn": "{count, plural, one {# trukket svar} other {# trukne svar}}",
"email_digest_line_comment_created": "{count, plural, one {# ny kommentar} other {# nye kommentarer}}",
"email_digest_line_signup_full": "Alle plasser er nå tatt"
```

Then run `bun run postinstall` to recompile Paraglide.

- [ ] **Step 4: Rewrite the template and its render helper**

`emails/Digest.tsx` takes `lines: DigestLine[]` and renders one `<Text>` per line: the localised count string, followed by `names.join(', ')` when `names` is non-empty. Keep the existing `Layout`, heading, button and URL markup exactly as it is — only the middle block changes. Export `type DigestLine = { event: DigestEvent; names: string[]; count: number }` from `emails/Digest.tsx` and re-export it from `src/server/mailer/templates.tsx`.

- [ ] **Step 5: Run the template test**

Run: `bun run test:unit -- emails/__tests__/templates.test.tsx`
Expected: PASS.

- [ ] **Step 6: Write the failing fan-out test**

```ts
// src/do/__tests__/poll-room.workers.test.ts — add a describe
describe('digest fan-out', () => {
  it('sends one digest per subscribed recipient with their own grid applied', async () => {
    const db = createDb(env.DB)
    const { userId: owner, orgId } = await makeOrgUser(db)
    const mate = await makeMember(db, orgId)
    const pollId = await makePoll(db, { orgId, createdBy: owner })
    await ensureCreatorSubscription(db, { type: 'poll', id: pollId }, owner)
    await followScope(db, { type: 'poll', id: pollId }, mate)
    // The teammate wants responses but not comments.
    await setScopeChannels(db, { type: 'poll', id: pollId }, mate, {
      'comment.created': { email: false, push: false },
    })

    const stub = env.POLL_ROOM.getByName(pollId)
    await stub.enqueueDigest(pollId, {
      event: 'response.created',
      name: 'Ada',
      at: new Date().toISOString(),
      actorUserId: null,
    })
    await stub.enqueueDigest(pollId, {
      event: 'comment.created',
      name: 'Bob',
      at: new Date().toISOString(),
      actorUserId: null,
    })

    const sent: { to: string; html: string }[] = []
    await runInDurableObject(stub, async (instance: PollRoom) => {
      instance.mailer = async (_env, msg) => {
        sent.push({ to: msg.to, html: msg.html })
        return true
      }
      await instance.alarm()
    })

    expect(sent).toHaveLength(2)
    const mateMail = sent.find((s) => s.to.includes('mate'))!
    expect(mateMail.html).toContain('Ada')
    expect(mateMail.html).not.toContain('Bob')
  })

  it('applies a preference flipped during the debounce window', async () => {
    // enqueue, then turn the event off, then fire the alarm → no mail for that recipient
  })
})
```

Fill in the second test body following the same shape: enqueue a `response.created` item, call `setScopeChannels(..., { 'response.created': { email: false, push: false } })`, run the alarm, assert `sent` is empty.

- [ ] **Step 7: Run it and watch it fail**

Run: `bun run test:workers -- src/do/__tests__/poll-room.workers.test.ts`
Expected: FAIL — the digest still resolves a single owner.

- [ ] **Step 8: Rewrite `#processDigest`**

Replace the body so it:

1. Loads the items and the poll (with `organizationId`), bailing to `#clearDigest()` when the poll is missing or soft-deleted.
2. Builds the set of distinct events present in `items`.
3. For each event, calls `resolveRecipients(db, scope, event)` — **no `actorUserId`**, because each item carries its own actor; filter per item instead.
4. Inverts that into a per-recipient list of items: a recipient receives an item when they are in that event's email list and are not that item's `actorUserId`.
5. For each recipient with a non-empty list, groups items into `DigestLine[]` (names deduped, `count` = item count) and sends one mail.
6. Applies the existing retry ladder to the batch as a whole: if **any** send returns false, increment `retry:count` and re-arm as today; only clear when all succeeded or `MAX_RETRIES` is reached.

Keep `DIGEST_DELAY_MS`, `RETRY_DELAY_MS`, `MAX_RETRIES`, `#clearDigest` and `#rearm` exactly as they are.

- [ ] **Step 9: Run the tests**

Run: `bun run test:workers -- src/do/__tests__/poll-room.workers.test.ts`
Expected: PASS, including the pre-existing digest tests once their `enqueueDigest` calls are updated to the new `DigestItem` shape.

- [ ] **Step 10: Commit**

```bash
git add src/do/PollRoom.ts emails/Digest.tsx src/server/mailer/templates.tsx messages/ src/paraglide/ src/do/__tests__/ emails/__tests__/
git commit -m "feat(notifications): fan the poll digest out to every subscriber"
```

---

### Task 7: Immediate lifecycle and booking sends

**Files:**

- Create: `emails/Notification.tsx`
- Modify: `src/server/notifications/emit.ts` (replace the Task 5 stubs)
- Modify: `src/server/mailer/templates.tsx`, `messages/en.json`, `messages/nb.json`
- Modify: `src/do/BookingRoom.ts`, `src/server/bookings/emails.ts`
- Test: `emails/__tests__/templates.test.tsx`, `src/server/notifications/__tests__/emit.workers.test.ts`

**Interfaces:**

- Consumes: `resolveRecipients` (Task 3), `sendMail` (`src/server/mailer/mailer.ts`).
- Produces: `renderNotification(p: { event: NotificationEvent; title: string; url: string; detail?: string; locale: string }): Promise<Rendered>`.

- [ ] **Step 1: Write the failing template test**

```tsx
it('renders a lifecycle notification per event', async () => {
  const { subject, html } = await renderNotification({
    event: 'deadline.approaching',
    title: 'Team lunch',
    url: 'https://x/p/1',
    locale: 'en',
  })
  expect(subject).toContain('Team lunch')
  expect(html).toMatch(/deadline/i)
})
```

- [ ] **Step 2: Run it, watch it fail**

Run: `bun run test:unit -- emails/__tests__/templates.test.tsx`
Expected: FAIL — `renderNotification` is not exported.

- [ ] **Step 3: Add strings, template and render helper**

Add `email_notification_subject_<event>` and `email_notification_body_<event>` pairs to both message files for `deadline.approaching`, `poll.closed`, `poll.finalized`, `booking.created`, `booking.cancelled`, `booking.rescheduled`. `emails/Notification.tsx` mirrors `emails/Closed.tsx`'s structure — `Layout`, heading, body text, CTA button, raw URL. `renderNotification` picks the subject/body message by event with an exhaustive `switch` so a new event is a type error rather than a silent blank email.

- [ ] **Step 4: Replace the emit stubs**

```ts
async function sendImmediatePollEmails(
  poll: { id: string; title: string },
  event: PollNotificationEvent,
  recipients: Recipient[],
): Promise<void> {
  const url = `${env.APP_URL}/p/${poll.id}`
  await Promise.allSettled(
    recipients.map(async (r) => {
      const rendered = await renderNotification({
        event,
        title: poll.title,
        url,
        locale: r.locale,
      })
      await sendMail(env, { to: r.email, ...rendered })
    }),
  )
}
```

`sendImmediateBookingEmails` is the same shape, loading the booking for its start time to pass as `detail` and linking to the page. `Promise.allSettled` so one bad address cannot stop the rest — matching `sendFinalizedEmails`.

- [ ] **Step 5: Route the existing organiser notices through emit**

In `src/do/BookingRoom.ts`, the `book` / `cancel` / `reschedule` methods currently call `sendBookingEmails` for both visitor and organiser. Split that: keep the **visitor** mail exactly as it is (transactional, never gated), and replace the **organiser** half with `emitBookingEvent(pageId, 'booking.created' | 'booking.cancelled' | 'booking.rescheduled', { bookingId, actorUserId: null })`. Add a `SendBookingEmailsKind` variant or an options flag so `sendBookingEmails` can send visitor-only.

In `src/do/PollRoom.ts#processDeadline`, replace the direct `renderClosed` + `this.mailer` call with `emitPollEvent(pollId, 'poll.closed', { actorUserId: null })`. In `src/server/polls/polls.functions.ts`'s `finalizePoll`, add `emitPollEvent(pollId, 'poll.finalized', { actorUserId })` **after** the existing participant `sendFinalizedEmails` call, which stays untouched.

- [ ] **Step 6: Add the emit routing test**

```ts
it('sends a lifecycle event immediately to every subscriber', async () => {
  // seed org + poll + creator subscription, stub the mailer via the module seam,
  // call emitPollEvent(pollId, 'poll.closed'), assert one mail to the creator
})
```

- [ ] **Step 7: Run the suites**

Run: `bun run test:workers -- src/server/notifications/ src/do/` and `bun run test:unit -- emails/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add emails/Notification.tsx src/server/notifications/emit.ts src/server/mailer/templates.tsx messages/ src/paraglide/ src/do/ src/server/bookings/ src/server/polls/polls.functions.ts emails/__tests__/
git commit -m "feat(notifications): immediate lifecycle and booking notifications"
```

---

### Task 8: Wire the poll server functions

**Files:**

- Modify: `src/server/polls/participants.functions.ts`
- Modify: `src/do/PollRoom.ts` (`claim` → `signup.full`; `syncDeadline` → `remind:at`)
- Modify: `src/server/polls/viewmodel.ts`, `src/server/polls/schemas.ts`
- Test: `src/server/polls/__tests__/participants.workers.test.ts`, `src/do/__tests__/poll-room.workers.test.ts`

**Interfaces:**

- Consumes: `emitPollEvent` (Task 5).
- Produces: `PollView.notifications` becomes `{ channels: NotificationGrid | null; following: boolean } | null`.

- [ ] **Step 1: Write the failing tests**

One test per call site, asserting the digest queue after the server function runs:

```ts
it('queues response.updated when a participant edits their answers', async () => {
  // seed poll + participant + creator subscription
  await updateParticipant({ data: { pollId, participantId, editToken, answers: [...] } })
  const items = await pollRoom(pollId).peekDigestItems()
  expect(items.map((i) => i.event)).toEqual(['response.updated'])
})

it('queues response.withdrawn when a participant is removed', async () => { /* ... */ })
it('queues signup.full when the last slot is claimed', async () => { /* ... */ })
```

- [ ] **Step 2: Run them, watch them fail**

Run: `bun run test:workers -- src/server/polls/__tests__/participants.workers.test.ts`
Expected: FAIL — no items queued for update/withdraw.

- [ ] **Step 3: Replace every `queueDigest` call with `emitPollEvent`**

| Location                             | Call                                                                                                                    |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------- |
| `addParticipant` (line ~113)         | `emitPollEvent(data.pollId, 'response.created', { actorName: data.name, actorUserId: userId })`                         |
| `updateParticipant` (after line 135) | `emitPollEvent(data.pollId, 'response.updated', { actorName: data.name ?? participantName, actorUserId: userId })`      |
| `removeParticipant` (after line 153) | `emitPollEvent(data.pollId, 'response.withdrawn', { actorName, actorUserId: userId })`                                  |
| `addComment` (line ~180)             | `emitPollEvent(data.pollId, 'comment.created', { actorName: authorName, actorUserId: userId })`                         |
| `claimSlot` (line ~234)              | `emitPollEvent(data.pollId, 'response.created', { actorName: claimant?.name ?? data.name ?? '', actorUserId: userId })` |
| unclaim (line ~273)                  | `emitPollEvent(data.pollId, 'response.withdrawn', { actorName, actorUserId: userId })`                                  |

`updateParticipant` and `removeParticipant` need the participant's name — load it before the mutation, since after a removal it is gone.

- [ ] **Step 4: Add `signup.full` inside `PollRoom#claim`**

After `applyClaim` returns `changed: true`, check whether the poll's remaining capacity across all options is now zero; if so, `emitPollEvent(pollId, 'signup.full', { actorUserId: null })`. Do this inside the existing `#serialize` block so the capacity read is still atomic.

- [ ] **Step 5: Add the `remind:at` alarm**

```ts
const REMIND_AT_KEY = 'remind:at'
export const REMINDER_LEAD_MS = 24 * 60 * 60_000
```

In `syncDeadline`: when `deadlineAt` is null, delete the key; otherwise compute `new Date(deadlineAt).getTime() - REMINDER_LEAD_MS` and store it only when it is in the future. Add it to `#rearm`'s candidate list. In `alarm()`, add a third `try`/`catch` branch mirroring the existing two: if `remind:at <= now`, `emitPollEvent(pollId, 'deadline.approaching', { actorUserId: null })` then delete the key.

- [ ] **Step 6: Update the viewmodel and schema**

`src/server/polls/viewmodel.ts:49` becomes:

```ts
notifications: { channels: NotificationGrid | null; following: boolean } | null
```

populated from the viewer's own subscription row (null when the viewer is not an org member). `src/server/polls/schemas.ts:202-203` replaces the two booleans with `channels: gridSchema.nullable()`. Update the three `service.ts` sites and the assertions at `service.workers.test.ts:114`, `:175`, `:766`.

- [ ] **Step 7: Run the full workers suite**

Run: `bun run test:workers`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add src/server/polls/ src/do/
git commit -m "feat(notifications): emit on every poll mutation, add deadline reminder alarm"
```

---

### Task 9: Preference server functions

**Files:**

- Create: `src/server/notifications/prefs.functions.ts`
- Test: `src/server/notifications/__tests__/prefs.functions.workers.test.ts`

**Interfaces:**

- Consumes: `gridSchema` (Task 1); subscription helpers (Task 4).
- Produces: `getNotificationPrefs`, `setNotificationPrefs`, `setPollNotificationPrefs`, `setPollFollowing` — all TanStack `createServerFn`, following the `SERVER_FN_MIDDLEWARE` + `.validator()` pattern used in `participants.functions.ts`.

- [ ] **Step 1: Write the failing test**

Cover: reading returns `SYSTEM_DEFAULTS` when no row exists; writing then reading round-trips; an unauthenticated call is rejected; a non-member cannot set prefs on another org's poll; `setPollFollowing(false)` deletes the row.

- [ ] **Step 2: Run it, watch it fail**

Run: `bun run test:workers -- src/server/notifications/__tests__/prefs.functions.workers.test.ts`

- [ ] **Step 3: Implement the four server functions**

Each requires a session (reuse `sessionMiddleware`); each validates its grid through `gridSchema`; `setPollNotificationPrefs` and `setPollFollowing` first assert the caller is a member of the poll's org, throwing `AppError('FORBIDDEN')` otherwise — reuse whatever membership check `requireIsOwner` already builds on rather than writing a new one.

- [ ] **Step 4: Run, verify pass, commit**

```bash
git add src/server/notifications/prefs.functions.ts src/server/notifications/__tests__/prefs.functions.workers.test.ts
git commit -m "feat(notifications): server functions for reading and writing preferences"
```

---

### Task 10: The preference grid UI

**Files:**

- Create: `src/components/notifications/NotificationGrid.tsx`
- Create: `src/components/notifications/__tests__/NotificationGrid.test.tsx`
- Create: `src/routes/settings/notifications.tsx`
- Modify: `src/components/poll/AdminBar.tsx`
- Modify: `messages/en.json`, `messages/nb.json`

**Interfaces:**

- Consumes: `NOTIFICATION_EVENTS`, `SYSTEM_DEFAULTS`, `resolveChannels` (Task 1); the server functions (Task 9); `entitlements.push` from the client session.

- [ ] **Step 1: Write the failing component test**

```tsx
it('renders a row per event and reflects the resolved value', () => {
  render(
    <NotificationGrid
      events={POLL_NOTIFICATION_EVENTS}
      value={{ 'response.created': { email: false, push: false } }}
      defaults={null}
      pushAvailable
      onChange={() => {}}
    />,
  )
  expect(screen.getAllByRole('checkbox')).toHaveLength(POLL_NOTIFICATION_EVENTS.length * 2)
  expect(screen.getByLabelText(/new response.*email/i)).not.toBeChecked()
})

it('disables the push column and shows an upgrade hint when push is unavailable', () => {
  render(
    <NotificationGrid
      events={POLL_NOTIFICATION_EVENTS}
      value={null}
      defaults={null}
      pushAvailable={false}
      onChange={() => {}}
    />,
  )
  for (const box of screen.getAllByLabelText(/push/i)) expect(box).toBeDisabled()
  expect(screen.getByText(/premium/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run, watch fail, then implement**

The component takes `events`, `value: NotificationGrid | null`, `defaults: NotificationGrid | null`, `pushAvailable: boolean`, `onChange(next: NotificationGrid)`. It groups rows under "Responses" / "Comments" / "Poll lifecycle" / "Bookings" headings, renders two checkboxes per row using the existing shadcn primitives already in `src/components/ui`, and resolves each checkbox's state through `resolveChannels`. Every label is a Paraglide message.

- [ ] **Step 3: Replace the AdminBar switches**

Delete the `notifyOnVote` / `notifyOnComment` switches at `AdminBar.tsx:200-220` and the `prefs` state at `:80`. Render `<NotificationGrid events={POLL_NOTIFICATION_EVENTS} .../>` fed by `poll.notifications`, saving through `setPollNotificationPrefs`. Add a "reset to my defaults" button that calls it with `null`, and a follow/unfollow toggle for non-creator members bound to `setPollFollowing`.

- [ ] **Step 4: Build the settings route**

`src/routes/settings/notifications.tsx` loads the user's defaults, renders `<NotificationGrid events={NOTIFICATION_EVENTS} ... />`, and saves through `setNotificationPrefs`. Follow whatever layout the existing settings routes use.

- [ ] **Step 5: Run unit tests and the type check**

Run: `bun run test:unit && bun run typecheck`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add src/components/notifications/ src/routes/settings/ src/components/poll/AdminBar.tsx messages/ src/paraglide/
git commit -m "feat(notifications): preference grid in settings and on the poll admin bar"
```

---

### Task 11: End-to-end coverage and full verification

**Files:**

- Create: `e2e/notifications.spec.ts`
- Modify: `README.md`

- [ ] **Step 1: Write the E2E spec**

Sign in, create a poll, open the admin bar, uncheck "new comment / email", reload, assert it stayed unchecked. Then hit "reset to my defaults" and assert it returns to the default state. Follow the existing specs in `e2e/` for the sign-in helper and selectors.

- [ ] **Step 2: Run it**

Run: `bun run test:e2e -- notifications.spec.ts`
Expected: PASS.

- [ ] **Step 3: Update the README**

The architecture section (around line 162–177) describes `enqueueDigest` and the owner-only digest. Rewrite that paragraph for the new model: eleven events, subscriptions, per-recipient fan-out, immediate lifecycle sends. Add the notification tables to whatever schema summary the README carries.

- [ ] **Step 4: Run the full gate**

Run: `bun run typecheck && bun run lint && bun run format:check && bun run test && bun run build`
Expected: all pass.

- [ ] **Step 5: Commit and open the PR**

```bash
git add e2e/notifications.spec.ts README.md
git commit -m "test(notifications): e2e coverage for the preference grid"
```

---

## Self-Review

**Spec coverage:**

| Spec section                                                                | Task           |
| --------------------------------------------------------------------------- | -------------- |
| §1 catalogue                                                                | 1              |
| §1 grid encoding, `SYSTEM_DEFAULTS`                                         | 1              |
| §1 three tables, polymorphic scope                                          | 2              |
| §1 migration + backfill, column drop                                        | 2              |
| §2 membership authority, prefs merge, entitlement gate, actor suppression   | 3              |
| §2 subscription creation/deletion                                           | 4              |
| §3 emit boundary, digest vs immediate split                                 | 5, 7           |
| §3 `DigestItem` change, per-recipient fan-out, prefs resolved at alarm time | 6              |
| §3 `remind:at` alarm, `signup.full`                                         | 8              |
| §3 booking events through emit, visitor mail untouched                      | 7              |
| §5 grid UI, AdminBar, settings page, follow control                         | 9, 10          |
| §5 Paraglide en/nb                                                          | 6, 7, 10       |
| §6 unit / workers / e2e coverage                                            | throughout, 11 |

§4 (web push) is deliberately absent — Phase 2. Task 5 resolves `recipients.push` and leaves it unused so Phase 2 adds a delivery call rather than a second resolution path.

**Placeholder scan:** Tasks 6 (step 6, second test), 9 (step 3) and 10 (steps 2–4) describe behaviour and shape rather than giving complete code. These are the UI and server-function tasks where the surrounding conventions (shadcn primitives, `SERVER_FN_MIDDLEWARE` composition, the settings route layout) must be read from neighbouring files anyway; each names the exact file to copy the pattern from. Every core-logic task carries full code.

**Type consistency:** `PollNotificationEvent` (not `PollEvent` — that name belongs to the WebSocket protocol) is used consistently in Tasks 1, 5, 7, 8. `NotificationGrid` is the grid type everywhere; `ChannelPrefs` is the per-event value. `DigestItem` gains `event`/`actorUserId` in Task 5 and every later reference uses that shape. `resolveRecipients` returns `{ email, push }` in Task 3 and is consumed with that shape in Tasks 5 and 7. `Recipient` is defined once in Task 3 and imported by Task 7.

**Known follow-ups for the review:**

- The `ensureCreatorSubscription` call sites in Task 4 step 5 assume `createPoll` and the duplicate path are the only two creation routes — verify against `service.ts` before implementing.
- Task 6's retry semantics re-send the whole batch to **every** recipient when any single send fails, which can duplicate mail for the recipients that succeeded. Today's single-recipient ladder has no such failure mode. Worth deciding at review whether per-recipient retry state is warranted.

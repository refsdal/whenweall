# whenweall v4 Phase 2 — Billing & Entitlements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Organizations can subscribe to Premium ($7/mo flat, ≤10 seats) via Stripe; a single `entitlements` module answers "what can this org do"; Premium orgs can invite members (with a working accept flow); Free orgs see upgrade CTAs.

**Architecture:** `@better-auth/stripe` plugin with the organization as `referenceId` (only org owners pass `authorizeReference`); webhooks keep a `subscription` row in D1 so request-path checks never call Stripe; `src/server/billing/entitlements.ts` is the sole source of plan rules; the Phase 1 invitation hard-block becomes an entitlements-driven seat gate, and `/accept-invitation/$id` completes the loop. Google-sync and branding gates read entitlements in Phase 3.

**Tech Stack:** stripe (Node SDK with `createFetchHttpClient` for Workers), @better-auth/stripe, msw + @msw/cloudflare for Stripe HTTP mocking, existing stack.

**Spec:** `docs/superpowers/specs/2026-08-25-whenweall-v4-tenancy-design.md` (§3 Billing, §4 Entitlements binding; §5 billing UI slice)

## Global Constraints

- Bun/bunx only; `#/` imports; `import * as z from 'zod'`; i18n keys in BOTH `messages/en.json` and `messages/nb.json`.
- Workers tests `*.workers.test.ts`; TDD with RED/GREEN evidence for new behavior; full gate `bun run typecheck && bun run lint && bun run format:check && bun run test` green at each task's end (suite ~2.5 min, foreground, 400s timeouts).
- **No plan rule outside `entitlements.ts`** — every gate reads it (spec §4). No Stripe call on any request path except checkout/portal creation; state reads come from D1.
- No real Stripe network calls in tests: msw intercepts `https://api.stripe.com`. Secrets are dummies in `.dev.vars.example` / test env.
- Env contract (spec §3): secrets `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` (in `.dev.vars`); vars `STRIPE_PRICE_PREMIUM_MONTHLY`, `STRIPE_PRICE_PREMIUM_YEARLY` (in `wrangler.jsonc` + test config).
- Phase 3 owns: google-sync gate, branding gate, org switcher, full org-settings page, invite UI. Do not build them.
- Controller rulings (binding): Stripe customers are created lazily at first upgrade (`createCustomerOnSignUp: false` — no Stripe dependency on signup); plan names are `'premium'` (paid) with Free implicit (no subscription row = free); drizzle migration APPENDS (no squash this phase); commits end with "Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>".

---

### Task 1: Stripe plugin wiring + subscription schema

**Files:**
- Create: `src/server/billing/stripe.ts`
- Modify: `src/server/auth/auth.ts`, `src/server/auth/auth.cli.ts`, `src/server/auth/client.ts`
- Modify: `.dev.vars.example`, `wrangler.jsonc`, `test/wrangler.test.jsonc`
- Regenerate: `src/server/db/auth-schema.ts` (`bun run auth:generate`), `worker-configuration.d.ts` (`bun run cf-typegen`), drizzle migration append (`bun run db:generate`, `bun run db:migrate:local`)
- Test: `src/server/billing/__tests__/stripe.workers.test.ts`

**Interfaces:**
- Consumes: `createAuth` env plumbing (AuthEnv type), org tables.
- Produces: `getStripe(env): Stripe` (memoized, fetch http client); auth plugin configured with `stripe({ stripeClient, stripeWebhookSecret, createCustomerOnSignUp: false, subscription: { enabled: true, plans: [{ name: 'premium', priceId: env.STRIPE_PRICE_PREMIUM_MONTHLY, annualDiscountPriceId: env.STRIPE_PRICE_PREMIUM_YEARLY }], authorizeReference } })`; `subscription` table in schema; client plugin `stripeClient({ subscription: true })`.

- [ ] **Step 1: Failing workers test** — `authorizeReference` semantics: org owner → true for `upgrade-subscription`; admin/member → false; non-member → false. Drive via the exported `authorizeSubscriptionReference(db, { userId, referenceId })` helper (test it directly like `canManageContent`).

```ts
// src/server/billing/__tests__/stripe.workers.test.ts (core cases)
import { env } from 'cloudflare:workers'
import { describe, expect, it } from 'vitest'
import { createDb } from '#/server/db/client'
import { authorizeSubscriptionReference } from '#/server/billing/stripe'
import { makeOrg, makeUser } from '../../../../test/helpers'

describe('authorizeSubscriptionReference', () => {
  it('allows only org owners', async () => {
    const db = createDb(env.DB)
    const owner = await makeUser(db)
    const org = await makeOrg(db, owner.id)
    expect(await authorizeSubscriptionReference(db, { userId: owner.id, referenceId: org.id })).toBe(true)
    const outsider = await makeUser(db)
    expect(await authorizeSubscriptionReference(db, { userId: outsider.id, referenceId: org.id })).toBe(false)
  })
})
```

- [ ] **Step 2: Implement `src/server/billing/stripe.ts`**

```ts
import Stripe from 'stripe'
import { and, eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { member } from '#/server/db/schema'

/** Workers has no Node http — stripe-node must use its fetch client. */
export function createStripeClient(secretKey: string): Stripe {
  return new Stripe(secretKey, {
    httpClient: Stripe.createFetchHttpClient(),
    apiVersion: '2026-06-24.dahlia',
  })
}

/** Spec §3: only the org owner manages billing. */
export async function authorizeSubscriptionReference(
  db: Db,
  { userId, referenceId }: { userId: string; referenceId: string },
): Promise<boolean> {
  const m = await db.query.member.findFirst({
    where: and(eq(member.organizationId, referenceId), eq(member.userId, userId)),
  })
  return m?.role === 'owner'
}
```

- [ ] **Step 3: Wire the plugin** in `auth.ts` (and mirror in `auth.cli.ts` with dummy values): install `stripe` + `@better-auth/stripe` (`bun add stripe @better-auth/stripe`); plugin entry before `captcha`; `authorizeReference` delegates to `authorizeSubscriptionReference(createDb(d1), { userId: user.id, referenceId })`. Add `STRIPE_*` to the `AuthEnv` pick. Client: `stripeClient({ subscription: true })` from `@better-auth/stripe/client`.
- [ ] **Step 4: Env plumbing** — `.dev.vars.example`: `STRIPE_SECRET_KEY=sk_test_dummy`, `STRIPE_WEBHOOK_SECRET=whsec_dummy`; `wrangler.jsonc` vars + `test/wrangler.test.jsonc` vars: `STRIPE_PRICE_PREMIUM_MONTHLY=price_premium_monthly_dev`, `STRIPE_PRICE_PREMIUM_YEARLY=price_premium_yearly_dev`; regen typegen.
- [ ] **Step 5: Regenerate schema + migration** — `bun run auth:generate` (subscription table + `stripeCustomerId` fields), verify hand-written relations survive (known pitfall), `bun run db:generate` (append), `bun run db:migrate:local`.
- [ ] **Step 6: Full gate + commit** — `feat(billing): stripe plugin with org-scoped subscriptions`

---

### Task 2: Entitlements module

**Files:**
- Create: `src/server/billing/entitlements.ts`
- Modify: `test/helpers.ts` (add `makeSubscription`)
- Test: `src/server/billing/__tests__/entitlements.workers.test.ts`

**Interfaces:**
- Consumes: `subscription` table (Task 1).
- Produces (spec §4, exact): `type Entitlements = { plan: 'free' | 'premium'; maxSeats: 1 | 10; googleSync: boolean; branding: boolean }`; `getEntitlements(db: Db, orgId: string): Promise<Entitlements>`; `PREMIUM_MAX_SEATS = 10`; helper `makeSubscription(db, orgId, overrides?)` (defaults: plan 'premium', status 'active').

- [ ] **Step 1: Failing workers test** — free org (no row) → `{ plan: 'free', maxSeats: 1, googleSync: false, branding: false }`; active premium → `{ plan: 'premium', maxSeats: 10, googleSync: true, branding: true }`; `trialing` counts as premium; `canceled`/`incomplete`/`past_due` → free; multiple rows → any active/trialing wins.
- [ ] **Step 2: Implement**

```ts
import { eq } from 'drizzle-orm'
import type { Db } from '#/server/db/client'
import { subscription } from '#/server/db/schema'

export const PREMIUM_MAX_SEATS = 10
export type Entitlements = {
  plan: 'free' | 'premium'
  maxSeats: 1 | typeof PREMIUM_MAX_SEATS
  googleSync: boolean
  branding: boolean
}

const ACTIVE_STATUSES = new Set(['active', 'trialing'])

/** Spec §4: the ONE place plan rules live. Reads D1 only — never Stripe. */
export async function getEntitlements(db: Db, orgId: string): Promise<Entitlements> {
  const rows = await db.query.subscription.findMany({
    where: eq(subscription.referenceId, orgId),
  })
  const premium = rows.some((s) => s.plan === 'premium' && ACTIVE_STATUSES.has(s.status))
  return premium
    ? { plan: 'premium', maxSeats: PREMIUM_MAX_SEATS, googleSync: true, branding: true }
    : { plan: 'free', maxSeats: 1, googleSync: false, branding: false }
}
```

- [ ] **Step 3: Full gate + commit** — `feat(billing): entitlements module`

---

### Task 3: Seat-gated invitations + accept flow

**Files:**
- Modify: `src/server/auth/auth.ts` (+ `auth.cli.ts`): replace the Phase 1 `beforeCreateInvitation` hard-block with the entitlements seat gate
- Create: `src/routes/accept-invitation/$id.tsx`
- Modify: `messages/en.json`, `messages/nb.json`
- Test: extend `src/server/auth/__tests__/org-plugin.workers.test.ts`; create `src/server/auth/__tests__/invitations.workers.test.ts`

**Interfaces:**
- Consumes: `getEntitlements` (Task 2), Phase 1's `sendInvitationEmail` wiring + OrgInvite template (already built), Better-Auth `organization.acceptInvitation`.
- Produces: invitation flow — Free org invite → rejected with code the UI can map to an upgrade CTA (`APIError('FORBIDDEN', { message: 'UPGRADE_REQUIRED' })`); Premium org under cap → invitation created + email sent; at cap (`members + pending invitations >= maxSeats`) → rejected `SEAT_LIMIT_REACHED`. Route `/accept-invitation/$id`: signed-in → `authClient.organization.acceptInvitation` then redirect to `/dashboard` (org switched active); signed-out → redirect to `/login?next=/accept-invitation/$id`; invalid/expired → error card with i18n message.

- [ ] **Step 1: Failing workers tests** — via `auth.api` on a real auth instance: (a) Free org owner invites → FORBIDDEN/UPGRADE_REQUIRED; (b) premium org (seed row via `makeSubscription`) invites → invitation row created; (c) premium org with 9 members + 1 pending invite → SEAT_LIMIT_REACHED; (d) accepted invitation → member row exists, invitation status accepted.
- [ ] **Step 2: Implement the hook** (count = current members + pending invitations for the org; compare against `getEntitlements(...).maxSeats`; keep the check inside `beforeCreateInvitation` so the raw endpoint stays gated). Mirror in `auth.cli.ts`.
- [ ] **Step 3: Accept route** — follow `verify-email.tsx` patterns (AuthCard, i18n, redirect handling). New keys: `org_invite_accept_title`, `org_invite_accept_body`, `org_invite_accept_cta`, `org_invite_invalid`, EN + NB.
- [ ] **Step 4: Full gate + commit** — `feat(org): seat-gated invitations with accept flow`

---

### Task 4: Billing UI (settings section) + seedable plans

**Files:**
- Create: `src/components/billing/BillingSection.tsx`
- Modify: `src/routes/settings.tsx` (owner-only Billing section), `src/server/auth/session.functions.ts` (add `entitlements` to the session payload — read via `getEntitlements` in `buildClientSession`)
- Modify: `src/routes/api/test/seed.ts` (optional `plan: 'premium'` seeds a subscription row)
- Modify: `messages/en.json`, `messages/nb.json`
- Test: `src/components/billing/__tests__/BillingSection.test.tsx`; extend `session.functions.workers.test.ts`

**Interfaces:**
- Consumes: `authClient.subscription.upgrade/cancel/billingPortal` (client plugin, Task 1); `session.org` (Phase 1); `getEntitlements`.
- Produces: Billing section visible only to `session.org.role === 'owner'`: plan card (Free: name + "Upgrade to Premium" button calling `authClient.subscription.upgrade({ plan: 'premium', referenceId: org.id, successUrl: '/settings?upgraded=1', cancelUrl: '/settings' })`, monthly/annual toggle; Premium: status, period end, seat usage `X of 10`, "Manage billing" → `authClient.subscription.billingPortal({ referenceId, returnUrl: '/settings' })`, cancel note when `cancelAtPeriodEnd`). `ClientSession` gains `entitlements: Entitlements`.

- [ ] **Step 1: Failing component test** — Free owner sees upgrade CTA; Premium owner sees plan status + manage button; non-owner renders nothing (component returns null).
- [ ] **Step 2: Implement** — i18n keys `billing_*` (title, free_plan, premium_plan, upgrade_cta, manage_cta, seats_used {used}/{max}, renews {date}, cancels {date}), EN + NB. Buttons disable while awaiting redirect; errors via existing toast pattern.
- [ ] **Step 3: Seed route** — `plan: 'premium'` inserts a subscription row (plan 'premium', status 'active', referenceId = seeded org) so e2e can exercise premium states without Stripe.
- [ ] **Step 4: Full gate + commit** — `feat(billing): settings billing section and premium seeding`

---

### Task 5: Webhook state test, e2e, verification

**Files:**
- Test: `src/server/billing/__tests__/webhook.workers.test.ts`
- Modify: `e2e/settings.spec.ts` or new `e2e/billing.spec.ts` (free → upgrade CTA visible; seeded premium → plan card + seat usage; invite gate: premium org can send an invite from the API and the accept URL resolves) — static-check only locally, CI runs it
- Modify: `README.md` (billing section: env vars, Stripe setup steps, webhook URL `/api/auth/stripe/webhook`)

**Interfaces:** consumes everything above.

- [ ] **Step 1: Webhook workers test** — POST a `customer.subscription.updated` (and `.deleted`) event to the mounted auth handler at `/api/auth/stripe/webhook`, signed with the dummy `whsec` via `stripe.webhooks.generateTestHeaderString` (works offline); assert the `subscription` row's status transitions (active → canceled) and that `getEntitlements` flips accordingly. If Better-Auth's handler requires customer/subscription objects pre-seeded, create them directly in D1 first — the test asserts D1 state transitions, not Stripe.
- [ ] **Step 2: e2e spec** (uses seed route's premium option; no live Stripe).
- [ ] **Step 3: README** — Stripe setup (create product + 2 prices, set secrets via `wrangler secret put`, register webhook, price ids as vars).
- [ ] **Step 4: Full local gate; commit** — `test(billing): webhook transitions and billing e2e` + `docs: stripe setup`
- [ ] **Step 5:** Controller: final whole-branch review, push, PR, CI.

# samla v2 — Sign-up sheets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the `signup` poll type — slots with capacity that participants claim — on top of v1, reusing participants/votes/edit tokens/DO/emails.

**Architecture:** `votes` rows with `answer='yes'` are claims. Capacity is enforced atomically by routing claim/unclaim writes through the poll's `PollRoom` Durable Object (`claim`/`unclaim` RPC → `src/server/polls/claims.ts` service executed inside the DO). Server functions verify auth (owner/session/edit token) before calling the DO. The poll page renders a `SlotBoard` for `signup` polls instead of the vote grid.

**Tech Stack:** unchanged from v1 (TanStack Start, D1 + Drizzle, Better-Auth, PollRoom DO, Paraglide, Tailwind/shadcn/motion, Vitest unit + workers, Playwright).

**Spec:** `docs/superpowers/specs/2026-08-21-samla-v2-signup-sheets-design.md`

## Global Constraints

- Everything in the v1 plan's Global Constraints still applies (bun only; latest versions; `m.*` strings in en + nb with parity test; auth enforced server-side; `import * as z from 'zod'`; server modules import request helpers from `@tanstack/start-server-core/request-response`; middleware factories live in `src/server/http/rate-limit.middleware.ts`; no `cloudflare:workers` in `dist/client`; TDD; commits with the `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>` trailer).
- New error codes: `SLOT_FULL`, `CLAIM_LIMIT_REACHED`, `CAPACITY_BELOW_CLAIMS` (append to `ERROR_CODES` in `src/lib/errors.ts`).
- Limits: capacity 1..10000 or null (unlimited); `signupMaxClaims` 1..100 (default 1).
- `signup` polls: options may be date, datetime or text (not mixed); `allowIfNeedBe` is irrelevant (force false on create); `finalizePoll` → `AppError('VALIDATION')`; `addParticipant`/`updateParticipant` server fns → `AppError('VALIDATION')` for signup polls (claims only via `claimSlot`/`unclaimSlot`).
- `PollView` for signup: `scores = {}`, `bestOptionId = null`, new `claims: Record<optionId, { count: number; capacity: number | null; full: boolean }>`, `settings.signupMaxClaims: number`.
- Existing tests must stay green; v1 behaviour for `datetime`/`options` polls must not change.

---

### Task 1: Schema, migration, error codes, zod schemas

**Files:** Modify `src/server/db/schema.ts` (POLL_TYPES add `'signup'`; `polls.signupMaxClaims = integer('signup_max_claims').notNull().default(1)`), `src/lib/errors.ts`, `src/server/polls/schemas.ts`, `src/server/polls/viewmodel.ts` (add `claims` + `settings.signupMaxClaims`); create `drizzle/0001_*.sql` via `bun run db:generate`; tests `src/server/polls/__tests__/schemas.test.ts` (extend), `src/server/db/__tests__/schema.workers.test.ts` (extend: insert a signup poll with capacity + signupMaxClaims).

**Interfaces (Produces):**
```ts
// schemas.ts additions
export const capacitySchema = z.number().int().min(1).max(10000).nullable()
// each optionInputSchema variant gains `capacity: capacitySchema.optional()`
// createPollBase gains `signupMaxClaims: z.number().int().min(1).max(100).optional()`
// refinePollOptions: type 'signup' → kinds date|datetime|text but all the same kind (path ['options', i]); non-signup → every option.capacity must be undefined/null (path ['options', i, 'capacity']); signupMaxClaims only allowed when type==='signup' (path ['signupMaxClaims'])
export const claimSchema = z.object({ pollId: pollIdSchema, optionId: z.string(), participantId: z.string().optional(), editToken: z.string().optional(), name: z.string().trim().min(1).max(LIMITS.name).optional(), email: z.union([z.literal(''), z.email().max(254)]).optional(), turnstileToken: z.string().optional() })
export const unclaimSchema = z.object({ pollId: pollIdSchema, optionId: z.string(), participantId: z.string(), editToken: z.string().optional() })
export type ClaimInput / UnclaimInput
```
- [ ] Tests first (schema rules above incl. mixed kinds rejected for signup, capacity on non-signup rejected, signupMaxClaims bounds; workers insert/select of the new column), RED → implement → `bun run db:generate` → GREEN; commit `feat(signup): schema, migration, error codes and zod schemas for sign-up sheets`.

### Task 2: Claims service + poll service changes

**Files:** Create `src/server/polls/claims.ts`, `src/server/polls/__tests__/claims.workers.test.ts`; modify `src/server/polls/service.ts` (+tests), `test/helpers.ts` (`makeSignupPoll(db, ownerId, { capacities: (number|null)[], maxClaims? })`).

**Interfaces (Produces):**
```ts
// claims.ts (server-only; db first; throws AppError; no cloudflare:workers)
export type ClaimIdentity = { participantId: string } | { name: string; email?: string | null; userId: string | null; locale?: string | null }
export async function applyClaim(db: Db, pollId: string, optionId: string, identity: ClaimIdentity): Promise<{ participantId: string; editToken: string | null; claimedOptionIds: string[]; created: boolean }>
//  NOT_FOUND (poll missing/deleted, option not in poll, participant not in poll); VALIDATION unless poll.type==='signup'; POLL_CLOSED unless open; EMAIL_REQUIRED when required and new participant has no email; LIMIT_REACHED (participants ≥ LIMITS.participants) for new participants; CLAIM_LIMIT_REACHED when participant already has signupMaxClaims claims (and this option isn't among them); SLOT_FULL when capacity !== null && count(yes votes on option) >= capacity; idempotent when already claimed. New participants: token like addParticipant (null for userId). All writes in one db.batch.
export async function removeClaim(db: Db, pollId: string, optionId: string, participantId: string): Promise<{ remainingOptionIds: string[] }>
//  NOT_FOUND / VALIDATION / POLL_CLOSED; deleting a non-existent claim is a no-op. (Authorization is the caller's job.)
export async function countClaims(db: Db, pollId: string): Promise<Record<string, number>>
// service.ts
// createPoll/duplicatePoll/updatePoll persist option.capacity and signupMaxClaims; for signup force allowIfNeedBe=false.
// updatePoll: if any retained option's new capacity !== null && < current claim count → AppError('CAPACITY_BELOW_CLAIMS') (check before batch).
// finalizePoll: AppError('VALIDATION') when type==='signup'.
// getPollView: claims map (+ full flag), settings.signupMaxClaims, scores {} / bestOptionId null for signup.
```
- [ ] Tests first for every rule above (incl. capacity-null unlimited, idempotent claim, remove, counts, updatePoll guard, finalize guard, view claims); commit `feat(signup): claims service, capacity guards and view model`.

### Task 3: DO claim RPC, server functions, confirmation email, roster CSV

**Files:** Modify `src/do/PollRoom.ts` (+tests), `src/server/polls/participants.functions.ts` (+manifest + `test/server-functions.workers.test.ts`), `src/server/mailer/templates.tsx`, create `emails/ClaimConfirmation.tsx`, `src/server/notifications/claim-emails.ts`, `src/server/polls/roster.ts`, `src/routes/p/$id/roster[.]csv.ts`, tests `src/server/polls/__tests__/roster.workers.test.ts`, `emails/__tests__/templates.test.tsx` (extend), messages (email keys).

**Interfaces (Produces):**
```ts
// PollRoom
async claim(pollId: string, optionId: string, identity: ClaimIdentity): Promise<ReturnType<typeof applyClaim>>   // runs applyClaim(createDb(this.env.DB), …) then broadcast poll.changed 'vote'
async unclaim(pollId: string, optionId: string, participantId: string): Promise<{ remainingOptionIds: string[] }>  // removeClaim then broadcast
// errors thrown inside the DO propagate to the caller as Error with message = code (DO RPC serialises Error message) — server fn maps back via errorCode(); test that the code survives the RPC boundary.
// do-client.ts: export function claimViaRoom(pollId, optionId, identity) / unclaimViaRoom(...) — NOT best-effort: errors propagate.
// server fns (participants.functions.ts)
export const claimSlot   // POST; [sessionMiddleware, rateLimitMiddleware('vote')]; input claimSchema; logic: load poll (NOT_FOUND); if participantId given → verify auth (owner | userId match | editToken) else require name (+ Turnstile for guests) and build new identity with userId from session, locale getLocale(); call claimViaRoom; on created → saveable editToken returned; queueDigest({kind:'vote', name}); send confirmation email best-effort (claim-emails.ts: renderClaimConfirmation with slot labels via formatOptionLabel in poll tz + ics attachment for date/datetime slots); return { participantId, editToken, claimedOptionIds }
export const unclaimSlot // POST; [sessionMiddleware, rateLimitMiddleware('vote')]; auth like updateParticipant (owner/userId/editToken); unclaimViaRoom; return { remainingOptionIds }
// addParticipant/updateParticipant server fns: throw AppError('VALIDATION') when poll.type==='signup'
// roster.ts
export async function buildRosterCsv(db: Db, pollId: string, opts: { locale: string }): Promise<string>   // header: slot,capacity,claimed,participant,email ; one row per claim (slot label via formatOptionLabel in poll tz), slots with zero claims get one row with empty participant; UTF-8 BOM; RFC 4180 quoting
// roster route: GET /p/$id/roster.csv — session required and poll.ownerId === user.id else 403 (404 if poll missing); Content-Disposition attachment samla-<id>-roster.csv
// templates.tsx: renderClaimConfirmation({ name, pollTitle, pollUrl, slots: string[], locale }) → { subject, html, text }
```
- [ ] Tests: DO concurrency (two `claim` calls for a capacity-1 slot started together → one ok, one `SLOT_FULL`), RPC error code survives, manifest assertions for new fns, roster CSV (quoting, BOM, zero-claim slot), email render en/nb; commit `feat(signup): DO-serialised claims, claim/unclaim server functions, confirmation email and roster CSV`.

### Task 4: Creator + edit support for sign-up sheets

**Files:** Modify `src/components/creator/creator-state.ts` (+tests), `TypeStep.tsx` (third card), `OptionsStep.tsx`/`DateOptionsEditor.tsx`/`TimeSlotEditor.tsx`/`TextOptionsEditor.tsx` (capacity per option when type==='signup'), `SettingsStep.tsx` (signupMaxClaims; hide if-need-be for signup), `CreatorWizard.tsx`, `src/routes/p/$id/edit.tsx` (capacity + CAPACITY_BELOW_CLAIMS message), create `src/components/creator/CapacityField.tsx` (+test), messages.

**Interfaces (Produces):** `DraftSlot`/`DraftDateOption`/`DraftTextOption` gain `capacity?: number | null` (undefined = default 1 for signup; ignored for other types); `CreatorDraft.signupMaxClaims: number` (default 1); actions `setSlotCapacity { date, index, capacity }`, `setDateCapacity { date, capacity }`, `setTextOptionCapacity { index, capacity }`; `draftToInput` emits `capacity` only for signup (null = unlimited, default 1) and `signupMaxClaims`; `draftFromPoll` restores them. `CapacityField({ value: number | null; onChange(v: number | null): void; id?: string })` — number input (min 1) + "Unlimited" switch.
- [ ] Tests first (reducer/capacity round-trips, CapacityField interactions); manual runtime check creating a signup sheet via the wizard (curl SSR of /new still 200); commit `feat(signup): creator and edit support for capacity and max claims`.

### Task 5: Slot board poll page, admin bar, dashboard

**Files:** Create `src/components/signup/SlotBoard.tsx`, `SlotCard.tsx`, `CapacityBar.tsx`, `ClaimButton.tsx`, `ClaimantList.tsx`, `IdentitySheet.tsx`, `src/lib/use-claims.ts` (client helper: viewer identity from session/edit token, `claim(optionId)`/`unclaim(optionId)` calling the server fns, pending state per option), tests `src/components/signup/__tests__/{SlotCard,CapacityBar,IdentitySheet}.test.tsx`; modify `src/components/poll/PollPage.tsx` (branch on `poll.type === 'signup'`), `AdminBar.tsx` (hide Finalize for signup; add "Download roster" link to `/p/$id/roster.csv`), `src/components/dashboard/PollCard.tsx` (signup icon + "N sign-ups"), messages.

**Behaviour:** `SlotCard` shows label (formatOptionLabel in viewer tz), `CapacityBar` ("2 of 5" or "2 signed up" when unlimited; full state), `ClaimantList` (names, "You" badge, owner sees remove (unclaim) per claimant), `ClaimButton` (Claim / Leave; disabled when full (unless you hold it), when poll closed, or when you've hit `signupMaxClaims` — with tooltip reason). First claim by an unknown viewer opens `IdentitySheet` (Dialog: name, email (required if setting), Turnstile for guests) then performs the claim; token saved via `saveEditToken`; `celebrate('vote')` on first claim. Live updates + presence reuse `useLivePoll`. Errors mapped: SLOT_FULL, CLAIM_LIMIT_REACHED, POLL_CLOSED, EMAIL_REQUIRED, CAPTCHA_FAILED, RATE_LIMITED.
- [ ] Tests first; runtime check: seed route gets `withSignup: true` (creates a signup sheet with 2 slots capacity 1 + unlimited) → `/p/<id>` SSR shows `data-testid="slot-board"`; commit `feat(signup): slot board with claims, identity sheet, roster link and dashboard card`.

### Task 6: E2E, docs, final verification

**Files:** Create `e2e/signup.spec.ts` (create sheet via wizard OR seed `withSignup` → guest A claims slot 1 → guest B (new context) sees slot 1 full, claims slot 2 → owner downloads roster 200 `text/csv` containing both names); modify `README.md` (features: sign-up sheets; roadmap: v2 done, v3 next), `e2e/screenshots.spec.ts` (add signup page), messages if needed.
- [ ] `bun run typecheck && lint && format:check && test && build`, client-leak grep, `playwright test --list`; commit `test(e2e): sign-up sheet journey; docs: v2 in README`.

## Self-review notes
Spec coverage: decisions 1–12 → T1 (1,3), T2 (2,4 partly,6,9,12), T3 (4,7,8), T4 (10,12), T5 (11,5), T6 (tests/docs). Type names reused across tasks: `ClaimIdentity`, `applyClaim`, `removeClaim`, `claimSchema`, `unclaimSchema`, `claims` view field, `signupMaxClaims`.

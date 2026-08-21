# samla — v2 design spec: Sign-up sheets

Date: 2026-08-21 · Builds on v1 (`docs/superpowers/specs/2026-08-20-samla-v1-design.md`). The v1 spec reserved v2 for "sign-up sheets (options gain `capacity`; participants *claim* instead of vote)". Anders asked that the next phase start automatically after v1; the decisions below are the controller's rulings, recorded for his review.

## Goal

Let an organiser publish a sheet of **slots** (dates, times or free-text items) each with a **capacity**, and let people **claim** slots — volunteer shifts, parent-teacher meetings, bring-a-dish lists — with the same low-friction guest flow, live updates, emails and admin tools as v1 polls.

## Decisions (rulings)

1. **New poll type `signup`** alongside `datetime` and `options`. A sign-up sheet is a poll whose options are slots; `poll_options.capacity` (already in the schema, reserved in v1) becomes meaningful: integer ≥ 1, or `null` = unlimited.
2. **Claims reuse the `votes` table** with `answer = 'yes'` only (no if-need-be / no). A participant's claims are their `votes` rows. This keeps `getPollView`, participants, comments, edit tokens, digest emails and the DO working unchanged.
3. **Per-participant limit:** new column `polls.signup_max_claims` (integer, default 1, max 100). A participant may claim at most that many slots.
4. **Capacity is enforced atomically per poll** by routing claim writes through the poll's `PollRoom` Durable Object (`claim` RPC): the DO is single-threaded per poll, so "count current claims → compare to capacity → insert" cannot race. Non-signup polls keep writing directly from server functions.
5. **No waitlist in v2** (explicit out of scope), no per-slot notes, no swapping. Organiser can raise capacity, remove a claimant, or close the sheet.
6. **Finalize does not apply** to sign-up sheets (no "winning option"); the organiser **closes** the sheet (existing `closed` status + deadline auto-close). `finalizePoll` throws `VALIDATION` for `signup` polls; UI hides Finalize.
7. **Emails:** participants who leave an email get a **claim confirmation** (their slots, with `.ics` when the slot is a date/datetime) on first claim and on change; organiser digest unchanged ("new sign-ups"). On close, owner gets the existing closed email.
8. **Roster export:** organiser can download `/p/:id/roster.csv` (slot label, capacity, claimed count, participant names/emails — emails visible to the owner only, as the owner already receives them via finalize/close flows in v1). Owner-only server route (session required).
9. **Scoring/best option** are not computed for `signup` (scores = `{}`, `bestOptionId = null`); the view exposes `claims: Record<optionId, { count: number; capacity: number | null; full: boolean }>`.
10. **Creator:** third type card "Sign-up sheet"; options step reuses the date/time-slot and text editors plus a per-option capacity input (default 1, "unlimited" toggle) and a sheet-wide "max sign-ups per person" setting. Slot kinds: date, datetime, text (mixed kinds not allowed within one sheet — same rule as v1).
11. **Poll page for `signup`:** replaces the vote grid with a **slot board**: one card per slot (label, capacity bar "2 of 5", avatars/names of claimants, Claim / Unclaim button, "Full" state, "You" badge). Live updates and presence reuse v1. The add-yourself flow asks name (+ optional/required email) on first claim (inline sheet), then claims are one click each.
12. **Edit page:** capacity editable per slot; lowering capacity below current claims is rejected with a clear message (`CAPACITY_BELOW_CLAIMS`).

## Data model changes

- `polls.type` enum gains `'signup'` (text enum in Drizzle; SQLite has no enum — no migration needed for the enum, but a migration adds the new column).
- New column `polls.signup_max_claims INTEGER NOT NULL DEFAULT 1`.
- `poll_options.capacity` semantics: `null` = unlimited; integer ≥ 1 otherwise. For non-signup polls it stays `null`.
- New migration `drizzle/0001_*.sql`.

## Service / API changes

- `schemas.ts`: `pollTypeSchema` gains `signup`; `optionInputSchema` variants gain optional `capacity: z.number().int().min(1).max(10000).nullable()`; `createPollSchema` refinement: for `signup`, option kinds may be date/datetime/text (but not mixed); for non-signup, `capacity` must be absent/null; new field `signupMaxClaims: z.number().int().min(1).max(100).optional()` (only for signup); `claimSchema = { pollId, optionId, name?, email?, editToken?, participantId?, turnstileToken? }`, `unclaimSchema = { pollId, optionId, participantId, editToken? }`.
- `service.ts`: `createPoll`/`updatePoll` persist `capacity` + `signupMaxClaims`; `updatePoll` throws `AppError('CAPACITY_BELOW_CLAIMS')` when lowering capacity under current claims; `getPollView` adds `claims` and `signupMaxClaims` to settings; `finalizePoll` → `VALIDATION` for signup.
- New `src/server/polls/claims.ts` (service, server-only): `applyClaim(db, pollId, { optionId, participant: {existing id | new name/email/userId/locale} })` and `removeClaim(db, pollId, participantId, optionId, auth)`, used by the DO. Rules: poll open; option belongs to poll; per-participant limit; capacity (`count(yes votes for option) < capacity` unless unlimited); idempotent (claiming an already-claimed slot is a no-op); returns `{ participantId, editToken | null, claimedOptionIds }`.
- `PollRoom` DO: new RPC `claim(pollId, input)` and `unclaim(pollId, input)` that call the claims service inside the DO (serialised), then broadcast `poll.changed`. Auth for the claim (owner/user/editToken) is verified by the server function BEFORE calling the DO (the DO trusts the server function; it is not reachable from the public internet except via the WS fetch handler).
- Server functions (`participants.functions.ts`): `claimSlot` (sessionMiddleware + rateLimit 'vote'; Turnstile for guests on first claim; resolves participant auth; calls `pollRoom.claim`; queues digest `{kind:'vote'}`; sends confirmation email best-effort) and `unclaimSlot`. Existing `addParticipant`/`updateParticipant` reject `signup` polls with `VALIDATION`.
- New error codes: `SLOT_FULL`, `CLAIM_LIMIT_REACHED`, `CAPACITY_BELOW_CLAIMS`.
- Roster route `src/routes/p/$id/roster[.]csv.ts` (owner only → 403 otherwise), UTF-8 CSV with BOM.
- Confirmation email template `emails/ClaimConfirmation.tsx` + `renderClaimConfirmation({ name, pollTitle, pollUrl, slots: string[], locale })`.

## UI

- Creator: type card; capacity input per option (shared `CapacityField` component: number input + "unlimited" switch); `signupMaxClaims` in settings step for signup.
- Poll page: `SlotBoard` (`src/components/signup/`): `SlotCard`, `CapacityBar`, `ClaimButton`, `ClaimantList`, `IdentitySheet` (name/email/turnstile collected once; stored edit token reused). Admin bar: Finalize hidden; "Download roster" added; Close/Reopen kept.
- Dashboard `PollCard`: type icon for signup; count shows "claims".
- i18n: all strings in en + nb.
- Delight: claim button pops + `celebrate('vote')` on first claim; capacity bar animates; "Full" slots desaturate.

## Testing

- Unit: schemas (capacity/type rules), CSV builder, `CapacityField`, `SlotCard` (full/claimed states), creator-state for signup (capacity in draft ↔ input round-trip).
- Workers: claims service (limit, capacity, idempotency, unclaim, closed poll, foreign option), DO `claim` serialisation (two concurrent claims on a capacity-1 slot → exactly one succeeds), `updatePoll` capacity guard, roster builder, view `claims`, confirmation email rendering.
- E2E: `signup.spec.ts` — create sheet (2 slots, capacity 1 and unlimited) → guest claims slot A → second guest sees A full, claims B → owner downloads roster (200, `text/csv`).

## Out of scope (v2)

Waitlists, slot notes, swapping, reminders, per-slot deadlines, paid tiers, v3 booking.

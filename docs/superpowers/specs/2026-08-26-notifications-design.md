# whenweall v5 — notification subsystem (email + web push)

**Date:** 2026-08-26 · **Status:** §1 approved in discussion, §2–§6 authored for review

## Context

Notifications exist today but only in one narrow shape: the poll creator gets a debounced digest
email about new responses and new comments, gated by two booleans on the poll row
(`polls.notifyOnVote` / `notifyOnComment`, surfaced as two switches in `AdminBar`). The batching,
alarm re-arming and retry ladder all live in the `PollRoom` durable object and work well.

Four gaps motivated this work:

1. **Editing a response notifies nobody.** `updateParticipant` only broadcasts over the WebSocket;
   `removeParticipant` likewise. The organiser never learns that an answer changed.
2. **Only the creator is ever notified.** Org teammates get nothing, and a poll whose creator has
   left silently notifies no one.
3. **No push channel at all** — no manifest, no service worker, no VAPID, nothing to build on.
4. **Cadence is hardcoded** to a 10-minute digest.

Decisions taken with Anders (2026-08-26): recipients are the creator plus opted-in org teammates
(guests/participants keep transactional mail only); all four event families are in scope
(response activity, comments, poll lifecycle, booking events); the preference grid is
**per-event × per-channel with the channel fixing cadence** — push is immediate, email batches;
preferences are **user-level defaults with a per-poll override**; **push is Premium-only**; the
PWA is **manifest + installable + push service worker, no offline support**.

## Goals

- One typed event catalogue shared by polls and booking pages; one emit boundary.
- An organiser hears about response edits and withdrawals, not just new responses.
- Teammates can follow a poll and get their own preferences, independent of the creator's.
- Web push as a second channel on installable Chrome / Firefox / Edge / Android, and on iOS once
  the user has added the app to their home screen.
- Existing organisers' notification behaviour is unchanged the day this ships, apart from the
  response-edit gap being closed.

## Non-goals (deferred)

- **Offline support / asset caching.** The service worker registers a no-op passthrough `fetch`
  handler purely so installability does not depend on browser-version differences in the install
  criteria. It caches nothing and has no invalidation story, deliberately.
- Daily or weekly summary cadence. The grid's shape leaves room for it; nothing implements it.
- Participant-facing notifications (a guest opting in to "someone commented"). That needs signed
  unsubscribe links and abuse thinking, and is a separate project.
- SMS, Slack, webhooks, or any third channel.
- In-app notification centre / unread badge.

## §1 Event catalogue and data model

**Catalogue** (`src/server/notifications/events.ts`) — stable string keys, one union for both
halves of the app:

| Key                    | Scope        | Fired from                                                                   |
| ---------------------- | ------------ | ---------------------------------------------------------------------------- |
| `response.created`     | poll         | `addParticipant`, `claimSlot`                                                |
| `response.updated`     | poll         | `updateParticipant`                                                          |
| `response.withdrawn`   | poll         | `removeParticipant`, unclaim                                                 |
| `comment.created`      | poll         | `addComment`                                                                 |
| `deadline.approaching` | poll         | `PollRoom` alarm, 24h before `deadlineAt`                                    |
| `poll.closed`          | poll         | `PollRoom#processDeadline` (deadline-driven; there is no manual close today) |
| `poll.finalized`       | poll         | `finalizePoll`                                                               |
| `signup.full`          | poll         | `PollRoom#claim` when the last slot goes                                     |
| `booking.created`      | booking_page | `BookingRoom#book`                                                           |
| `booking.cancelled`    | booking_page | `BookingRoom#cancel`                                                         |
| `booking.rescheduled`  | booking_page | `BookingRoom#reschedule`                                                     |

**Boundary — transactional mail is not a notification.** Visitor booking confirmations, claim
confirmations, the `Finalized` mail + `.ics` to participants, verify-email and reset-password are
untouched by this subsystem and ignore preferences entirely. The grid governs only what an
_organiser or teammate_ receives. Without this line a checkbox could silence a confirmation a
visitor needs.

**Grid encoding.** A Zod-validated JSON column per scope:
`{"response.created": {"email": true, "push": true}, ...}`. Recipient sets are capped by seats
(≤10 on Premium) so resolution happens in JS regardless, and 11 events × 2 scopes as rows would
mean ~11 writes to change one poll's settings. Unknown keys are ignored on read and missing keys
fall back to `SYSTEM_DEFAULTS`, so adding a twelfth event is a code change with no migration.

**Tables:**

- `notification_prefs` — `userId` PK → `user.id` (cascade), `channels` JSON, `createdAt`,
  `updatedAt`. The user's default grid. Absent row = `SYSTEM_DEFAULTS`.
- `notification_subscriptions` — PK `(scopeType, scopeId, userId)`; `scopeType`
  `'poll' | 'booking_page'`; `source` `'creator' | 'follow'`; `channels` JSON **nullable**
  (null = inherit the user's defaults); `createdAt`, `updatedAt`. Index on `(scopeType, scopeId)`.
- `push_subscriptions` — `id` PK, `userId` → `user.id` (cascade), `endpoint` text + unique index,
  `p256dh`, `auth`, `userAgent` (nullable, so the settings page can say "Chrome on Linux"),
  `createdAt`, `lastSeenAt`.

**Polymorphic scope, decided.** `notification_subscriptions` carries a bare `scopeId` rather than
two FK columns, so polls and booking pages share one resolver and one emit path — which is the
drift risk approach A carries. SQLite cannot put a foreign key on a polymorphic column, so there
is no cascade: the delete paths for polls and booking pages delete their subscription rows
explicitly, and the delivery path already loads-and-skips a missing or soft-deleted scope, so a
leaked row is cosmetic rather than dangerous.

**`SYSTEM_DEFAULTS`:** email on for `response.created`, `response.updated`, `comment.created`,
`deadline.approaching`, `poll.closed`, `poll.finalized`, `signup.full`, and all three booking
events; email off for `response.withdrawn`. Push on for `response.created`, `comment.created`,
`deadline.approaching` and `booking.created`; off elsewhere. Rationale: withdrawals are the
noisiest and least actionable event, and push should default to the handful of things worth
interrupting someone for.

**Migration.** One migration creates the three tables, then backfills:

- One `creator` subscription per poll for `polls.createdBy` (skipping rows where the creator's
  account is gone), mapping `notifyOnVote` → email on for `response.created` **and**
  `response.updated`, `notifyOnComment` → email on for `comment.created`.
- One `creator` subscription per booking page for `memberUserId ?? createdBy`, with the three
  booking events on for email — organiser notices are unconditional today, so this preserves
  current behaviour exactly.
- Drops `polls.notify_on_vote` and `polls.notify_on_comment`.

Backfilling `response.updated` as **on** switches on something those organisers never had. This is
deliberate — it matches the intent of the switch they ticked and it is the gap that motivated the
work — but it does mean existing polls get chattier the day it ships.

Removing the two columns touches `src/server/db/schema.ts`, `src/server/polls/schemas.ts`,
`src/server/polls/viewmodel.ts`, `src/server/polls/service.ts` (three sites) and replaces the two
switches in `src/components/poll/AdminBar.tsx` with the grid.

## §2 Recipient and preference resolution

`src/server/notifications/recipients.ts` exposes one function:

```
resolveRecipients(db, scope: {type, id, organizationId}, event)
  → { email: Recipient[]; push: (Recipient & {devices: PushSubscription[]})[] }
```

Steps, in order:

1. Load `notification_subscriptions` for `(scopeType, scopeId)`.
2. **Membership is the authority.** Inner-join against `member` on the scope's `organizationId`.
   A user who has left the org (or lost their seat) receives nothing, creator included — they no
   longer have access to the poll, so they should not hear about it either. Their subscription row
   is left in place, so re-adding them restores their settings.
3. For each surviving subscriber, resolve the effective grid key-by-key:
   `subscription.channels[event] ?? prefs.channels[event] ?? SYSTEM_DEFAULTS[event]`. The merge is
   per-key, not whole-object, so a per-poll override of one event does not reset the rest.
4. Split into the email list and the push list for this event.
5. **Entitlement gate.** `Entitlements` gains `push: boolean` (false on Free, true on Premium),
   evaluated against the _scope's_ org, not the recipient's personal org. On a lapsed subscription
   the push list resolves empty; subscription rows and device rows are kept untouched, so
   re-upgrading restores push with no re-permission prompt. The field reaches the client for free —
   `buildClientSession` (`src/server/auth/session.functions.ts`) already ships `Entitlements` to the
   browser, so the settings UI can lock the push column without a new endpoint.
6. Load each recipient's `email` and `locale` from `user` for rendering.

**Actor suppression.** The user who caused an event never receives it. A teammate editing their
own response, or the organiser closing their own poll, generates no notification for themselves.
The emit call passes `actorUserId` (nullable — guests have none) and the resolver filters it out.

## §3 Emit and delivery

`src/server/notifications/emit.ts` is the only boundary the rest of the app touches:

```
emitPollEvent(pollId, event, { actorUserId, actorName })
emitBookingEvent(pageId, event, { bookingId, actorUserId })
```

Both are best-effort and never throw — a stalled DO or push service must not fail the request that
triggered it, matching the existing `sendClaimConfirmation` / `notifyChanged` contract.

**Push, always immediate.** `emit` resolves push recipients and posts to every device endpoint via
`Promise.allSettled`. Sends are awaited inline, matching how `sendClaimConfirmation` already works
in this codebase; if the added latency shows up in practice the call moves behind `waitUntil`
without changing the boundary.

**Email splits by event character:**

- _Activity events_ — `response.*`, `comment.created`, `signup.full` — go through the existing
  `PollRoom` digest. `DigestItem` changes from `{kind: 'vote'|'comment', name, at}` to
  `{event: NotificationEvent, name, at, actorUserId}`. `#processDigest` grows from "the one owner"
  to: load the poll, resolve recipients per item event, group items per recipient, render one
  digest per recipient. Preferences are resolved at alarm time, not enqueue time, so a toggle
  flipped during the debounce window takes effect. The 10-minute debounce, three-retry ladder and
  alarm re-arming are unchanged; a retry re-sends the whole batch.
- _Lifecycle events_ — `deadline.approaching`, `poll.closed`, `poll.finalized` — are singular and
  time-sensitive, so they send immediately rather than waiting out a debounce window.
- _Booking events_ send immediately through the existing `sendBookingEmails` organiser path, now
  gated by preferences. A booking made 30 minutes before its slot cannot wait for a digest.

**New alarm key.** `PollRoom` gains `remind:at` = `deadlineAt − 24h`, written by `syncDeadline`,
included in `#rearm`'s `Math.min`, and handled in `alarm()` alongside the existing deadline and
digest branches — each in its own try/catch, as the existing branches are. It is skipped when the
deadline is already inside 24 hours at creation time.

**`signup.full`** fires from inside `PollRoom#claim`, where the capacity check already runs
serialised, at the moment the last slot goes.

## §4 Web push

**Keys.** A P-256 ECDSA VAPID keypair. Public key exposed to the client through the root loader;
private key as the Worker secret `VAPID_PRIVATE_KEY`, with `VAPID_SUBJECT` (a `mailto:`) in
`wrangler.jsonc` vars. Generated once, documented in the README's provisioning checklist alongside
the existing secrets.

**Signing and encryption** (`src/server/notifications/push/`):

- VAPID JWT: ES256 via `crypto.subtle.sign('ECDSA', {hash: 'SHA-256'})`, `aud` = the push
  service's origin, `exp` ≤ 24h, `sub` = `VAPID_SUBJECT`.
- Payload: RFC 8291 `aes128gcm` — ECDH against the subscription's `p256dh`, HKDF-SHA256 for the
  content-encryption key and nonce, then AES-GCM. Entirely expressible in WebCrypto.
- **Build vs. borrow:** try a Workers-targeted library (`@block65/webcrypto-web-push` or
  equivalent) first; fall back to a vendored ~150-line module if it does not run cleanly on
  Workers. Either path is verified against the RFC 8291 published test vectors, so the decision is
  reversible and the tests do not change.

**Failure handling.** `404` or `410` from the push service means the subscription is dead — delete
the row. `429` and `5xx` are transient — log and leave the row alone; the next event retries
naturally. Nothing is queued for redelivery: a missed push is a missed push, and the email channel
is the durable one.

**Service worker** (`public/sw.js`), three handlers and no caching:

- `push` → `showNotification(title, {body, icon, badge, tag, data: {url}})`, `tag` keyed per scope
  so repeated events on one poll collapse into a single notification rather than stacking.
- `notificationclick` → focus an existing client already on that URL, otherwise open it.
- `fetch` → a no-op passthrough that never calls `respondWith`. It exists only because browser
  install criteria have historically required a fetch handler; it caches nothing.

**Manifest** (`public/manifest.webmanifest`): `name`, `short_name`, `start_url: "/"`,
`display: "standalone"`, theme and background colours from the existing brand tokens, and icons
from `public/brand` (192, 512, plus a maskable variant). Linked from the root document head
alongside an `apple-touch-icon` pointing at the existing `icon-180.png`.

**Permission UX.** Account settings shows "Enable push on this device", which requests permission,
subscribes via `pushManager.subscribe({userVisibleOnly: true, applicationServerKey})` and POSTs the
subscription to a server function. Below it, the list of registered devices with a remove button.
On iOS, `display-mode: standalone` is detected first: if the app is not installed the control is
replaced by an "Add to Home Screen to enable push" hint, because Safari will not offer the prompt
otherwise. On a Free org the whole push column is shown locked with an upgrade link.

## §5 UI

- **Account settings** — the default grid (11 rows × 2 checkbox columns, grouped under "Responses",
  "Comments", "Poll lifecycle", "Bookings"), plus the push device list and enable control.
- **`AdminBar`** — the same grid for one poll, in an "inheriting your defaults" state until the
  organiser changes something, with a "reset to defaults" control that deletes the override by
  writing `channels = null`.
- **Booking page settings** — the same grid scoped to the three booking events.
- **Follow control** — org teammates viewing a poll they do not own get a follow/unfollow toggle,
  which creates or deletes their `source: 'follow'` subscription row.
- All strings go through Paraglide in both `en` and `nb`, matching the existing message files.
- New email template `emails/Notification.tsx` for the immediate lifecycle sends; `emails/Digest.tsx`
  is extended to render the new event kinds ("Ada changed their answer") rather than only counts.

## §6 Testing & verification

- **Unit** (`vitest --project unit`): grid merge precedence including per-key fallback; catalogue
  exhaustiveness; VAPID JWT structure; `aes128gcm` encryption against the RFC 8291 test vectors;
  template rendering for each new event in `en` and `nb`.
- **Workers** (`vitest --project workers`, real D1 + DO): recipient resolution — creator only,
  creator + follower, ex-member excluded, actor suppressed; entitlement gate on a Free org;
  `PollRoom` digest fanning out to two recipients with different grids; preferences flipped
  mid-debounce taking effect; `remind:at` arming and firing; `410` pruning a device row; the
  migration backfill preserving today's behaviour for a poll with `notifyOnVote = false`.
- **E2E** (Playwright, Chromium): the settings grid persists across reload; the per-poll override
  and its reset; `context.grantPermissions(['notifications'])` plus a service-worker registration
  assertion for the subscribe flow. Asserting an actual delivered push is out of scope.
- **Manual**: install on Android and on iOS via Add to Home Screen, confirm a real push arrives on
  each.

## Phasing

1. **Core** — catalogue, tables, migration + backfill, resolver, emit boundary, `PollRoom` digest
   fan-out, the new alarm, booking integration, settings + `AdminBar` UI. Ships on its own and
   closes the response-edit gap; push columns render disabled.
2. **Push** — VAPID, encryption, device table wiring, service worker, manifest, permission UX,
   Premium gate.

Phase 1 is independently useful and independently shippable; phase 2 does not modify phase 1's
boundary, only fills in the push half of an already-resolved recipient list.

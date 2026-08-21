# samla v3 — 1:1 Booking pages Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Personal booking pages (`/book/<handle>/<slug>`): weekly availability + duration + buffers, optional Google Calendar busy-check + event creation, visitor booking with confirmation/cancel/reschedule via secure link, reminders, live slot updates.

**Architecture:** New tables `booking_pages` + `bookings` (no coupling to polls). Pure availability generator in `src/lib/availability.ts`. Per-page `BookingRoom` Durable Object serialises book/cancel/reschedule (mutex + D1 write inside the DO, same pattern as `PollRoom` claims), broadcasts `page.changed`, and runs reminder alarms. Google Calendar via the user's linked Google account (Better-Auth `linkSocial` with calendar scope; `auth.api.getAccessToken`). Server functions enforce auth (owner session / manage token / Turnstile for visitors). UI: organiser editor + dashboard section, public booking page, manage page.

**Tech Stack:** unchanged (TanStack Start, D1+Drizzle, Better-Auth 1.7, DOs, Email Service, Paraglide, Tailwind/shadcn/motion, Vitest unit+workers, Playwright; msw for Google API mocks).

**Spec:** `docs/superpowers/specs/2026-08-21-samla-v3-booking-design.md`

## Global Constraints

- All v1/v2 Global Constraints apply (bun only; `m.*` en+nb parity; auth server-side; `import * as z from 'zod'`; request helpers from `@tanstack/start-server-core/request-response`; middleware factories in `rate-limit.middleware.ts` (add `'book'` action); no `cloudflare:workers` in `dist/client`; TDD; Co-Authored-By trailer).
- New error codes: `SLOT_UNAVAILABLE`, `HANDLE_TAKEN`, `SLUG_TAKEN`, `GOOGLE_NOT_CONNECTED`, `BOOKING_PAST`, `PAGE_PAUSED`.
- Limits: `slot_duration_min` 15..480; buffers 0..120; `min_notice_min` 0..10080 (default 120); `max_days_ahead` 1..365 (default 60); handle `^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$` (3..30); slug same pattern; title ≤ 200; description ≤ 2000; location ≤ 500; visitor note ≤ 1000; availability JSON ≤ 20 ranges/day, ranges `HH:mm` 15-minute aligned, start < end, non-overlapping; date_overrides ≤ 366 entries.
- Timestamps UTC ISO text; all tz math via `@date-fns/tz` / `Intl` (no manual offsets).
- Google API calls only in `src/server/google/calendar.ts`; every failure degrades (booking still saved; organiser notified by email on sync failure).
- v1/v2 behaviour unchanged.

---

### Task 1: Schema, migration, error codes, zod schemas, availability generator

**Files:** Modify `src/server/db/schema.ts` (tables `bookingPages`, `bookings`, relations; `user.handle` via `src/server/auth/auth.ts` + `auth.cli.ts` `additionalFields.handle` + regenerate `auth-schema.ts` → migration), `src/lib/errors.ts`, `src/server/http/rate-limit.middleware.ts` (`'book'`), create `src/server/bookings/schemas.ts`, `src/lib/availability.ts`, tests `src/lib/__tests__/availability.test.ts`, `src/server/bookings/__tests__/schemas.test.ts`, `src/server/db/__tests__/schema.workers.test.ts` (extend); `drizzle/0002_*.sql` via `bun run db:generate`.

**Interfaces (Produces):**

```ts
// schema.ts
export const bookingPages = sqliteTable('booking_pages', { id, ownerId→user cascade, slug, title, description, location, timezone, slotDurationMin, bufferBeforeMin, bufferAfterMin, minNoticeMin, maxDaysAhead, availability: text (JSON), dateOverrides: text (JSON, nullable), googleSync: bool default false, reminders: bool default true, status: text enum ['active','paused'] default 'active', createdAt, updatedAt, deletedAt }, unique(ownerId, slug))
export const bookings = sqliteTable('bookings', { id, pageId→bookingPages cascade, startAt, endAt, visitorName, visitorEmail, visitorNote, visitorLocale, visitorTimezone, status: enum ['confirmed','cancelled'] default 'confirmed', cancelledBy: enum ['visitor','organiser'] nullable, manageTokenHash, googleEventId, createdAt, updatedAt }, index(pageId, startAt))
// schemas.ts
export const handleSchema, slugSchema, timeRangeSchema ({ start:'HH:mm', end:'HH:mm' }), availabilitySchema (record weekday '0'..'6' → ranges[], validated 15-min aligned, ordered, non-overlapping), dateOverridesSchema (record 'YYYY-MM-DD' → ranges[] (empty = day off))
export const createBookingPageSchema = z.object({ slug, title, description?, location?, timezone, slotDurationMin, bufferBeforeMin, bufferAfterMin, minNoticeMin, maxDaysAhead, availability, dateOverrides?, googleSync, reminders })
export const updateBookingPageSchema = createBookingPageSchema.partial().extend({ pageId, status?: z.enum(['active','paused']) })
export const publicAvailabilityQuerySchema = z.object({ handle, slug, from: z.iso.date(), to: z.iso.date(), timezone })  // ≤ 62 days window
export const bookSlotSchema = z.object({ pageId, startAt: z.iso.datetime(), name (≤80), email (z.email), note?, timezone, turnstileToken? })
export const manageBookingSchema = z.object({ bookingId, token?: z.string() })   // owner path has no token
export const rescheduleSchema = manageBookingSchema.extend({ startAt: z.iso.datetime() })
// availability.ts (pure)
export type Interval = { start: string; end: string }  // UTC ISO
export type PageRules = { timezone, slotDurationMin, bufferBeforeMin, bufferAfterMin, minNoticeMin, maxDaysAhead, availability: Record<string, {start,end}[]>, dateOverrides: Record<string, {start,end}[]> | null }
export function generateSlots(rules: PageRules, opts: { from: Date; to: Date; now: Date; busy: Interval[] }): Interval[]
//  slots start on duration grid from each range start; slot [s, s+dur) allowed if within a range (override replaces weekly ranges for that local date), s ≥ now+minNotice, s+dur ≤ now+maxDaysAhead, and [s-bufferBefore, s+dur+bufferAfter) does not overlap any busy interval; DST-safe (TZDate per local date); deterministic, sorted, deduped.
export function isSlotAvailable(rules, startAt: string, opts): boolean
```

- [ ] Tests: availability (weekly ranges → slots; overrides off-day and extra day; buffers; notice; horizon; busy subtraction with partial overlap; DST spring-forward/fall-back Oslo; 15-min vs 60-min duration; empty availability → []); schemas (handle/slug rules, overlapping ranges rejected, window ≤ 62 days); workers schema insert/select + unique(ownerId, slug); regenerate auth schema with `handle`.
- [ ] Commit `feat(booking): schema, migration, zod schemas and availability generator`.

### Task 2: Booking pages + bookings services, Google Calendar client, emails

**Files:** Create `src/server/bookings/pages.ts`, `bookings.ts`, `viewmodel.ts`, `src/server/google/calendar.ts`, `src/server/bookings/emails.ts`, `emails/BookingConfirmed.tsx`, `BookingCancelled.tsx`, `BookingReminder.tsx`, `BookingOrganiserNotice.tsx`, tests `src/server/bookings/__tests__/{pages,bookings}.workers.test.ts`, `src/server/google/__tests__/calendar.workers.test.ts` (msw), `emails/__tests__/templates.test.tsx` (extend), `test/helpers.ts` (`makeBookingPage`, `makeBooking`); messages.

**Interfaces (Produces):**

```ts
// pages.ts
createPage(db, ownerId, input) → { id }  (SLUG_TAKEN); updatePage(db, pageId, ownerId, input) (NOT_FOUND/FORBIDDEN/SLUG_TAKEN); deletePage (soft); listMyPages(db, ownerId) → PageSummary[] (upcomingCount); getOwnedPage(db, pageId, ownerId) → PageView; getPublicPage(db, handle, slug) → PublicPageView | null (active only; owner { name }); setUserHandle(db, userId, handle) (HANDLE_TAKEN; via drizzle update on user)
// bookings.ts
listBookings(db, pageId, ownerId, { from, to }) → BookingView[] (visitor fields visible to owner); getBookingForManage(db, bookingId, token | { ownerId }) → BookingView + page summary (INVALID_TOKEN/FORBIDDEN); createBooking(db, pageId, input) → { bookingId, manageToken } (PAGE_PAUSED; BOOKING_PAST; SLOT_UNAVAILABLE if not in generateSlots(busyFromDb ∪ googleBusy) — busy intervals passed in by the caller); cancelBooking(db, bookingId, by: 'visitor'|'organiser') ; rescheduleBooking(db, bookingId, newStartAt, busy) → atomically cancel+create (same manage token kept); helper bookedIntervals(db, pageId, range) (confirmed only, with buffers)
// viewmodel.ts: PageSummary, PageView (owner), PublicPageView (no ids/emails), BookingView
// google/calendar.ts  (fetch only; injectable fetch for tests)
getFreeBusy(accessToken, { timeMin, timeMax }) → Interval[]; createEvent(accessToken, { summary, description, start, end, attendeeEmail, timezone }) → { eventId }; deleteEvent(accessToken, eventId); getGoogleAccessToken(userId) → string | null via getAuth().api.getAccessToken({ body: { providerId: 'google', userId } }) (null when not connected/missing scope)
// emails.ts: renderBookingConfirmed (visitor; manage link; ics), renderBookingOrganiserNotice, renderBookingCancelled (both), renderBookingReminder; sendBookingEmails(env, kind, bookingId) best-effort
```

- [ ] Tests for every rule; msw for Google (freebusy parse; create returns id; delete; 401 → degrade). Commit `feat(booking): pages/bookings services, Google Calendar client and emails`.

### Task 3: BookingRoom DO, server functions, routes (ws, ics), test seed

**Files:** Create `src/do/BookingRoom.ts` (+tests), `src/server/bookings/*.functions.ts` (pages.functions.ts, bookings.functions.ts with SERVER_FN_MIDDLEWARE manifests), `src/server/notifications/booking-client.ts`, routes `src/routes/api/bookings/$pageId/ws.ts`, `src/routes/booking/$id/calendar[.]ics.ts`; modify `src/server.ts` + `test/worker.ts` (export `BookingRoom`), `wrangler.jsonc` + `test/wrangler.test.jsonc` (`BOOKING_ROOM` binding + migration tag v2 `new_sqlite_classes: ['BookingRoom']`), `src/routes/api/test/seed.ts` (`withBookingPage`), `test/server-functions.workers.test.ts`.

**Interfaces:** `BookingRoom.book(pageId, input, busy)`, `cancel(pageId, bookingId, by)`, `reschedule(pageId, bookingId, startAt, busy)` (all serialised via promise-chain mutex; write through services; broadcast `{type:'page.changed'}`); `scheduleReminder(bookingId, startAt)` / `cancelReminder(bookingId)` (storage keys `reminder:<id>`; alarm = earliest; on fire send reminder emails for due bookings still confirmed, owner prefs `reminders`); server fns: `createBookingPage`, `updateBookingPage`, `deleteBookingPage`, `listMyBookingPages`, `getBookingPage`, `setHandle`, `getPublicAvailability` (GET: computes busy = booked ∪ google (if googleSync && token) then generateSlots; returns `{ page: PublicPageView, slots: Interval[] }`), `bookSlot` (POST; [sessionMiddleware, rateLimitMiddleware('book')]; Turnstile always; busy as above; `BOOKING_ROOM.book`; emails; google event create (store eventId) best-effort), `getManagedBooking` (token or owner), `cancelBooking`, `rescheduleBooking`, `listPageBookings` (owner), `connectGoogleCalendar` returns the linkSocial URL params? → ruling: client calls `authClient.linkSocial({ provider:'google', scopes:['https://www.googleapis.com/auth/calendar.events','https://www.googleapis.com/auth/calendar.readonly'], callbackURL })`; server fn `getGoogleCalendarStatus` → `{ connected: boolean }` (token probe); `disconnectGoogleCalendar` sets `googleSync=false` on pages (account unlink is via settings).

- [ ] Tests: DO double-book race; reminder alarm; manifests; ics route; seed; Commit `feat(booking): BookingRoom DO, server functions, ws/ics routes`.

### Task 4: Organiser UI — pages list, editor, bookings view, handle in settings

**Files:** routes `src/routes/bookings/index.tsx`, `new.tsx`, `$id/index.tsx`, `$id/edit.tsx`; components `src/components/booking/{PageEditor,AvailabilityEditor,DateOverridesEditor,PageCard,BookingsTable,GoogleCalendarCard,HandleField}.tsx`, `src/components/booking/editor-state.ts` (+tests), settings page handle field; dashboard link "Booking pages"; messages.

- [ ] Tests: editor-state reducer (ranges add/remove/copy-to-all, validation), AvailabilityEditor interactions, HandleField validation; runtime SSR checks. Commit `feat(booking): organiser pages, availability editor and bookings view`.

### Task 5: Public booking page + manage page

**Files:** routes `src/routes/book/$handle/$slug.tsx`, `src/routes/booking/$id/index.tsx`; components `src/components/booking/{MonthPicker,SlotList,BookingForm,BookingConfirmed,ManageBooking,RescheduleDialog}.tsx`, `src/lib/use-live-page.ts` (reuse use-live-poll pattern with the bookings ws route); messages.

- [ ] Tests: MonthPicker (available days), SlotList (tz rendering), BookingForm validation; runtime checks. Commit `feat(booking): public booking page and manage page`.

### Task 6: E2E, README, final verification

- `e2e/booking.spec.ts` (seed page → visitor books → confirmation → manage cancel → slot reappears live in another context); screenshots spec add; README (v3 features, roadmap done, config for Google scopes); full verification. Commit `test(e2e): booking journey; docs: v3 in README`.

## Self-review notes

Spec decisions 1–9 map to T1 (1,2,6 schema), T2 (2,3,7), T3 (4,5), T4/T5 (8), T6 (tests/docs). Shared names: `generateSlots`, `Interval`, `PageRules`, `BookingRoom`, `bookSlotSchema`, `PublicPageView`.

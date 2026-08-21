# samla — v3 design spec: 1:1 booking pages

Date: 2026-08-21 · Builds on v1 + v2. The v1 spec reserved v3 for "1:1 booking pages (Calendly-style: organiser sets availability, a visitor books a single slot)". Anders asked that phases continue automatically; decisions below are controller rulings recorded for his review.

## Goal

Let a signed-in organiser publish a personal **booking page** (`/book/<handle>/<slug>`): weekly availability rules + duration + buffers, optionally synced with their Google Calendar for conflicts and event creation; visitors pick a slot in their own time zone and book with name + email; both sides get confirmation emails with .ics; bookings can be cancelled/rescheduled via a secure link. Same low-friction, live-updating, localized experience as polls and sheets.

## Decisions (rulings)

1. **Model:** new tables (not polls): `booking_pages` (one per organiser per slug) and `bookings`. A booking page is not a poll — the poll/participant machinery stays untouched. v1/v2 behaviour unchanged.
2. **Availability:** weekly rules in the organiser's time zone (`Mon 09:00–12:00, 13:00–17:00`, …) stored as JSON (`availability: { [weekday 0-6]: Array<{ start: 'HH:mm', end: 'HH:mm' }> }`), `slot_duration_min` (15..480), `buffer_before_min`/`buffer_after_min` (0..120), `min_notice_min` (0..10080; default 120), `max_days_ahead` (1..365; default 60), optional `date_overrides` JSON (`{ 'YYYY-MM-DD': [] | [{start,end}] }`) for days off / extra hours. Slots are generated on the fly (`lib/availability.ts`), subtracting existing bookings (+buffers) and, when connected, busy intervals from Google Calendar FreeBusy.
3. **Google Calendar (optional):** uses the existing Better-Auth Google social account with extra scopes `https://www.googleapis.com/auth/calendar.events` + `calendar.freebusy`? → ruling: request `calendar` scope incrementally via Better-Auth `linkSocial`/`requestAdditionalScopes`-style flow (Better-Auth 1.7 supports `authClient.linkSocial({ provider:'google', scopes:[...] })`); tokens read via `auth.api.getAccessToken({ providerId:'google' })` (auto-refresh). When connected: FreeBusy query over the candidate range when generating slots; on booking create a Google Calendar event (with attendee = visitor email, Google Meet conference optional → ruling: no Meet in v3); on cancel delete it. Failures degrade gracefully (booking still recorded; organiser emailed).
4. **Booking identity:** visitor provides name + email (required) + optional note; Turnstile for everyone (visitors are guests); rate-limited. Booking gets a `manage_token` (hashed) emailed as a manage link (`/booking/<id>?t=…`) for cancel/reschedule. Organiser manages via dashboard ("Bookings" tab) with owner auth.
5. **Atomicity:** bookings for one page are serialised through a Durable Object `BookingRoom` (per booking page) — same pattern as `PollRoom` claims (mutex + D1 write inside the DO): prevents double-booking of the same slot. The DO also broadcasts `page.changed` to open booking pages (live slot removal) and hosts reminder alarms (email reminder 24 h before, to both parties, opt-out per page).
6. **Handles:** organiser picks a `handle` (unique, 3..30, `[a-z0-9-]`) stored on `user.handle` (Better-Auth additionalField); booking page slug unique per user. Public URL `/book/<handle>/<slug>`.
7. **Emails:** visitor confirmation (with .ics + manage link), organiser notification (with .ics), cancellation (both), reschedule (both), reminder (both, 24 h before). Localized by each party's locale (visitor: page locale / request locale; organiser: user.locale).
8. **UI:** `/bookings/new` + `/bookings/:id/edit` (page editor: title, description, location/meeting link or "ask visitor", duration, buffers, notice, horizon, weekly availability grid editor, date overrides, Google Calendar connect toggle, reminders toggle, slug), dashboard "Booking pages" section listing pages with upcoming counts, `/bookings/:id` (organiser view: upcoming/past bookings, cancel), public `/book/<handle>/<slug>` (month/week calendar of available days in the visitor's tz, slot list per day, booking form, confirmation state with add-to-calendar), `/booking/<bookingId>?t=` (manage: details, cancel, reschedule → picks a new slot on the same page).
9. **Out of scope (v3):** group/round-robin pages, payments, Meet/Zoom links generation, multiple calendars, Outlook/CalDAV, recurring bookings, custom questions beyond the note.

## Data model

- `user.handle` (text, unique, nullable) — Better-Auth additional field; settings page gets a handle field.
- `booking_pages`: `id` (nanoid 12), `owner_id`, `slug`, `title`, `description?`, `location?` (free text or URL), `timezone`, `slot_duration_min`, `buffer_before_min`, `buffer_after_min`, `min_notice_min`, `max_days_ahead`, `availability` (JSON text), `date_overrides` (JSON text, nullable), `google_sync` (bool), `reminders` (bool), `status` (`active|paused`), `created_at`, `updated_at`, `deleted_at?`; unique `(owner_id, slug)`.
- `bookings`: `id` (nanoid 12), `page_id`, `start_at` (UTC ISO), `end_at`, `visitor_name`, `visitor_email`, `visitor_note?`, `visitor_locale?`, `visitor_timezone`, `status` (`confirmed|cancelled`), `cancelled_by?` (`visitor|organiser`), `manage_token_hash`, `google_event_id?`, `created_at`, `updated_at`; index `(page_id, start_at)`.
- Migration `0002_*`.

## Services / API

- `lib/availability.ts` (pure): `generateSlots({ page, from, to, now, busy: Interval[] })` → `Array<{ startAt, endAt }>` honouring rules, overrides, duration, buffers, notice, horizon, DST via `@date-fns/tz`; `intervalsOverlap`; tested heavily.
- `server/bookings/pages.ts`: CRUD for pages (owner), slug validation/uniqueness, `getPublicPage(handle, slug)`; `server/bookings/bookings.ts`: `createBooking` (inside DO), `cancelBooking`, `rescheduleBooking` (cancel+create atomically), `listBookings(pageId, range)`, `getBookingForManage(id, token)`; `server/google/calendar.ts`: `getBusy(accessToken, range)`, `createEvent`, `deleteEvent` (fetch to Google APIs; mocked with msw in tests); `server/bookings/emails.ts`.
- `do/BookingRoom.ts`: `book(pageId, input)`, `cancel`, `reschedule` (serialised), broadcast, reminder alarms (`reminder:<bookingId>` keys → alarm at start−24h; one alarm re-armed to the earliest).
- Server fns: `createBookingPage`, `updateBookingPage`, `deleteBookingPage`, `listMyBookingPages`, `getBookingPage`, `getPublicAvailability(handle, slug, monthISO, timezone)` (GET; returns slots grouped by local day), `bookSlot` (POST; Turnstile; rate-limit 'book'), `cancelBooking(token|owner)`, `rescheduleBooking(token|owner)`, `listPageBookings`, `connectGoogleCalendar` (via authClient.linkSocial scopes) / `disconnectGoogleCalendar`.
- Routes: `/bookings`, `/bookings/new`, `/bookings/$id`, `/bookings/$id/edit`, `/book/$handle/$slug`, `/booking/$id`, `/booking/$id/calendar.ics`, `/api/bookings/$pageId/ws` (DO WS).
- Error codes: `SLOT_UNAVAILABLE`, `HANDLE_TAKEN`, `SLUG_TAKEN`, `GOOGLE_NOT_CONNECTED`, `BOOKING_PAST`.

## Testing

Unit: availability generator (DST, buffers, notice, overrides, busy subtraction), slug/handle validation, ics for bookings. Workers: pages CRUD + uniqueness, bookings create/cancel/reschedule, DO double-booking race (two `book` for the same slot → one `SLOT_UNAVAILABLE`), reminders alarm, Google client with msw (freebusy/create/delete + failure degradation), server-fn middleware manifest, manage-token auth. E2E: create page via UI → public page as visitor → book a slot → confirmation → manage link cancel → slot reappears live on another context.

## UI polish

Visitor calendar: month grid with available days highlighted, slot chips per day, tz switch, `motion` transitions, confetti on booking confirmation; organiser availability editor: weekday rows with time-range chips, copy-to-all, date override picker; everything localized (en + nb).

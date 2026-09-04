# Go Rewrite Plan 5/8 — Bookings API

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Calendly-style booking pages: availability computation, double-book-proof slot booking, manage links (cancel/reschedule), the full booking mail set, reminders, and optional Google Calendar sync.

**Architecture:** `internal/bookings` ports `src/server/bookings/*` — each task names its TS source; read it first. Slot booking uses the same tx + `SELECT ... FOR UPDATE` discipline proven in plan 4, with its own racing test. Google sync ports `src/server/google/calendar.ts` behind an interface so tests stub HTTP, and degrades to off without config.

**Tech Stack:** sqlc (extend plan 4's setup with `internal/bookings/queries`), `golang.org/x/oauth2` (google token refresh), stdlib.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§2, §4-atomicity, §5)

## Global Constraints

Plan 1's Global Constraints apply, plus plan 4's envelope/captcha/Emit rules.
- All slot math is done in the page's IANA timezone via `time.LoadLocation` (tzdata is embedded — plan 1). Availability rules (`slot_duration_min`, buffers, `min_notice_min`, `max_days_ahead`, weekly `availability` jsonb, `date_overrides` jsonb) port exactly from `pageRulesFrom` in `src/server/bookings/bookings.ts`.

---

### Task 1: Migration 00004 + booking queries

**Files:**
- Create: `migrations/00004_bookings.sql`, `internal/bookings/queries/bookings.sql`
- Test: extend `internal/db/db_test.go` table list.

- [ ] **Step 1:** Transcribe `booking_pages` + `bookings` from `drizzle/0000_orange_zzzax.sql` with the §6 re-cut (`timestamptz` for start/end/created/updated/deleted, `availability`/`date_overrides` → `jsonb`), keeping the partial unique index `booking_pages_org_slug_uidx ... WHERE deleted_at IS NULL` and `bookings_page_start_idx`. Add sqlc config block for `internal/bookings/queries`.
- [ ] **Step 2:** `sqlc generate`; `go test ./internal/db/` green. **Commit** — `git commit -m "feat(bookings): schema re-cut and query scaffolding"`

---

### Task 2: Pages service

**Source to port:** `src/server/bookings/pages.ts` + `pages.functions.ts` validation (`schemas.ts`), `viewmodel.ts` shapes.

**Files:**
- Create: `internal/bookings/pages.go`
- Test: `internal/bookings/pages_test.go`

**Interfaces:**

```go
package bookings

type Service struct { /* db, q, mailEnqueue, googleSync GoogleSync (nil-able) */ }
func NewService(sqlDB *sql.DB) *Service

type PageInput struct { /* schemas.ts pageSchema port; Validate() error — duration 5..480, buffers >=0, availability weekday windows well-formed, timezone must LoadLocation */ }
func (s *Service) CreatePage(ctx, orgID, userID string, in PageInput) (*PageView, error)
func (s *Service) UpdatePage(ctx, pageID, orgID string, in PageInput) (*PageView, error)
func (s *Service) DeletePage(ctx, pageID, orgID string) error       // soft delete
func (s *Service) ListMyPages(ctx, orgID string) ([]PageSummary, error)
func (s *Service) GetOwnedPage(ctx, pageID, orgID string) (*PageView, error)
func (s *Service) GetPublicPage(ctx, orgSlug, pageSlug string) (*PublicPageView, error) // hides internals; 404 on deleted/paused per pages.ts
func (s *Service) SetOrgSlug(ctx, orgID, slug string) error         // the public /book/{org}/{page} handle
```

- [ ] Steps: failing tests ported from `pages.workers.test.ts` (slug uniqueness within org incl. soft-delete resurrection, wrong-org forbidden, public view field hiding) → implement → green → commit `feat(bookings): booking pages service`.

---

### Task 3: Availability + booking with the double-book proof

**Source to port:** `src/server/bookings/bookings.ts` (`pageRulesFrom`, `bookedIntervals`, `createBooking`, `cancelBooking`, `rescheduleBooking`, `getBookingForManage`) — carry its rule comments.

**Files:**
- Create: `internal/bookings/availability.go`, `internal/bookings/bookings.go`
- Test: `internal/bookings/availability_test.go`, `internal/bookings/bookings_test.go`

**Interfaces:**

```go
// Slots returns bookable start times between from/to in the page's timezone:
// weekly windows ∩ date overrides, minus booked intervals (+buffers), minus min-notice,
// capped at max_days_ahead. Pure function over inputs — no DB — so it tests exhaustively.
func Slots(rules PageRules, booked []Interval, now time.Time, from, to time.Time) []time.Time

func (s *Service) PublicAvailability(ctx, orgSlug, pageSlug string, from, to time.Time) ([]time.Time, error)
// Book: tx → SELECT the page FOR UPDATE (serializes per page) → recompute the slot's validity
// from live bookedIntervals → insert or ErrSlotTaken. Returns manage token (plaintext once;
// only its sha256 hash is stored — port manage_token_hash semantics).
func (s *Service) Book(ctx, orgSlug, pageSlug string, in BookInput) (*BookingResult, error)
func (s *Service) ManagedBooking(ctx, bookingID, manageToken string) (*ManagedBookingView, error)
func (s *Service) Cancel(ctx, bookingID, manageToken string, byOrganiser bool) error
func (s *Service) Reschedule(ctx, bookingID, manageToken string, newStart time.Time) (*BookingResult, error)
func (s *Service) ListPageBookings(ctx, pageID, orgID string, from, to time.Time) ([]BookingView, error)
```

- [ ] **Step 1: Failing `Slots` table tests** — port every case from `bookings.workers.test.ts`'s availability coverage: DST transition days in `Europe/Oslo`, overrides removing/adding windows, buffer collisions, min-notice trimming *now*, max-days-ahead horizon.
- [ ] **Step 2: The racing test** (same shape as plan 4's claim proof): 16 goroutines `Book` the same slot → exactly one winner, others `ErrSlotTaken`; DB has one row. Run with `-race -count 5`.
- [ ] **Step 3: Failing manage-flow tests** — wrong token → not found (port: token compare is against sha256 hash); cancel is idempotent-safe; reschedule frees the old interval and re-validates the new one atomically.
- [ ] **Step 4: Implement; every mutation emits `rooms.Emit(tx, "booking:"+pageID, ...)` with event names from `src/do/booking-protocol.ts`. Step 5: green. Step 6: commit** — `git commit -m "feat(bookings): availability engine and double-book-proof booking"`

---

### Task 4: Booking mail + reminders

**Source to port:** `src/server/bookings/emails.ts` (the kind→template map and ids-only re-read rule), reminder arming from `src/do/BookingRoom.ts`.

**Files:**
- Create: `internal/bookings/emails.go`
- Test: `internal/bookings/emails_test.go`

**Interfaces:**

```go
// RegisterJobs wires: "mail:booking" {kind, bookingId} → re-read booking (skip if cancelled-since,
// per queue.ts rationale) → render booking_confirmed / booking_cancelled / booking_rescheduled(+organiser)
// / booking_organiser_notice / booking_reminder with .ics attachment (reuse plan 4's ics writer style
// in internal/bookings/ics.go — VEVENT from booking start/end/location) → mailer.Send.
// "booking.reminder" (roomKey booking:<bookingId>) scheduled at start-24h when reminders enabled;
// cancel/reschedule re-arms or cancels it.
func (s *Service) RegisterJobs(w *jobs.Worker, m *mailer.Mailer)
```

- [ ] Steps: failing tests (booking → confirmation jobs enqueued for visitor+organiser with ids-only payloads; cancelled booking's queued mail becomes a no-op at run time; reminder job re-arms on reschedule and cancels on cancel) → implement → green → commit `feat(bookings): booking mail set and reminders`.

---

### Task 5: Google Calendar sync

**Source to port:** `src/server/google/calendar.ts` + `src/server/bookings/google-sync.ts` (busy-check + event insert/delete, token refresh, `booking_sync_failed` mail on hard failure).

**Files:**
- Create: `internal/bookings/google.go`
- Test: `internal/bookings/google_test.go`

**Interfaces:**

```go
type GoogleSync interface {
    Busy(ctx context.Context, userID string, from, to time.Time) ([]Interval, error)
    InsertEvent(ctx context.Context, userID string, b *BookingView) (eventID string, err error)
    DeleteEvent(ctx context.Context, userID, eventID string) error
}
// NewGoogleSync returns nil when !cfg.Capabilities.Google — callers treat nil as "sync off".
// Tokens come from Limen's oauth account storage: read the google account row (access/refresh token)
// via a small query against Limen's accounts table; refresh with golang.org/x/oauth2 when expired
// and write back. Base URL injectable for tests.
func NewGoogleSync(cfg *config.Config, sqlDB *sql.DB) GoogleSync
```

- Availability subtracts `Busy` intervals when the page has `google_sync`; `Book` inserts the event post-commit via a `"google:sync"` job (never inline — an API stall must not fail a booking); failures follow the TS fallback: booking stands, organiser gets `booking_sync_failed` mail.
- [ ] Steps: failing tests with `httptest` Google API stub (busy merge, insert stores `google_event_id`, refresh-on-401-then-retry, hard failure → sync-failed mail job) → implement → green → commit `feat(bookings): optional google calendar sync`.

---

### Task 6: HTTP handlers — the booking API surface

**Files:**
- Create: `internal/bookings/handlers.go`
- Test: `internal/bookings/handlers_test.go`

**Interfaces:** `func (s *Service) Register(mux *http.ServeMux, a Auth, cfg *config.Config)` mounting (frontend contract):

```
POST   /api/v1/booking-pages                          auth      → CreatePage
GET    /api/v1/booking-pages                          auth      → ListMyPages
GET    /api/v1/booking-pages/{id}                     auth+org  → GetOwnedPage
PATCH  /api/v1/booking-pages/{id}                     auth+org  → UpdatePage
DELETE /api/v1/booking-pages/{id}                     auth+org  → DeletePage
GET    /api/v1/booking-pages/{id}/bookings            auth+org  → ListPageBookings (?from&to RFC3339)
POST   /api/v1/org/handle                             auth      → SetOrgSlug
GET    /api/v1/book/{org}/{page}                      public    → GetPublicPage
GET    /api/v1/book/{org}/{page}/availability         public    → PublicAvailability (?from&to)
POST   /api/v1/book/{org}/{page}/bookings             public+captcha → Book (201 {booking, manageToken}; 409 slot_taken)
GET    /api/v1/bookings/{id}/manage                   public(token) → ManagedBooking (?token=)
POST   /api/v1/bookings/{id}/cancel                   public(token)|auth+org → Cancel
POST   /api/v1/bookings/{id}/reschedule               public(token) → Reschedule
GET    /api/v1/booking-pages/{id}/google-status       auth      → connected? (nil GoogleSync → {"available":false})
POST   /api/v1/me/google/disconnect                   auth      → disconnectGoogleSync port
```

- [ ] Steps: failing handler tests per row (status codes, envelope codes incl. `slot_taken`, manage-token auth, captcha) → implement thin handlers → green → wire `Register` into httpserver + serve() → `go test ./...` green → commit `feat(bookings): http surface for booking pages`.

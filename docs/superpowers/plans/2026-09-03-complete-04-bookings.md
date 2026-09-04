# Completion Plan D — Bookings

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every bookings-area finding from the 2026-09-03 audit on `feat/go-rewrite`: live `page.changed` on organiser edits, one mail job per recipient, locale-aware booking mail for both parties, the `.ics` organiser-session fallback, strict PATCH validation, split public rate-limit buckets, a single-query page list, the two dormant-code test gaps, and the fixed user decision "Google Calendar sync: DISABLE for now" carried through Go, web and README.

**Architecture:** All Go changes live in `internal/bookings` (service + thin handlers over `internal/httpserver` helpers; jobs via `internal/jobs`; realtime via `rooms.Emit` inside the same transaction as the write). The mail pipeline gets one pure composition seam (`composeBookingMail`, exposed to tests through `export_test.go`) so recipient/locale behaviour is asserted without SMTP. Web changes are small edits to `web/src/api/bookings.ts` and the booking components, with vitest + msw. Google sync code stays in the tree but dormant: the SPA never shows it, the API refuses `googleSync: true`, status is a constant.

**Tech Stack:** Go 1.26 (`go`, `sqlc`, `golangci-lint` in `~/go/bin`), Postgres via `internal/testdb` (testcontainers), Mailpit testcontainer for real-send tests, React 19 + TanStack Router, vitest + Testing Library + msw, paraglide messages.

**Spec:** `docs/superpowers/specs/2026-09-01-go-rewrite-design.md` (§2 booking pages, §4 atomicity/Emit, §5 mail localisation); earlier plan for conventions: `docs/superpowers/plans/2026-09-01-go-rewrite-05-bookings.md`; findings: `scratchpad/briefs/findings-full.txt` (titles quoted per task).

## Global Constraints

- **User decision (fixed):** Google Calendar sync is DISABLED for now. Hide the UI, status endpoint answers "not connected" unconditionally, README says not yet available, do NOT build a custom consent flow, KEEP the Go sync code (`internal/bookings/google.go`) dormant and tested.
- **User decision (fixed):** Email locale is RESTORED — visitor locale captured on booking, organiser locale per user, nb mail renders, dates in mail are locale-aware.
- **Still dropped by design (never reintroduce):** passkeys, billing, magic links, TOTP 2FA, staff impersonation, SSR/OG tags, web push, booking-page follower notifications.
- **Consumed from Plan A (exact names, exist before this plan runs):** `func (s *auth.Service) LocaleFor(ctx context.Context, userID string) string` ("en" fallback); `mailer.SupportedLocales = []string{"en","nb"}`; `mailer.FormatDateTime(locale string, t time.Time, loc *time.Location) string` (en `Mon 1 Sep, 18:30`, nb `man. 1. sep., 18:30`); `mailer.FormatDate(locale, t, loc)` (en `Tue 1 Sep`, nb `tir. 1. sep.`); `mailer.FormatTimeRange(locale, start, end, loc)` (`18:30–19:30`); the web `bookSlot` request body carries `locale: getLocale()`.
- **No migrations in this plan.** After any change to `internal/bookings/queries/bookings.sql` run `sqlc generate` (root `sqlc.yaml`) and commit the regenerated `internal/bookings/queries/*.go`.
- **Conventions:** TDD (failing test → run → implement → run → commit). Go tests use `internal/testdb` (live Postgres; `testdb.New(t)` gives an isolated DB per test). Handler tests use the `fakeAuth`/`doRequest`/`setupHandlerPage` helpers already in `internal/bookings/handlers_test.go`. Web tests: vitest + Testing Library; HTTP mocked with msw (`web/src/api/__tests__/client.test.ts` is the pattern).
- **Error envelope** `{"error":{"code","message","fields"?}}`; codes snake_case. Every new user-facing string goes into BOTH `web/messages/en.json` and `web/messages/nb.json` (this plan adds none; it removes orphaned ones from both).
- **Commit messages:** conventional (`fix(bookings): …`) ending with exactly these two trailer lines:
  ```
  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p
  ```
- **Gates before this plan is declared done:** `go build ./... && go vet ./... && golangci-lint run ./... && go test ./...`; `cd web && bun run typecheck && bun run lint && bunx vitest run`. (Playwright is Plan F.)
- All paths below are relative to the repo root `/home/anders/projects/refsdal/whenweall`. Run Go commands from the root; run `bunx vitest run …` from `web/`.

---

### Task 1: "Add to calendar" on the confirmation card links to the real `.ics` endpoint

Finding: *"'Add to calendar' on the post-booking confirmation card links to a dead path and downloads index.html"* — `BookingConfirmed.tsx:115` still hard-codes `/booking/{id}/calendar.ics?t=`, a path the SPA handler answers with `index.html`. `ManageBooking.tsx` already uses the `bookingCalendarIcsUrl` helper.

**Files:**
- Modify: `web/src/components/booking/BookingConfirmed.tsx` (imports at lines 1-8, the `<a href=…>` at line 115)
- Create: `web/src/components/booking/__tests__/BookingConfirmed.test.tsx`

**Interfaces:**
- Consumes: `bookingCalendarIcsUrl(bookingId: string, token?: string): string` from `web/src/api/bookings.ts` (exists).
- Produces: nothing new.

- [ ] **Step 1: Write the failing test**

Create `web/src/components/booking/__tests__/BookingConfirmed.test.tsx`:

```tsx
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { createRootRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import { BookingConfirmed } from '#/components/booking/BookingConfirmed'

afterEach(() => cleanup())

/** `Link` needs a router in context, so the card is rendered inside a minimal one (the same
 * shape `components/dashboard/__tests__/PollCard.test.tsx` uses). */
async function renderConfirmed() {
  const rootRoute = createRootRoute({
    component: () => (
      <BookingConfirmed
        bookingId="bk_1"
        manageToken="tok/with+chars"
        title="Intro call"
        location={null}
        slot={{ start: '2026-09-15T07:00:00.000Z', end: '2026-09-15T07:30:00.000Z' }}
        timeZone="Europe/Oslo"
        email="ada@example.com"
        onBookAnother={vi.fn()}
      />
    ),
  })
  const router = createRouter({ routeTree: rootRoute })
  render(<RouterProvider router={router} />)
  await screen.findByTestId('booking-confirmed')
}

describe('BookingConfirmed', () => {
  it('links "Add to calendar" at the Go .ics endpoint, carrying the manage token', async () => {
    await renderConfirmed()

    const link = screen.getByRole('link', { name: /add to calendar/i })
    expect(link).toHaveAttribute(
      'href',
      '/api/v1/bookings/bk_1/calendar.ics?t=' + encodeURIComponent('tok/with+chars'),
    )
  })
})
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd web && bunx vitest run src/components/booking/__tests__/BookingConfirmed.test.tsx`
Expected: FAIL — `expected … to have attribute "href" … received "/booking/bk_1/calendar.ics?t=tok%2Fwith%2Bchars"`.

- [ ] **Step 3: Use the helper**

In `web/src/components/booking/BookingConfirmed.tsx` add the import (after the `slotSummary` import on line 8):

```ts
import { bookingCalendarIcsUrl } from '#/api/bookings'
```

and replace the anchor's `href` (line 115):

```tsx
          <a href={bookingCalendarIcsUrl(bookingId, manageToken)} download>
```

(the old line was `href={`/booking/${bookingId}/calendar.ics?t=${encodeURIComponent(manageToken)}`}`).

- [ ] **Step 4: Run the test and the web gates**

Run: `cd web && bunx vitest run src/components/booking/__tests__/BookingConfirmed.test.tsx && bun run typecheck && bun run lint`
Expected: `1 passed`; typecheck and lint clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/booking/BookingConfirmed.tsx web/src/components/booking/__tests__/BookingConfirmed.test.tsx
git commit -m "fix(web): point the post-booking \"Add to calendar\" link at the .ics API

The confirmation card still linked /booking/{id}/calendar.ics, a path the SPA
handler answers with index.html. Use bookingCalendarIcsUrl like ManageBooking.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 2: Unknown `/book/{handle}/{slug}` renders the not-found card

Finding: *"Unknown /book/{handle}/{slug} shows the generic error card instead of BookNotFound — getPublicPage never returns null"* — `api()` throws `ApiError('not_found')` on the 404, so the route loader's `if (!result) throw notFound()` (`web/src/routes/book/$handle/$slug.tsx:82`) is unreachable and `RescheduleDialog`'s fetch falls into its generic catch.

**Files:**
- Modify: `web/src/api/bookings.ts` (import on line 2; `getPublicPage`/`getPublicAvailability` at lines 197-226)
- Create: `web/src/api/__tests__/bookings.test.ts`

**Interfaces:**
- Consumes: `ApiError` (`web/src/api/client.ts`, exported; `.code` is the envelope code).
- Produces: `getPublicPage(handle, slug): Promise<PublicPageView | null>` and `getPublicAvailability(input): Promise<{page, slots} | null>` now really resolve `null` on `not_found` (callers in `routes/book/$handle/$slug.tsx` and `RescheduleDialog.tsx` already handle `null`). Later tasks (4, 9) add more `describe` blocks to this same test file.

- [ ] **Step 1: Write the failing test**

Create `web/src/api/__tests__/bookings.test.ts`:

```ts
import { afterAll, afterEach, beforeAll, describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { setupServer } from 'msw/node'
import { ApiError } from '#/api/client'
import { getPublicAvailability, getPublicPage } from '#/api/bookings'

const server = setupServer()

beforeAll(() => server.listen({ onUnhandledRequest: 'error' }))
afterEach(() => server.resetHandlers())
afterAll(() => server.close())

const notFound = () =>
  HttpResponse.json({ error: { code: 'not_found', message: 'not found' } }, { status: 404 })

describe('getPublicPage', () => {
  it('resolves null for an unknown handle/slug (404 not_found), so the route can throw notFound()', async () => {
    server.use(http.get('/api/v1/book/ada/missing', notFound))

    expect(await getPublicPage('ada', 'missing')).toBeNull()
  })

  it('still throws every other ApiError', async () => {
    server.use(
      http.get('/api/v1/book/ada/intro', () =>
        HttpResponse.json({ error: { code: 'rate_limited', message: 'slow down' } }, { status: 429 }),
      ),
    )

    await expect(getPublicPage('ada', 'intro')).rejects.toBeInstanceOf(ApiError)
  })
})

describe('getPublicAvailability', () => {
  it('resolves null for an unknown page even though the availability call 404s too', async () => {
    server.use(
      http.get('/api/v1/book/ada/missing', notFound),
      http.get('/api/v1/book/ada/missing/availability', notFound),
    )

    expect(
      await getPublicAvailability({ handle: 'ada', slug: 'missing', from: '2026-09-01', to: '2026-10-02' }),
    ).toBeNull()
  })

  it('pairs each slot start with an end computed from slotDurationMin', async () => {
    server.use(
      http.get('/api/v1/book/ada/intro', () =>
        HttpResponse.json({
          id: 'pg_1', handle: 'ada', slug: 'intro', title: 'Intro', description: null, location: null,
          timezone: 'UTC', slotDurationMin: 30, maxDaysAhead: 60, status: 'active', owner: { name: 'Ada' },
        }),
      ),
      http.get('/api/v1/book/ada/intro/availability', () =>
        HttpResponse.json({ slots: ['2026-09-15T07:00:00.000Z'] }),
      ),
    )

    const result = await getPublicAvailability({ handle: 'ada', slug: 'intro', from: '2026-09-01', to: '2026-10-02' })
    expect(result?.slots).toEqual([
      { start: '2026-09-15T07:00:00.000Z', end: '2026-09-15T07:30:00.000Z' },
    ])
  })
})
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd web && bunx vitest run src/api/__tests__/bookings.test.ts`
Expected: the two `null` cases FAIL with `ApiError: not found` rejected; the other two pass.

- [ ] **Step 3: Catch `not_found` in both fetchers**

In `web/src/api/bookings.ts` change line 2 to:

```ts
import { api, ApiError } from '#/api/client'
```

and replace lines 197-226 (`getPublicPage` through the end of `getPublicAvailability`) with:

```ts
/** `null` for an unknown handle/slug — the old TS `getPublicPage` returned `null` and the route's
 * `notFoundComponent` (`routes/book/$handle/$slug.tsx`) depends on that; `api()` itself throws
 * `ApiError('not_found')` on the Go 404, so it's translated back here. Any other failure (rate
 * limit, 5xx, network) still throws, so the generic error card keeps its retry button for those. */
export async function getPublicPage(handle: string, slug: string): Promise<PublicPageView | null> {
  try {
    return await api<PublicPageView>('GET', `/api/v1/book/${handle}/${slug}`)
  } catch (err) {
    if (err instanceof ApiError && err.code === 'not_found') return null
    throw err
  }
}

/** Slot start times only — the Go endpoint's own response shape (`{"slots": [iso, ...]}`,
 * internal/bookings/handlers.go's handlePublicAvailability). `null` on `not_found`, same as
 * `getPublicPage`, so `getPublicAvailability` can resolve `null` without `Promise.all` rejecting
 * on the availability half of the pair. */
async function fetchPublicSlots(input: {
  handle: string
  slug: string
  from: string
  to: string
}): Promise<string[] | null> {
  try {
    const result = await api<{ slots: string[] }>(
      'GET',
      `/api/v1/book/${input.handle}/${input.slug}/availability`,
      undefined,
      { query: { from: input.from, to: input.to } },
    )
    return result.slots
  } catch (err) {
    if (err instanceof ApiError && err.code === 'not_found') return null
    throw err
  }
}

/** `{start, end}` pairs — `end` is computed here from `page.slotDurationMin`, the same width every
 * slot on a page has, since the Go endpoint only returns start times (unlike the old TS
 * `getPublicAvailability`, which already returned `Interval[]`). `null` when the page is unknown. */
export async function getPublicAvailability(input: {
  handle: string
  slug: string
  from: string
  to: string
}): Promise<{ page: PublicPageView; slots: { start: string; end: string }[] } | null> {
  const [page, starts] = await Promise.all([
    getPublicPage(input.handle, input.slug),
    fetchPublicSlots(input),
  ])
  if (!page || starts === null) return null
  const slots = starts.map((start) => ({
    start,
    end: new Date(new Date(start).getTime() + page.slotDurationMin * 60_000).toISOString(),
  }))
  return { page, slots }
}
```

- [ ] **Step 4: Run the tests and the web gates**

Run: `cd web && bunx vitest run src/api/__tests__/bookings.test.ts && bun run typecheck && bun run lint`
Expected: `4 passed`; typecheck and lint clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/api/bookings.ts web/src/api/__tests__/bookings.test.ts
git commit -m "fix(web): unknown booking page renders the not-found card

getPublicPage/getPublicAvailability now resolve null on ApiError not_found,
making the route loader's throw notFound() reachable again.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 3: UpdatePage/DeletePage broadcast `page.changed`

Finding: *"Booking page update/delete no longer broadcast page.changed to live viewers"* (CONFIRMED). Old `updateBookingPage`/`deleteBookingPage` called `notifyPageChanged`; the Go `UpdatePage`/`DeletePage` (`internal/bookings/pages.go:227-311`) write via `s.q` with no transaction and no `rooms.Emit`, so a visitor on the public page keeps a stale slot grid until they submit.

**Files:**
- Modify: `internal/bookings/pages.go` (imports lines 7-22; `UpdatePage` lines 227-298; `DeletePage` lines 300-311)
- Test: `internal/bookings/pages_test.go` (append)

**Interfaces:**
- Consumes: `rooms.Emit(ctx context.Context, tx db.DBTX, roomKey, eventType string, data any) error` (`internal/rooms/emit.go`); room key convention `"booking:"+pageID`, event `"page.changed"`, `nil` data — exactly what `Book`/`Cancel`/`Reschedule` in `bookings.go` already emit.
- Produces: `UpdatePage`/`DeletePage` keep their signatures; both now run in one transaction with the emit.

- [ ] **Step 1: Write the failing tests**

Append to `internal/bookings/pages_test.go`:

```go
// pageChangedEvents counts the "page.changed" room_events rows in pageID's own room — the
// broadcast a visitor sitting on the public page (web/src/lib/use-live-page.ts) refetches on.
func pageChangedEvents(t *testing.T, d *sql.DB, pageID string) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(),
		`SELECT count(*) FROM room_events WHERE room_key = $1 AND event->>'type' = 'page.changed'`,
		"booking:"+pageID,
	).Scan(&n); err != nil {
		t.Fatalf("count room_events for %s: %v", pageID, err)
	}
	return n
}

// TestUpdatePageEmitsPageChanged ports pages.functions.ts's notifyPageChanged after updatePage:
// an organiser pausing/editing a page must wake every open public page.
func TestUpdatePageEmitsPageChanged(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := bookings.NewService(testConfig(t), d)
	orgID, ownerID := seedOrgAndUser(t, d)

	page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if n := pageChangedEvents(t, d, page.ID); n != 0 {
		t.Fatalf("events before update = %d, want 0", n)
	}

	if _, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
		in.Status = "paused"
	})); err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if n := pageChangedEvents(t, d, page.ID); n != 1 {
		t.Errorf("events after update = %d, want 1", n)
	}

	t.Run("a rejected update emits nothing (write and emit share one transaction)", func(t *testing.T) {
		if _, err := s.CreatePage(ctx, orgID, ownerID, baseInput(func(in *bookings.PageInput) {
			in.Slug = "taken-slug"
		})); err != nil {
			t.Fatalf("CreatePage(taken-slug): %v", err)
		}
		if _, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
			in.Slug = "taken-slug"
		})); !errors.Is(err, bookings.ErrSlugTaken) {
			t.Fatalf("UpdatePage(slug collision) error = %v, want ErrSlugTaken", err)
		}
		if n := pageChangedEvents(t, d, page.ID); n != 1 {
			t.Errorf("events after rejected update = %d, want still 1", n)
		}
	})
}

// TestDeletePageEmitsPageChanged ports notifyPageChanged after deletePage: an open public page
// refetches, gets the 404, and shows "not found" instead of a live-looking grid for a dead page.
func TestDeletePageEmitsPageChanged(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := bookings.NewService(testConfig(t), d)
	orgID, ownerID := seedOrgAndUser(t, d)

	page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if err := s.DeletePage(ctx, page.ID, orgID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if n := pageChangedEvents(t, d, page.ID); n != 1 {
		t.Errorf("events after delete = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/bookings/ -run 'TestUpdatePageEmitsPageChanged|TestDeletePageEmitsPageChanged' -v`
Expected: FAIL — `events after update = 0, want 1` and `events after delete = 0, want 1`.

- [ ] **Step 3: Wrap both writes in a transaction with the emit**

In `internal/bookings/pages.go` add to the import block:

```go
	"github.com/refsdal/whenweall/internal/rooms"
```

Replace `UpdatePage` (lines 227-298) with:

```go
// UpdatePage ports updatePage. Unlike the TS source's partial update, this replaces every editable
// field with in's value (see PageInput's doc comment) — id/organizationId/createdBy/memberUserId/
// createdAt are carried over from the existing row untouched. The write and its "page.changed"
// broadcast share ONE transaction (rooms.Emit must run inside the same tx as the write it
// announces — internal/rooms's package doc), mirroring Book/Cancel/Reschedule: a visitor sitting on
// the public page (useLivePage) refetches the moment an organiser pauses the page, changes its
// availability or renames it — pages.functions.ts's own notifyPageChanged after updatePage.
func (s *Service) UpdatePage(ctx context.Context, pageID, orgID string, in PageInput) (*PageView, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	existing, err := requireOrgPage(ctx, q, pageID, orgID)
	if err != nil {
		return nil, err
	}

	availabilityJSON, err := json.Marshal(in.Availability)
	if err != nil {
		return nil, err
	}
	dateOverridesJSON, err := marshalDateOverrides(in.DateOverrides)
	if err != nil {
		return nil, err
	}

	status := in.Status
	if status == "" {
		status = "active"
	}
	now := time.Now().UTC()

	if err := q.UpdateBookingPage(ctx, queries.UpdateBookingPageParams{
		ID:              pageID,
		Slug:            in.Slug,
		Title:           strings.TrimSpace(in.Title),
		Description:     optionalTrimmedString(in.Description),
		Location:        optionalTrimmedString(in.Location),
		Timezone:        in.Timezone,
		SlotDurationMin: int32(in.SlotDurationMin),
		BufferBeforeMin: int32(in.BufferBeforeMin),
		BufferAfterMin:  int32(in.BufferAfterMin),
		MinNoticeMin:    int32(in.MinNoticeMin),
		MaxDaysAhead:    int32(in.MaxDaysAhead),
		Availability:    availabilityJSON,
		DateOverrides:   dateOverridesJSON,
		GoogleSync:      in.GoogleSync,
		Reminders:       in.Reminders,
		Status:          status,
		UpdatedAt:       now,
	}); err != nil {
		if isSlugConflict(err) {
			return nil, ErrSlugTaken
		}
		return nil, err
	}

	if err := rooms.Emit(ctx, tx, "booking:"+pageID, "page.changed", nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	existing.Slug = in.Slug
	existing.Title = strings.TrimSpace(in.Title)
	existing.Description = optionalTrimmedString(in.Description)
	existing.Location = optionalTrimmedString(in.Location)
	existing.Timezone = in.Timezone
	existing.SlotDurationMin = int32(in.SlotDurationMin)
	existing.BufferBeforeMin = int32(in.BufferBeforeMin)
	existing.BufferAfterMin = int32(in.BufferAfterMin)
	existing.MinNoticeMin = int32(in.MinNoticeMin)
	existing.MaxDaysAhead = int32(in.MaxDaysAhead)
	existing.Availability = availabilityJSON
	existing.DateOverrides = dateOverridesJSON
	existing.GoogleSync = in.GoogleSync
	existing.Reminders = in.Reminders
	existing.Status = status
	existing.UpdatedAt = now

	return toPageView(existing)
}

// DeletePage ports deletePage: a soft delete (deleted_at set) plus notifyPageChanged's
// "page.changed" broadcast, in one transaction (see UpdatePage). Freeing the page's slug for reuse
// happens implicitly — booking_pages_org_slug_uidx only covers live (deleted_at IS NULL) rows.
func (s *Service) DeletePage(ctx context.Context, pageID, orgID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	if _, err := requireOrgPage(ctx, q, pageID, orgID); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := q.SoftDeleteBookingPage(ctx, queries.SoftDeleteBookingPageParams{
		ID:        pageID,
		DeletedAt: sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return err
	}
	if err := rooms.Emit(ctx, tx, "booking:"+pageID, "page.changed", nil); err != nil {
		return err
	}
	return tx.Commit()
}
```

(This replaces the old `DeletePage` at lines 300-311 too — delete the old function body.)

- [ ] **Step 4: Run the package tests**

Run: `go build ./... && go test ./internal/bookings/`
Expected: `ok  github.com/refsdal/whenweall/internal/bookings` (the two new tests pass; every existing UpdatePage/DeletePage test still passes).

- [ ] **Step 5: Commit**

```bash
git add internal/bookings/pages.go internal/bookings/pages_test.go
git commit -m "fix(bookings): broadcast page.changed on organiser page update/delete

UpdatePage and DeletePage now run in one transaction with rooms.Emit, so an
open public page refetches when the organiser pauses, edits or deletes it —
the notifyPageChanged behaviour the TS pages.functions.ts had.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 4: PATCH requires `availability` and `status`; web schema drops `.partial()`

Finding: *"PATCH /booking-pages/{id} is a full replace and accepts a missing availability (stored as JSON null)"*. `Validate()` (`schemas.go:143`) never rejects a nil `Availability` (marshalled as the literal `null` into a NOT NULL jsonb column), `UpdatePage` defaults an omitted `status` to `"active"` (silently un-pausing), and the web `updateBookingPageSchema` (`web/src/api/bookings.ts:142-147`) is still `.partial()`.

**Files:**
- Modify: `internal/bookings/schemas.go` (PageInput doc lines 68-97; `Validate` line 143), `internal/bookings/pages.go` (`UpdatePage`: the status default), `internal/bookings/handlers.go` (`handleUpdatePage` doc, lines 305-309)
- Test: `internal/bookings/schemas_test.go` (`TestPageInputValidate`), `internal/bookings/pages_test.go` (`baseInput`, `TestUpdatePage`), `internal/bookings/bookings_test.go` (`openPageInput`), `internal/bookings/handlers_test.go` (`TestHandlerUpdatePage`)
- Modify: `web/src/api/bookings.ts:142-147`; Test: `web/src/api/__tests__/bookings.test.ts`

**Interfaces:**
- Produces: `PageInput.Validate()` returns `*ValidationError{Fields:{"availability": "availability is required"}}` for a nil map (an empty non-nil map stays valid); `UpdatePage` returns `*ValidationError{Fields:{"status": "status is required"}}` for `Status == ""`. `CreatePage` still ignores `Status`. Web: `updateBookingPageSchema = createBookingPageSchema.extend({pageId, status})` (no `.partial()`), so `UpdateBookingPageInput` requires every field.

- [ ] **Step 1: Write the failing Go tests**

In `internal/bookings/schemas_test.go`, inside `TestPageInputValidate`, replace the subtest named `"empty status is valid (defaults to active at the service layer)"` with these three:

```go
	t.Run("empty status passes Validate (CreatePage ignores status; UpdatePage requires it itself)", func(t *testing.T) {
		if err := baseInput(func(in *bookings.PageInput) { in.Status = "" }).Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("rejects an omitted availability (nil map) — never stored as JSON null", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) { in.Availability = nil })
		fields := fieldsOf(t, in.Validate())
		if fields["availability"] != "availability is required" {
			t.Errorf("Fields = %+v, want availability: availability is required", fields)
		}
	})

	t.Run("accepts an empty availability object (a page with no open days is valid, just unbookable)", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) { in.Availability = bookings.Availability{} })
		if err := in.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})
```

In `internal/bookings/pages_test.go` `baseInput`, add `Status: "active",` after `Reminders: true,`. In `internal/bookings/bookings_test.go` `openPageInput`, add `Status: "active",` after `Reminders: true,`. (Every existing UpdatePage caller passes one of these two; CreatePage ignores the field.)

Append this subtest inside `TestUpdatePage` in `pages_test.go` (after the existing `t.Run("updates fields and enforces NOT_FOUND/SLUG_TAKEN", …)` block):

```go
	t.Run("requires status — an omitted status must never un-pause a page", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		if _, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
			in.Status = "paused"
		})); err != nil {
			t.Fatalf("UpdatePage(paused): %v", err)
		}

		_, err = s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) { in.Status = "" }))
		var verr *bookings.ValidationError
		if !errors.As(err, &verr) || verr.Fields["status"] != "status is required" {
			t.Fatalf("UpdatePage(no status) error = %v, want ValidationError{status: status is required}", err)
		}

		got, err := s.GetOwnedPage(ctx, page.ID, orgID)
		if err != nil {
			t.Fatalf("GetOwnedPage: %v", err)
		}
		if got.Status != "paused" {
			t.Errorf("Status = %q, want paused (the rejected update must not have un-paused the page)", got.Status)
		}
	})
```

In `internal/bookings/handlers_test.go` `TestHandlerUpdatePage`, append two subtests (after the `requirement (a)` one):

```go
	t.Run("422 when status is omitted (no default to active)", func(t *testing.T) {
		noStatus := map[string]any{}
		for k, v := range body {
			noStatus[k] = v
		}
		delete(noStatus, "status")

		rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, noStatus, sessHeader(p.ownerID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		if errFields(t, rec)["status"] == "" {
			t.Errorf("fields = %+v, want a status entry", errFields(t, rec))
		}
	})

	t.Run("422 when availability is omitted (never stored as JSON null)", func(t *testing.T) {
		noAvailability := map[string]any{}
		for k, v := range body {
			noAvailability[k] = v
		}
		delete(noAvailability, "availability")

		rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, noAvailability, sessHeader(p.ownerID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		if errFields(t, rec)["availability"] == "" {
			t.Errorf("fields = %+v, want an availability entry", errFields(t, rec))
		}
	})
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/bookings/ -run 'TestPageInputValidate|TestUpdatePage|TestHandlerUpdatePage' -v`
Expected: FAIL on `rejects an omitted availability`, `requires status`, `422 when status is omitted`, `422 when availability is omitted` (they currently get nil / 200).

- [ ] **Step 3: Implement**

`internal/bookings/schemas.go`, in `Validate`, replace line 143 (`validateDayRangesMap("availability", …)`) with:

```go
	if in.Availability == nil {
		// An absent JSON key decodes to a nil map; marshalling that would store the literal
		// `null` in the NOT NULL jsonb column. A present-but-empty object ({}) is a valid page
		// that simply has no open days.
		fields["availability"] = "availability is required"
	} else {
		validateDayRangesMap("availability", weekdayKeyRegexp, "Weekday keys must be '0'..'6'", in.Availability, 0, fields)
	}
```

Replace the `PageInput` doc comment (lines 68-78) with:

```go
// PageInput ports schemas.ts's pageSchema — the shape createBookingPageSchema/
// updateBookingPageSchema share. Validate() enforces the field-level rules (Availability is
// required — a nil map is rejected, an empty one accepted). The create/update distinction is
// carried by the two Service methods: CreatePage always writes status "active" and ignores Status;
// UpdatePage REQUIRES Status ("active"/"paused") and replaces every field with in's value — there
// is no PATCH-style "omitted means unchanged", and no defaulting: an omitted status must never
// silently un-pause a page. The web schema (web/src/api/bookings.ts updateBookingPageSchema) is
// the same full shape, so a caller changing one field round-trips GetOwnedPage first.
```

and the `Status` field comment (lines 94-95) with:

```go
	// Status is required by UpdatePage ("active" or "paused"); CreatePage ignores it and always
	// writes "active", matching createBookingPageSchema having no status field at all.
```

`internal/bookings/pages.go`, in `UpdatePage`: directly after the `in.Validate()` check add

```go
	// Status is required on an update (updateBookingPageSchema's own z.enum(['active','paused'])
	// now that its .partial() is gone — web/src/api/bookings.ts): an omitted status used to
	// default to "active" here, silently un-pausing a paused page on any PATCH missing the field.
	if in.Status == "" {
		return nil, &ValidationError{Fields: map[string]string{"status": "status is required"}}
	}
```

then delete the four lines

```go
	status := in.Status
	if status == "" {
		status = "active"
	}
```

and change `Status: status,` to `Status: in.Status,` and `existing.Status = status` to `existing.Status = in.Status`.

`internal/bookings/handlers.go`: replace the `handleUpdatePage` doc comment (lines 305-309) with:

```go
// handleUpdatePage ports updateBookingPage (pages.functions.ts) as a FULL replacement of every
// editable field: availability and status are required (422 "invalid" with field errors when
// omitted — see PageInput's doc comment, schemas.go), there is no "omitted means unchanged", so a
// client changing one field round-trips GetOwnedPage first and sends the whole shape back.
```

- [ ] **Step 4: Run the Go package**

Run: `go build ./... && go vet ./internal/bookings/ && go test ./internal/bookings/`
Expected: `ok`.

- [ ] **Step 5: Write the failing web test**

Append to `web/src/api/__tests__/bookings.test.ts` (add `updateBookingPageSchema` to the import from `'#/api/bookings'`):

```ts
const validCreateInput = {
  slug: 'intro-call',
  title: 'Intro call',
  timezone: 'Europe/Oslo',
  slotDurationMin: 30,
  bufferBeforeMin: 0,
  bufferAfterMin: 0,
  minNoticeMin: 0,
  maxDaysAhead: 60,
  availability: { '1': [{ start: '09:00', end: '17:00' }] },
  googleSync: false,
  reminders: true,
}

describe('updateBookingPageSchema', () => {
  it('is a full replacement: every create field plus status is required', () => {
    expect(updateBookingPageSchema.safeParse({ pageId: 'p1', status: 'active' }).success).toBe(false)
    expect(updateBookingPageSchema.safeParse({ ...validCreateInput, pageId: 'p1' }).success).toBe(false)
    expect(
      updateBookingPageSchema.safeParse({ ...validCreateInput, pageId: 'p1', status: 'paused' }).success,
    ).toBe(true)
  })
})
```

Run: `cd web && bunx vitest run src/api/__tests__/bookings.test.ts`
Expected: FAIL — the first two `expect(...).toBe(false)` receive `true` (still `.partial()`).

- [ ] **Step 6: Drop `.partial()`**

Replace lines 142-145 of `web/src/api/bookings.ts` with:

```ts
/** A full replacement, not a partial patch: `PATCH /booking-pages/{id}` (internal/bookings/
 * handlers.go handleUpdatePage) overwrites every field and rejects an omitted availability or
 * status with 422, so the client schema requires them too. `draftToUpdate` (editor-state.ts)
 * always sends the whole draft. */
export const updateBookingPageSchema = createBookingPageSchema.extend({
  pageId: z.string(),
  status: z.enum(['active', 'paused']),
})
```

- [ ] **Step 7: Run the web gates**

Run: `cd web && bunx vitest run && bun run typecheck && bun run lint`
Expected: all vitest suites pass (`editor-state.test.ts` still passes — `draftToUpdate` already sends every field and `status`); typecheck/lint clean.

- [ ] **Step 8: Commit**

```bash
git add internal/bookings/schemas.go internal/bookings/pages.go internal/bookings/handlers.go internal/bookings/schemas_test.go internal/bookings/pages_test.go internal/bookings/bookings_test.go internal/bookings/handlers_test.go web/src/api/bookings.ts web/src/api/__tests__/bookings.test.ts
git commit -m "fix(bookings): PATCH requires availability and status instead of silently defaulting

A nil availability was stored as JSON null and an omitted status un-paused
the page. Validate rejects nil availability, UpdatePage requires status, and
the web updateBookingPageSchema drops .partial() to match the full-replace
contract.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 5: Separate read and mutating public rate-limit buckets

Finding: *"Shared 20/min public limiter now covers twice as many requests per month view"*. `Register` (`handlers.go:78-99`) puts GET page, GET availability, GET manage and GET calendar.ics in the same 20/min `bookLimit` bucket as Book/Cancel/Reschedule; the SPA issues two GETs per month view (plus one pair after the timezone correction and on every `page.changed`), so a visitor paging through months hits 429.

**Files:**
- Modify: `internal/bookings/handlers.go` (`Register` and its doc comment, lines 57-99)
- Test: `internal/bookings/handlers_test.go` (`TestHandlerGetPublicPage` rate-limit subtest at lines 625-647; `TestHandlerManagedBooking` M1 subtest at lines 834-859)

**Interfaces:**
- Consumes: `httpserver.PublicRateLimit(db *sql.DB, cfg *config.Config, namespace, name string, limit int, window time.Duration) func(http.Handler) http.Handler` (Plan B's signature: `cfg` replaces the old trailing `trustProxy bool`; the limiter is a pass-through when `cfg.EnableTestRoutes`) (buckets are namespaced `namespace.name`; distinct names never share a counter).
- Produces: exported constants `bookings.PublicBookRateLimit = 20` and `bookings.PublicReadRateLimit = 120` (per client IP, per minute) — tests loop over them instead of literals.

- [ ] **Step 1: Rewrite the two rate-limit subtests as failing tests**

In `handlers_test.go`, replace the whole `t.Run("the shared 'book' rate limiter applies", …)` block inside `TestHandlerGetPublicPage` (and the comment above it, lines 625-647) with:

```go
	// Reads (page, availability, manage, .ics) have their own, roomier bucket: the SPA fetches
	// page + availability per month view, again after the timezone correction, and again on every
	// page.changed — a visitor flipping through months must never hit the 20/min budget that
	// exists to slow down booking/cancel/reschedule abuse.
	t.Run("read bucket: PublicReadRateLimit per minute, separate from the mutating bucket", func(t *testing.T) {
		p2 := setupHandlerPage(t, testConfig(t))
		var last *httptest.ResponseRecorder
		for i := 0; i < bookings.PublicReadRateLimit; i++ {
			last = doRequest(t, p2.h, "GET", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug, nil, nil)
			if last.Code != http.StatusOK {
				t.Fatalf("read %d: status = %d, want 200; body=%s", i+1, last.Code, last.Body)
			}
		}
		last = doRequest(t, p2.h, "GET", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug, nil, nil)
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("read %d: status = %d, want 429; body=%s", bookings.PublicReadRateLimit+1, last.Code, last.Body)
		}
		if errCode(t, last) != "rate_limited" {
			t.Errorf("code = %q, want rate_limited", errCode(t, last))
		}

		// Exhausting the read bucket leaves the mutating bucket untouched: a booking still lands.
		rec := doRequest(t, p2.h, "POST", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug+"/bookings", bookBody(futureUTCSlot(3, 9, 0)), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("Book after read-bucket exhaustion: status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("mutating bucket: PublicBookRateLimit per minute, and exhausting it leaves reads alone", func(t *testing.T) {
		p2 := setupHandlerPage(t, testConfig(t))
		var last *httptest.ResponseRecorder
		for i := 0; i < bookings.PublicBookRateLimit; i++ {
			// A cancel against an unknown id is a cheap, always-404 mutating request that still
			// counts against the bucket (the limiter runs before the handler).
			last = doRequest(t, p2.h, "POST", "/api/v1/bookings/missing/cancel?t=x", nil, nil)
			if last.Code != http.StatusNotFound {
				t.Fatalf("cancel %d: status = %d, want 404; body=%s", i+1, last.Code, last.Body)
			}
		}
		last = doRequest(t, p2.h, "POST", "/api/v1/bookings/missing/cancel?t=x", nil, nil)
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("cancel %d: status = %d, want 429; body=%s", bookings.PublicBookRateLimit+1, last.Code, last.Body)
		}

		rec := doRequest(t, p2.h, "GET", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET page after book-bucket exhaustion: status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
	})
```

In `TestHandlerManagedBooking`, replace the M1 comment + subtest (lines 834-859) with:

```go
	// M1: this endpoint was unmetered — an anonymous caller could brute-force a booking's
	// 43-character manage token with no rate limit at all. It sits in the read bucket
	// (PublicReadRateLimit/min, Register's own doc comment), separate from the Book call that
	// created the booking, so exactly PublicReadRateLimit lookups succeed and the next one 429s.
	t.Run("M1: the read rate limiter applies", func(t *testing.T) {
		p2 := setupHandlerPage(t, testConfig(t))
		start2 := futureUTCSlot(3, 9, 0)
		bookRec2 := doRequest(t, p2.h, "POST", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug+"/bookings", bookBody(start2), nil)
		booked2 := decodeBody[struct {
			Booking     bookings.BookingView `json:"booking"`
			ManageToken string               `json:"manageToken"`
		}](t, bookRec2)

		var last *httptest.ResponseRecorder
		for i := 0; i < bookings.PublicReadRateLimit; i++ {
			last = doRequest(t, p2.h, "GET", "/api/v1/bookings/"+booked2.Booking.ID+"/manage?t="+booked2.ManageToken, nil, nil)
			if last.Code != http.StatusOK {
				t.Fatalf("lookup %d: status = %d, want 200; body=%s", i+1, last.Code, last.Body)
			}
		}
		last = doRequest(t, p2.h, "GET", "/api/v1/bookings/"+booked2.Booking.ID+"/manage?t="+booked2.ManageToken, nil, nil)
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("lookup %d: status = %d, want 429; body=%s", bookings.PublicReadRateLimit+1, last.Code, last.Body)
		}
		if errCode(t, last) != "rate_limited" {
			t.Errorf("code = %q, want rate_limited", errCode(t, last))
		}
	})
```

- [ ] **Step 2: Run and watch them fail to compile / fail**

Run: `go test ./internal/bookings/ -run 'TestHandlerGetPublicPage|TestHandlerManagedBooking' -v`
Expected: compile error `undefined: bookings.PublicReadRateLimit` (the constants don't exist yet).

- [ ] **Step 3: Split the buckets**

In `internal/bookings/handlers.go`, replace the `Register` doc comment and function (lines 57-99) with:

```go
// Public rate-limit budgets, per client IP per minute (httpserver.PublicRateLimit's fixed window).
// The old TS 'book' bucket (20/min) metered one request per month view — page + slots came back
// in one payload. This REST split costs two GETs per month view (page, availability), a second
// pair after the timezone-correction navigation, and a pair on every page.changed refetch, so
// reads get their own, roomier bucket; the 20/min budget stays exactly where abuse actually
// hurts — creating, cancelling and rescheduling bookings. Exported so handlers_test.go loops over
// the same numbers Register mounts.
const (
	PublicBookRateLimit = 20
	PublicReadRateLimit = 120
)

// Register mounts this package's whole HTTP surface on mux, following internal/polls/
// handlers.go's Register: thin handlers, and two per-IP rate limiters for the visitor-facing
// (session-less) endpoints — readLimit ("bookings.read", PublicReadRateLimit/min) on every GET
// (public page, availability, manage lookup, .ics download — the last two are token-authenticated
// lookups an attacker could otherwise use to brute-force a 43-character manage token unmetered),
// bookLimit ("bookings.book", PublicBookRateLimit/min) on every mutation (book, cancel,
// reschedule), mirroring bookings.functions.ts's own 'book' bucket. Captcha-if-anon
// (RequireCaptchaIfAnon) is narrower than either limiter: only Book calls requireTurnstile in the
// TS source, so only handleBook checks it here — cancel/reschedule are already authenticated by
// the manage token (or an organiser session).
func (s *Service) Register(mux *http.ServeMux, a Auth, cfg *config.Config) {
	bookLimit := httpserver.PublicRateLimit(s.db, cfg, "bookings", "book", PublicBookRateLimit, time.Minute)
	readLimit := httpserver.PublicRateLimit(s.db, cfg, "bookings", "read", PublicReadRateLimit, time.Minute)

	mux.Handle("POST /api/v1/booking-pages", httpserver.WithOrgSession(a, s.handleCreatePage))
	mux.Handle("GET /api/v1/booking-pages", httpserver.WithOrgSession(a, s.handleListMyPages))
	mux.Handle("GET /api/v1/booking-pages/{id}", httpserver.WithOrgSession(a, s.handleGetOwnedPage))
	mux.Handle("PATCH /api/v1/booking-pages/{id}", httpserver.WithOrgSession(a, s.handleUpdatePage))
	mux.Handle("DELETE /api/v1/booking-pages/{id}", httpserver.WithOrgSession(a, s.handleDeletePage))
	mux.Handle("GET /api/v1/booking-pages/{id}/bookings", httpserver.WithOrgSession(a, s.handleListPageBookings))
	mux.Handle("GET /api/v1/booking-pages/{id}/google-status", httpserver.WithOrgSession(a, s.handleGoogleStatus))

	mux.Handle("POST /api/v1/org/handle", httpserver.WithOrgSession(a, s.handleSetOrgSlug))
	mux.Handle("POST /api/v1/me/google/disconnect", s.handleDisconnectGoogle(a))

	mux.Handle("GET /api/v1/book/{org}/{page}", readLimit(http.HandlerFunc(s.handleGetPublicPage)))
	mux.Handle("GET /api/v1/book/{org}/{page}/availability", readLimit(http.HandlerFunc(s.handlePublicAvailability)))
	mux.Handle("POST /api/v1/book/{org}/{page}/bookings", bookLimit(http.HandlerFunc(s.handleBook(a, cfg))))

	mux.Handle("GET /api/v1/bookings/{id}/manage", readLimit(http.HandlerFunc(s.handleManagedBooking(a))))
	mux.Handle("GET /api/v1/bookings/{id}/calendar.ics", readLimit(http.HandlerFunc(s.handleBookingICS(cfg))))
	mux.Handle("POST /api/v1/bookings/{id}/cancel", bookLimit(http.HandlerFunc(s.handleCancel(a))))
	mux.Handle("POST /api/v1/bookings/{id}/reschedule", bookLimit(http.HandlerFunc(s.handleReschedule(a))))
}
```

- [ ] **Step 4: Run the package**

Run: `go build ./... && go test ./internal/bookings/`
Expected: `ok` — the two rewritten subtests pass, and no other test in the package loops against a limiter (the only two 21-request loops in `handlers_test.go` were the ones rewritten in Step 1; `grep -n "i < 2[01]" internal/bookings/handlers_test.go` prints nothing).

- [ ] **Step 5: Commit**

```bash
git add internal/bookings/handlers.go internal/bookings/handlers_test.go
git commit -m "fix(bookings): separate read and mutating public rate-limit buckets

GET page/availability/manage/.ics move to a 120/min read bucket; book,
cancel and reschedule keep the 20/min bucket. A visitor paging through
months no longer 429s into the generic error card.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 6: `.ics` download — organiser-session fallback and `Content-Disposition`

Finding: *".ics download lost the organiser-session fallback and Content-Disposition"*. Old `bookingIcsResponse` (`main:src/routes/booking/$id/calendar[.]ics.ts`) accepted `?t=` OR an owner session (401 no session / 403 wrong token or role / 404 unknown) and sent `Content-Disposition: attachment; filename="whenweall-booking-{id}.ics"`. Go `BookingICS` (`bookings.go:698-730`) verifies the token only and `handleBookingICS` (`handlers.go:582-603`) sets no filename, so the organiser's "Add to calendar" button on the manage page (no token) gets a 403 JSON.

**Files:**
- Modify: `internal/bookings/bookings.go` (`BookingICS`, lines 688-730), `internal/bookings/handlers.go` (`Register` line for calendar.ics; `handleBookingICS`, lines 568-590 after Task 5)
- Test: `internal/bookings/handlers_test.go` (`TestHandlerBookingICS`)

**Interfaces:**
- Consumes: `requireOwnerSession(w, r, a) (*auth.Session, bool)` and `s.RequireManageableBooking(ctx, bookingID, orgID, userID) error` (both exist in this package — the same organiser fallback `handleManagedBooking` uses).
- Produces: `func (s *Service) BookingICS(ctx context.Context, bookingID, manageToken string, byOrganiser bool, appURL string) ([]byte, error)` — new `byOrganiser` parameter, same position convention as `ManagedBooking`.

- [ ] **Step 1: Extend the handler test**

In `TestHandlerBookingICS`, after the `UID:` assertion add:

```go
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="whenweall-booking-`+booked.Booking.ID+`.ics"` {
		t.Errorf("Content-Disposition = %q, want the whenweall-booking-{id}.ics attachment filename", cd)
	}
```

Replace the subtest `"a missing token is 403 invalid_token"` with:

```go
	t.Run("no token and no session is 401 unauthenticated", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "unauthenticated" {
			t.Errorf("code = %q, want unauthenticated", errCode(t, rec))
		}
	})

	t.Run("an organiser session downloads without a token (the dashboard's own 'Add to calendar')", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, sessHeader(p.ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/calendar; charset=utf-8" {
			t.Errorf("Content-Type = %q, want text/calendar; charset=utf-8", ct)
		}
		if !strings.Contains(rec.Body.String(), "UID:"+booked.Booking.ID+"@whenweall") {
			t.Errorf("body missing booking UID: %q", rec.Body.String())
		}
	})

	t.Run("a same-org plain member (not creator, no managing role) is 403 forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "forbidden" {
			t.Errorf("code = %q, want forbidden", errCode(t, rec))
		}
	})

	t.Run("a session from another org is 404 (no cross-org existence leak)", func(t *testing.T) {
		otherOrgID, otherUserID := seedOrgAndUser(t, p.d)
		addOrgMember(t, p.d, otherOrgID, otherUserID, "owner")
		p.a.login(&auth.Session{UserID: otherUserID, ActiveOrgID: otherOrgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, sessHeader(otherUserID))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
	})
```

- [ ] **Step 2: Run and watch it fail**

Run: `go test ./internal/bookings/ -run TestHandlerBookingICS -v`
Expected: FAIL — `Content-Disposition = ""`, `status = 403, want 401`, `status = 403, want 200` (organiser), etc.

- [ ] **Step 3: Implement**

`internal/bookings/bookings.go`: replace `BookingICS` and its doc comment (lines 688-730) with:

```go
// BookingICS builds bookingID's own .ics download — the same visitor-facing calendar file
// sendBookingConfirmedMail/sendBookingRescheduledMail attach to their mails (BuildBookingICS,
// ics.go), reachable as a standalone GET so a visitor who lost that mail can re-download the
// invite from the manage page, and so the organiser's own dashboard/manage view can offer the
// same button. Auth mirrors ManagedBooking exactly (the old TS route, main:src/routes/booking/
// $id/calendar[.]ics.ts, accepted either credential too): a manage token (byOrganiser: false) is
// verified against the booking — wrong or empty is ErrInvalidToken; byOrganiser: true skips the
// token check and is only ever set by the HTTP layer after RequireManageableBooking (authz.go)
// has established the caller manages the booking's page.
func (s *Service) BookingICS(ctx context.Context, bookingID, manageToken string, byOrganiser bool, appURL string) ([]byte, error) {
	booking, err := s.q.GetBooking(ctx, bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !byOrganiser && !s.verifyManageToken(bookingID, manageToken) {
		return nil, ErrInvalidToken
	}

	page, err := s.q.GetBookingPage(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	manageURL := s.bookingManageURL(appURL, booking.ID)
	return BuildBookingICS(booking.ID, page.Title, pageDescriptionText(page), pageLocationText(page), booking.StartAt, booking.EndAt, manageURL), nil
}
```

`internal/bookings/handlers.go`: in `Register` change the calendar.ics line to

```go
	mux.Handle("GET /api/v1/bookings/{id}/calendar.ics", readLimit(http.HandlerFunc(s.handleBookingICS(a, cfg))))
```

and replace `handleBookingICS` with its doc comment by:

```go
// handleBookingICS serves bookingID's .ics download. Credential resolution is the same as
// handleManagedBooking's: a `?t=` manage token, or — without one — a signed-in caller who manages
// the booking's page (requireOwnerSession → RequireManageableBooking → BookingICS(byOrganiser:
// true)), so the organiser's own "Add to calendar" button on the dashboard/manage page works with
// no token in hand (the token lives in the visitor's mail, not the dashboard). Content-Disposition
// names the file whenweall-booking-{id}.ics exactly as the old TS route did — browsers save the
// download under a recognisable name instead of "calendar.ics" or the URL's last segment.
func (s *Service) handleBookingICS(a Auth, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := r.PathValue("id")

		var ics []byte
		var err error
		if token := manageTokenFromQuery(r); token != "" {
			ics, err = s.BookingICS(r.Context(), bookingID, token, false, cfg.AppURL)
		} else {
			sess, ok := requireOwnerSession(w, r, a)
			if !ok {
				return
			}
			if err := s.RequireManageableBooking(r.Context(), bookingID, sess.ActiveOrgID, sess.UserID); err != nil {
				writeServiceError(w, err)
				return
			}
			ics, err = s.BookingICS(r.Context(), bookingID, "", true, cfg.AppURL)
		}
		if err != nil {
			writeServiceError(w, err)
			return
		}

		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="whenweall-booking-`+bookingID+`.ics"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(ics)
	}
}
```

- [ ] **Step 4: Run the package**

Run: `go build ./... && go vet ./internal/bookings/ && go test ./internal/bookings/ -run 'TestHandlerBookingICS|TestHandlerManagedBooking' -v && go test ./internal/bookings/`
Expected: all PASS; `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/bookings/bookings.go internal/bookings/handlers.go internal/bookings/handlers_test.go
git commit -m "fix(bookings): .ics download accepts an organiser session and names the file

Restores the old route's contract: ?t= manage token OR a session that manages
the booking's page (401/403/404 mapping), plus Content-Disposition
whenweall-booking-{id}.ics.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 7: `ListMyPages` in one query

Finding: *"Confirmed: poll list is 2N+1 queries and booking-page list is N+1"* — `ListMyPages` (`pages.go:314-345`) calls `ListBookingPagesByOrg` then `CountUpcomingConfirmedBookings` once per page. The old Drizzle query was one relational query. (The poll half is Plan C's.)

**Files:**
- Modify: `internal/bookings/queries/bookings.sql` (replace `ListBookingPagesByOrg` at lines 16-17 and `CountUpcomingConfirmedBookings` at lines 42-43 with one query), regenerate `internal/bookings/queries/bookings.sql.go` + `querier.go` via `sqlc generate`
- Modify: `internal/bookings/pages.go` (`ListMyPages`)
- Test: `internal/bookings/pages_test.go` (`TestListMyPages`)

**Interfaces:**
- Produces (generated by sqlc): `func (q *Queries) ListBookingPageSummariesByOrg(ctx, arg ListBookingPageSummariesByOrgParams) ([]ListBookingPageSummariesByOrgRow, error)` with `Params{StartAt time.Time; OrganizationID int64}` and `Row{ID, Slug, Title, Status string; CreatedAt, UpdatedAt time.Time; UpcomingCount int64}`. `ListBookingPagesByOrg` and `CountUpcomingConfirmedBookings` are removed (their only caller was `ListMyPages`; verify with `grep -rn "ListBookingPagesByOrg\|CountUpcomingConfirmedBookings" internal cmd --include=*.go | grep -v queries/` → only `pages.go`).

- [ ] **Step 1: Extend the failing test**

Append this subtest inside `TestListMyPages` in `pages_test.go` (after the existing `"reports upcomingCount from confirmed future bookings"` block):

```go
	t.Run("many pages: per-page counts, zero for a page with none, newest first", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		var ids []string
		for i, slug := range []string{"first-page", "second-page", "third-page"} {
			page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(func(in *bookings.PageInput) { in.Slug = slug }))
			if err != nil {
				t.Fatalf("CreatePage(%s): %v", slug, err)
			}
			// created_at is set from time.Now() in CreatePage; nudge it so the ORDER BY is
			// deterministic even on a coarse clock.
			if _, err := d.ExecContext(ctx, `UPDATE booking_pages SET created_at = now() + ($2 || ' seconds')::interval WHERE id = $1`,
				page.ID, fmt.Sprint(i)); err != nil {
				t.Fatalf("nudge created_at: %v", err)
			}
			ids = append(ids, page.ID)
		}
		// first-page: 2 upcoming (+1 past, +1 cancelled ignored); second-page: none; third-page: 1.
		makeBooking(t, d, ids[0], time.Now().Add(24*time.Hour), "confirmed")
		makeBooking(t, d, ids[0], time.Now().Add(48*time.Hour), "confirmed")
		makeBooking(t, d, ids[0], time.Now().Add(-24*time.Hour), "confirmed")
		makeBooking(t, d, ids[0], time.Now().Add(72*time.Hour), "cancelled")
		makeBooking(t, d, ids[2], time.Now().Add(24*time.Hour), "confirmed")

		list, err := s.ListMyPages(ctx, orgID)
		if err != nil {
			t.Fatalf("ListMyPages: %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("len(list) = %d, want 3", len(list))
		}
		// Newest first: third, second, first.
		wantOrder := []string{ids[2], ids[1], ids[0]}
		wantCounts := []int{1, 0, 2}
		for i, summary := range list {
			if summary.ID != wantOrder[i] {
				t.Errorf("list[%d].ID = %q, want %q (newest first)", i, summary.ID, wantOrder[i])
			}
			if summary.UpcomingCount != wantCounts[i] {
				t.Errorf("list[%d].UpcomingCount = %d, want %d", i, summary.UpcomingCount, wantCounts[i])
			}
		}
	})
```

- [ ] **Step 2: Run it**

Run: `go test ./internal/bookings/ -run TestListMyPages -v`
Expected: PASS (it pins the behaviour the single query must preserve; the implementation change below must keep it green).

- [ ] **Step 3: Replace the two queries with one**

In `internal/bookings/queries/bookings.sql` delete

```sql
-- name: ListBookingPagesByOrg :many
SELECT * FROM booking_pages WHERE organization_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC;
```

and

```sql
-- name: CountUpcomingConfirmedBookings :one
SELECT count(*) FROM bookings WHERE page_id = $1 AND status = 'confirmed' AND start_at >= $2;
```

and add, where `ListBookingPagesByOrg` was:

```sql
-- name: ListBookingPageSummariesByOrg :many
-- ListMyPages' one-query list (pages.go): each live page with its count of confirmed bookings
-- starting at/after start_at, via a LATERAL aggregate — count(*) always yields exactly one row,
-- so a plain (CROSS) JOIN LATERAL is equivalent to the LEFT JOIN LATERAL + COALESCE form and
-- keeps the generated column a non-null bigint. Replaces the former per-page
-- CountUpcomingConfirmedBookings round trip (N+1).
SELECT bp.id, bp.slug, bp.title, bp.status, bp.created_at, bp.updated_at, c.upcoming_count
FROM booking_pages bp
CROSS JOIN LATERAL (
  SELECT count(*) AS upcoming_count
  FROM bookings b
  WHERE b.page_id = bp.id AND b.status = 'confirmed' AND b.start_at >= sqlc.arg(start_at)
) c
WHERE bp.organization_id = sqlc.arg(organization_id) AND bp.deleted_at IS NULL
ORDER BY bp.created_at DESC;
```

Run: `sqlc generate && grep -n "UpcomingCount\|type ListBookingPageSummariesByOrg" internal/bookings/queries/bookings.sql.go`
Expected output includes `UpcomingCount  int64` inside `type ListBookingPageSummariesByOrgRow struct` and `type ListBookingPageSummariesByOrgParams struct` with `StartAt time.Time` and `OrganizationID int64`.

- [ ] **Step 4: Use it**

Replace `ListMyPages` in `internal/bookings/pages.go` (lines 313-345) with:

```go
// ListMyPages ports listMyPages as ONE query (queries.ListBookingPageSummariesByOrg, a LATERAL
// count per page) — the same single relational query the TS source's Drizzle `with: bookings`
// made — rather than one CountUpcomingConfirmedBookings round trip per page. This backs the
// /bookings route loader, so every dashboard load used to pay N+1 round trips.
func (s *Service) ListMyPages(ctx context.Context, orgID string) ([]PageSummary, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}

	rows, err := s.q.ListBookingPageSummariesByOrg(ctx, queries.ListBookingPageSummariesByOrgParams{
		StartAt: time.Now().UTC(), OrganizationID: orgIDInt,
	})
	if err != nil {
		return nil, err
	}

	out := make([]PageSummary, 0, len(rows))
	for _, p := range rows {
		out = append(out, PageSummary{
			ID:            p.ID,
			Slug:          p.Slug,
			Title:         p.Title,
			Status:        p.Status,
			UpcomingCount: int(p.UpcomingCount),
			CreatedAt:     formatISO(p.CreatedAt),
			UpdatedAt:     formatISO(p.UpdatedAt),
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Build and test**

Run: `go build ./... && go vet ./... && go test ./internal/bookings/ -run 'TestListMyPages|TestHandlerListMyPages|TestDeletePage' -v && go test ./internal/bookings/`
Expected: all PASS; `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/bookings/queries/bookings.sql internal/bookings/queries/bookings.sql.go internal/bookings/queries/querier.go internal/bookings/pages.go internal/bookings/pages_test.go
git commit -m "perf(bookings): list booking pages with their upcoming counts in one query

ListMyPages was N+1 (one count per page); a LATERAL count in the list query
restores the single relational query the TS implementation had.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 8: One `mail:booking` job per recipient

Finding: *"A failed organiser send re-sends the visitor's already-delivered booking mail on every retry"* (CONFIRMED). Every kind-specific sender in `emails.go` does the visitor `m.Send` then the organiser `m.Send` in one job; if the second fails, the job retries (up to 10×) and the visitor gets a duplicate each time. Polls avoid this with one `mail:poll` job per recipient.

**Files:**
- Modify: `internal/bookings/emails.go` (package doc lines 1-55; everything from `type mailBookingPayload` to end of file), `internal/bookings/google.go` (lines 537 and 575: the two `enqueueMailBooking(ctx, s.db, "sync_failed", …)` calls)
- Create: `internal/bookings/export_test.go`
- Test: `internal/bookings/emails_test.go` (payload mirror, `TestBookEnqueuesConfirmedMailJob`, `TestMailBookingJobSkipsStaleConfirmedAfterCancellation`, two new tests), `internal/bookings/bookings_test.go` (`TestCancelRaceEnqueuesExactlyOneMailJob`), `internal/bookings/google_test.go` (`TestBookInsertsGoogleEventAndStoresEventID` line 269)

**Interfaces:**
- Consumes: `jobs.Schedule`, `mailer.Mailer.Send(ctx, mailer.Message) error`, `mailer.Message{To, ToName, Template string; Data map[string]any; Attachments []mailer.Attachment}`, `mailer.Attachment{Filename, ContentType string; Content []byte}`.
- Produces (package-private unless noted):
  - payload `mailBookingPayload{Kind, BookingID, Recipient string; PreviousStartAt *time.Time}` with `Recipient` ∈ {`"visitor"`, `"organiser"`} (constants `mailRecipientVisitor`, `mailRecipientOrganiser`);
  - `enqueueMailBookingTo(ctx, tx, kind, bookingID, recipient string, previousStartAt *time.Time) error` (one row) and `enqueueMailBooking(ctx, tx, kind, bookingID string, previousStartAt *time.Time) error` (one row per recipient — Book/Cancel/Reschedule keep calling this);
  - `func (s *Service) composeBookingMail(ctx context.Context, appURL string, payload mailBookingPayload) (*mailer.Message, error)` — `(nil, nil)` means "nothing to send";
  - test-only exports (`export_test.go`): `type bookings.MailBookingPayload = mailBookingPayload`, `func (s *Service) ComposeBookingMailForTest(ctx, appURL string, p MailBookingPayload) (*mailer.Message, error)`. Task 9 builds on both.

- [ ] **Step 1: Update the existing assertions and add the failing tests**

`internal/bookings/emails_test.go`:

1. Change the mirror type to
```go
type mailBookingPayload struct {
	Kind            string     `json:"kind"`
	BookingID       string     `json:"bookingId"`
	Recipient       string     `json:"recipient"`
	PreviousStartAt *time.Time `json:"previousStartAt,omitempty"`
}
```
2. Replace the body of `TestBookEnqueuesConfirmedMailJob` from `rows := listJobs(...)` to the end with:
```go
	rows := listJobs(t, p.db, "mail:booking")
	if len(rows) != 2 {
		t.Fatalf("mail:booking jobs = %d, want 2 (one per recipient)", len(rows))
	}
	for _, row := range rows {
		if strings.Contains(string(row.Payload), "@") {
			t.Errorf("mail:booking payload contains an address: %s", row.Payload)
		}
		if row.RoomKey.Valid {
			t.Errorf("mail:booking job has a room_key (%s), want none", row.RoomKey.String)
		}
	}

	recipients := map[string]bool{}
	for _, payload := range decodeMailBookingJobs(t, rows) {
		if payload.Kind != "confirmed" {
			t.Errorf("Kind = %q, want confirmed", payload.Kind)
		}
		if payload.BookingID != result.BookingID {
			t.Errorf("BookingID = %q, want %q", payload.BookingID, result.BookingID)
		}
		recipients[payload.Recipient] = true
	}
	if !recipients["visitor"] || !recipients["organiser"] || len(recipients) != 2 {
		t.Errorf("recipients = %v, want exactly {visitor, organiser}", recipients)
	}
```
3. In `TestMailBookingJobSkipsStaleConfirmedAfterCancellation` change the three counts: `after Book … != 1` → `!= 2` (`want 2 (visitor + organiser)`), `after Cancel … != 2` → `!= 4` (`want 4 (2 stale confirmed + 2 fresh cancelled)`), `remaining … != 1` → `!= 2` (`want 2 (only the cancelled pair, retrying against unreachable SMTP)`), and replace the final `payload := decodeMailBookingJobs(t, rows)[0] … Kind != "cancelled"` check with:
```go
	for _, payload := range decodeMailBookingJobs(t, rows) {
		if payload.Kind != "cancelled" {
			t.Errorf("remaining job Kind = %q, want cancelled (confirmed should have completed as a no-op)", payload.Kind)
		}
	}
```
4. Append two new tests:

```go
// TestMailBookingComposesOneRecipientPerJob: each "mail:booking" row addresses exactly one party,
// so a failing organiser send (retried by the worker) can never re-deliver the visitor's mail.
func TestMailBookingComposesOneRecipientPerJob(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	owner := ownerEmail(t, p.db, p.pageID)
	const appURL = "https://whenweall.example"

	visitor, err := p.svc.ComposeBookingMailForTest(ctx, appURL, bookings.MailBookingPayload{
		Kind: "confirmed", BookingID: result.BookingID, Recipient: "visitor",
	})
	if err != nil || visitor == nil {
		t.Fatalf("compose visitor: msg=%v err=%v", visitor, err)
	}
	if visitor.To != "bob@example.com" || visitor.Template != "booking_confirmed" {
		t.Errorf("visitor message = To %q Template %q, want bob@example.com booking_confirmed", visitor.To, visitor.Template)
	}

	organiser, err := p.svc.ComposeBookingMailForTest(ctx, appURL, bookings.MailBookingPayload{
		Kind: "confirmed", BookingID: result.BookingID, Recipient: "organiser",
	})
	if err != nil || organiser == nil {
		t.Fatalf("compose organiser: msg=%v err=%v", organiser, err)
	}
	if organiser.To != owner || organiser.Template != "booking_organiser_notice" {
		t.Errorf("organiser message = To %q Template %q, want %q booking_organiser_notice", organiser.To, organiser.Template, owner)
	}

	t.Run("an unknown recipient is an error, never a double send", func(t *testing.T) {
		msg, err := p.svc.ComposeBookingMailForTest(ctx, appURL, bookings.MailBookingPayload{
			Kind: "confirmed", BookingID: result.BookingID, Recipient: "",
		})
		if err == nil || msg != nil {
			t.Fatalf("compose with empty recipient = (%v, %v), want (nil, error)", msg, err)
		}
	})

	t.Run("the organiser job is a no-op when the page has no assigned member", func(t *testing.T) {
		if _, err := p.db.ExecContext(ctx, `UPDATE booking_pages SET member_user_id = NULL WHERE id = $1`, p.pageID); err != nil {
			t.Fatalf("clear member_user_id: %v", err)
		}
		msg, err := p.svc.ComposeBookingMailForTest(ctx, appURL, bookings.MailBookingPayload{
			Kind: "confirmed", BookingID: result.BookingID, Recipient: "organiser",
		})
		if err != nil || msg != nil {
			t.Fatalf("compose organiser without member = (%v, %v), want (nil, nil)", msg, err)
		}
		visitor, err := p.svc.ComposeBookingMailForTest(ctx, appURL, bookings.MailBookingPayload{
			Kind: "confirmed", BookingID: result.BookingID, Recipient: "visitor",
		})
		if err != nil || visitor == nil {
			t.Fatalf("compose visitor without member = (%v, %v), want a message", visitor, err)
		}
	})
}

// TestMailBookingOrganiserFailureDoesNotResendVisitor is the finding's own scenario against a
// live Mailpit: the organiser's address is broken so ONLY that send fails (mailer rejects it
// before dialling); the visitor's confirmation goes out once and stays sent across the retry.
func TestMailBookingOrganiserFailureDoesNotResendVisitor(t *testing.T) {
	smtpHost, smtpPort, apiBaseURL := startMailpitForBookings(t)
	m := mailer.New(&config.Config{
		SMTPHost: smtpHost, SMTPPort: smtpPort,
		EmailFrom: "whenweall <no-reply@whenweall.example>", AppURL: "https://whenweall.example",
	})

	ctx := context.Background()
	p := setupBookablePage(t, nil)
	if _, err := p.db.ExecContext(ctx,
		`UPDATE users SET email = 'not-an-address' WHERE id = (SELECT member_user_id FROM booking_pages WHERE id = $1)`, p.pageID,
	); err != nil {
		t.Fatalf("break organiser address: %v", err)
	}
	if _, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(futureUTCSlot(3, 9, 0), "ada@example.com")); err != nil {
		t.Fatalf("Book: %v", err)
	}

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, m)
	runAllJobs(t, ctx, p.db, w)

	if total := mailpitTotal(t, apiBaseURL); total != 1 {
		t.Fatalf("mailpit total after first pass = %d, want 1 (the visitor only)", total)
	}
	rows := listJobs(t, p.db, "mail:booking")
	if len(rows) != 1 {
		t.Fatalf("mail:booking jobs remaining = %d, want 1 (the failed organiser send, backing off)", len(rows))
	}
	if pl := decodeMailBookingJobs(t, rows)[0]; pl.Recipient != "organiser" {
		t.Fatalf("remaining job Recipient = %q, want organiser", pl.Recipient)
	}

	// Pull the retry forward and run again: the organiser send fails again, the visitor is NOT
	// re-sent — before this fix the retry re-ran the visitor Send too.
	if _, err := p.db.ExecContext(ctx,
		`UPDATE scheduled_jobs SET run_at = now() - interval '1 second', locked_by = NULL, locked_at = NULL WHERE kind = 'mail:booking'`,
	); err != nil {
		t.Fatalf("force retry: %v", err)
	}
	runAllJobs(t, ctx, p.db, w)
	if total := mailpitTotal(t, apiBaseURL); total != 1 {
		t.Fatalf("mailpit total after retry = %d, want still 1", total)
	}
}
```

`internal/bookings/bookings_test.go`: rename `TestCancelRaceEnqueuesExactlyOneMailJob` to `TestCancelRaceEnqueuesOneMailJobPerRecipient`, change its doc comment's "exactly ONE "cancelled" mail:booking job is enqueued, never one per racer" to "exactly ONE "cancelled" mail:booking job PER RECIPIENT (visitor + organiser = 2) is enqueued, never one pair per racer", and change the final check to:
```go
	if cancelled != 2 {
		t.Fatalf(`"cancelled" mail:booking jobs = %d, want exactly 2 (one per recipient, among %+v) — a race would enqueue a pair per racer`, cancelled, payloads)
	}
```

`internal/bookings/google_test.go` line 269-271 (`TestBookInsertsGoogleEventAndStoresEventID`): change to
```go
	if n := countJobs(t, p.db, "mail:booking"); n != 2 {
		t.Fatalf(`countJobs("mail:booking") = %d, want 2 (just "confirmed", one row per recipient)`, n)
	}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/bookings/ -run 'TestBookEnqueuesConfirmedMailJob|TestMailBookingComposesOneRecipientPerJob' -v`
Expected: compile error `undefined: bookings.MailBookingPayload` / `ComposeBookingMailForTest` (and, once those exist, `mail:booking jobs = 1, want 2`).

- [ ] **Step 3: Add the test-only export file**

Create `internal/bookings/export_test.go`:

```go
package bookings

// Test-only exports for package bookings_test (the usual Go export-test-file convention; see
// internal/rooms/export_test.go for the precedent). Nothing here is compiled into the binary.

import (
	"context"

	"github.com/refsdal/whenweall/internal/mailer"
)

// MailBookingPayload exposes the "mail:booking" job payload shape, which external tests otherwise
// only see as raw JSON in scheduled_jobs.
type MailBookingPayload = mailBookingPayload

// ComposeBookingMailForTest exposes composeBookingMail so tests can assert what ONE "mail:booking"
// job would send — recipient, template, locale, the rendered When line — without SMTP: the
// composition is this package's own logic, delivery is internal/mailer's.
func (s *Service) ComposeBookingMailForTest(ctx context.Context, appURL string, p MailBookingPayload) (*mailer.Message, error) {
	return s.composeBookingMail(ctx, appURL, p)
}
```

- [ ] **Step 4: Rewrite the mail pipeline in `emails.go`**

In the package doc comment (lines 1-55) replace the two bullets starting `//   - Payload is ids-only (kind + bookingId)` … through `//     the booking row has been updated …` with:

```go
//   - Payload is ids-only (kind + bookingId + recipient), same rationale as mailer/queue.ts's
//     MailJob: never an address, and the handler re-reads the booking fresh at send time so a
//     booking cancelled between scheduling and sending is a no-op rather than a stale send (see
//     composeBookingMail's per-kind skip checks below).
//
//     ONE JOB PER RECIPIENT (visitor / organiser), like internal/polls/timers.go's one mail:poll
//     row per recipient: a job that sent both halves in sequence re-sent the visitor's already-
//     delivered mail on every retry of a failing organiser send (up to mailBookingMaxAttempts
//     times). Each row now composes exactly one mailer.Message (composeBookingMail) and the worker
//     retries only that one.
//
//     "rescheduled" also carries previousStartAt (a plain timestamp, never personal data): TS's own
//     sendBookingEmails needs it to render "moved from {previousWhen} to {when}", and it isn't
//     recoverable once the booking row has been updated.
```

Then replace everything from `// mailBookingPayload is the "mail:booking" job's payload` (line ~85) through the end of `enqueueMailBooking` with:

```go
// Mail recipients — one "mail:booking" row per value (see this file's package doc comment).
const (
	mailRecipientVisitor   = "visitor"
	mailRecipientOrganiser = "organiser"
)

// mailBookingPayload is the "mail:booking" job's payload — see this file's package doc comment for
// why Recipient and previousStartAt sit alongside the ids.
type mailBookingPayload struct {
	Kind            string     `json:"kind"`
	BookingID       string     `json:"bookingId"`
	Recipient       string     `json:"recipient"`
	PreviousStartAt *time.Time `json:"previousStartAt,omitempty"`
}

// enqueueMailBookingTo schedules ONE "mail:booking" job for one recipient. Not room-scoped
// (RoomKey nil): every queued mail is independent, matching internal/polls/timers.go's
// enqueueMailPoll — two reschedules in quick succession must leave two rows per recipient, never
// an upsert collapsing them into one stale send.
func enqueueMailBookingTo(ctx context.Context, tx db.DBTX, kind, bookingID, recipient string, previousStartAt *time.Time) error {
	return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:  jobKindMailBooking,
		RunAt: time.Now(),
		Payload: mailBookingPayload{
			Kind: kind, BookingID: bookingID, Recipient: recipient, PreviousStartAt: previousStartAt,
		},
		MaxAttempts: mailBookingMaxAttempts,
	})
}

// enqueueMailBooking schedules kind's mail for BOTH parties — one row for the visitor, one for the
// organiser — the shape every lifecycle kind (confirmed/cancelled/rescheduled/reminder) wants.
// Organiser-only kinds (sync_failed) call enqueueMailBookingTo directly.
func enqueueMailBooking(ctx context.Context, tx db.DBTX, kind, bookingID string, previousStartAt *time.Time) error {
	for _, recipient := range []string{mailRecipientVisitor, mailRecipientOrganiser} {
		if err := enqueueMailBookingTo(ctx, tx, kind, bookingID, recipient, previousStartAt); err != nil {
			return err
		}
	}
	return nil
}
```

Leave `reminderRoomKey`, `armBookingReminder`, `cancelBookingReminder`, `RegisterJobs` and `handleBookingReminderJob` unchanged. Then replace everything from `// handleMailBookingJob is "mail:booking"'s body` through the end of the file with:

```go
// handleMailBookingJob is "mail:booking"'s body: compose the ONE message this row stands for
// (composeBookingMail — nil means the world has moved on and there is nothing to send), then
// deliver it. A Send failure is returned so the worker retries this row alone.
func (s *Service) handleMailBookingJob(ctx context.Context, m *mailer.Mailer, job jobs.Job) error {
	var payload mailBookingPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("bookings: decode mail:booking payload: %w", err)
	}
	msg, err := s.composeBookingMail(ctx, m.AppURL(), payload)
	if err != nil {
		return err
	}
	if msg == nil {
		return nil
	}
	return m.Send(ctx, *msg)
}

// composeBookingMail re-reads the booking/page/org fresh (any one missing — including a
// soft-deleted page — is a silent no-op, the world has moved on since this was scheduled), applies
// the per-kind skip rules (queue.ts's own rationale: a booking cancelled between scheduling and
// sending must not get its confirmation anyway), and builds the single mailer.Message for
// payload.Recipient. (nil, nil) means nothing to send. An unknown recipient or kind is an error —
// never a guess that could send twice.
func (s *Service) composeBookingMail(ctx context.Context, appURL string, payload mailBookingPayload) (*mailer.Message, error) {
	if payload.Recipient != mailRecipientVisitor && payload.Recipient != mailRecipientOrganiser {
		return nil, fmt.Errorf("bookings: unknown mail:booking recipient %q", payload.Recipient)
	}

	booking, err := s.q.GetBooking(ctx, payload.BookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to send: the booking is gone
	}
	if err != nil {
		return nil, err
	}
	page, err := s.q.GetBookingPage(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to send: the page is gone or soft-deleted
	}
	if err != nil {
		return nil, err
	}
	org, err := s.q.GetOrganization(ctx, page.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to send: the org is gone
	}
	if err != nil {
		return nil, err
	}

	switch payload.Kind {
	case "confirmed":
		if booking.Status == "cancelled" {
			return nil, nil //nolint:nilnil // cancelled since scheduling
		}
		return s.composeBookingConfirmed(ctx, appURL, payload.Recipient, booking, page, org)
	case "cancelled":
		return s.composeBookingCancelled(ctx, appURL, payload.Recipient, booking, page, org)
	case "rescheduled":
		if booking.Status == "cancelled" {
			return nil, nil //nolint:nilnil // cancelled since scheduling
		}
		return s.composeBookingRescheduled(ctx, appURL, payload.Recipient, booking, page, org, payload.PreviousStartAt)
	case "reminder":
		// Re-checked here too (handleBookingReminderJob already checked once when it enqueued
		// this): the booking could have been cancelled, or the page's reminders toggled off, in
		// the time between that enqueue and this job actually running.
		if booking.Status != "confirmed" || !page.Reminders {
			return nil, nil //nolint:nilnil // no longer wanted
		}
		return s.composeBookingReminder(ctx, appURL, payload.Recipient, booking, page, org)
	case "sync_failed":
		// Ports sendGoogleSyncFailedNotice's contract (emails.ts): organiser-only, unconditional on
		// booking.Status — the booking itself is unaffected by a sync failure (google.go), only
		// the organiser's calendar may be out of sync.
		if payload.Recipient != mailRecipientOrganiser {
			return nil, nil //nolint:nilnil // there is no visitor half of this notice
		}
		return s.composeGoogleSyncFailed(ctx, page)
	default:
		return nil, fmt.Errorf("bookings: unknown mail:booking kind %q", payload.Kind)
	}
}

// resolveOrganiser looks up page.MemberUserID's account (the organiser recipient — see this file's
// package doc comment on why this port doesn't resolve a full subscriber list). Returns the
// display name to use in visitor-facing copy ("your booking with {organiser}") — the assigned
// member's name, or the org's own name when there's no member assigned or their account is gone —
// alongside the user row itself (nil when there's no organiser to mail).
func (s *Service) resolveOrganiser(ctx context.Context, page queries.BookingPage, org queries.Organization) (name string, owner *queries.User, err error) {
	if !page.MemberUserID.Valid {
		return org.Name, nil, nil
	}
	u, err := s.q.GetUser(ctx, page.MemberUserID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return org.Name, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return displayName(u), &u, nil
}

// displayName builds a recipient's display name from a Go `users` row, which (unlike Drizzle's
// `user.name`) has no single name column — only nullable FirstName/LastName. Mirrors
// internal/polls/notifications.go's own displayName exactly.
func displayName(u queries.User) string {
	first := ""
	if u.FirstName.Valid {
		first = strings.TrimSpace(u.FirstName.String)
	}
	last := ""
	if u.LastName.Valid {
		last = strings.TrimSpace(u.LastName.String)
	}
	name := strings.TrimSpace(strings.TrimSpace(first + " " + last))
	if name != "" {
		return name
	}
	if i := strings.IndexByte(u.Email, '@'); i > 0 {
		return u.Email[:i]
	}
	return u.Email
}

// orDefaultLocale returns l if set, else "en" — bookings (unlike users) do carry their own
// visitor_locale column.
func orDefaultLocale(l sql.NullString) string {
	if l.Valid && l.String != "" {
		return l.String
	}
	return "en"
}

// pageLocationText/pageDescriptionText read a booking page's optional Location/Description as a
// plain string, "" when unset.
func pageLocationText(page queries.BookingPage) string {
	if page.Location.Valid {
		return page.Location.String
	}
	return ""
}

func pageDescriptionText(page queries.BookingPage) string {
	if page.Description.Valid {
		return page.Description.String
	}
	return ""
}

// bookingWhenText renders a plain-English "When" line for a booking mail — see this file's package
// doc comment on why this isn't locale-aware the way src/lib/time.ts's formatOptionLabel is. end is
// nil for a bare point in time (a reschedule's *previous* start, whose end was never recorded —
// mirrors emails.ts's own bookingWhen(startAt, endAt | null, ...) signature).
func bookingWhenText(start time.Time, end *time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	s := start.In(loc).Format("Monday, January 2, 2006 3:04 PM")
	if end != nil {
		s += " – " + end.In(loc).Format("3:04 PM")
	}
	return s
}

// bookingManageURL/bookingDashboardURL/bookingPublicPageURL mirror emails.ts's manageUrl/
// dashboardUrl/publicPageUrl. bookingManageURL is a Service method because it appends a real
// `?t=` credential: a booking's manage token is deterministically re-derivable from its id alone
// (bookings.go's manageToken), so this queued, ids-only job can rebuild a working manage link —
// and the .ics invite's URL property gets the same link (the visitor's own credential for their
// own booking, no more sensitive in the attachment than in the body right above it).
func (s *Service) bookingManageURL(appURL, bookingID string) string {
	return appURL + "/booking/" + bookingID + "?t=" + s.manageToken(bookingID)
}

func bookingDashboardURL(appURL, pageID string) string {
	return appURL + "/bookings/" + pageID
}

func bookingPublicPageURL(appURL, orgSlug, pageSlug string) string {
	return appURL + "/book/" + orgSlug + "/" + pageSlug
}

// bookingICSAttachment is the .ics invite both confirmed/rescheduled mails carry (visitor and
// organiser get the same file).
func (s *Service) bookingICSAttachment(appURL string, booking queries.Booking, page queries.BookingPage) []mailer.Attachment {
	ics := BuildBookingICS(booking.ID, page.Title, pageDescriptionText(page), pageLocationText(page),
		booking.StartAt, booking.EndAt, s.bookingManageURL(appURL, booking.ID))
	return []mailer.Attachment{{Filename: "calendar.ics", ContentType: "text/calendar", Content: ics}}
}

// composeBookingConfirmed ports sendBookingEmails' kind==='confirmed' branch for ONE recipient:
// the visitor's confirmation (with its .ics invite), or the organiser notice — nil when the page
// has no assigned member whose account still resolves.
func (s *Service) composeBookingConfirmed(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)
	manageURL := s.bookingManageURL(appURL, booking.ID)
	attachments := s.bookingICSAttachment(appURL, booking, page)

	if recipient == mailRecipientVisitor {
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_confirmed",
			Data: map[string]any{
				"VisitorName":   booking.VisitorName,
				"PageTitle":     page.Title,
				"OrganiserName": organiserName,
				"When":          bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"Location":      location,
				"ManageURL":     manageURL,
				"Locale":        orDefaultLocale(booking.VisitorLocale),
			},
			Attachments: attachments,
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_organiser_notice",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"VisitorEmail": booking.VisitorEmail,
			"VisitorNote":  nullString(booking.VisitorNote),
			"When":         bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":     location,
			"ViewURL":      bookingDashboardURL(appURL, page.ID),
			"Locale":       "en",
		},
		Attachments: attachments,
	}, nil
}

// composeBookingCancelled ports sendBookingEmails' kind==='cancelled' branch for ONE recipient:
// both sides get the "cancelled" template, worded relative to who they are (bookingCancelledBody,
// internal/mailer/helpers.go) — "you cancelled" for whichever side caused it, "the organiser/
// visitor cancelled" for the other.
func (s *Service) composeBookingCancelled(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	cancelledBy := "organiser"
	if booking.CancelledBy.Valid {
		cancelledBy = booking.CancelledBy.String
	}

	if recipient == mailRecipientVisitor {
		visitorCancelledBy := "organiser"
		if cancelledBy == "visitor" {
			visitorCancelledBy = "you"
		}
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_cancelled",
			Data: map[string]any{
				"RecipientName": booking.VisitorName,
				"PageTitle":     page.Title,
				"When":          bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"CancelledBy":   visitorCancelledBy,
				"ViewURL":       bookingPublicPageURL(appURL, org.Slug, page.Slug),
				"Locale":        orDefaultLocale(booking.VisitorLocale),
			},
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	organiserCancelledBy := "visitor"
	if cancelledBy == "organiser" {
		organiserCancelledBy = "you"
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_cancelled",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone),
			"CancelledBy":   organiserCancelledBy,
			"VisitorName":   booking.VisitorName,
			"ViewURL":       bookingDashboardURL(appURL, page.ID),
			"Locale":        "en",
		},
	}, nil
}

// composeBookingRescheduled ports sendBookingEmails' kind==='rescheduled' branch for ONE recipient.
// previousStartAt is this row's own payload field; nil falls back to reusing the current When for
// PreviousWhen too, matching emails.ts's own `opts.previousStartAt ? ... : visitorWhen` fallback.
func (s *Service) composeBookingRescheduled(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization, previousStartAt *time.Time) (*mailer.Message, error) {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)
	manageURL := s.bookingManageURL(appURL, booking.ID)
	attachments := s.bookingICSAttachment(appURL, booking, page)

	if recipient == mailRecipientVisitor {
		visitorWhen := bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone)
		previousVisitorWhen := visitorWhen
		if previousStartAt != nil {
			previousVisitorWhen = bookingWhenText(*previousStartAt, nil, booking.VisitorTimezone)
		}
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_rescheduled",
			Data: map[string]any{
				"VisitorName":   booking.VisitorName,
				"PageTitle":     page.Title,
				"OrganiserName": organiserName,
				"PreviousWhen":  previousVisitorWhen,
				"When":          visitorWhen,
				"Location":      location,
				"ManageURL":     manageURL,
				"Locale":        orDefaultLocale(booking.VisitorLocale),
			},
			Attachments: attachments,
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	organiserWhen := bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone)
	previousOrganiserWhen := organiserWhen
	if previousStartAt != nil {
		previousOrganiserWhen = bookingWhenText(*previousStartAt, nil, page.Timezone)
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_rescheduled_organiser",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"PreviousWhen": previousOrganiserWhen,
			"When":         organiserWhen,
			"Location":     location,
			"ViewURL":      bookingDashboardURL(appURL, page.ID),
			"Locale":       "en",
		},
		Attachments: attachments,
	}, nil
}

// composeBookingReminder ports sendBookingEmails' kind==='reminder' branch for ONE recipient (no
// notification-event gating — the reminder has none, per emails.ts's own ORGANISER_EVENT comment).
// No .ics attachment, matching the TS source (a reminder isn't a new calendar entry).
func (s *Service) composeBookingReminder(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)

	if recipient == mailRecipientVisitor {
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_reminder",
			Data: map[string]any{
				"RecipientName": booking.VisitorName,
				"PageTitle":     page.Title,
				"When":          bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"Location":      location,
				"ViewURL":       s.bookingManageURL(appURL, booking.ID),
				"Locale":        orDefaultLocale(booking.VisitorLocale),
			},
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to remind
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_reminder",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":      location,
			"ViewURL":       bookingDashboardURL(appURL, page.ID),
			"Locale":        "en",
		},
	}, nil
}

// composeGoogleSyncFailed ports sendGoogleSyncFailedNotice (emails.ts): a best-effort organiser
// notice that a Google Calendar sync failed (google.go's googleSyncInsert/Delete/Reschedule are the
// only enqueuers of the "sync_failed" kind). nil when the page has no assigned member (nothing to
// notify), matching the TS source's own `if (!owner) return`.
func (s *Service) composeGoogleSyncFailed(ctx context.Context, page queries.BookingPage) (*mailer.Message, error) {
	if !page.MemberUserID.Valid {
		return nil, nil //nolint:nilnil // nothing to notify
	}
	owner, err := s.q.GetUser(ctx, page.MemberUserID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to notify
	}
	if err != nil {
		return nil, err
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_sync_failed",
		Data:     map[string]any{"PageTitle": page.Title},
	}, nil
}

// nullString reads a sql.NullString as a plain string, "" when unset.
func nullString(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}
```

`internal/bookings/google.go`: change both `return enqueueMailBooking(ctx, s.db, "sync_failed", booking.ID, nil)` (line 537) and `return false, enqueueMailBooking(ctx, s.db, "sync_failed", booking.ID, nil)` (line 575) to use `enqueueMailBookingTo(ctx, s.db, "sync_failed", booking.ID, mailRecipientOrganiser, nil)` (keeping the `return` / `return false,` prefixes).

- [ ] **Step 5: Build, vet, lint, test**

Run: `go build ./... && go vet ./internal/bookings/ && golangci-lint run ./internal/bookings/ && go test ./internal/bookings/`
Expected: no lint findings (the old `sendBooking*Mail` functions are gone, nothing unused remains); `ok`. The Mailpit tests (`TestMailBookingDeliversRealMail`, `TestMailBookingOrganiserFailureDoesNotResendVisitor`) run when Docker is available and skip otherwise.

- [ ] **Step 6: Commit**

```bash
git add internal/bookings/emails.go internal/bookings/google.go internal/bookings/export_test.go internal/bookings/emails_test.go internal/bookings/bookings_test.go internal/bookings/google_test.go
git commit -m "fix(bookings): one mail:booking job per recipient so a retry never re-sends the other side

Payload gains recipient (visitor|organiser); Book/Cancel/Reschedule enqueue
one row each, sync_failed only the organiser's. The job handler composes and
sends exactly one message (composeBookingMail), so a failing organiser send
retries alone instead of re-delivering the visitor's mail up to 10 times.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 9: Locale-aware booking mail for visitor and organiser

Findings: *"Visitor locale is no longer captured on booking, so visitor mails are always English"* (server accepts `locale`; Plan A makes the web `bookSlot` send it — this task proves it end to end and validates the value) and *"Organiser-side mails always render 'en' and the 'When' line is not locale-aware"* (`emails.go` hard-codes `"Locale": "en"` for every organiser mail, and `bookingWhenText` renders a fixed English format for both parties).

**Files:**
- Modify: `internal/bookings/schemas.go` (imports; `BookInput.Validate`), `internal/bookings/pages.go` (`Service` struct + new `SetLocaleResolver`), `internal/bookings/emails.go` (imports; `bookingWhenText`; the four `compose*` functions; package doc bullets), `cmd/whenweall/main.go` (lines 134-153: create `authSvc` before `bookingsSvc`, wire the resolver)
- Test: `internal/bookings/schemas_test.go` (`TestBookInputValidate`), `internal/bookings/handlers_test.go` (`TestHandlerBook`), `internal/bookings/emails_test.go` (new `TestBookingMailLocale`), `web/src/api/__tests__/bookings.test.ts` (`bookSlot` sends `locale`)

**Interfaces:**
- Consumes (Plan A): `authSvc.LocaleFor(ctx, userID string) string`; `mailer.SupportedLocales`; `mailer.FormatDate`, `mailer.FormatDateTime`, `mailer.FormatTimeRange`; web `bookSlot` sends `locale`.
- Consumes (Task 8): `composeBookingMail`, `ComposeBookingMailForTest`, `MailBookingPayload`.
- Produces: `func (s *Service) SetLocaleResolver(fn func(ctx context.Context, userID string) string)` (nil/unset → "en"); `bookingWhenText(locale string, start time.Time, end *time.Time, timezone string) string`; `BookInput.Validate` rejects a `Locale` outside `mailer.SupportedLocales` (field `locale`).

- [ ] **Step 1: Write the failing Go tests**

`internal/bookings/schemas_test.go`, inside `TestBookInputValidate`, add:

```go
	t.Run("rejects a locale the mailer has no catalog for", func(t *testing.T) {
		de := "de"
		in := baseBookInput(func(in *bookings.BookInput) { in.Locale = &de })
		fields := fieldsOf(t, in.Validate())
		if fields["locale"] == "" {
			t.Errorf("Fields = %+v, want a locale entry", fields)
		}
	})

	t.Run("accepts every supported locale, and no locale at all", func(t *testing.T) {
		for _, l := range []string{"en", "nb"} {
			locale := l
			if err := baseBookInput(func(in *bookings.BookInput) { in.Locale = &locale }).Validate(); err != nil {
				t.Errorf("Validate(locale=%s) = %v, want nil", l, err)
			}
		}
		if err := baseBookInput(func(in *bookings.BookInput) { in.Locale = nil }).Validate(); err != nil {
			t.Errorf("Validate(no locale) = %v, want nil", err)
		}
	})
```

`internal/bookings/handlers_test.go`, inside `TestHandlerBook`, append:

```go
	t.Run("the visitor's locale lands in visitor_locale and the response", func(t *testing.T) {
		body := bookBody(futureUTCSlot(4, 10, 0))
		body["locale"] = "nb"
		rec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", body, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		booked := decodeBody[struct {
			Booking bookings.BookingView `json:"booking"`
		}](t, rec)
		if booked.Booking.VisitorLocale == nil || *booked.Booking.VisitorLocale != "nb" {
			t.Errorf("response visitorLocale = %v, want nb", booked.Booking.VisitorLocale)
		}
		var stored sql.NullString
		if err := p.d.QueryRowContext(context.Background(),
			`SELECT visitor_locale FROM bookings WHERE id = $1`, booked.Booking.ID).Scan(&stored); err != nil {
			t.Fatalf("read visitor_locale: %v", err)
		}
		if !stored.Valid || stored.String != "nb" {
			t.Errorf("visitor_locale = %+v, want nb", stored)
		}
	})

	t.Run("422 for an unsupported locale", func(t *testing.T) {
		body := bookBody(futureUTCSlot(4, 11, 0))
		body["locale"] = "de"
		rec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", body, nil)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		if errFields(t, rec)["locale"] == "" {
			t.Errorf("fields = %+v, want a locale entry", errFields(t, rec))
		}
	})
```

`internal/bookings/emails_test.go`, append:

```go
// TestBookingMailLocale ports emails.ts's per-recipient locale handling: the visitor's mail follows
// bookings.visitor_locale, the organiser's follows their own account locale (auth.Service.LocaleFor
// via SetLocaleResolver), and the "When" line is rendered in that locale (mailer.FormatDate/
// FormatTimeRange) — not a fixed English layout for everyone.
func TestBookingMailLocale(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil) // page timezone UTC, 30-minute slots
	start := futureUTCSlot(3, 9, 0)
	end := start.Add(30 * time.Minute)
	in := bookInput(start, "bob@example.com") // visitor timezone UTC
	nb := "nb"
	in.Locale = &nb
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, in)
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	const appURL = "https://whenweall.example"

	wantNB := mailer.FormatDate("nb", start, time.UTC) + ", " + mailer.FormatTimeRange("nb", start, end, time.UTC)
	wantEN := mailer.FormatDate("en", start, time.UTC) + ", " + mailer.FormatTimeRange("en", start, end, time.UTC)
	if wantNB == wantEN {
		t.Fatalf("test precondition: nb and en renderings must differ, both are %q", wantNB)
	}

	compose := func(t *testing.T, recipient string) *mailer.Message {
		t.Helper()
		msg, err := p.svc.ComposeBookingMailForTest(ctx, appURL, bookings.MailBookingPayload{
			Kind: "confirmed", BookingID: result.BookingID, Recipient: recipient,
		})
		if err != nil || msg == nil {
			t.Fatalf("compose %s: msg=%v err=%v", recipient, msg, err)
		}
		return msg
	}

	t.Run("visitor mail follows visitor_locale", func(t *testing.T) {
		msg := compose(t, "visitor")
		if msg.Data["Locale"] != "nb" {
			t.Errorf("Locale = %v, want nb", msg.Data["Locale"])
		}
		if msg.Data["When"] != wantNB {
			t.Errorf("When = %q, want %q", msg.Data["When"], wantNB)
		}
	})

	t.Run("organiser mail is en when no locale resolver is wired", func(t *testing.T) {
		msg := compose(t, "organiser")
		if msg.Data["Locale"] != "en" {
			t.Errorf("Locale = %v, want en", msg.Data["Locale"])
		}
		if msg.Data["When"] != wantEN {
			t.Errorf("When = %q, want %q", msg.Data["When"], wantEN)
		}
	})

	t.Run("organiser mail follows the resolver's locale for the page's member", func(t *testing.T) {
		var askedFor string
		p.svc.SetLocaleResolver(func(_ context.Context, userID string) string {
			askedFor = userID
			return "nb"
		})
		msg := compose(t, "organiser")
		if askedFor != ownerUserID(t, p.db, p.pageID) {
			t.Errorf("resolver asked for user %q, want the page's member %q", askedFor, ownerUserID(t, p.db, p.pageID))
		}
		if msg.Data["Locale"] != "nb" {
			t.Errorf("Locale = %v, want nb", msg.Data["Locale"])
		}
		if msg.Data["When"] != wantNB {
			t.Errorf("When = %q, want %q", msg.Data["When"], wantNB)
		}
	})

	t.Run("a reschedule's previous start (no end) renders as a bare date-time", func(t *testing.T) {
		prev := start.Add(-24 * time.Hour)
		msg, err := p.svc.ComposeBookingMailForTest(ctx, appURL, bookings.MailBookingPayload{
			Kind: "rescheduled", BookingID: result.BookingID, Recipient: "visitor", PreviousStartAt: &prev,
		})
		if err != nil || msg == nil {
			t.Fatalf("compose rescheduled: msg=%v err=%v", msg, err)
		}
		if want := mailer.FormatDateTime("nb", prev, time.UTC); msg.Data["PreviousWhen"] != want {
			t.Errorf("PreviousWhen = %q, want %q", msg.Data["PreviousWhen"], want)
		}
	})
}
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/bookings/ -run 'TestBookInputValidate|TestHandlerBook|TestBookingMailLocale' -v`
Expected: compile error `p.svc.SetLocaleResolver undefined`; after Step 3's struct change but before the rest, `When = "Monday, …" want "…"`, `422 … status = 201`.

- [ ] **Step 3: Implement**

`internal/bookings/schemas.go`: add `"slices"` and `"github.com/refsdal/whenweall/internal/mailer"` to the import block. In `BookInput.Validate`, after the `in.Note` check add:

```go
	// bookSlotSchema's locale was `z.enum(locales)` in the TS source; only a locale the mailer has
	// a catalog for may be stored, or the visitor's mail would silently fall back to English.
	if in.Locale != nil && !slices.Contains(mailer.SupportedLocales, *in.Locale) {
		fields["locale"] = "locale must be one of " + strings.Join(mailer.SupportedLocales, ", ")
	}
```

`internal/bookings/pages.go`: add to the `Service` struct (after the `google GoogleSync` field):

```go
	// localeFor resolves a user's preferred mail locale — nil until SetLocaleResolver wires
	// auth.Service.LocaleFor (cmd/whenweall/main.go); emails.go's organiserLocale falls back to
	// "en" when unset, so every existing NewService caller/test keeps rendering English.
	localeFor func(ctx context.Context, userID string) string
```

and after `SetGoogleSync`:

```go
// SetLocaleResolver wires the per-user locale lookup the organiser half of every booking mail
// renders with (emails.go's organiserLocale) — auth.Service.LocaleFor in production. A setter,
// like SetGoogleSync, so NewService's signature and every existing caller stay unchanged; nil
// means "always en".
func (s *Service) SetLocaleResolver(fn func(ctx context.Context, userID string) string) {
	s.localeFor = fn
}
```

`internal/bookings/emails.go`:

1. Add `"strconv"` to the imports.
2. In the package doc comment delete the two bullets that begin `//   - The email body's "When" text is plain English` and `//   - No user-locale column exists in this Go port's` (each bullet runs to the blank `//` line after it) and put this one in their place:

```go
//   - Both mails are rendered in their recipient's own locale, as emails.ts did: the visitor's from
//     bookings.visitor_locale, the organiser's from their account preference (SetLocaleResolver →
//     auth.Service.LocaleFor, user_preferences.locale); the "When" line goes through internal/
//     mailer's FormatDate/FormatTimeRange/FormatDateTime (the Go stand-in for formatOptionLabel's
//     Intl.DateTimeFormat), so a Norwegian organiser reads "tir. 1. sep., 09:00–09:30".
```

3. Replace `bookingWhenText` (doc comment + function) with:

```go
// organiserLocale resolves the organiser recipient's mail locale through the resolver wired by
// SetLocaleResolver (auth.Service.LocaleFor in production — the per-user locale persisted in
// user_preferences). "en" when no resolver is wired, there is no owner, or the resolver has no
// answer.
func (s *Service) organiserLocale(ctx context.Context, owner *queries.User) string {
	if s.localeFor == nil || owner == nil {
		return "en"
	}
	if l := s.localeFor(ctx, strconv.FormatInt(owner.ID, 10)); l != "" {
		return l
	}
	return "en"
}

// bookingWhenText renders a booking mail's "When" line in the recipient's locale and timezone —
// the port of emails.ts's bookingWhen (formatOptionLabel in the recipient's locale). end is nil for
// a bare point in time (a reschedule's *previous* start, whose end was never recorded).
//
//	en: "Tue 1 Sep, 09:00–09:30"    nb: "tir. 1. sep., 09:00–09:30"    no end: "Tue 1 Sep, 09:00"
func bookingWhenText(locale string, start time.Time, end *time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	if end == nil {
		return mailer.FormatDateTime(locale, start, loc)
	}
	return mailer.FormatDate(locale, start, loc) + ", " + mailer.FormatTimeRange(locale, start, *end, loc)
}
```

4. Replace the four compose functions written in Task 8 with these locale-aware versions:

```go
// composeBookingConfirmed ports sendBookingEmails' kind==='confirmed' branch for ONE recipient:
// the visitor's confirmation (with its .ics invite), or the organiser notice — nil when the page
// has no assigned member whose account still resolves. Each side gets its own locale and timezone.
func (s *Service) composeBookingConfirmed(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)
	manageURL := s.bookingManageURL(appURL, booking.ID)
	attachments := s.bookingICSAttachment(appURL, booking, page)

	if recipient == mailRecipientVisitor {
		locale := orDefaultLocale(booking.VisitorLocale)
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_confirmed",
			Data: map[string]any{
				"VisitorName":   booking.VisitorName,
				"PageTitle":     page.Title,
				"OrganiserName": organiserName,
				"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"Location":      location,
				"ManageURL":     manageURL,
				"Locale":        locale,
			},
			Attachments: attachments,
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	locale := s.organiserLocale(ctx, owner)
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_organiser_notice",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"VisitorEmail": booking.VisitorEmail,
			"VisitorNote":  nullString(booking.VisitorNote),
			"When":         bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":     location,
			"ViewURL":      bookingDashboardURL(appURL, page.ID),
			"Locale":       locale,
		},
		Attachments: attachments,
	}, nil
}

// composeBookingCancelled ports sendBookingEmails' kind==='cancelled' branch for ONE recipient:
// both sides get the "cancelled" template, worded relative to who they are (bookingCancelledBody,
// internal/mailer/helpers.go) — "you cancelled" for whichever side caused it, "the organiser/
// visitor cancelled" for the other.
func (s *Service) composeBookingCancelled(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	cancelledBy := "organiser"
	if booking.CancelledBy.Valid {
		cancelledBy = booking.CancelledBy.String
	}

	if recipient == mailRecipientVisitor {
		visitorCancelledBy := "organiser"
		if cancelledBy == "visitor" {
			visitorCancelledBy = "you"
		}
		locale := orDefaultLocale(booking.VisitorLocale)
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_cancelled",
			Data: map[string]any{
				"RecipientName": booking.VisitorName,
				"PageTitle":     page.Title,
				"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"CancelledBy":   visitorCancelledBy,
				"ViewURL":       bookingPublicPageURL(appURL, org.Slug, page.Slug),
				"Locale":        locale,
			},
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	organiserCancelledBy := "visitor"
	if cancelledBy == "organiser" {
		organiserCancelledBy = "you"
	}
	locale := s.organiserLocale(ctx, owner)
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_cancelled",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone),
			"CancelledBy":   organiserCancelledBy,
			"VisitorName":   booking.VisitorName,
			"ViewURL":       bookingDashboardURL(appURL, page.ID),
			"Locale":        locale,
		},
	}, nil
}

// composeBookingRescheduled ports sendBookingEmails' kind==='rescheduled' branch for ONE recipient.
// previousStartAt is this row's own payload field; nil falls back to reusing the current When for
// PreviousWhen too, matching emails.ts's own `opts.previousStartAt ? ... : visitorWhen` fallback.
func (s *Service) composeBookingRescheduled(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization, previousStartAt *time.Time) (*mailer.Message, error) {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)
	manageURL := s.bookingManageURL(appURL, booking.ID)
	attachments := s.bookingICSAttachment(appURL, booking, page)

	if recipient == mailRecipientVisitor {
		locale := orDefaultLocale(booking.VisitorLocale)
		when := bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone)
		previousWhen := when
		if previousStartAt != nil {
			previousWhen = bookingWhenText(locale, *previousStartAt, nil, booking.VisitorTimezone)
		}
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_rescheduled",
			Data: map[string]any{
				"VisitorName":   booking.VisitorName,
				"PageTitle":     page.Title,
				"OrganiserName": organiserName,
				"PreviousWhen":  previousWhen,
				"When":          when,
				"Location":      location,
				"ManageURL":     manageURL,
				"Locale":        locale,
			},
			Attachments: attachments,
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	locale := s.organiserLocale(ctx, owner)
	when := bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone)
	previousWhen := when
	if previousStartAt != nil {
		previousWhen = bookingWhenText(locale, *previousStartAt, nil, page.Timezone)
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_rescheduled_organiser",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"PreviousWhen": previousWhen,
			"When":         when,
			"Location":     location,
			"ViewURL":      bookingDashboardURL(appURL, page.ID),
			"Locale":       locale,
		},
		Attachments: attachments,
	}, nil
}

// composeBookingReminder ports sendBookingEmails' kind==='reminder' branch for ONE recipient (no
// notification-event gating — the reminder has none, per emails.ts's own ORGANISER_EVENT comment).
// No .ics attachment, matching the TS source (a reminder isn't a new calendar entry).
func (s *Service) composeBookingReminder(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)

	if recipient == mailRecipientVisitor {
		locale := orDefaultLocale(booking.VisitorLocale)
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_reminder",
			Data: map[string]any{
				"RecipientName": booking.VisitorName,
				"PageTitle":     page.Title,
				"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"Location":      location,
				"ViewURL":       s.bookingManageURL(appURL, booking.ID),
				"Locale":        locale,
			},
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to remind
	}
	locale := s.organiserLocale(ctx, owner)
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_reminder",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":      location,
			"ViewURL":       bookingDashboardURL(appURL, page.ID),
			"Locale":        locale,
		},
	}, nil
}
```

`cmd/whenweall/main.go`: replace lines 134-153 (from the `// bookingsSvc owns the booking-page domain` comment through the `authSvc, err := auth.New(cfg, sqlDB)` block) with:

```go
	// authSvc is built before bookingsSvc: the booking mail pipeline resolves the organiser's mail
	// locale through authSvc.LocaleFor (SetLocaleResolver below), and it must be wired before
	// RegisterJobs/worker.Run so no "mail:booking" job ever sees a half-built Service.
	authSvc, err := auth.New(cfg, sqlDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// bookingsSvc owns the booking-page domain: its HTTP surface (Register, below) and its own
	// three scheduled job kinds (booking.reminder/mail:booking/google:sync — RegisterJobs) share
	// this one instance, the same shape pollsSvc uses above. SetGoogleSync wires in a real Google
	// Calendar client (nil — sync off — when the capability itself isn't configured; see
	// NewGoogleSync's own doc comment; the feature is disabled in the API/UI regardless, see
	// handlers.go's handleGoogleStatus), SetLocaleResolver the per-user mail locale — both BEFORE
	// RegisterJobs so jobs the worker picks up immediately after Run starts see a fully wired
	// Service, never a half-built one.
	bookingsSvc := bookings.NewService(cfg, sqlDB)
	bookingsSvc.SetGoogleSync(bookings.NewGoogleSync(cfg, sqlDB))
	bookingsSvc.SetLocaleResolver(authSvc.LocaleFor)
	bookingsSvc.RegisterJobs(worker, m)

	if err := jobs.EnsureScheduled(ctx, sqlDB); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	go worker.Run(ctx)
```

(The former `authSvc, err := auth.New(cfg, sqlDB)` block that followed `go worker.Run(ctx)` is removed; `srv := httpserver.New(cfg, sqlDB, authSvc)` stays as is.)

- [ ] **Step 4: Build, vet, lint, test**

Run: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./internal/bookings/`
Expected: clean; `ok`.

- [ ] **Step 5: Web — prove `bookSlot` sends the locale**

Append to `web/src/api/__tests__/bookings.test.ts` (add `bookSlot` to the import from `'#/api/bookings'`):

```ts
describe('bookSlot', () => {
  it('sends the visitor locale so the confirmation mail renders in their language', async () => {
    let seenBody: Record<string, unknown> | null = null
    server.use(
      http.post('/api/v1/book/ada/intro/bookings', async ({ request }) => {
        seenBody = (await request.json()) as Record<string, unknown>
        return HttpResponse.json(
          { booking: { id: 'bk_1' }, manageToken: 'tok' },
          { status: 201 },
        )
      }),
    )

    await bookSlot('ada', 'intro', {
      startAt: '2026-09-15T07:00:00.000Z',
      name: 'Ada',
      email: 'ada@example.com',
      timezone: 'Europe/Oslo',
    })

    // paraglide's runtime resolves the base locale ("en") under vitest.
    expect(seenBody).toMatchObject({ locale: 'en', timezone: 'Europe/Oslo' })
  })
})
```

Run: `cd web && bunx vitest run src/api/__tests__/bookings.test.ts`
Expected: PASS — Plan A's plumbing already sends `locale` (Global Constraints). If, and only if, the assertion reports `locale` missing, Plan A's step did not land in `bookSlot`: add `import { getLocale } from '#/lib/i18n'` to `web/src/api/bookings.ts` and `locale: getLocale(),` to the request body object inside `bookSlot` (after `timezone: input.timezone,`), then re-run until PASS.

- [ ] **Step 6: Web gates**

Run: `cd web && bun run typecheck && bun run lint && bunx vitest run`
Expected: clean; all suites pass.

- [ ] **Step 7: Commit**

```bash
git add internal/bookings/schemas.go internal/bookings/pages.go internal/bookings/emails.go cmd/whenweall/main.go internal/bookings/schemas_test.go internal/bookings/handlers_test.go internal/bookings/emails_test.go web/src/api/__tests__/bookings.test.ts web/src/api/bookings.ts
git commit -m "feat(bookings): locale-aware booking mail for visitor and organiser

Visitor locale is validated against mailer.SupportedLocales and proven to
land in visitor_locale; the organiser side resolves its locale through
auth.Service.LocaleFor (SetLocaleResolver); the When line renders via
mailer.FormatDate/FormatTimeRange/FormatDateTime in the recipient's locale.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 10: Google Calendar sync disabled at the API

User decision (fixed): Google Calendar sync is DISABLED for now. Root finding: *"'Connect Google Calendar' cannot grant calendar scopes — Limen's /oauth/google/link ignores `scopes`"* — no consent flow exists, so "connected" was a lie. The Go sync code (`google.go`) stays dormant and tested (Task 13); this task makes the HTTP surface honest.

**Files:**
- Modify: `internal/bookings/handlers.go` (`Register`: drop the disconnect route; `handleCreatePage`/`handleUpdatePage`: reject `googleSync: true`; `handleGoogleStatus`: constant; delete `handleDisconnectGoogle`), `internal/bookings/google.go` (doc comments of `GoogleStatus` and `DisconnectGoogleSync`)
- Test: `internal/bookings/handlers_test.go` (`TestHandlerCreatePage`, `TestHandlerUpdatePage`, `TestHandlerGoogleStatus`, `TestHandlerDisconnectGoogle`)

**Interfaces:**
- Produces: `GET /api/v1/booking-pages/{id}/google-status` → `200 {"connected":false,"syncEnabled":false}` after the existing `RequireManageablePage` gate (401/403/404 unchanged). `POST /api/v1/booking-pages` and `PATCH /api/v1/booking-pages/{id}` → `400 {"error":{"code":"google_sync_unavailable"}}` when the body has `"googleSync": true`. `POST /api/v1/me/google/disconnect` is no longer mounted (404). Service methods `GoogleStatus`/`DisconnectGoogleSync` remain (exported, dormant). Task 11 removes the web callers.

- [ ] **Step 1: Rewrite/extend the handler tests**

Replace `TestHandlerGoogleStatus`'s main case (from `p := setupHandlerPage…` through the `if out.Available {…}` check, keeping the three `t.Run` subtests below it) with:

```go
	p := setupHandlerPage(t, testConfig(t))

	// The answer is a constant — even with the Google capability configured, a linked Google
	// account for the page's member AND the page's own google_sync flag on. Google Calendar sync
	// is disabled in v5 (user decision 2026-09-03); Service.GoogleStatus stays dormant.
	p.s.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.d))
	insertGoogleAccount(t, p.d, ownerUserID(t, p.d, p.pageID), "access-tok", "refresh-tok")
	if _, err := p.d.ExecContext(context.Background(), `UPDATE booking_pages SET google_sync = true WHERE id = $1`, p.pageID); err != nil {
		t.Fatalf("set google_sync: %v", err)
	}

	rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/google-status", nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var out map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]bool{"connected": false, "syncEnabled": false}
	if len(out) != 2 || out["connected"] != want["connected"] || out["syncEnabled"] != want["syncEnabled"] {
		t.Errorf("body = %v, want exactly %v", out, want)
	}
```

Replace `TestHandlerDisconnectGoogle` entirely with:

```go
// ---- row 15: POST /api/v1/me/google/disconnect — NOT mounted (Google Calendar sync disabled) ---

func TestHandlerDisconnectGoogleRouteRemoved(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	rec := doRequest(t, p.h, "POST", "/api/v1/me/google/disconnect", nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route not mounted while Google Calendar sync is disabled); body=%s", rec.Code, rec.Body)
	}
}
```

In `TestHandlerCreatePage` append a subtest:

```go
	t.Run("400 google_sync_unavailable when googleSync is true (sync disabled in v5)", func(t *testing.T) {
		withSync := map[string]any{}
		for k, v := range body {
			withSync[k] = v
		}
		withSync["slug"] = "synced-call"
		withSync["googleSync"] = true
		rec := doRequest(t, h, "POST", "/api/v1/booking-pages", withSync, sessHeader(userID))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "google_sync_unavailable" {
			t.Errorf("code = %q, want google_sync_unavailable", errCode(t, rec))
		}
	})
```

In `TestHandlerUpdatePage` append a subtest:

```go
	t.Run("400 google_sync_unavailable when googleSync is true (sync disabled in v5)", func(t *testing.T) {
		withSync := map[string]any{}
		for k, v := range body {
			withSync[k] = v
		}
		withSync["googleSync"] = true
		rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, withSync, sessHeader(p.ownerID))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "google_sync_unavailable" {
			t.Errorf("code = %q, want google_sync_unavailable", errCode(t, rec))
		}
		view := decodeBody[bookings.PageView](t, doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(p.ownerID)))
		if view.GoogleSync {
			t.Errorf("GoogleSync = true after a rejected PATCH, want false")
		}
	})
```

- [ ] **Step 2: Run and watch them fail**

Run: `go test ./internal/bookings/ -run 'TestHandlerGoogleStatus|TestHandlerDisconnectGoogleRouteRemoved|TestHandlerCreatePage|TestHandlerUpdatePage' -v`
Expected: FAIL — `body = map[available:true]`, `status = 200, want 404`, `status = 201, want 400`, `status = 200, want 400`.

- [ ] **Step 3: Implement**

`internal/bookings/handlers.go`:

1. In `Register` delete the line `mux.Handle("POST /api/v1/me/google/disconnect", s.handleDisconnectGoogle(a))`.
2. Delete `handleDisconnectGoogle` (doc comment + function).
3. Add, after `respondOK`:

```go
// rejectGoogleSync enforces the v5 decision that Google Calendar sync is not available (user
// decision 2026-09-03): a page request asking for googleSync=true is refused up front with 400
// google_sync_unavailable, before CreatePage/UpdatePage ever run. The service layer itself still
// accepts the flag — google.go's sync code is dormant, not deleted, and its own tests drive it
// directly — so this handler-level check is the one switch the SPA can reach. Returns true when
// the response has been written.
func rejectGoogleSync(w http.ResponseWriter, req pageRequest) bool {
	if !req.GoogleSync {
		return false
	}
	httpserver.Err(w, http.StatusBadRequest, "google_sync_unavailable",
		"google calendar sync is not available in this version", nil)
	return true
}
```

4. In `handleCreatePage` and `handleUpdatePage`, directly after `if !httpserver.DecodeJSON(w, r, &req) { return }` add:

```go
	if rejectGoogleSync(w, req) {
		return
	}
```

5. Replace `handleGoogleStatus` (doc comment + function) with:

```go
// handleGoogleStatus — GET /api/v1/booking-pages/{id}/google-status. Google Calendar sync is
// disabled in v5 (user decision 2026-09-03): after the same RequireManageablePage gate every other
// owner-facing page route has (a page id's existence must never leak across orgs — requirement
// (a)), this answers a constant {"connected":false,"syncEnabled":false} regardless of the page's
// own google_sync flag, the capability config, or any linked Google account. Service.GoogleStatus
// (google.go) stays as dormant code for when the feature returns with a real consent flow.
func (s *Service) handleGoogleStatus(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pageID := r.PathValue("id")
	if err := s.RequireManageablePage(r.Context(), pageID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"connected": false, "syncEnabled": false})
}
```

`internal/bookings/google.go`: prepend one line to the `GoogleStatus` doc comment and one to `DisconnectGoogleSync`'s:

```go
// DORMANT (Google Calendar sync is disabled in v5 — handlers.go's handleGoogleStatus answers a
// constant and no route calls this; kept, with its tests, for the feature's return).
```

- [ ] **Step 4: Build, lint, test**

Run: `go build ./... && go vet ./... && golangci-lint run ./internal/bookings/ && go test ./internal/bookings/`
Expected: clean (no unused-function finding — `handleDisconnectGoogle` is deleted, `DisconnectGoogleSync` is exported); `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/bookings/handlers.go internal/bookings/google.go internal/bookings/handlers_test.go
git commit -m "feat(bookings): disable Google Calendar sync at the API

google-status answers a constant not-connected, POST/PATCH refuse googleSync
true with 400 google_sync_unavailable, and the disconnect route is unmounted.
The sync code in google.go stays dormant and tested.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 11: Google Calendar sync disabled in the SPA

Same user decision, web side: remove the Google card and the dead `scopes` plumbing (*"README promises incremental Google Calendar consent"* / *"'Connect Google Calendar' cannot grant calendar scopes"*); the editor never sends `googleSync: true` (the API now refuses it); orphaned message keys go.

**Files:**
- Delete: `web/src/components/booking/GoogleCalendarCard.tsx`
- Modify: `web/src/components/booking/PageEditor.tsx` (import line 7; props lines 130-141; the `{!isCreate && (<Section title={m.booking_editor_section_integrations()}>…)}` block, lines 396-409), `web/src/routes/bookings/new.tsx` (lines 19-27), `web/src/routes/bookings/$id/edit.tsx` (lines 22-32), `web/src/components/booking/editor-state.ts` (`draftToInput`, line 268), `web/src/api/bookings.ts` (delete `getGoogleCalendarStatus` lines 181-185 and `disconnectGoogleCalendar` lines 191-193), `web/src/api/auth.ts` (`oauthLinkUrl`, lines 130-149), `web/messages/en.json` and `web/messages/nb.json` (delete keys)
- Test: `web/src/components/booking/__tests__/editor-state.test.ts`

**Interfaces:**
- Produces: `PageEditor({ page, handle, appUrl })` — `googleEnabled` prop removed; `oauthLinkUrl(provider: string, opts?: { redirectUri?: string }): Promise<string>`; `draftToInput` always emits `googleSync: false`. `publicConfig.googleEnabled` stays (login/signup buttons use it).

- [ ] **Step 1: Write the failing editor-state test**

In `web/src/components/booking/__tests__/editor-state.test.ts`, inside `describe('draftFromPage', …)`, change the `'round-trips a page through the draft unchanged'` expectation line `googleSync: page.googleSync,` to `googleSync: false,` and rename that test to `'round-trips a page through the draft, except googleSync which is always off'`. Then add:

```ts
  it('never asks for googleSync, even for a page that had it on (sync is disabled in v5)', () => {
    const input = draftToInput(draftFromPage(pageFixture({ googleSync: true })))

    expect(input?.googleSync).toBe(false)
  })
```

Run: `cd web && bunx vitest run src/components/booking/__tests__/editor-state.test.ts`
Expected: FAIL — `expected true to be false` (twice).

- [ ] **Step 2: Editor never sends `googleSync: true`**

In `web/src/components/booking/editor-state.ts` `draftToInput`, replace `    googleSync: draft.googleSync,` with:

```ts
    // Google Calendar sync is disabled in v5: the API refuses `googleSync: true` (400
    // google_sync_unavailable), so the editor never asks for it — even for a page saved with the
    // flag on before the switch was thrown.
    googleSync: false,
```

Run: `cd web && bunx vitest run src/components/booking/__tests__/editor-state.test.ts`
Expected: PASS.

- [ ] **Step 3: Remove the card and its plumbing**

1. `git rm web/src/components/booking/GoogleCalendarCard.tsx`
2. `web/src/components/booking/PageEditor.tsx`: delete line 7 (`import { GoogleCalendarCard } …`); change the component signature to

```tsx
export function PageEditor({
  page,
  handle,
  appUrl,
}: {
  page: PageView | null
  handle: string | null
  appUrl: string
}) {
```

and delete the whole block

```tsx
      {!isCreate && (
        <Section title={m.booking_editor_section_integrations()}>
          {/* Google Calendar status is per-PAGE now … */}
          <GoogleCalendarCard … />
        </Section>
      )}
```

3. `web/src/routes/bookings/new.tsx`: the component becomes

```tsx
function NewBookingPageRoute() {
  const { session } = Route.useRouteContext()

  return <PageEditor page={null} handle={session?.org?.slug ?? null} appUrl={window.location.origin} />
}
```

4. `web/src/routes/bookings/$id/edit.tsx`: the component becomes

```tsx
function EditBookingPageRoute() {
  const page = Route.useLoaderData()
  const { session } = Route.useRouteContext()

  return (
    <PageEditor
      // Remounts when a different page is opened, so the draft never carries over from the last.
      key={page.id}
      page={page}
      handle={session?.org?.slug ?? null}
      appUrl={window.location.origin}
    />
  )
}
```

5. `web/src/api/bookings.ts`: delete `getGoogleCalendarStatus` (lines 181-185) and `disconnectGoogleCalendar` (lines 191-193).

6. `web/src/api/auth.ts`: replace `oauthLinkUrl` (doc comment + function, lines 130-149) with

```ts
/** `GET /oauth/:provider/link` — links a second sign-in provider to an already-signed-in caller
 * (protected). Limen's handler reads only `redirect_uri`/`error_redirect_uri` and always requests
 * the provider's fixed default scopes (openid/email/profile for Google): there is no incremental
 * consent here, which is why Google Calendar sync is disabled in v5. */
export async function oauthLinkUrl(provider: string, opts?: { redirectUri?: string }): Promise<string> {
  const { url } = await api<AuthorizeResponse>(
    'GET',
    `/api/v1/auth/oauth/${provider}/link`,
    undefined,
    { query: opts?.redirectUri ? { redirect_uri: opts.redirectUri } : undefined },
  )
  return url
}
```

7. Delete these keys from BOTH `web/messages/en.json` and `web/messages/nb.json` (each is one line; keep the JSON valid — mind the trailing comma on the new last key of each block): `booking_editor_section_integrations`, `booking_google_title`, `booking_google_desc_connected`, `booking_google_desc_disconnected`, `booking_google_connect`, `booking_google_disconnect`, `booking_google_sync_label`, `booking_google_disconnected_toast`, `booking_google_error`, `booking_google_checking`, `booking_google_unavailable`.

- [ ] **Step 4: Web gates**

Run: `cd web && bun run typecheck && bun run lint && bunx vitest run && grep -rn "booking_google\|GoogleCalendarCard\|getGoogleCalendarStatus\|disconnectGoogleCalendar\|booking_editor_section_integrations" src messages`
Expected: typecheck/lint clean, all suites pass, the grep prints nothing.

- [ ] **Step 5: Commit**

```bash
git add -A web/src/components/booking web/src/routes/bookings web/src/api/bookings.ts web/src/api/auth.ts web/messages/en.json web/messages/nb.json
git commit -m "feat(web): remove the Google Calendar card and dead calendar-scope plumbing

Google Calendar sync is disabled in v5: no card in the page editor, the
editor never sends googleSync true, oauthLinkUrl drops the scopes param
Limen ignored, and the orphaned booking_google_* messages go.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 12: README — Google Calendar sync is not available in v5 yet

Finding: *"README promises incremental Google Calendar consent that the code does not perform"* (README.md:314-323) plus every other paragraph describing calendar sync as a working feature. Keep "Continue with Google" (works).

**Files:**
- Modify: `README.md` (lines 44-45, 161-164, 173-174, 213, 219, 227, 257-259, 263-265, 276, 298, 314-323, 490, 539-540, 547-549)

**Interfaces:** none.

- [ ] **Step 1: Apply these exact replacements**

1. Lines 44-45 — replace
```
ever makes are the ones you configure yourself (your SMTP relay, and Google's API if you
turn on calendar sync).
```
with
```
ever makes are the ones you configure yourself (your SMTP relay, and Google's OAuth
endpoints if you turn on Google sign-in).
```

2. Lines 161-164 — replace
```
  for one-off days off or extra hours. Optionally connect **Google Calendar**: busy
  times block slots automatically, and every booking creates (and later removes) a real
  event on your calendar. Turn on **reminder e-mails** 24 hours out, and see every
  booking — upcoming and past, with cancel — on the page's own roster.
```
with
```
  for one-off days off or extra hours. Turn on **reminder e-mails** 24 hours out, and see
  every booking — upcoming and past, with cancel — on the page's own roster. Google
  Calendar sync (busy-time blocking, events on your calendar) is **not available in v5
  yet** — see [Roadmap](#roadmap).
```

3. Lines 173-174 — replace
```
  endpoints and the built SPA, runs scheduled jobs (digests, reminders, Google Calendar
  sync) in-process, and runs its own migrations on boot. Postgres is the only other
```
with
```
  endpoints and the built SPA, runs scheduled jobs (digests, reminders) in-process, and
  runs its own migrations on boot. Postgres is the only other
```

4. Diagram: line 213 `J["Job worker<br/>internal/jobs<br/>digests · reminders · google:sync"]` → `J["Job worker<br/>internal/jobs<br/>digests · reminders · housekeeping"]`; delete line 219 `G["Google Calendar API<br/>freebusy · events (optional)"]` and line 227 `J -. "freebusy, create/delete event (optional)" .-> G`.

5. Lines 257-259 — replace
```
booking endpoint call it with the same inputs — booked slots (with their buffers) plus,
if the organiser connected Google Calendar, that account's freebusy over the requested
range — so a slot the visitor is shown is always still bookable a moment later, short of
```
with
```
booking endpoint call it with the same inputs — booked slots, with their buffers — so a
slot the visitor is shown is always still bookable a moment later, short of
```

6. Lines 263-265 — replace
```
manage link — nothing to store or leak from a database) and, if Google Calendar is
connected, a real calendar event; cancelling or rescheduling reverses both. A
`booking.reminder` job, scheduled per booking, e-mails both parties 24 hours out.
```
with
```
manage link — nothing to store or leak from a database). A `booking.reminder` job,
scheduled per booking, e-mails both parties 24 hours out.
```

7. Line 276 — `integration below (Turnstile, Google sign-in/calendar, OIDC) is all-or-nothing: leave` → `integration below (Turnstile, Google sign-in, OIDC) is all-or-nothing: leave`.

8. Line 298 — replace the `GOOGLE_CLIENT_ID` row's description `Optional "Continue with Google" and Google Calendar sync. Needs `GOOGLE_CLIENT_SECRET` too.` with `Optional "Continue with Google". Needs `GOOGLE_CLIENT_SECRET` too.` (pad the cell so the table column stays aligned).

9. Lines 314-323 — replace the whole **Google Calendar scopes.** paragraph with
```
**Google Calendar sync is not available in v5 yet.** The `GOOGLE_CLIENT_ID`/
`GOOGLE_CLIENT_SECRET` pair only powers "Continue with Google", which requests the
default openid/email/profile scopes and nothing else. The v3 booking-page calendar
integration (busy-time blocking, events on the organiser's calendar) has not been
re-enabled in the Go backend: the page editor shows no Google card, the API refuses
`googleSync: true`, and `GET /api/v1/booking-pages/{id}/google-status` always answers
"not connected". You do not need to add any calendar scopes to your OAuth consent screen.
```

10. Line 490 — `  jobs/                   scheduled-job worker: digests, reminders, google:sync, housekeeping` → `  jobs/                   scheduled-job worker: digests, reminders, housekeeping`.

11. Lines 539-540 — replace
```
- **Go rewrite — done.** The whole backend left Cloudflare Workers/D1/Durable Objects
  for a single Go binary and Postgres — see [Architecture](#architecture).
```
with
```
- **Go rewrite — done.** The whole backend left Cloudflare Workers/D1/Durable Objects
  for a single Go binary and Postgres — see [Architecture](#architecture). One v3
  feature did not make the cut yet: Google Calendar sync (below).
```

12. In "What's next", before the `- **Google Meet links**` bullet add
```
- **Google Calendar sync (return)** — re-enable v3's busy-time blocking and event
  creation once incremental consent for the calendar scopes is wired through the auth
  layer; the Go sync code is in the tree, dormant.
```
and change the Meet bullet's first line from `- **Google Meet links** — auto-attach a Meet link to the calendar event a booking` to `- **Google Meet links** — once calendar sync is back, auto-attach a Meet link to the event a booking`.

- [ ] **Step 2: Verify**

Run: `grep -n -i "calendar sync\|google calendar\|freebusy\|google:sync" README.md`
Expected: only lines that say the feature is *not available* / *not yet* / roadmap (the two Features paragraphs, the Configuration paragraph, the Go-rewrite and What's-next bullets, the v3 history bullet at ~535 which describes what v3 shipped and stays). No line describes sync as currently working.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: Google Calendar sync is not available in v5 yet

Replace the incremental-consent instructions and every paragraph describing
calendar sync as live with a short not-yet note; Google sign-in still works.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

### Task 13: Re-express the two missing old assertions

Finding: *"Specific old assertions not re-expressed in Go (consolidated list)"* items (3) — `DeleteEvent` treats 404/410 as success (`google.go:339-344`, untested; old `main:src/server/google/__tests__/calendar.workers.test.ts:99-113`) — and (6) — the reminder job skips a booking on a soft-deleted page (old `main:src/do/__tests__/booking-room.workers.test.ts:374`).

**Files:**
- Test: `internal/bookings/google_test.go` (append), `internal/bookings/emails_test.go` (append)

**Interfaces:**
- Consumes: `bookings.NewGoogleSync(cfg, db) GoogleSync` and `GoogleSync.DeleteEvent(ctx, userID, eventID string) error`; test helpers `testGoogleConfig`, `withGoogleAPIStub`, `insertGoogleAccount`, `runAllJobs` (google_test.go), `seedUser` (handlers_test.go), `reminderJob`, `listJobs`, `decodeMailBookingJobs`, `testBookingMailer` (emails_test.go), `setupBookablePage`, `futureUTCSlot`, `bookInput` (bookings_test.go).

- [ ] **Step 1: Write the tests (these pin existing behaviour and pass immediately)**

Append to `internal/bookings/google_test.go`:

```go
// TestGoogleSyncDeleteEventTreatsGoneAsSuccess ports calendar.workers.test.ts's deleteEvent cases:
// a 404 or 410 (the event is already gone, however that happened) is success, anything else is a
// hard failure. Exercised directly on the dormant GoogleSync (Google Calendar sync is disabled in
// v5's API/UI; the client is kept for its return).
func TestGoogleSyncDeleteEventTreatsGoneAsSuccess(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		wantErr bool
	}{
		{"404 is success", http.StatusNotFound, false},
		{"410 is success", http.StatusGone, false},
		{"500 is a hard failure", http.StatusInternalServerError, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			d := testdb.New(t)
			userID := seedUser(t, d)
			insertGoogleAccount(t, d, userID, "access-tok", "refresh-tok")

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/calendars/primary/events/evt_123" {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(tc.status)
			}))
			withGoogleAPIStub(t, ts)

			g := bookings.NewGoogleSync(testGoogleConfig(), d)
			err := g.DeleteEvent(ctx, userID, "evt_123")
			if tc.wantErr && err == nil {
				t.Fatalf("DeleteEvent(%d) = nil, want an error", tc.status)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("DeleteEvent(%d) = %v, want nil (already gone counts as deleted)", tc.status, err)
			}
		})
	}
}
```

Append to `internal/bookings/emails_test.go`:

```go
// TestReminderJobSkipsSoftDeletedPage ports booking-room.workers.test.ts's "skips a booking on a
// soft-deleted page but still clears its key": a due reminder for a booking whose page has since
// been deleted enqueues no reminder mail, and the reminder row itself is consumed (not retried).
func TestReminderJobSkipsSoftDeletedPage(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "alice@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if _, ok := reminderJob(t, p.db, result.BookingID); !ok {
		t.Fatal("booking.reminder job not armed after Book")
	}

	if err := p.svc.DeletePage(ctx, p.pageID, p.orgID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	// Make the reminder due now (it was armed at start-24h, days away).
	if _, err := p.db.ExecContext(ctx,
		`UPDATE scheduled_jobs SET run_at = now() - interval '1 second' WHERE kind = 'booking.reminder' AND room_key = $1`,
		"booking:"+result.BookingID,
	); err != nil {
		t.Fatalf("force reminder due: %v", err)
	}

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	runAllJobs(t, ctx, p.db, w)

	if _, ok := reminderJob(t, p.db, result.BookingID); ok {
		t.Error("booking.reminder job still present after running against a deleted page, want consumed")
	}
	for _, pl := range decodeMailBookingJobs(t, listJobs(t, p.db, "mail:booking")) {
		if pl.Kind == "reminder" {
			t.Fatalf("a reminder mail:booking job was enqueued for a booking on a deleted page: %+v", pl)
		}
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/bookings/ -run 'TestGoogleSyncDeleteEventTreatsGoneAsSuccess|TestReminderJobSkipsSoftDeletedPage' -v`
Expected: PASS (3 subtests + 1). Then `go test ./internal/bookings/` → `ok`.

- [ ] **Step 3: Full gates for the plan**

Run: `go build ./... && go vet ./... && golangci-lint run ./... && go test ./... && cd web && bun run typecheck && bun run lint && bunx vitest run`
Expected: everything green.

- [ ] **Step 4: Commit**

```bash
git add internal/bookings/google_test.go internal/bookings/emails_test.go
git commit -m "test(bookings): pin DeleteEvent 404/410-as-success and reminder skip on a deleted page

Re-expresses the two old assertions the audit found missing, on the dormant
Google client and the booking.reminder job.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01RCiiYnh2N1nANzdYW9YG1p"
```

---

## Coverage map (self-review)

| Finding (plan-D brief item) | Task |
|---|---|
| 1 "Add to calendar" dead path | 1 |
| 2 update/delete `page.changed` | 3 |
| 3 organiser send re-sends visitor mail | 8 |
| 4 visitor locale captured end-to-end; organiser locale + locale-aware When | 9 |
| 5 unknown page → BookNotFound | 2 |
| 6 `.ics` organiser fallback + Content-Disposition | 6 |
| 7 PATCH full replace / nil availability / status | 4 |
| 8 shared 20/min limiter | 5 |
| 9 booking-page list N+1 | 7 |
| 10 Google Calendar DISABLE (API, UI, README, scopes param, messages) | 10, 11, 12 |
| 11 old assertions (3) DeleteEvent 404/410, (6) reminder on deleted page | 13 |
| 12 Google switch unavailable while creating | moot after 10/11 — no action |

Type consistency checked across tasks: `BookingICS(ctx, id, token, byOrganiser, appURL)` (6); `PublicBookRateLimit`/`PublicReadRateLimit` (5); `mailBookingPayload.Recipient`, `enqueueMailBookingTo`, `composeBookingMail`, `MailBookingPayload`, `ComposeBookingMailForTest` (8 → 9, 13); `bookingWhenText(locale, start, end, tz)` and `SetLocaleResolver` (9); `ListBookingPageSummariesByOrg{Params,Row}` (7); `baseInput`/`openPageInput` carry `Status: "active"` from Task 4 onward.

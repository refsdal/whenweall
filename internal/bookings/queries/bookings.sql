-- Task 2 (booking pages service) queries below.

-- name: InsertBookingPage :exec
INSERT INTO booking_pages (
  id, organization_id, created_by, member_user_id, slug, title, description, location,
  timezone, slot_duration_min, buffer_before_min, buffer_after_min, min_notice_min,
  max_days_ahead, availability, date_overrides, google_sync, reminders, status,
  created_at, updated_at
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21
);

-- name: GetBookingPage :one
SELECT * FROM booking_pages WHERE id = $1 AND deleted_at IS NULL;

-- name: ListBookingPagesByOrg :many
SELECT * FROM booking_pages WHERE organization_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC;

-- name: UpdateBookingPage :exec
UPDATE booking_pages SET
  slug = $2,
  title = $3,
  description = $4,
  location = $5,
  timezone = $6,
  slot_duration_min = $7,
  buffer_before_min = $8,
  buffer_after_min = $9,
  min_notice_min = $10,
  max_days_ahead = $11,
  availability = $12,
  date_overrides = $13,
  google_sync = $14,
  reminders = $15,
  status = $16,
  updated_at = $17
WHERE id = $1;

-- name: SoftDeleteBookingPage :exec
UPDATE booking_pages SET deleted_at = $2, updated_at = $2 WHERE id = $1;

-- name: CountUpcomingConfirmedBookings :one
SELECT count(*) FROM bookings WHERE page_id = $1 AND status = 'confirmed' AND start_at >= $2;

-- name: GetOrganizationBySlug :one
SELECT * FROM organizations WHERE slug = $1;

-- name: GetBookingPageByOrgSlug :one
SELECT * FROM booking_pages WHERE organization_id = $1 AND slug = $2 AND deleted_at IS NULL;

-- name: UpdateOrganizationSlug :exec
UPDATE organizations SET slug = $2, updated_at = now() WHERE id = $1;

-- Task 3 (booking creation/manage service) queries below.

-- name: GetBookingPageByOrgSlugForUpdate :one
-- Locks the page row for the duration of the enclosing transaction — Book/Reschedule's own
-- invariant lock (see bookings.go's package doc comment): serializes every concurrent
-- book/reschedule attempt against THIS page, so the "recompute busy intervals, then insert"
-- sequence below can never interleave across transactions.
SELECT * FROM booking_pages WHERE organization_id = $1 AND slug = $2 AND deleted_at IS NULL FOR UPDATE;

-- name: GetBookingPageForUpdate :one
-- Same lock as GetBookingPageByOrgSlugForUpdate, keyed by id instead of (org, slug) — used by
-- Reschedule, which already has the booking's page_id in hand.
SELECT * FROM booking_pages WHERE id = $1 AND deleted_at IS NULL FOR UPDATE;

-- name: InsertBooking :exec
INSERT INTO bookings (
  id, page_id, start_at, end_at, visitor_name, visitor_email, visitor_note, visitor_locale,
  visitor_timezone, status, cancelled_by, manage_token_hash, google_event_id, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: GetBooking :one
SELECT * FROM bookings WHERE id = $1;

-- name: ListConfirmedBookingsInRange :many
-- Confirmed bookings on a page overlapping [range_from, range_to) as their raw stored interval —
-- see bookedIntervalsForPage's (bookings.go) doc comment on why no buffer is applied here.
SELECT * FROM bookings
WHERE page_id = $1
  AND status = 'confirmed'
  AND start_at < sqlc.arg(range_to)
  AND end_at > sqlc.arg(range_from)
ORDER BY start_at;

-- name: ListBookingsInRange :many
-- Every booking (any status) on a page overlapping [range_from, range_to) — ListPageBookings'
-- own query, unfiltered by status (matching listBookings, bookings.ts).
SELECT * FROM bookings
WHERE page_id = $1
  AND start_at < sqlc.arg(range_to)
  AND end_at > sqlc.arg(range_from)
ORDER BY start_at;

-- name: UpdateBookingStatus :exec
UPDATE bookings SET status = $2, cancelled_by = $3, updated_at = $4 WHERE id = $1;

-- name: UpdateBookingSchedule :exec
UPDATE bookings SET start_at = $2, end_at = $3, updated_at = $4 WHERE id = $1;

-- name: GetOrganization :one
SELECT * FROM organizations WHERE id = $1;

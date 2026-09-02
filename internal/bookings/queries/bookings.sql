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

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
  visitor_timezone, status, cancelled_by, google_event_id, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetBooking :one
SELECT * FROM bookings WHERE id = $1;

-- name: GetBookingForUpdate :one
-- M2/M7 (bookings.go): locks the booking row for the duration of the enclosing transaction.
-- Cancel takes this alone (it never touches the page row — there's no availability invariant to
-- protect on a cancel); Reschedule takes it too, AFTER GetBookingPageForUpdate (lock order is
-- always page then booking — see Reschedule's own doc comment for why that order, and why it
-- never deadlocks against Cancel, which only ever takes this one lock).
SELECT * FROM bookings WHERE id = $1 FOR UPDATE;

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

-- Task 4 (booking mail set and reminders) queries below.

-- name: GetUser :one
-- The page's memberUserId owner — who receives the organiser half of a lifecycle mail (emails.go).
SELECT * FROM users WHERE id = $1;

-- Task 5 (Google Calendar sync) queries below.

-- name: UpdateBookingGoogleEventID :exec
-- Persists (or clears, when null) the booking's known Google Calendar event id. See
-- Service.SetGoogleEventID (google.go).
UPDATE bookings SET google_event_id = $2, updated_at = $3 WHERE id = $1;

-- name: GetGoogleAccount :one
-- The linked Google account row backing a page's member — google.go's own token store. Limen owns
-- this table (migrations/00002_auth.sql); this package only ever reads/refreshes the one row a
-- Google Calendar sync needs, never any other provider's row.
SELECT id, access_token, refresh_token, access_token_expires_at
FROM accounts
WHERE user_id = $1 AND provider = 'google'
ORDER BY id
LIMIT 1;

-- name: UpdateGoogleAccountToken :exec
-- Persists a refreshed access (and, when Google issued a new one, refresh) token back onto the
-- account row google.go just refreshed it from.
UPDATE accounts
SET access_token = $2, refresh_token = $3, access_token_expires_at = $4, updated_at = $5
WHERE id = $1;

-- Task 6 (HTTP surface — accumulated requirement (a), the canManageContent-shaped authz gate)
-- queries below.

-- name: IsOrgMember :one
-- Ports the membership-is-the-authority rule (canManageContent's own membership precondition —
-- see internal/polls/queries's identical query for the sibling rationale): does userId belong to
-- organizationId at all, regardless of role?
SELECT EXISTS (
  SELECT 1 FROM organization_members WHERE organization_id = $1 AND user_id = $2
) AS is_member;

-- name: MemberHasManagingRole :one
-- Ports canManageContent's role half (org-roles.ts): does userId hold an 'owner' or 'admin' role
-- in organizationId? (The creator-manages-their-own-content half is checked separately by the
-- caller against the resource's own createdBy — this query only ever answers the role question.)
SELECT EXISTS (
  SELECT 1 FROM organization_members om
  JOIN organization_member_roles omr ON omr.member_id = om.id
  WHERE om.organization_id = $1 AND om.user_id = $2 AND omr.role IN ('owner', 'admin')
) AS has_role;

-- name: MemberHasOwnerRole :one
-- Ports requireOwnerRole's own predicate (org-roles.ts): does userId hold the 'owner' role
-- specifically (not admin) in organizationId? Used only by SetOrgSlug/org-handle's own
-- RequireOwnerRole (authz.go) — every other owner-facing route accepts admin too
-- (MemberHasManagingRole above).
SELECT EXISTS (
  SELECT 1 FROM organization_members om
  JOIN organization_member_roles omr ON omr.member_id = om.id
  WHERE om.organization_id = $1 AND om.user_id = $2 AND omr.role = 'owner'
) AS has_role;

-- name: DisableGoogleSyncForMember :exec
-- Ports disconnectGoogleSync (pages.ts): turns googleSync off on every booking page whose
-- member_user_id is userId — the account row itself (accounts, Limen's) is left untouched; only
-- this user's own pages stop trying to sync against it.
UPDATE booking_pages SET google_sync = false, updated_at = $2 WHERE member_user_id = $1;

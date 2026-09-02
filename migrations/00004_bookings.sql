-- +goose Up

-- Booking pages (a page owner's public scheduling page) + bookings (a visitor's reserved slot on
-- one), transcribed from drizzle/0000_orange_zzzax.sql with the spec §6 re-cut applied: every
-- text-typed timestamp column (created_at/updated_at/deleted_at/start_at/end_at) becomes
-- timestamptz, and availability/date_overrides (stored as JSON-encoded text in the drizzle/SQLite
-- source) become jsonb.
--
-- Ruling (same as migrations/00003_polls.sql): organization_id/created_by/member_user_id are
-- bigint, referencing Limen's organizations/users (migrations/00002_auth.sql). Domain entity ids
-- (booking_pages.id, bookings.id) stay text nanoids, unchanged from drizzle. FK ON DELETE
-- behaviors are carried over from drizzle's own ALTER TABLE ... ADD CONSTRAINT statements
-- (organization_id: cascade: a deleted org takes its booking pages with it; created_by/
-- member_user_id: set null, matching bookingPages.createdBy/memberUserId's own nullable columns;
-- bookings.page_id: cascade).

CREATE TABLE booking_pages (
  id text PRIMARY KEY NOT NULL,
  organization_id bigint NOT NULL,
  created_by bigint,
  member_user_id bigint,
  slug text NOT NULL,
  title text NOT NULL,
  description text,
  location text,
  timezone text NOT NULL,
  slot_duration_min integer NOT NULL,
  buffer_before_min integer NOT NULL,
  buffer_after_min integer NOT NULL,
  min_notice_min integer NOT NULL,
  max_days_ahead integer NOT NULL,
  availability jsonb NOT NULL,
  date_overrides jsonb,
  google_sync boolean NOT NULL DEFAULT false,
  reminders boolean NOT NULL DEFAULT true,
  status text NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz,
  CONSTRAINT fk_booking_pages_organization_id FOREIGN KEY (organization_id) REFERENCES organizations (id) ON DELETE CASCADE ON UPDATE NO ACTION,
  CONSTRAINT fk_booking_pages_created_by FOREIGN KEY (created_by) REFERENCES users (id) ON DELETE SET NULL ON UPDATE NO ACTION,
  CONSTRAINT fk_booking_pages_member_user_id FOREIGN KEY (member_user_id) REFERENCES users (id) ON DELETE SET NULL ON UPDATE NO ACTION
);
CREATE UNIQUE INDEX booking_pages_org_slug_uidx ON booking_pages (organization_id, slug) WHERE deleted_at IS NULL;

CREATE TABLE bookings (
  id text PRIMARY KEY NOT NULL,
  page_id text NOT NULL,
  start_at timestamptz NOT NULL,
  end_at timestamptz NOT NULL,
  visitor_name text NOT NULL,
  visitor_email text NOT NULL,
  visitor_note text,
  visitor_locale text,
  visitor_timezone text NOT NULL,
  status text NOT NULL DEFAULT 'confirmed',
  cancelled_by text,
  manage_token_hash text NOT NULL,
  google_event_id text,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  CONSTRAINT fk_bookings_page_id FOREIGN KEY (page_id) REFERENCES booking_pages (id) ON DELETE CASCADE ON UPDATE NO ACTION
);
CREATE INDEX bookings_page_start_idx ON bookings (page_id, start_at);

-- +goose Down
DROP TABLE bookings;
DROP TABLE booking_pages;

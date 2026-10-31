-- +goose Up
-- A reversible takedown for one poll, sign-up sheet or booking page (#48), short of deleting it
-- or locking its owner's whole account.
--
-- A row with suspended_at set is treated as gone by every read the app makes — the public page,
-- the organiser's dashboard and editor, the WebSocket room, every mail job — exactly as if it were
-- soft-deleted, except that the public page says it was removed rather than that it never
-- existed. softdelete:purge skips a suspended row even after its owner deletes it, so the
-- evidence is kept, and clearing the column brings the content back. See docs/abuse-reports.md
-- for when and how staff set it, and what does not come back (mail that fell due meanwhile).
ALTER TABLE polls ADD COLUMN suspended_at timestamptz;
ALTER TABLE booking_pages ADD COLUMN suspended_at timestamptz;

-- +goose Down
ALTER TABLE booking_pages DROP COLUMN suspended_at;
ALTER TABLE polls DROP COLUMN suspended_at;

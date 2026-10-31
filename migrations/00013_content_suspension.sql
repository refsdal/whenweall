-- +goose Up
-- A reversible takedown for one poll, sign-up sheet or booking page (#48), short of deleting it
-- or locking its owner's whole account.
--
-- A row with suspended_at set is treated as gone by every read the app makes — the public page,
-- the organiser's dashboard and editor, the WebSocket room, every mail job — exactly as if it were
-- soft-deleted, except that the public page says it was removed rather than that it never
-- existed. Unlike deleted_at it is never purged (softdelete:purge only looks at deleted_at), so
-- the evidence is kept and clearing the column undoes the takedown completely. See
-- docs/abuse-reports.md for when and how staff set it.
ALTER TABLE polls ADD COLUMN suspended_at timestamptz;
ALTER TABLE booking_pages ADD COLUMN suspended_at timestamptz;

-- +goose Down
ALTER TABLE booking_pages DROP COLUMN suspended_at;
ALTER TABLE polls DROP COLUMN suspended_at;

package jobs_test

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

// seedPurgeOrg inserts a bare organization for the purge tests' polls and booking pages to hang
// off — nothing in the sweep looks at it beyond the FK.
func seedPurgeOrg(t *testing.T, d *sql.DB) int64 {
	t.Helper()
	var id int64
	if err := d.QueryRowContext(context.Background(),
		`INSERT INTO organizations (name, slug) VALUES ('Purge', $1) RETURNING id`, "purge-"+db.NewID(),
	).Scan(&id); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	return id
}

// seedPurgePoll inserts a poll soft-deleted deletedAgo ago ("" for a live poll), with one
// participant and one comment so the test can see the FK cascade take them too.
func seedPurgePoll(t *testing.T, d *sql.DB, orgID int64, deletedAgo string) string {
	t.Helper()
	ctx := context.Background()
	id := db.NewID()
	deletedAt := "NULL"
	if deletedAgo != "" {
		deletedAt = "now() - interval '" + deletedAgo + "'"
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO polls (id, organization_id, type, title, timezone, created_at, updated_at, deleted_at)
		VALUES ($1, $2, 'dates', 'Poll', 'UTC', now(), now(), `+deletedAt+`)
	`, id, orgID); err != nil {
		t.Fatalf("seed poll: %v", err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO participants (id, poll_id, name, email, created_at, updated_at)
		VALUES ($1, $2, 'Ada', 'ada@example.com', now(), now())
	`, db.NewID(), id); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	seedPurgeComment(t, d, id, "")
	return id
}

// seedPurgeComment inserts a comment on pollID, soft-deleted deletedAgo ago ("" for a live one).
func seedPurgeComment(t *testing.T, d *sql.DB, pollID, deletedAgo string) string {
	t.Helper()
	id := db.NewID()
	deletedAt := "NULL"
	if deletedAgo != "" {
		deletedAt = "now() - interval '" + deletedAgo + "'"
	}
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO comments (id, poll_id, author_name, body, created_at, deleted_at)
		VALUES ($1, $2, 'Ada', 'hello', now(), `+deletedAt+`)
	`, id, pollID); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	return id
}

// seedPurgePage inserts a booking page soft-deleted deletedAgo ago ("" for a live page), with one
// booking so the test can see the visitor's name and address go with it.
func seedPurgePage(t *testing.T, d *sql.DB, orgID int64, deletedAgo string) string {
	t.Helper()
	ctx := context.Background()
	id := db.NewID()
	deletedAt := "NULL"
	if deletedAgo != "" {
		deletedAt = "now() - interval '" + deletedAgo + "'"
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO booking_pages (id, organization_id, slug, title, timezone, slot_duration_min,
			buffer_before_min, buffer_after_min, min_notice_min, max_days_ahead, availability,
			created_at, updated_at, deleted_at)
		VALUES ($1, $2, $3, 'Page', 'UTC', 30, 0, 0, 0, 30, '{}'::jsonb, now(), now(), `+deletedAt+`)
	`, id, orgID, "page-"+id); err != nil {
		t.Fatalf("seed booking page: %v", err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO bookings (id, page_id, start_at, end_at, visitor_name, visitor_email,
			visitor_timezone, created_at, updated_at)
		VALUES ($1, $2, now(), now() + interval '30 minutes', 'Ada', 'ada@example.com', 'UTC', now(), now())
	`, db.NewID(), id); err != nil {
		t.Fatalf("seed booking: %v", err)
	}
	return id
}

func countRows(t *testing.T, d *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func runSoftDeletePurge(t *testing.T, d *sql.DB) {
	t.Helper()
	ctx := context.Background()
	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d, rooms.BroadcastPresenceTotal)
	room := "global"
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "softdelete:purge", RoomKey: &room, RunAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
}

// TestSoftDeletePurgeRemovesContentPastGracePeriod (#44): a poll or booking page soft-deleted
// more than 30 days ago is hard-deleted, and with it — by FK cascade — every participant, vote,
// comment and booking that still held someone's name or address. One deleted inside the grace
// period, and one never deleted, survive untouched.
func TestSoftDeletePurgeRemovesContentPastGracePeriod(t *testing.T) {
	d := testdb.New(t)
	org := seedPurgeOrg(t, d)

	oldPoll := seedPurgePoll(t, d, org, "31 days")
	recentPoll := seedPurgePoll(t, d, org, "29 days")
	livePoll := seedPurgePoll(t, d, org, "")
	oldPage := seedPurgePage(t, d, org, "31 days")
	recentPage := seedPurgePage(t, d, org, "29 days")
	livePage := seedPurgePage(t, d, org, "")

	runSoftDeletePurge(t, d)

	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{"31-day-old deleted poll", oldPoll, 0},
		{"29-day-old deleted poll", recentPoll, 1},
		{"live poll", livePoll, 1},
	} {
		if got := countRows(t, d, `SELECT count(*) FROM polls WHERE id = $1`, tc.id); got != tc.want {
			t.Errorf("%s: %d rows, want %d", tc.name, got, tc.want)
		}
	}
	if got := countRows(t, d, `SELECT count(*) FROM participants WHERE poll_id = $1`, oldPoll); got != 0 {
		t.Errorf("purged poll left %d participants behind, want 0 (FK cascade)", got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM comments WHERE poll_id = $1`, oldPoll); got != 0 {
		t.Errorf("purged poll left %d comments behind, want 0 (FK cascade)", got)
	}

	for _, tc := range []struct {
		name string
		id   string
		want int
	}{
		{"31-day-old deleted page", oldPage, 0},
		{"29-day-old deleted page", recentPage, 1},
		{"live page", livePage, 1},
	} {
		if got := countRows(t, d, `SELECT count(*) FROM booking_pages WHERE id = $1`, tc.id); got != tc.want {
			t.Errorf("%s: %d rows, want %d", tc.name, got, tc.want)
		}
	}
	if got := countRows(t, d, `SELECT count(*) FROM bookings WHERE page_id = $1`, oldPage); got != 0 {
		t.Errorf("purged page left %d bookings behind, want 0 (FK cascade)", got)
	}
}

// TestSoftDeletePurgeRemovesDeletedCommentsOnLivePolls: a comment deleted on a poll that is
// itself still live keeps its body in the row — the same retention problem one level down.
func TestSoftDeletePurgeRemovesDeletedCommentsOnLivePolls(t *testing.T) {
	d := testdb.New(t)
	org := seedPurgeOrg(t, d)
	poll := seedPurgePoll(t, d, org, "")
	oldComment := seedPurgeComment(t, d, poll, "31 days")
	recentComment := seedPurgeComment(t, d, poll, "29 days")

	runSoftDeletePurge(t, d)

	if got := countRows(t, d, `SELECT count(*) FROM comments WHERE id = $1`, oldComment); got != 0 {
		t.Errorf("31-day-old deleted comment still present")
	}
	if got := countRows(t, d, `SELECT count(*) FROM comments WHERE id = $1`, recentComment); got != 1 {
		t.Errorf("29-day-old deleted comment purged, want kept until the grace period ends")
	}
	if got := countRows(t, d, `SELECT count(*) FROM comments WHERE poll_id = $1 AND deleted_at IS NULL`, poll); got != 1 {
		t.Errorf("live comment count = %d, want 1 (untouched)", got)
	}
}

// TestSoftDeletePurgeDrainsMoreThanOneBatch: the sweep deletes in bounded batches, but loops until
// nothing eligible is left rather than stopping after the first.
func TestSoftDeletePurgeDrainsMoreThanOneBatch(t *testing.T) {
	d := testdb.New(t)
	org := seedPurgeOrg(t, d)
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO polls (id, organization_id, type, title, timezone, created_at, updated_at, deleted_at)
		SELECT 'bulk-' || g, $1, 'dates', 'Poll', 'UTC', now(), now(), now() - interval '40 days'
		FROM generate_series(1, $2::int) g
	`, org, jobs.PurgeBatchSize*2+1); err != nil {
		t.Fatalf("seed bulk polls: %v", err)
	}

	runSoftDeletePurge(t, d)

	if got := countRows(t, d, `SELECT count(*) FROM polls`); got != 0 {
		t.Errorf("%d polls left after purge, want 0", got)
	}
}

func TestSoftDeletePurgeReschedulesItself(t *testing.T) {
	d := testdb.New(t)
	runSoftDeletePurge(t, d)

	var runAt time.Time
	if err := d.QueryRowContext(context.Background(),
		`SELECT run_at FROM scheduled_jobs WHERE kind = 'softdelete:purge' AND room_key = 'global'`,
	).Scan(&runAt); err != nil {
		t.Fatalf("select rescheduled job: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("run_at = %v, want in the future (rescheduled)", runAt)
	}
}

// TestSoftDeletePurgeKeepsSuspendedContent (#48): content staff have suspended is kept as
// evidence, so an owner deleting it after a report must not let the 30-day sweep erase it.
func TestSoftDeletePurgeKeepsSuspendedContent(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	org := seedPurgeOrg(t, d)
	poll := seedPurgePoll(t, d, org, "31 days")
	page := seedPurgePage(t, d, org, "31 days")
	if _, err := d.ExecContext(ctx, `UPDATE polls SET suspended_at = now() WHERE id = $1`, poll); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `UPDATE booking_pages SET suspended_at = now() WHERE id = $1`, page); err != nil {
		t.Fatal(err)
	}

	runSoftDeletePurge(t, d)

	if got := countRows(t, d, `SELECT count(*) FROM polls WHERE id = $1`, poll); got != 1 {
		t.Errorf("suspended, deleted poll purged; want kept as evidence")
	}
	if got := countRows(t, d, `SELECT count(*) FROM comments WHERE poll_id = $1`, poll); got != 1 {
		t.Errorf("suspended poll's comments = %d, want 1 kept", got)
	}
	if got := countRows(t, d, `SELECT count(*) FROM booking_pages WHERE id = $1`, page); got != 1 {
		t.Errorf("suspended, deleted booking page purged; want kept as evidence")
	}
}

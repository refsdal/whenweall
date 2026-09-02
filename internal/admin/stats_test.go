package admin_test

// Task 2's own tests: stats.ts had no ported test file to port case-for-case (its
// stats.workers.test.ts asserts deltas, because the workers test pool shares D1 storage across a
// file's tests — see that file's own comment). testdb.New gives every Go test an isolated,
// freshly-migrated database instead, so these assert exact numbers against a controlled seed, per
// the task brief, rather than deltas.

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/admin"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/testdb"
)

func daysAgo(n int) time.Time {
	return time.Now().Add(-time.Duration(n) * 24 * time.Hour)
}

func insertUserAt(t *testing.T, d *sql.DB, createdAt time.Time) string {
	t.Helper()
	n := seedSeq.Add(1)
	var uid int64
	err := d.QueryRowContext(context.Background(), `
		INSERT INTO users (email, created_at, updated_at) VALUES ($1, $2, now()) RETURNING id
	`, fmt.Sprintf("stats-user-%d@example.com", n), createdAt).Scan(&uid)
	if err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return fmt.Sprint(uid)
}

func insertOrgAt(t *testing.T, d *sql.DB, createdAt time.Time) string {
	t.Helper()
	n := seedSeq.Add(1)
	var oid int64
	err := d.QueryRowContext(context.Background(), `
		INSERT INTO organizations (name, slug, created_at, updated_at) VALUES ('Stats Org', $1, $2, now()) RETURNING id
	`, fmt.Sprintf("stats-org-%d", n), createdAt).Scan(&oid)
	if err != nil {
		t.Fatalf("seeding organization: %v", err)
	}
	return fmt.Sprint(oid)
}

func insertPoll(t *testing.T, d *sql.DB, orgID, pollType, status string, deletedAt sql.NullTime, createdAt time.Time) string {
	t.Helper()
	id := db.NewID()
	_, err := d.ExecContext(context.Background(), `
		INSERT INTO polls (id, organization_id, type, title, timezone, status, created_at, updated_at, deleted_at)
		VALUES ($1, $2, $3, 'Stats Poll', 'Europe/Oslo', $4, $5, $5, $6)
	`, id, orgID, pollType, status, createdAt, deletedAt)
	if err != nil {
		t.Fatalf("seeding poll: %v", err)
	}
	return id
}

func insertParticipant(t *testing.T, d *sql.DB, pollID string, createdAt time.Time) {
	t.Helper()
	_, err := d.ExecContext(context.Background(), `
		INSERT INTO participants (id, poll_id, name, created_at, updated_at)
		VALUES ($1, $2, 'Participant', $3, $3)
	`, db.NewID(), pollID, createdAt)
	if err != nil {
		t.Fatalf("seeding participant: %v", err)
	}
}

func insertBookingPage(t *testing.T, d *sql.DB, orgID string, deletedAt sql.NullTime, createdAt time.Time) string {
	t.Helper()
	n := seedSeq.Add(1)
	id := db.NewID()
	_, err := d.ExecContext(context.Background(), `
		INSERT INTO booking_pages (
			id, organization_id, slug, title, timezone, slot_duration_min, buffer_before_min,
			buffer_after_min, min_notice_min, max_days_ahead, availability, created_at, updated_at, deleted_at
		) VALUES ($1, $2, $3, 'Stats Page', 'Europe/Oslo', 30, 0, 0, 0, 30, '{}'::jsonb, $4, $4, $5)
	`, id, orgID, fmt.Sprintf("stats-page-%d", n), createdAt, deletedAt)
	if err != nil {
		t.Fatalf("seeding booking page: %v", err)
	}
	return id
}

func insertBooking(t *testing.T, d *sql.DB, pageID string, createdAt time.Time) {
	t.Helper()
	_, err := d.ExecContext(context.Background(), `
		INSERT INTO bookings (id, page_id, start_at, end_at, visitor_name, visitor_email, visitor_timezone, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'Visitor', 'visitor@example.com', 'Europe/Oslo', $3, $3)
	`, db.NewID(), pageID, createdAt, createdAt.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("seeding booking: %v", err)
	}
}

func insertJob(t *testing.T, d *sql.DB, kind string, attempts, maxAttempts int) {
	t.Helper()
	_, err := d.ExecContext(context.Background(), `
		INSERT INTO scheduled_jobs (id, kind, run_at, attempts, max_attempts)
		VALUES ($1, $2, now(), $3, $4)
	`, db.NewID(), kind, attempts, maxAttempts)
	if err != nil {
		t.Fatalf("seeding scheduled job: %v", err)
	}
}

func TestStats_EmptyDatabaseIsAllZeros(t *testing.T) {
	d := testdb.New(t)
	got, err := admin.Stats(context.Background(), d)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := &admin.DashboardStats{}
	if *got != *want {
		t.Errorf("Stats on an empty database = %+v, want all zero", *got)
	}
}

func TestStats_SeededExactNumbers(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	// Users: 3 total — one now, one 10 days ago (last30 only), one 40 days ago (total only).
	insertUserAt(t, d, daysAgo(0))
	insertUserAt(t, d, daysAgo(10))
	insertUserAt(t, d, daysAgo(40))

	// Orgs: 2 total — one now, one 40 days ago.
	orgNow := insertOrgAt(t, d, daysAgo(0))
	insertOrgAt(t, d, daysAgo(40))

	// Polls (non-signup, live): 2 — one now, one 20 days ago (last30 only). One of these is
	// finalized. A third non-signup poll is soft-deleted and must be excluded entirely.
	pollNow := insertPoll(t, d, orgNow, "datetime", "finalized", sql.NullTime{}, daysAgo(0))
	insertPoll(t, d, orgNow, "datetime", "open", sql.NullTime{}, daysAgo(20))
	insertPoll(t, d, orgNow, "datetime", "open", sql.NullTime{Time: daysAgo(0), Valid: true}, daysAgo(0))

	// Signup sheets: 2 — one now, one 40 days ago (total only).
	insertPoll(t, d, orgNow, "signup", "open", sql.NullTime{}, daysAgo(0))
	insertPoll(t, d, orgNow, "signup", "open", sql.NullTime{}, daysAgo(40))

	// Participants: 4 — two now, one 10 days ago, one 40 days ago.
	insertParticipant(t, d, pollNow, daysAgo(0))
	insertParticipant(t, d, pollNow, daysAgo(0))
	insertParticipant(t, d, pollNow, daysAgo(10))
	insertParticipant(t, d, pollNow, daysAgo(40))

	// Booking pages: 2 live (one now, one 40 days ago) + 1 soft-deleted, excluded.
	pageNow := insertBookingPage(t, d, orgNow, sql.NullTime{}, daysAgo(0))
	insertBookingPage(t, d, orgNow, sql.NullTime{}, daysAgo(40))
	insertBookingPage(t, d, orgNow, sql.NullTime{Time: daysAgo(0), Valid: true}, daysAgo(0))

	// Bookings: 3 — one now, one 10 days ago, one 40 days ago.
	insertBooking(t, d, pageNow, daysAgo(0))
	insertBooking(t, d, pageNow, daysAgo(10))
	insertBooking(t, d, pageNow, daysAgo(40))

	// Mail queue depth: only "mail:send" jobs that haven't exhausted their attempts count.
	insertJob(t, d, "mail:send", 0, 5) // pending
	insertJob(t, d, "mail:send", 2, 5) // pending
	insertJob(t, d, "mail:send", 5, 5) // dead — excluded from queue depth, counted in FailedJobs
	insertJob(t, d, "mail:poll", 0, 5) // a different kind entirely — never counted as mail queue depth

	// Failed jobs: every kind's dead-letter rows, mirroring jobs.Dead's own predicate.
	insertJob(t, d, "poll.digest", 5, 5)

	got, err := admin.Stats(ctx, d)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}

	want := &admin.DashboardStats{
		Users:          admin.Count{Total: 3, Last7: 1, Last30: 2},
		Orgs:           admin.Count{Total: 2, Last7: 1, Last30: 1},
		Polls:          admin.Count{Total: 2, Last7: 1, Last30: 2},
		PollsFinalized: 1,
		SignupSheets:   admin.Count{Total: 2, Last7: 1, Last30: 1},
		Participants:   admin.Count{Total: 4, Last7: 2, Last30: 3},
		BookingPages:   admin.Count{Total: 2, Last7: 1, Last30: 1},
		Bookings:       admin.Count{Total: 3, Last7: 1, Last30: 2},
		MailQueueDepth: 2,
		FailedJobs:     2,
	}
	if *got != *want {
		t.Errorf("Stats = %+v, want %+v", *got, *want)
	}
}

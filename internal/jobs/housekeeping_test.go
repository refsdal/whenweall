package jobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

// awaitHubListening blocks until hub's dedicated LISTEN connection is confirmed active, probing
// with real Emits on a private room rather than a fixed sleep (the same technique
// internal/rooms's own hub_test.go uses for the identical reason — a NOTIFY sent before LISTEN is
// established is gone forever, so a test that ran the sweep before this returned could flake).
func awaitHubListening(t *testing.T, hub *rooms.Hub, sqlDB *sql.DB) {
	t.Helper()
	ctx := context.Background()
	probeRoom := "probe:" + db.NewID()
	frames, unsubscribe := hub.Subscribe(probeRoom)
	defer unsubscribe()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := rooms.Emit(ctx, tx, probeRoom, "probe", nil); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		select {
		case <-frames:
			return
		case <-time.After(150 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for hub to start listening")
}

func TestRoomsPruneDeletesOldEventsAndReschedules(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d, rooms.BroadcastPresenceTotal)

	if _, err := d.ExecContext(ctx,
		`INSERT INTO room_events (room_key, event, created_at) VALUES ('room-1', '{}'::jsonb, now() - interval '2 hours')`,
	); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO room_events (room_key, event, created_at) VALUES ('room-1', '{}'::jsonb, now())`,
	); err != nil {
		t.Fatalf("insert fresh row: %v", err)
	}

	room := "global"
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "rooms:prune", RoomKey: &room, RunAt: time.Now().Add(-time.Second),
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

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM room_events").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (only the fresh row survives)", count)
	}

	var runAt time.Time
	if err := d.QueryRowContext(ctx,
		"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "rooms:prune", "global",
	).Scan(&runAt); err != nil {
		t.Fatalf("select rescheduled job: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("run_at = %v, want in the future (rescheduled)", runAt)
	}
}

func TestPresenceSweepDeletesStaleRowsAndReschedules(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d, rooms.BroadcastPresenceTotal)

	if _, err := d.ExecContext(ctx,
		`INSERT INTO ws_presence (room_key, replica_id, count, heartbeat_at) VALUES ('room-1', 'stale-replica', 1, now() - interval '5 minutes')`,
	); err != nil {
		t.Fatalf("insert stale row: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO ws_presence (room_key, replica_id, count, heartbeat_at) VALUES ('room-1', 'fresh-replica', 1, now())`,
	); err != nil {
		t.Fatalf("insert fresh row: %v", err)
	}

	room := "global"
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "presence:sweep", RoomKey: &room, RunAt: time.Now().Add(-time.Second),
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

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM ws_presence").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (only the fresh row survives)", count)
	}

	var runAt time.Time
	if err := d.QueryRowContext(ctx,
		"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "presence:sweep", "global",
	).Scan(&runAt); err != nil {
		t.Fatalf("select rescheduled job: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("run_at = %v, want in the future (rescheduled)", runAt)
	}
}

// TestPresenceSweepBroadcastsCorrectedTotal is I1's regression test: deleting a stale replica's
// ws_presence row leaves every live subscriber's own last-known count wrong (too high) until
// something re-broadcasts the corrected total — the sweep itself must do that, not just delete the
// row and move on.
func TestPresenceSweepBroadcastsCorrectedTotal(t *testing.T) {
	url, d := testdb.URL(t)
	ctx := context.Background()

	hub := rooms.NewHub(url, d, slog.Default())
	hubCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = hub.Run(hubCtx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	awaitHubListening(t, hub, d)

	const roomKey = "poll:presence-sweep-test"
	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	// One stale replica row (past the 90s heartbeat threshold) and one fresh one: the corrected
	// total the sweep broadcasts must reflect only the survivor's count.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO ws_presence (room_key, replica_id, count, heartbeat_at) VALUES ($1, 'stale-replica', 3, now() - interval '5 minutes')`,
		roomKey,
	); err != nil {
		t.Fatalf("insert stale row: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO ws_presence (room_key, replica_id, count, heartbeat_at) VALUES ($1, 'fresh-replica', 2, now())`,
		roomKey,
	); err != nil {
		t.Fatalf("insert fresh row: %v", err)
	}

	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d, rooms.BroadcastPresenceTotal)
	room := "global"
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "presence:sweep", RoomKey: &room, RunAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("subscriber channel closed unexpectedly")
		}
		var got map[string]any
		if err := json.Unmarshal(frame, &got); err != nil {
			t.Fatalf("unmarshal frame %s: %v", frame, err)
		}
		if got["type"] != "presence" {
			t.Fatalf("frame type = %v, want presence", got["type"])
		}
		if count, ok := got["count"].(float64); !ok || int(count) != 2 {
			t.Errorf("presence count = %v, want 2 (only the fresh replica's row survives the sweep)", got["count"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a corrected presence broadcast after the sweep")
	}
}

func TestRatelimitSweepDeletesExpiredRowsAndReschedules(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d, rooms.BroadcastPresenceTotal)

	if _, err := d.ExecContext(ctx,
		`INSERT INTO rate_limits (key, count, reset_at) VALUES ('old-key', 1, now() - interval '2 hours')`,
	); err != nil {
		t.Fatalf("insert expired row: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO rate_limits (key, count, reset_at) VALUES ('fresh-key', 1, now() + interval '1 hour')`,
	); err != nil {
		t.Fatalf("insert fresh row: %v", err)
	}

	room := "global"
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "ratelimit:sweep", RoomKey: &room, RunAt: time.Now().Add(-time.Second),
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

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM rate_limits").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (only the fresh row survives)", count)
	}

	var runAt time.Time
	if err := d.QueryRowContext(ctx,
		"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "ratelimit:sweep", "global",
	).Scan(&runAt); err != nil {
		t.Fatalf("select rescheduled job: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("run_at = %v, want in the future (rescheduled)", runAt)
	}
}

// seedDeadRow inserts a dead-lettered scheduled_jobs row (attempts == max_attempts, so jobs.Dead
// lists it and ClaimDue never will) directly, with run_at pushed `age` (a Postgres interval
// literal, e.g. "2 days") into the past — the sweep measures a dead row's age on run_at (see
// sweepDeadLetters). A nil payload inserts SQL NULL.
func seedDeadRow(t *testing.T, d *sql.DB, kind string, payload *string, age string) string {
	t.Helper()
	id := db.NewID()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO scheduled_jobs (id, kind, run_at, payload, attempts, max_attempts, last_error)
		VALUES ($1, $2, now() - $3::interval, $4::jsonb, 3, 3, 'smtp: connection refused')
	`, id, kind, age, payload); err != nil {
		t.Fatalf("seeding dead %s row: %v", kind, err)
	}
	return id
}

func strPtr(s string) *string { return &s }

// deadRowState is what the sweep must and must not touch on a row; found == false once deleted.
type deadRowState struct {
	found      bool
	hasPayload bool
	attempts   int
	lastError  sql.NullString
}

func readDeadRow(t *testing.T, d *sql.DB, id string) deadRowState {
	t.Helper()
	var s deadRowState
	err := d.QueryRowContext(context.Background(),
		`SELECT payload IS NOT NULL, attempts, last_error FROM scheduled_jobs WHERE id = $1`, id,
	).Scan(&s.hasPayload, &s.attempts, &s.lastError)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return deadRowState{}
	case err != nil:
		t.Fatalf("reading job %s: %v", id, err)
	}
	s.found = true
	return s
}

// runDeadletterSweep schedules deadletter:sweep due now and runs one worker pass, the same way the
// other housekeeping tests in this file drive their kinds. Exactly one job must be processed —
// the sweep itself; the seeded dead rows are never claimable.
func runDeadletterSweep(t *testing.T, d *sql.DB) {
	t.Helper()
	ctx := context.Background()
	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d, rooms.BroadcastPresenceTotal)
	room := "global"
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "deadletter:sweep", RoomKey: &room, RunAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 (only the sweep itself is claimable)", processed)
	}
}

// TestDeadletterSweepPurgesOldMailPayloadsOnly: a dead mail:send row older than 24h loses its
// payload (the recipient address and any verify/reset token) but keeps kind/attempts/last_error,
// so the console still shows WHAT failed and WHY; a dead mail:send row younger than 24h keeps its
// payload (a staff member may still Retry it); a dead non-mail:send row keeps its payload
// regardless (ids only, nothing sensitive, and Retry must keep working for it — see
// TestDeadletterSweepNeverPurgesMailPollOrMailBookingPayloads below for the two other mail kinds
// specifically).
func TestDeadletterSweepPurgesOldMailPayloadsOnly(t *testing.T) {
	d := testdb.New(t)
	mailPayload := strPtr(`{"to":"secret-recipient@example.com","data":{"URL":"http://app.example/verify-email?token=super-secret"}}`)
	oldMail := seedDeadRow(t, d, "mail:send", mailPayload, "2 days")
	freshMail := seedDeadRow(t, d, "mail:send", mailPayload, "1 hour")
	oldOther := seedDeadRow(t, d, "poll.digest", strPtr(`{"pollId":"p1"}`), "2 days")

	runDeadletterSweep(t, d)

	if got := readDeadRow(t, d, oldMail); !got.found || got.hasPayload || got.attempts != 3 ||
		!got.lastError.Valid || got.lastError.String != "smtp: connection refused" {
		t.Errorf("old mail row after sweep = %+v, want present with payload NULL and attempts/last_error intact", got)
	}
	if got := readDeadRow(t, d, freshMail); !got.found || !got.hasPayload {
		t.Errorf("fresh mail row after sweep = %+v, want untouched (younger than 24h — staff may still retry it)", got)
	}
	if got := readDeadRow(t, d, oldOther); !got.found || !got.hasPayload {
		t.Errorf("old non-mail:send row after sweep = %+v, want payload kept (only mail:send payloads carry addresses/tokens)", got)
	}

	var runAt time.Time
	if err := d.QueryRowContext(context.Background(),
		"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "deadletter:sweep", "global",
	).Scan(&runAt); err != nil {
		t.Fatalf("select rescheduled job: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("run_at = %v, want in the future (rescheduled)", runAt)
	}
}

// TestDeadletterSweepNeverPurgesMailPollOrMailBookingPayloads: the payload purge is narrower than
// "mail:*" — only "mail:send" carries anything sensitive (a recipient address, and for
// verify/reset mail the raw token). "mail:poll" (internal/polls) and "mail:booking"
// (internal/bookings) carry ids only, so an old dead row of either kind must keep its payload
// (and so stay retryable — jobs.PayloadExpired must report false for both) even long past the
// 24h payload retention window, while an equally old "mail:send" row is still purged and becomes
// unretryable. This is the regression test for IMPORTANT 1: a broader "mail:%" purge would
// permanently strand a dead-lettered booking confirmation or poll digest after an SMTP outage,
// mail a visitor cannot simply re-request.
func TestDeadletterSweepNeverPurgesMailPollOrMailBookingPayloads(t *testing.T) {
	d := testdb.New(t)
	oldPoll := seedDeadRow(t, d, "mail:poll", strPtr(`{"pollId":"p1","event":"closed","userId":"u1"}`), "2 days")
	oldBooking := seedDeadRow(t, d, "mail:booking", strPtr(`{"kind":"confirmation","bookingId":"b1","recipient":"visitor"}`), "2 days")
	oldSend := seedDeadRow(t, d, "mail:send", strPtr(`{"to":"secret@example.com"}`), "2 days")

	runDeadletterSweep(t, d)

	pollRow := readDeadRow(t, d, oldPoll)
	if !pollRow.found || !pollRow.hasPayload {
		t.Errorf("old mail:poll row after sweep = %+v, want payload kept (ids only, never purged)", pollRow)
	}
	if jobs.PayloadExpired("mail:poll", pollRow.hasPayload) {
		t.Errorf("PayloadExpired(mail:poll, hasPayload=%v) = true, want false — mail:poll is never purged", pollRow.hasPayload)
	}

	bookingRow := readDeadRow(t, d, oldBooking)
	if !bookingRow.found || !bookingRow.hasPayload {
		t.Errorf("old mail:booking row after sweep = %+v, want payload kept (ids only, never purged)", bookingRow)
	}
	if jobs.PayloadExpired("mail:booking", bookingRow.hasPayload) {
		t.Errorf("PayloadExpired(mail:booking, hasPayload=%v) = true, want false — mail:booking is never purged", bookingRow.hasPayload)
	}

	sendRow := readDeadRow(t, d, oldSend)
	if !sendRow.found || sendRow.hasPayload {
		t.Errorf("old mail:send row after sweep = %+v, want payload purged", sendRow)
	}
	if !jobs.PayloadExpired("mail:send", sendRow.hasPayload) {
		t.Errorf("PayloadExpired(mail:send, hasPayload=%v) = false, want true — mail:send IS purged", sendRow.hasPayload)
	}
}

// TestDeadletterSweepDeletesDeadRowsOlderThan30Days: the dead-letter screen is a to-do list, not
// an archive — a dead row of ANY kind older than 30 days is deleted outright; a 29-day-old one
// survives (payload purged, as above); a LIVE row is never touched however old its run_at is.
func TestDeadletterSweepDeletesDeadRowsOlderThan30Days(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	ancientMail := seedDeadRow(t, d, "mail:send", nil, "31 days")
	ancientOther := seedDeadRow(t, d, "booking.reminder", strPtr(`{"bookingId":"b1"}`), "31 days")
	recentMail := seedDeadRow(t, d, "mail:send", strPtr(`{"to":"x@example.com"}`), "29 days")

	// A live row (attempts < max_attempts) with an ancient run_at is merely overdue, not dead. It
	// is locked by another replica a moment ago so THIS worker's RunOnce skips it (ClaimDue's
	// lock check) and the sweep is the only job processed.
	liveID := db.NewID()
	if _, err := d.ExecContext(ctx, `
		INSERT INTO scheduled_jobs (id, kind, run_at, payload, attempts, max_attempts, locked_by, locked_at)
		VALUES ($1, 'mail:send', now() - interval '40 days', '{"to":"live@example.com"}'::jsonb, 1, 5, 'other-replica', now())
	`, liveID); err != nil {
		t.Fatalf("seeding live row: %v", err)
	}

	runDeadletterSweep(t, d)

	if got := readDeadRow(t, d, ancientMail); got.found {
		t.Errorf("31-day-old dead mail row still present (%+v), want deleted", got)
	}
	if got := readDeadRow(t, d, ancientOther); got.found {
		t.Errorf("31-day-old dead booking.reminder row still present (%+v), want deleted — the 30-day delete is for every kind", got)
	}
	if got := readDeadRow(t, d, recentMail); !got.found || got.hasPayload {
		t.Errorf("29-day-old dead mail row = %+v, want present with payload purged", got)
	}
	if got := readDeadRow(t, d, liveID); !got.found || !got.hasPayload {
		t.Errorf("live row = %+v, want untouched — the sweep only ever touches attempts >= max_attempts", got)
	}
}

func TestEnsureScheduledSeedsAllFourSingletons(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.EnsureScheduled(ctx, d); err != nil {
		t.Fatalf("EnsureScheduled: %v", err)
	}

	kinds := []string{"rooms:prune", "presence:sweep", "ratelimit:sweep", "deadletter:sweep"}
	firstRunAt := make(map[string]time.Time, len(kinds))
	for _, kind := range kinds {
		var count, maxAttempts int
		var runAt time.Time
		if err := d.QueryRowContext(ctx,
			"SELECT count(*), max(run_at), max(max_attempts) FROM scheduled_jobs WHERE kind = $1 AND room_key = 'global' GROUP BY kind",
			kind,
		).Scan(&count, &runAt, &maxAttempts); err != nil {
			t.Fatalf("count %s: %v", kind, err)
		}
		if count != 1 {
			t.Errorf("kind %s: count = %d, want 1", kind, count)
		}
		if maxAttempts != 1_000_000 {
			t.Errorf("kind %s: max_attempts = %d, want 1000000 (a housekeeping chain must never die from a transient blip)", kind, maxAttempts)
		}
		firstRunAt[kind] = runAt
	}

	// Calling it again must not create duplicates (ScheduleIfAbsent's DO NOTHING makes boot
	// idempotent across replicas)...
	if err := jobs.EnsureScheduled(ctx, d); err != nil {
		t.Fatalf("EnsureScheduled (2nd): %v", err)
	}
	var total int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs").Scan(&total); err != nil {
		t.Fatalf("total count: %v", err)
	}
	if total != len(kinds) {
		t.Errorf("total = %d, want %d (no duplicates)", total, len(kinds))
	}

	// ...and, IMPORTANT 4 (reboot starvation), it must not move run_at either. A restart-happy
	// deploy calling EnsureScheduled again on every boot must never pull an already-scheduled job
	// back to "shortly after boot" — that would starve the chain of ever reaching a real run_at.
	for kind, want := range firstRunAt {
		var got time.Time
		if err := d.QueryRowContext(ctx,
			"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = 'global'", kind,
		).Scan(&got); err != nil {
			t.Fatalf("select run_at %s: %v", kind, err)
		}
		if !got.Equal(want) {
			t.Errorf("kind %s: run_at moved from %v to %v after a second EnsureScheduled call", kind, want, got)
		}
	}
}

// TestEnsureScheduledLeavesPreExistingFutureJobUntouched is the regression test for IMPORTANT 4:
// a job already scheduled far in the future (e.g. ratelimit:sweep, an hour out) must survive a
// reboot-time EnsureScheduled call completely untouched — not just "still exactly one row", but
// the exact same row, at the exact same run_at, with its existing attempts/last_error intact.
// Schedule's upsert would clobber all of that back to "shortly after boot" and reset attempts;
// ScheduleIfAbsent's DO NOTHING must leave it alone.
func TestEnsureScheduledLeavesPreExistingFutureJobUntouched(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	room := "global"

	farFuture := time.Now().Add(6 * time.Hour).Truncate(time.Millisecond)
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "ratelimit:sweep", RoomKey: &room, RunAt: farFuture, MaxAttempts: 3,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	var idBefore string
	if err := d.QueryRowContext(ctx,
		"SELECT id FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "ratelimit:sweep", room,
	).Scan(&idBefore); err != nil {
		t.Fatalf("select id before: %v", err)
	}

	if err := jobs.EnsureScheduled(ctx, d); err != nil {
		t.Fatalf("EnsureScheduled: %v", err)
	}

	var idAfter string
	var runAtAfter time.Time
	var maxAttemptsAfter int
	if err := d.QueryRowContext(ctx,
		"SELECT id, run_at, max_attempts FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "ratelimit:sweep", room,
	).Scan(&idAfter, &runAtAfter, &maxAttemptsAfter); err != nil {
		t.Fatalf("select after: %v", err)
	}

	if idAfter != idBefore {
		t.Errorf("id changed from %s to %s; EnsureScheduled must not touch a pre-existing job", idBefore, idAfter)
	}
	if !runAtAfter.Equal(farFuture) {
		t.Errorf("run_at = %v, want unchanged %v (reboot starvation: a reseed must not pull it back to shortly after boot)", runAtAfter, farFuture)
	}
	if maxAttemptsAfter != 3 {
		t.Errorf("max_attempts = %d, want unchanged 3", maxAttemptsAfter)
	}
}

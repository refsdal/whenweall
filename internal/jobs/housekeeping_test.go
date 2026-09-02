package jobs_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

func TestEnsureScheduledSeedsAllThreeSingletons(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.EnsureScheduled(ctx, d); err != nil {
		t.Fatalf("EnsureScheduled: %v", err)
	}

	firstRunAt := make(map[string]time.Time, 3)
	for _, kind := range []string{"rooms:prune", "presence:sweep", "ratelimit:sweep"} {
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
	if total != 3 {
		t.Errorf("total = %d, want 3 (no duplicates)", total)
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

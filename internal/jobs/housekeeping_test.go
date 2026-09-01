package jobs_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestRoomsPruneDeletesOldEventsAndReschedules(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d)

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
	jobs.RegisterHousekeeping(w, d)

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

func TestRatelimitSweepDeletesExpiredRowsAndReschedules(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	w := jobs.NewWorker(d, "w1", slog.Default())
	jobs.RegisterHousekeeping(w, d)

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

	for _, kind := range []string{"rooms:prune", "presence:sweep", "ratelimit:sweep"} {
		var count int
		if err := d.QueryRowContext(ctx,
			"SELECT count(*) FROM scheduled_jobs WHERE kind = $1 AND room_key = 'global'", kind,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", kind, err)
		}
		if count != 1 {
			t.Errorf("kind %s: count = %d, want 1", kind, count)
		}
	}

	// Calling it again must not create duplicates (upsert makes boot idempotent across replicas).
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
}

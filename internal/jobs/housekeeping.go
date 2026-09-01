package jobs

import (
	"context"
	"database/sql"
	"time"
)

// housekeepingRoomKey is the RoomKey every housekeeping job schedules under. None of these three
// jobs is actually room-scoped, but Schedule's upsert-by-(kind, room_key) semantics is exactly
// the singleton behaviour they want: a handler re-arming its own next run, and EnsureScheduled
// re-seeding at boot, must each leave exactly one row per kind rather than piling up duplicates —
// a nil RoomKey would append instead of upsert.
const housekeepingRoomKey = "global"

// Housekeeping job kinds and the interval each reschedules itself for after a run.
const (
	roomsPruneKind         = "rooms:prune"
	roomsPruneInterval     = 10 * time.Minute
	presenceSweepKind      = "presence:sweep"
	presenceSweepInterval  = time.Minute
	ratelimitSweepKind     = "ratelimit:sweep"
	ratelimitSweepInterval = time.Hour
)

// RegisterHousekeeping wires the three self-rescheduling housekeeping jobs into w: pruning old
// room_events rows, sweeping stale ws_presence rows, and sweeping expired rate_limits rows. Each
// handler deletes its rows and then reschedules its own next run, so once EnsureScheduled has
// seeded the first run, the chain keeps itself alive without a cron process or external
// scheduler.
func RegisterHousekeeping(w *Worker, sqlDB *sql.DB) {
	w.Register(roomsPruneKind, func(ctx context.Context, _ Job) error {
		if _, err := sqlDB.ExecContext(ctx, `DELETE FROM room_events WHERE created_at < now() - interval '1 hour'`); err != nil {
			return err
		}
		return rescheduleHousekeeping(ctx, sqlDB, roomsPruneKind, roomsPruneInterval)
	})

	w.Register(presenceSweepKind, func(ctx context.Context, _ Job) error {
		if _, err := sqlDB.ExecContext(ctx, `DELETE FROM ws_presence WHERE heartbeat_at < now() - interval '90 seconds'`); err != nil {
			return err
		}
		return rescheduleHousekeeping(ctx, sqlDB, presenceSweepKind, presenceSweepInterval)
	})

	w.Register(ratelimitSweepKind, func(ctx context.Context, _ Job) error {
		if _, err := sqlDB.ExecContext(ctx, `DELETE FROM rate_limits WHERE reset_at < now() - interval '1 hour'`); err != nil {
			return err
		}
		return rescheduleHousekeeping(ctx, sqlDB, ratelimitSweepKind, ratelimitSweepInterval)
	})
}

// EnsureScheduled seeds all three housekeeping jobs to run shortly after boot. It is meant to be
// called once from serve(): every replica calling it on startup is safe because Schedule's
// RoomKey upsert collapses concurrent seeding attempts (and a still-pending job from a previous
// boot) down to the one row per kind that scheduled_jobs_room_kind_idx allows.
//
// A boot-time reseed can also race a handler that's mid-run and about to reschedule itself; this
// is harmless the same way any two concurrent Schedule calls on the same kind+room_key are — the
// unique index and the upsert's id swap (see Schedule's doc comment) guarantee exactly one
// surviving row, just possibly re-armed to whichever of the two run_at values lands last.
func EnsureScheduled(ctx context.Context, sqlDB *sql.DB) error {
	for _, s := range []struct {
		kind     string
		interval time.Duration
	}{
		{roomsPruneKind, roomsPruneInterval},
		{presenceSweepKind, presenceSweepInterval},
		{ratelimitSweepKind, ratelimitSweepInterval},
	} {
		if err := seedHousekeeping(ctx, sqlDB, s.kind, s.interval); err != nil {
			return err
		}
	}
	return nil
}

// seedHousekeeping upserts kind's singleton job under housekeepingRoomKey to run after interval
// from now. It is safe to call with no job of that kind currently claimed (EnsureScheduled at
// boot): the upsert either inserts the first row or, if one is already pending from an earlier
// boot, folds into it.
func seedHousekeeping(ctx context.Context, sqlDB *sql.DB, kind string, interval time.Duration) error {
	room := housekeepingRoomKey
	return Schedule(ctx, sqlDB, ScheduleInput{
		Kind:    kind,
		RoomKey: &room,
		RunAt:   time.Now().Add(interval),
	})
}

// rescheduleHousekeeping is seedHousekeeping's counterpart for a handler rearming its own next
// run from inside RunOnce: a plain upsert under the same kind+room_key this handler was claimed
// on. This is safe to do mid-run because Schedule's conflict path takes EXCLUDED.id (see its doc
// comment) — the row that survives carries a fresh id, so Worker.process's post-handler
// Complete(job.ID) (keyed to the id this run claimed) matches nothing once Schedule has run.
func rescheduleHousekeeping(ctx context.Context, sqlDB *sql.DB, kind string, interval time.Duration) error {
	return seedHousekeeping(ctx, sqlDB, kind, interval)
}

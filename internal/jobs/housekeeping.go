package jobs

import (
	"context"
	"database/sql"
	"time"

	"github.com/refsdal/whenweall/internal/db"
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

// housekeepingMaxAttempts is deliberately enormous (IMPORTANT 5), unlike mailer's bounded
// mailMaxAttempts (10): these three jobs are a self-perpetuating chain, not a one-shot send — a
// dead-lettered rooms:prune/presence:sweep/ratelimit:sweep doesn't just fail once, it silently
// stops that entire class of cleanup forever, since nothing else re-arms it once the chain dies.
// A transient blip (a momentary DB connection hiccup) must never be able to permanently kill a
// chain that would otherwise self-heal on the very next scheduled run; each failed attempt still
// logs at WARN (Worker.process) on its way to the next retry, which is enough of a signal without
// needing the attempt budget itself to be the safety net.
const housekeepingMaxAttempts = 1_000_000

// broadcastPresenceTotal is rooms.BroadcastPresenceTotal's exact signature, redeclared here (not
// imported) so this package never depends on internal/rooms: that package already imports
// internal/httpserver -> internal/auth -> internal/mailer -> internal/jobs, so the reverse edge
// would be a compile-time import cycle. RegisterHousekeeping's caller (cmd/whenweall/main.go, which
// already imports both packages with no such cycle) passes rooms.BroadcastPresenceTotal itself as
// this parameter — the two signatures match exactly, so no adapter is needed.
type broadcastPresenceTotal func(ctx context.Context, sqlDB db.DBTX, roomKey string) error

// RegisterHousekeeping wires the three self-rescheduling housekeeping jobs into w: pruning old
// room_events rows, sweeping stale ws_presence rows, and sweeping expired rate_limits rows. Each
// handler deletes its rows and then reschedules its own next run, so once EnsureScheduled has
// seeded the first run, the chain keeps itself alive without a cron process or external
// scheduler.
//
// broadcastPresence is called once per distinct room the presence sweep just deleted a stale row
// from, after the DELETE commits — see this job's own doc comment below and
// rooms.BroadcastPresenceTotal's for why a sweep that only deletes and never re-broadcasts leaves
// every live subscriber's count wrong.
func RegisterHousekeeping(w *Worker, sqlDB *sql.DB, broadcastPresence broadcastPresenceTotal) {
	w.Register(roomsPruneKind, func(ctx context.Context, _ Job) error {
		if _, err := sqlDB.ExecContext(ctx, `DELETE FROM room_events WHERE created_at < now() - interval '1 hour'`); err != nil {
			return err
		}
		return rescheduleHousekeeping(ctx, sqlDB, roomsPruneKind, roomsPruneInterval)
	})

	w.Register(presenceSweepKind, func(ctx context.Context, _ Job) error {
		deletedRoomKeys, err := deleteStalePresenceRows(ctx, sqlDB)
		if err != nil {
			return err
		}
		// Every room a stale row was just deleted from now has a wrong (too-high) total in every
		// live subscriber's own last-known count — broadcast the corrected total so they catch up
		// (see rooms.BroadcastPresenceTotal's own doc comment). Best-effort: a broadcast failure
		// here is a live-UX nit, never a reason to fail the sweep itself or block its own
		// reschedule below (housekeepingMaxAttempts already treats this whole chain as one that
		// must never die from a transient blip).
		for roomKey := range deletedRoomKeys {
			if err := broadcastPresence(ctx, sqlDB, roomKey); err != nil {
				w.log.Error("presence sweep: broadcast corrected total", "room_key", roomKey, "error", err)
			}
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

// deleteStalePresenceRows deletes every ws_presence row whose heartbeat has lapsed past 90s and
// returns the distinct set of room_key values it touched — the rooms whose live total is now
// wrong (too high, by exactly what was just deleted) until a corrected broadcast goes out.
func deleteStalePresenceRows(ctx context.Context, sqlDB *sql.DB) (map[string]struct{}, error) {
	rows, err := sqlDB.QueryContext(ctx,
		`DELETE FROM ws_presence WHERE heartbeat_at < now() - interval '90 seconds' RETURNING room_key`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	roomKeys := make(map[string]struct{})
	for rows.Next() {
		var roomKey string
		if err := rows.Scan(&roomKey); err != nil {
			return nil, err
		}
		roomKeys[roomKey] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return roomKeys, nil
}

// EnsureScheduled seeds all three housekeeping jobs to run shortly after boot, but only the
// first time — a pre-existing job of that kind (from an earlier boot, still correctly scheduled
// for the future, or mid-chain) is left completely alone. It is meant to be called once from
// serve() on every replica startup; that's safe with any number of concurrent callers because
// seedHousekeeping's INSERT ... ON CONFLICT DO NOTHING (via ScheduleIfAbsent) means only the
// first caller to reach an absent row actually inserts it — every other concurrent or later
// caller's INSERT is a no-op against the same (kind, room_key) unique index
// (scheduled_jobs_room_kind_idx).
//
// This must not be Schedule's upsert (IMPORTANT 4): a chain's own reschedule (rescheduleHousekeeping,
// still Schedule) can leave run_at anywhere up to an hour out, and a naive reseed-on-every-boot
// would clobber that back to "shortly after boot" on every restart — a restart-happy deploy, or a
// crash loop, could then starve the chain of ever reaching a real run_at.
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

// seedHousekeeping inserts kind's singleton job under housekeepingRoomKey to run after interval
// from now, only if no such job exists yet (ScheduleIfAbsent — see EnsureScheduled's doc comment
// for why this must not be Schedule's upsert). A job already pending from an earlier boot, or
// mid-chain and due to reschedule itself shortly, is left untouched.
func seedHousekeeping(ctx context.Context, sqlDB *sql.DB, kind string, interval time.Duration) error {
	room := housekeepingRoomKey
	return ScheduleIfAbsent(ctx, sqlDB, ScheduleInput{
		Kind:        kind,
		RoomKey:     &room,
		RunAt:       time.Now().Add(interval),
		MaxAttempts: housekeepingMaxAttempts,
	})
}

// rescheduleHousekeeping is seedHousekeeping's counterpart for a handler rearming its own next
// run from inside RunOnce: a plain upsert (Schedule, not ScheduleIfAbsent — this must actually
// move run_at forward every time, unlike the boot-time seed) under the same kind+room_key this
// handler was claimed on. This is safe to do mid-run because Schedule's conflict path takes
// EXCLUDED.id (see its doc comment) — the row that survives carries a fresh id, so
// Worker.process's post-handler Complete(job.ID) (keyed to the id this run claimed) matches
// nothing once Schedule has run.
func rescheduleHousekeeping(ctx context.Context, sqlDB *sql.DB, kind string, interval time.Duration) error {
	room := housekeepingRoomKey
	return Schedule(ctx, sqlDB, ScheduleInput{
		Kind:        kind,
		RoomKey:     &room,
		RunAt:       time.Now().Add(interval),
		MaxAttempts: housekeepingMaxAttempts,
	})
}

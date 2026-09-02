// This file is Task 2's presence half: joining/leaving a room's live viewer count and keeping it
// honest across replicas and restarts. It ports src/rooms/presence.ts's design onto the Hub — see
// that file for the original, and the doc comments below for where this deliberately diverges.
package rooms

import (
	"context"
	"fmt"
	"time"

	"github.com/refsdal/whenweall/internal/db"
)

// presenceHeartbeatInterval is how often presenceHeartbeatLoop re-stamps this replica's
// ws_presence rows — the Go analogue of presence.ts's heartbeat(), run on the same kind of timer
// rather than as a scheduled job. internal/jobs/housekeeping.go's presence:sweep job is this
// design's staleness backstop on the READ side, DELETEing rows whose heartbeat has lapsed after
// 90s; the 3x margin between that and this 30s interval mirrors the same ratio presence.ts keeps
// between its own HEARTBEAT_INTERVAL_MS and STALE_AFTER_SECONDS.
const presenceHeartbeatInterval = 30 * time.Second

// presenceJoin increments this replica's viewer row for roomKey — INSERT ... ON CONFLICT DO
// UPDATE count = count + 1 — and broadcasts the room's new total (broadcastPresence).
//
// Unlike presence.ts's publishPresence, which re-publishes this replica's own absolute local
// socket count (computed by its caller from ctx.getWebSockets(), the durable object's own
// authoritative in-memory list), this increments/decrements in SQL directly: there is no
// equivalent single authoritative in-memory list to mirror here, across however many goroutines
// this replica happens to be running for the room, so the row's count IS the source of truth
// rather than a periodic republish of one. Errors are logged, not returned to ServeWS's caller: a
// presence miscount is a UX nit, never a reason to refuse or tear down a connection.
func (h *Hub) presenceJoin(ctx context.Context, roomKey string) {
	if _, err := h.sqlDB.ExecContext(ctx, `
		INSERT INTO ws_presence (room_key, replica_id, count, heartbeat_at)
		VALUES ($1, $2, 1, now())
		ON CONFLICT (room_key, replica_id) DO UPDATE
			SET count = ws_presence.count + 1, heartbeat_at = now()
	`, roomKey, h.replicaID); err != nil {
		h.log.Error("rooms: presence join", "room_key", roomKey, "error", err)
		return
	}
	h.broadcastPresence(ctx, roomKey)
}

// presenceLeave decrements this replica's viewer row for roomKey, floored at 0 — a decrement
// racing some other cleanup path (a crash that skipped this connection's own leave, say) must
// never drive a row negative — and broadcasts the room's new total.
func (h *Hub) presenceLeave(ctx context.Context, roomKey string) {
	if _, err := h.sqlDB.ExecContext(ctx, `
		UPDATE ws_presence SET count = GREATEST(count - 1, 0), heartbeat_at = now()
		WHERE room_key = $1 AND replica_id = $2
	`, roomKey, h.replicaID); err != nil {
		h.log.Error("rooms: presence leave", "room_key", roomKey, "error", err)
		return
	}
	h.broadcastPresence(ctx, roomKey)
}

// broadcastPresence reads roomKey's current total and Emits it as this room's "presence" event —
// see BroadcastPresenceTotal, which does the actual work; this is just that function bound to the
// hub's own sqlDB, with errors logged rather than returned (a presence miscount is a UX nit, never
// a reason to fail whatever join/leave call triggered it).
func (h *Hub) broadcastPresence(ctx context.Context, roomKey string) {
	if err := BroadcastPresenceTotal(ctx, h.sqlDB, roomKey); err != nil {
		h.log.Error("rooms: presence broadcast", "room_key", roomKey, "error", err)
	}
}

// BroadcastPresenceTotal re-reads roomKey's current total across every replica's ws_presence row
// and Emits it as this room's "presence" event ({"type":"presence","count":N}) — the same
// broadcast presenceJoin/presenceLeave trigger on every join/leave, exported so a caller with no
// Hub reference can trigger the identical broadcast. internal/jobs's presence:sweep housekeeping
// job (housekeeping.go) is that caller: it DELETEs stale ws_presence rows outright rather than
// filtering them at read time (see this function's own callers' doc comments for why), which means
// every live subscriber's last-known count for an affected room is now wrong by exactly the
// deleted rows' contribution — this is what corrects it, once per swept room, right after the
// DELETE that made the correction necessary.
//
// No staleness filter here, unlike presence.ts's readPresence, which excludes lapsed rows at READ
// time — this design's staleness backstop is the DELETE itself; a row still present when this
// query runs is, by definition, either live or not yet past the sweep's own threshold.
//
// Runs OUTSIDE any transaction, deliberately: whatever write made this total correct (a join/leave
// UPDATE, or the sweep's own DELETE) has already committed by the time this executes, and a reader
// racing in between that commit and this Emit seeing a briefly stale total is an acceptable,
// self-correcting condition (the very next join/leave/sweep on this room corrects it) — not worth
// paying for a cross-statement transaction here.
func BroadcastPresenceTotal(ctx context.Context, sqlDB db.DBTX, roomKey string) error {
	var total int64
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT coalesce(sum(count), 0) FROM ws_presence WHERE room_key = $1`, roomKey,
	).Scan(&total); err != nil {
		return fmt.Errorf("rooms: presence total: %w", err)
	}
	if err := Emit(ctx, sqlDB, roomKey, "presence", map[string]any{"count": total}); err != nil {
		return fmt.Errorf("rooms: presence broadcast: %w", err)
	}
	return nil
}

// presenceBootSweep deletes any ws_presence rows already tagged with this replica's id, called
// once from Run before it starts listening. In the hub's current design this is a defensive
// no-op in practice — NewHub's replicaID is a fresh random id every process start (db.NewID()),
// so no pre-existing row can carry it — but it is the direct port of presence.ts's
// clearReplicaPresence, run at boot rather than at graceful shutdown (Run has no shutdown hook to
// call it from; here, ctx simply ending IS how a replica goes away). It costs one cheap DELETE to
// keep as a safety net against a future identity scheme (e.g. a stable per-pod id that could
// survive a crash-restart) making it meaningful again.
func (h *Hub) presenceBootSweep(ctx context.Context) error {
	_, err := h.sqlDB.ExecContext(ctx, `DELETE FROM ws_presence WHERE replica_id = $1`, h.replicaID)
	return err
}

// presenceHeartbeatLoop re-stamps every ws_presence row this replica owns AND STILL HAS AT LEAST
// ONE VIEWER IN (M2: `count > 0`) every presenceHeartbeatInterval, until ctx ends. Started
// unconditionally from Run — regardless of whether this process's Hub ever actually serves a
// Presence-enabled route — because an idle replica with no presence rows simply re-stamps zero
// rows each tick; that is simpler than threading "does anything need this" state through Run just
// to skip a cheap no-op UPDATE.
//
// The `count > 0` guard is the fix, not an optimization: presenceLeave floors a row at 0 rather
// than deleting it (a decrement racing some other cleanup path must never drive it negative — see
// presenceLeave's own doc comment), so a room this replica served but nobody is currently viewing
// can sit at count=0 indefinitely. Without this guard, THIS loop would re-stamp that zero-count
// row's heartbeat_at forever, keeping it perpetually "fresh" and permanently invisible to
// internal/jobs's presence:sweep housekeeping job (which only deletes rows whose heartbeat has
// actually lapsed) — an unbounded ws_presence leak, one row per room this replica has ever served
// even once, that would never self-heal. Excluding count=0 rows here lets them age out and get
// swept normally, the same 90s after their last real join/leave as any other stale row.
func (h *Hub) presenceHeartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(presenceHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := h.sqlDB.ExecContext(ctx,
				`UPDATE ws_presence SET heartbeat_at = now() WHERE replica_id = $1 AND count > 0`, h.replicaID,
			); err != nil {
				h.log.Error("rooms: presence heartbeat", "replica_id", h.replicaID, "error", err)
			}
		}
	}
}

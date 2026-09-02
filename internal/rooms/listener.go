package rooms

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// initialBackoff and maxBackoff bound Run's reconnect delay: it starts short (a dropped
// connection is often transient) and backs off exponentially, capped, so a genuinely down
// database doesn't get hammered with reconnect attempts. healthyConnectionDuration gates when
// the backoff is allowed to reset back to initialBackoff — see Run: a connection that keeps
// flapping (dies again within moments of reconnecting) must not get to retry at the fast initial
// rate forever, or a struggling database gets hammered exactly when it can least afford it.
const (
	initialBackoff            = 250 * time.Millisecond
	maxBackoff                = 10 * time.Second
	healthyConnectionDuration = 30 * time.Second

	// dialTimeout (M3) bounds a single pgx.Connect attempt: without it, a dial that hangs (a
	// network partition mid-handshake, a firewall silently dropping packets rather than
	// rejecting the connection) never errors out and never retries — Run's own ctx lives for the
	// whole process, so nothing else would ever time this out on its behalf.
	dialTimeout = 10 * time.Second
)

// Run owns the hub's LISTEN connection for as long as ctx is alive. It dials its own dedicated
// pgx connection (never the sqlDB pool — a pooled connection cannot hold a LISTEN session across
// separate checkouts), issues LISTEN room_events, and processes notifications one at a time until
// the connection is lost or ctx is done, reconnecting with backoff in between.
//
// EVERY successful LISTEN — the very first one included, not only a reconnect (M1) — is followed
// by a resync nudge to every live local subscriber (see resyncAll). Reconnects need it for the
// reason explained below; the first-ever LISTEN needs it too because of a race this function
// cannot rule out from its own side: a subscriber can register (Hub.Subscribe, from a WS
// connection that happened to arrive right at boot) before THIS function's own first pgx.Connect +
// LISTEN has actually completed. That subscriber's watermark floor is established relative to
// whatever room_events existed at Subscribe-time, which says nothing about whether this hub's
// LISTEN session was already active by then — any NOTIFY that fired in between is exactly as lost
// as one fired during a genuine reconnect gap (Postgres doesn't queue it either way), so the fix is
// the same: nudge every subscriber to refetch a full snapshot, unconditionally, the moment a LISTEN
// session is confirmed live, whether or not `reconnecting` happens to be true.
//
// The reconnect case's own reason: this is a real, permanent loss mode distinct from the
// cursor-visibility hazard handleNotify closes. Postgres does not queue NOTIFYs for a session
// that isn't listening, so anything that fired while this hub was disconnected (or never yet
// connected) is gone for good — including a late committer whose id was at or below whatever
// this hub's watermark for that room happened to be. Once that NOTIFY is lost, no future
// "id > watermark" sweep will find that row either (its id is behind the watermark, permanently),
// so the ONLY way such a row is ever recovered after a disconnect is the client's own full
// snapshot refetch, triggered by this nudge — not any DB-side replay from here.
//
// Run always returns ctx.Err() (nil is never a possible return): the only way out of its loop is
// ctx ending, whether that happens before the first connection attempt or in the middle of one.
func (h *Hub) Run(ctx context.Context) error {
	// Presence's boot sweep and heartbeat (presence.go) are wired in here, not because they have
	// anything to do with the LISTEN session this function otherwise manages, but because Run is
	// this Hub's one "I am now alive as a replica" entry point — the natural place to start and
	// stop them alongside everything else this process does for as long as ctx lives.
	if err := h.presenceBootSweep(ctx); err != nil {
		h.log.Error("rooms: presence boot sweep", "replica_id", h.replicaID, "error", err)
	}
	go h.presenceHeartbeatLoop(ctx)

	backoff := initialBackoff
	reconnecting := false

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
		conn, err := pgx.Connect(dialCtx, h.listenURL)
		dialCancel()
		if err != nil {
			h.log.Error("rooms: hub listener connect failed", "replica_id", h.replicaID, "error", err)
			// A subscriber that joined while every dial attempt so far has failed has never once
			// had a working LISTEN session behind it; treat the eventual success exactly like a
			// reconnect (a resync nudge) rather than staying silent about the gap.
			reconnecting = true
			if !h.sleepBackoff(ctx, &backoff) {
				return ctx.Err()
			}
			continue
		}

		if _, err := conn.Exec(ctx, "LISTEN room_events"); err != nil {
			h.log.Error("rooms: hub LISTEN failed", "replica_id", h.replicaID, "error", err)
			_ = conn.Close(context.WithoutCancel(ctx))
			reconnecting = true
			if !h.sleepBackoff(ctx, &backoff) {
				return ctx.Err()
			}
			continue
		}

		h.log.Info("rooms: hub listening for room_events", "replica_id", h.replicaID, "reconnect", reconnecting)
		if reconnecting {
			// Every pendingNotify entry recorded before this reconnect is now dead weight: it was
			// awaiting its own NOTIFY to arrive (consumePending), and that NOTIFY — like every other
			// notification that fired during the gap this hub was disconnected — is gone for good
			// (Postgres does not queue notifications for an absent listener; see this function's own
			// doc comment). Nothing will ever consume those entries, so they would otherwise sit in
			// memory forever, growing on every reconnect a busy replica has over its lifetime — the
			// same map-leak shape pruneRoomLocked (hub.go) fixes per-room, here fixed wholesale,
			// across every room at once, in the one place that actually knows a reconnect happened.
			// Safe regardless of what (if anything) resyncAll below is about to nudge: a client told
			// to resync refetches a full snapshot, never relying on pendingNotify's bookkeeping.
			// Scoped to reconnect only (unlike resyncAll below, M1): the very first LISTEN can never
			// have a stale pendingNotify entry to begin with — it starts out empty (NewHub) — so
			// there is nothing for a first-connect call to clear.
			h.clearPendingNotify()
		}
		// Unconditional (M1): see this function's own doc comment for why the very first LISTEN
		// needs this nudge exactly as much as a reconnect does.
		h.resyncAll()

		connectedAt := time.Now()
		listenErr := h.listenLoop(ctx, conn)
		_ = conn.Close(context.WithoutCancel(ctx))

		if err := ctx.Err(); err != nil {
			return err
		}
		h.log.Warn("rooms: hub listener connection lost, reconnecting", "replica_id", h.replicaID, "error", listenErr)
		reconnecting = true
		if time.Since(connectedAt) >= healthyConnectionDuration {
			// Only a genuinely stable stretch earns the fast retry rate back; a connection that
			// dies again almost immediately keeps climbing the backoff instead.
			backoff = initialBackoff
		}
		if !h.sleepBackoff(ctx, &backoff) {
			return ctx.Err()
		}
	}
}

// listenLoop reads notifications from an established LISTEN session until it errors (connection
// lost, or ctx done) — the one thing it never does is return successfully, since there's no
// "end" to a LISTEN session short of losing it.
func (h *Hub) listenLoop(ctx context.Context, conn *pgx.Conn) error {
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}

		roomKey, id, err := parseNotifyPayload(notification.Payload)
		if err != nil {
			h.log.Error("rooms: malformed room_events notify payload", "payload", notification.Payload, "error", err)
			continue
		}
		h.handleNotify(ctx, roomKey, id)
	}
}

// parseNotifyPayload splits Emit's "<roomKey>:<id>" payload. It splits on the LAST colon, not
// the first: roomKey itself routinely contains colons (e.g. "poll:p1"), while the id suffix never
// does.
func parseNotifyPayload(payload string) (roomKey string, id int64, err error) {
	idx := strings.LastIndex(payload, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("no ':' separator in %q", payload)
	}
	id, err = strconv.ParseInt(payload[idx+1:], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("parsing id from %q: %w", payload, err)
	}
	return payload[:idx], id, nil
}

// handleNotify processes one NOTIFY for "<roomKey>:<id>" and is where the cursor-visibility
// hazard is actually closed.
//
// The hazard: room_events.id is a bigserial, allocated when a row is INSERTed — i.e. *before*
// its transaction commits — but pg_notify only fires (and is only delivered to listeners) once
// that transaction commits. Two transactions can therefore commit in an order that disagrees
// with their ids: transaction A inserts id 100 and holds the transaction open; transaction B
// inserts id 101 and commits immediately, well before A does. B's own NOTIFY ("room:101")
// arrives first. If this hub, on receiving it, simply fetched "id > (highest id we've fetched
// so far)" and moved its cursor up to 101, then when A finally commits and its own NOTIFY
// ("room:100") arrives, a naive "id > 101" catch-up query would never find it — id 100 is
// numerically behind the cursor forever, even though the hub had never actually seen or
// delivered it. The event is silently and permanently lost.
//
// The fix kept here is a per-room watermark plus a "late committer" escape hatch, with two extra
// branches for the states a room can be in before it has ever been swept:
//
//   - No local subscribers for roomKey right now: there is nothing to deliver to, so skip the
//     fetch entirely, but still fold this id into the room's watermark (via
//     establishWatermarkFloor, which only ever raises it) so it doesn't fall stale. This composes
//     directly with Subscribe's cold-start floor: when a subscriber eventually joins,
//     initWatermarkFloor's own max(id) query will already reflect at least this id, and if a
//     NOTIFY and a fresh Subscribe race each other, establishWatermarkFloor's monotonic-max merge
//     resolves it safely regardless of which runs first.
//   - watermark[room] is unset (this hub has never processed a NOTIFY for this room before, and
//     Subscribe's cold-start floor query either hasn't run yet or came back empty/failed): if
//     there IS a local subscriber, treat this NOTIFY's id as the floor itself — deliver exactly
//     this one row and set the watermark to it, rather than sweeping "id > 0" and replaying the
//     room's entire history at this subscriber the moment it happens to be present for the first
//     NOTIFY. This is the same cold-start protection Subscribe provides proactively, provided
//     reactively for whichever race gets there first.
//   - watermark[room] is known, and N > watermark: fetch every row with id > watermark (not just
//     N) and deliver them in id order, then raise watermark to the highest id just fetched.
//     Fetching everything newer than the watermark, rather than only row N, is what actually
//     closes the gap: any lower id that had committed in between the last check and now (but
//     hadn't fired its own NOTIFY yet, or fired one this hub hasn't processed yet) is picked up
//     here too, because a NOTIFY is only ever queued for delivery once its sending transaction
//     commits — so by construction, everything this query can see has already committed and is
//     safe to deliver and count towards the watermark.
//   - watermark[room] is known, and N <= watermark: this is the late-committer case. The
//     watermark has already moved past N without N ever being delivered, because N's row was not
//     yet visible (its transaction hadn't committed) at the time the watermark advanced — UNLESS
//     N was already delivered as part of an earlier deliverSince sweep that ran ahead of N's own
//     NOTIFY (see deliverSince's pendingNotify bookkeeping): deliverLateCommitter checks for that
//     first and, if so, treats this as the routine, harmless duplicate-notify case and skips
//     delivering again. Otherwise N's own commit — the NOTIFY being handled right now — is the
//     *only* notification this hub will ever receive for it: a future "id > watermark" fetch will
//     never surface it, since its id is permanently behind the watermark. So it is fetched by its
//     own exact id, standalone, and delivered without touching the watermark (which is already
//     correctly ahead of it).
//
// This requires notifications for one room to be handled one at a time, in the order Postgres
// delivers them (which matches commit order) — true here because listenLoop processes them
// serially in a single goroutine; concurrent processing of the same room's notifications would
// reopen the same class of race between two overlapping "id > watermark" fetches.
//
// Residual, honestly stated: this closes the gap for every subscriber that stays live-subscribed
// to the hub while it stays connected. It does not, by itself, fix EventsSince (the REST
// catch-up path — see its own doc comment), and it does not recover a NOTIFY that was lost
// entirely to a disconnect (see Run's doc comment) — that recovery is the resync nudge's job, not
// this function's.
func (h *Hub) handleNotify(ctx context.Context, roomKey string, id int64) {
	h.mu.Lock()
	watermark, known := h.watermark[roomKey]
	hasSubscribers := len(h.subs[roomKey]) > 0
	h.mu.Unlock()

	if !hasSubscribers {
		h.establishWatermarkFloor(roomKey, id)
		return
	}

	if !known {
		h.deliverExact(ctx, roomKey, id)
		h.establishWatermarkFloor(roomKey, id)
		return
	}

	if id <= watermark {
		h.deliverLateCommitter(ctx, roomKey, id)
		return
	}
	h.deliverSince(ctx, roomKey, watermark, id)
}

// deliverExact fetches and delivers exactly one row by id, unconditionally. It's the primitive
// both the "unseen room" cold-start branch and (via deliverLateCommitter) the genuine
// late-committer path build on.
func (h *Hub) deliverExact(ctx context.Context, roomKey string, id int64) {
	var raw []byte
	err := h.sqlDB.QueryRowContext(ctx,
		`SELECT event FROM room_events WHERE room_key = $1 AND id = $2`, roomKey, id,
	).Scan(&raw)
	if err != nil {
		h.log.Error("rooms: fetch event", "room_key", roomKey, "id", id, "error", err)
		return
	}

	frame, err := buildFrame(id, raw)
	if err != nil {
		h.log.Error("rooms: build frame", "room_key", roomKey, "id", id, "error", err)
		return
	}
	h.dispatchLocal(roomKey, frame)
}

// deliverLateCommitter handles a NOTIFY for an id at or below the room's watermark. Most such
// ids were already delivered by an earlier deliverSince sweep (tracked in h.pendingNotify) and
// this is just that id's own, now-redundant, notification catching up — see deliverSince's doc
// comment for exactly when that happens (routinely: e.g. two Emits in one transaction) — and
// that case is suppressed here via consumePending. A genuine late committer (its id was invisible
// to every sweep so far, hence never tracked) falls through to deliverExact.
func (h *Hub) deliverLateCommitter(ctx context.Context, roomKey string, id int64) {
	if h.consumePending(roomKey, id) {
		return
	}
	h.deliverExact(ctx, roomKey, id)
}

// consumePending reports whether id was pending delivery-confirmation for roomKey (i.e. already
// delivered by a deliverSince sweep, awaiting only its own NOTIFY to arrive) and, if so, removes
// it — see h.pendingNotify's doc comment on Hub.
func (h *Hub) consumePending(roomKey string, id int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	pending, ok := h.pendingNotify[roomKey]
	if !ok {
		return false
	}
	if _, present := pending[id]; !present {
		return false
	}
	delete(pending, id)
	if len(pending) == 0 {
		delete(h.pendingNotify, roomKey)
	}
	return true
}

// deliverSince fetches every row for roomKey with id > watermark, delivers them in id order, and
// raises the room's watermark to the highest id it just saw. See handleNotify for why this must
// re-query "since the watermark" rather than trust the notified id alone.
//
// triggeringID is the id of the NOTIFY that caused this call. Every OTHER id in the swept batch
// (there can be more than one — e.g. two Emits inside a single transaction produce two rows and
// two NOTIFYs, and by the time the first is processed both rows are already committed and visible
// to this query) is recorded in h.pendingNotify: its own NOTIFY hasn't been processed by
// listenLoop yet, and without this bookkeeping that later NOTIFY would hit the id<=watermark
// branch and deliverLateCommitter would fetch and deliver it a second time. triggeringID itself
// is never recorded — this is the one and only NOTIFY it will ever get, so there is nothing later
// to suppress and recording it would just leak. This makes routine duplicate delivery from this
// specific, common interleaving disappear in practice, but it is NOT a general exactly-once
// guarantee — see the CONTRACT block in hub.go: a consumer still must dedupe by seq.
func (h *Hub) deliverSince(ctx context.Context, roomKey string, watermark, triggeringID int64) {
	rows, err := h.sqlDB.QueryContext(ctx,
		`SELECT id, event FROM room_events WHERE room_key = $1 AND id > $2 ORDER BY id`,
		roomKey, watermark,
	)
	if err != nil {
		h.log.Error("rooms: fetch events since watermark", "room_key", roomKey, "watermark", watermark, "error", err)
		return
	}
	defer func() { _ = rows.Close() }()

	highest := watermark
	for rows.Next() {
		var (
			id  int64
			raw []byte
		)
		if err := rows.Scan(&id, &raw); err != nil {
			h.log.Error("rooms: scan event", "room_key", roomKey, "error", err)
			continue
		}
		if id > highest {
			highest = id
		}
		if id != triggeringID {
			h.trackPending(roomKey, id)
		}

		frame, err := buildFrame(id, raw)
		if err != nil {
			// Malformed stored JSON is a program bug, not an ordering hazard: log loudly and
			// move on rather than getting stuck on one row forever.
			h.log.Error("rooms: build frame", "room_key", roomKey, "id", id, "error", err)
			continue
		}
		h.dispatchLocal(roomKey, frame)
	}
	if err := rows.Err(); err != nil {
		h.log.Error("rooms: iterate events since watermark", "room_key", roomKey, "error", err)
	}

	if highest > watermark {
		h.establishWatermarkFloor(roomKey, highest)
	}
}

// trackPending records that id was just delivered by a deliverSince sweep ahead of its own
// NOTIFY — see deliverSince's doc comment and h.pendingNotify on Hub.
func (h *Hub) trackPending(roomKey string, id int64) {
	h.mu.Lock()
	if h.pendingNotify[roomKey] == nil {
		h.pendingNotify[roomKey] = make(map[int64]struct{})
	}
	h.pendingNotify[roomKey][id] = struct{}{}
	h.mu.Unlock()
}

// clearPendingNotify wipes h.pendingNotify entirely — see Run's own doc comment at its one call
// site for why every entry recorded before a reconnect is unconsumable and safe to discard
// wholesale, regardless of room.
func (h *Hub) clearPendingNotify() {
	h.mu.Lock()
	h.pendingNotify = make(map[string]map[int64]struct{})
	h.mu.Unlock()
}

// sleepBackoff waits out a random duration in [0, *backoff) — full jitter, not the backoff value
// itself, so that many replicas recovering from a shared outage (the DB blipping) don't all retry
// in lockstep — or until ctx is done, whichever comes first, then doubles *backoff, capped at
// maxBackoff, for next time. Returns false if ctx ended the wait.
func (h *Hub) sleepBackoff(ctx context.Context, backoff *time.Duration) bool {
	wait := rand.N(*backoff)
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}

	*backoff *= 2
	if *backoff > maxBackoff {
		*backoff = maxBackoff
	}
	return true
}

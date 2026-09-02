package rooms

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// initialBackoff and maxBackoff bound Run's reconnect delay: it starts short (a dropped
// connection is often transient) and backs off exponentially, capped, so a genuinely down
// database doesn't get hammered with reconnect attempts.
const (
	initialBackoff = 250 * time.Millisecond
	maxBackoff     = 10 * time.Second
)

// Run owns the hub's LISTEN connection for as long as ctx is alive. It dials its own dedicated
// pgx connection (never the sqlDB pool — a pooled connection cannot hold a LISTEN session across
// separate checkouts), issues LISTEN room_events, and processes notifications one at a time until
// the connection is lost or ctx is done, reconnecting with backoff in between.
//
// Every reconnect after the first is followed by a resync nudge to every live local subscriber
// (see resyncAll): notifications that fired while this hub was disconnected are gone for good
// (Postgres does not queue NOTIFYs for an absent listener), so the client-side recovery is a full
// snapshot refetch rather than trying to replay the gap from here.
func (h *Hub) Run(ctx context.Context) error {
	backoff := initialBackoff
	reconnecting := false

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		conn, err := pgx.Connect(ctx, h.listenURL)
		if err != nil {
			h.log.Error("rooms: hub listener connect failed", "replica_id", h.replicaID, "error", err)
			if !h.sleepBackoff(ctx, &backoff) {
				return ctx.Err()
			}
			continue
		}

		if _, err := conn.Exec(ctx, "LISTEN room_events"); err != nil {
			h.log.Error("rooms: hub LISTEN failed", "replica_id", h.replicaID, "error", err)
			_ = conn.Close(context.WithoutCancel(ctx))
			if !h.sleepBackoff(ctx, &backoff) {
				return ctx.Err()
			}
			continue
		}

		backoff = initialBackoff
		h.log.Info("rooms: hub listening for room_events", "replica_id", h.replicaID, "reconnect", reconnecting)
		if reconnecting {
			h.resyncAll()
		}

		listenErr := h.listenLoop(ctx, conn)
		_ = conn.Close(context.WithoutCancel(ctx))

		if err := ctx.Err(); err != nil {
			return err
		}
		h.log.Warn("rooms: hub listener connection lost, reconnecting", "replica_id", h.replicaID, "error", listenErr)
		reconnecting = true
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
// The fix kept here is a per-room watermark plus a "late committer" escape hatch:
//
//   - watermark[room] is the highest id this hub has confirmed delivered, directly or via a
//     batched fetch, for that room.
//   - On a NOTIFY for id N:
//   - If N > watermark: fetch every row with id > watermark (not just N) and deliver them in
//     id order, then raise watermark to the highest id just fetched. Fetching everything
//     newer than the watermark, rather than only row N, is what actually closes the gap: any
//     lower id that had committed in between the last check and now (but hadn't fired its own
//     NOTIFY yet, or fired one this hub hasn't processed yet) is picked up here too, because a
//     NOTIFY is only ever queued for delivery once its sending transaction commits — so by
//     construction, everything this query can see has already committed and is safe to
//     deliver and count towards the watermark.
//   - If N <= watermark: this is exactly the late-committer case above. The watermark has
//     already moved past N without N ever being delivered, because N's row was not yet
//     visible (its transaction hadn't committed) at the time the watermark advanced. N's own
//     commit — the NOTIFY we're handling right now — is the *only* notification this hub will
//     ever receive for it: a future "id > watermark" fetch will never surface it, since its id
//     is permanently behind the watermark. So it is fetched by its own exact id, standalone,
//     and delivered without touching the watermark (which is already correctly ahead of it).
//
// This requires notifications for one room to be handled one at a time, in the order Postgres
// delivers them (which matches commit order) — true here because listenLoop processes them
// serially in a single goroutine; concurrent processing of the same room's notifications would
// reopen the same class of race between two overlapping "id > watermark" fetches.
//
// Residual, honestly stated: this closes the gap for every subscriber that stays live-subscribed
// to the hub. It does NOT, by itself, fix EventsSince (the REST catch-up path) — see that
// function's doc comment for the client-side mitigation and the residual window that remains
// there regardless.
func (h *Hub) handleNotify(ctx context.Context, roomKey string, id int64) {
	h.mu.Lock()
	watermark := h.watermark[roomKey]
	h.mu.Unlock()

	if id <= watermark {
		h.deliverLateCommitter(ctx, roomKey, id)
		return
	}
	h.deliverSince(ctx, roomKey, watermark)
}

// deliverLateCommitter fetches and delivers exactly one row by id — the late-committer path
// documented on handleNotify. It does not touch the watermark.
func (h *Hub) deliverLateCommitter(ctx context.Context, roomKey string, id int64) {
	var raw []byte
	err := h.sqlDB.QueryRowContext(ctx,
		`SELECT event FROM room_events WHERE room_key = $1 AND id = $2`, roomKey, id,
	).Scan(&raw)
	if err != nil {
		h.log.Error("rooms: fetch late-committed event", "room_key", roomKey, "id", id, "error", err)
		return
	}

	frame, err := buildFrame(id, raw)
	if err != nil {
		h.log.Error("rooms: build frame for late-committed event", "room_key", roomKey, "id", id, "error", err)
		return
	}
	h.dispatchLocal(roomKey, frame)
}

// deliverSince fetches every row for roomKey with id > watermark, delivers them in id order, and
// raises the room's watermark to the highest id it just saw. See handleNotify for why this must
// re-query "since the watermark" rather than trust the notified id alone.
func (h *Hub) deliverSince(ctx context.Context, roomKey string, watermark int64) {
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
		h.mu.Lock()
		if highest > h.watermark[roomKey] {
			h.watermark[roomKey] = highest
		}
		h.mu.Unlock()
	}
}

// sleepBackoff waits out *backoff (or until ctx is done, whichever comes first) and then doubles
// it, capped at maxBackoff, for next time. Returns false if ctx ended the wait.
func (h *Hub) sleepBackoff(ctx context.Context, backoff *time.Duration) bool {
	timer := time.NewTimer(*backoff)
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

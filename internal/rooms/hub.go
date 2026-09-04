package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/refsdal/whenweall/internal/db"
)

// See PROTOCOL.md for the full wire protocol (every frame shape, every route's auth, the query
// params, and these same delivery rules restated for a client implementer who will never read this
// file) — this CONTRACT block is the Go-side source of truth PROTOCOL.md itself points back to.
//
// CONTRACT — read this before writing anything that consumes a Hub's frames (a WS handler, a
// frontend client, Task 2's backfill).
//
//   - Delivery is AT-LEAST-ONCE, never exactly-once. The hub may deliver the same room_events row
//     more than once (deliberately, in the routine case: see deliverSince's pendingNotify
//     bookkeeping in listener.go, which suppresses the common duplicate but is a best-effort
//     optimization, not a promise — do not build correctness on top of it). A consumer MUST
//     dedupe by keeping a SET of every "seq" (a frame's event id) it has already applied.
//   - "seq" is NOT monotonically increasing in delivery order. room_events.id is allocated before
//     its transaction commits, while pg_notify only fires after — so a client can, and under load
//     will, receive a lower seq strictly after a higher one (a "late committer": see
//     handleNotify in listener.go). A consumer that dedupes or filters with `seq <= lastSeenSeq`
//     will silently and permanently discard exactly the events this whole design exists to
//     deliver — that is the cursor-visibility hazard reopened at the client. Dedupe by set
//     membership only; if a catch-up cursor is kept at all (for EventsSince), advance it by the
//     MAXIMUM seq observed so far, never by "the last one that arrived".
//   - On {"type":"resync"} (sent at most once per reconnect — see Run), the client MUST refetch a
//     full snapshot of the affected view from its normal REST source. Never call EventsSince to
//     "catch up" a resync: while disconnected, an arbitrary and unknown set of NOTIFYs were lost
//     forever (Postgres does not queue notifications for an absent listener), so no id-ordered
//     query — EventsSince included — can be trusted to know what was missed; only a fresh read of
//     current state can. See EventsSince's doc comment for the narrower, but still real, hazard
//     that remains even outside a resync.
//   - A frame handed to a subscriber's channel is a read-only shared []byte: a consumer must not
//     mutate it after receiving it. (At-least-once delivery does not imply distinct copies —
//     the same slice can be handed to more than one local subscriber.)

// Event is one row of room_events, decoded into typed fields.
type Event struct {
	ID      int64
	RoomKey string
	Type    string
	Data    json.RawMessage
}

// Frame renders e as the flat wire frame a client receives: {"type":..., "seq":..., <Data's
// fields flattened at the top level>}. It routes through the exact same unwrap logic
// (buildFrameFromParts) that the hub's own live dispatch uses, so a row backfilled through
// EventsSince (Task 2's job) and the same row delivered live are byte-for-byte identical in
// shape — see the CONTRACT block above for what a consumer must do with the result.
func (e Event) Frame() ([]byte, error) {
	return buildFrameFromParts(e.ID, e.Type, e.Data)
}

// subscriberBufferSize bounds every subscriber's outgoing frame channel.
//
// This is the bounded-queue rule, and it exists because of a lesson learned the hard way in the
// Bun/Durable-Object era: an unbounded (or "just make it big") per-socket queue behind a slow or
// wedged client is exactly how one stuck WS write turns into unbounded memory growth for an
// entire process, because nothing ever pushes back on the producer. A small, fixed buffer plus
// "drop the offending subscriber, not the process" (see dispatchLocal) keeps a stalled consumer's
// failure local to that one connection.
const subscriberBufferSize = 64

// resyncFrame is sent to every live subscriber right after the hub's LISTEN connection comes back
// from a reconnect. It carries no seq: it isn't a room_events row, it's a nudge telling the client
// "you may have missed something while we were disconnected — go re-fetch your snapshot" (see the
// CONTRACT block above; the frontend's response to it is a plan-8 concern).
var resyncFrame = []byte(`{"type":"resync"}`)

// subscriber is one local (in this replica's process) consumer of a room's frames.
type subscriber struct {
	ch        chan []byte
	closeOnce sync.Once
}

func (s *subscriber) close() {
	s.closeOnce.Do(func() { close(s.ch) })
}

// Hub fans out room_events rows to local WS subscribers and keeps each subscribed room's
// delivery gap-free across the LISTEN/NOTIFY cursor-visibility hazard (see handleNotify).
//
// One Hub per replica process. Multiple Hub instances (multiple replicas) against the same
// database each run their own independent LISTEN session; Postgres broadcasts a NOTIFY to every
// currently-listening session, so no cross-replica coordination is needed beyond that.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[*subscriber]struct{}

	// watermark holds, per room, the highest room_events.id this hub has confirmed delivered
	// (directly, or by folding it into a batched catch-up fetch) to its current and former local
	// subscribers. A room absent from this map is "unseen" — see handleNotify and Subscribe's
	// initWatermarkFloor for why that is a distinct state from "watermark 0", and never mix the
	// two up: 0 means "everything since id 0 is wanted", unseen means "start from now".
	watermark map[string]int64

	// pendingNotify holds, per room, the set of ids that were delivered early by a deliverSince
	// sweep (see its doc comment) but whose own NOTIFY hasn't been processed yet. It exists
	// purely to suppress the routine duplicate that would otherwise occur when that NOTIFY does
	// arrive; see deliverSince and deliverLateCommitter in listener.go. Self-draining two ways: an
	// entry is removed when its own NOTIFY finally arrives (the common case, consumePending), when
	// its room's last local subscriber leaves (pruneRoomLocked, called from both dispatchLocal's
	// drop path and Subscribe's unsubscribe closure — see pruneRoomLocked's own doc comment for the
	// full lifecycle), or wholesale, for every room at once, when Run's LISTEN session reconnects
	// (listener.go: a NOTIFY lost to the disconnected gap will never arrive to consume its entry,
	// so every entry recorded before the reconnect is dead weight the moment it happens).
	pendingNotify map[string]map[int64]struct{}

	sqlDB     *sql.DB
	listenURL string
	log       *slog.Logger
	replicaID string

	// KeepaliveInterval/PingTimeout configure ws.go's per-connection keepalive (I4): every
	// connection ServeWS accepts pings the peer every KeepaliveInterval, bounded by PingTimeout,
	// to detect a dead peer. Exported — unlike ws.go's own unexported constants for values nothing
	// else needs to vary — purely so a test can shrink them, the same convention jobs.Worker's own
	// PollInterval/JobTimeout fields already use; NewHub seeds both to sane production defaults and
	// no production code should need to touch them.
	KeepaliveInterval time.Duration
	PingTimeout       time.Duration

	// ListenIdleTimeout/ListenPingTimeout bound Run's LISTEN session's liveness check
	// (listener.go's listenLoop): a WaitForNotification that has heard nothing for
	// ListenIdleTimeout is interrupted and the connection pinged, bounded by ListenPingTimeout;
	// a failed ping is treated as a lost connection so the ordinary reconnect + resyncAll path
	// fires. Without this a half-open TCP session (NAT timeout, DB failover behind a load
	// balancer) stalled every client on this replica silently for as long as OS keepalives take
	// to notice — ten minutes or more on Linux defaults. Exported for the same reason as
	// KeepaliveInterval: so a test can shrink them.
	ListenIdleTimeout time.Duration
	ListenPingTimeout time.Duration

	// listenPingCount counts every liveness ping listenLoop has SENT (successful or not; a failed
	// one is counted too, right before the connection loss it produces is returned) since this
	// Hub was constructed. Exported via LoadListenPingCount purely so a test can observe that an
	// idle LISTEN session actually gets pinged, rather than only inferring it from the absence of
	// a reconnect — no production code reads it.
	listenPingCount atomic.Int64

	// OriginPatterns (M8) is ws.go's ServeWS's own extra allow-list for the WS handshake's Origin
	// check (websocket.AcceptOptions.OriginPatterns) — ADDITIONAL to coder/websocket's own default
	// (a request's Origin must match ITS OWN Host header, when an Origin header is present at
	// all), never a replacement for it. nil (NewHub's default, and every test's) preserves that
	// default alone, which is all a same-origin deployment ever needs.
	//
	// A Hub field, set once by its caller right after NewHub returns — not a NewHub parameter —
	// so every existing NewHub call site (most of them in tests with no origin-check concern at
	// all) stays unchanged; cmd/whenweall/main.go's own serve() is the one production call site
	// that sets it, derived from cfg.AppURL's host. This exists because the default check alone
	// trusts whatever Host header THIS PARTICULAR request happened to carry, which is not
	// necessarily the same thing internal/httpserver.CheckOrigin checks for mutating REST calls
	// (that one compares Origin against the CONFIGURED AppURL, not the request's own Host) — the
	// two coincide behind an ordinary reverse proxy that forwards Host unchanged, but not every
	// topology guarantees that. Explicitly allow-listing the configured AppURL's own host here
	// closes that gap without weakening the existing check at all.
	OriginPatterns []string

	// connsMu guards conns and closing — the live-connection registry ServeWS maintains
	// (ws.go: trackConn/untrackConn) so Run's exit path (listener.go: shutdown → ws.go:
	// closeAllConns) can send every open WebSocket a StatusGoingAway close frame and wait for its
	// handler to unwind. http.Server.Shutdown will not do this for us: by documented design it
	// "does not attempt to close nor wait for hijacked connections such as WebSockets", so
	// without this registry a SIGTERM left every client with a bare TCP drop and every handler's
	// deferred presenceLeave unrun. connWG counts handlers that have registered and not yet
	// unregistered; closing, once set, refuses new registrations.
	connsMu sync.Mutex
	conns   map[*websocket.Conn]struct{}
	closing bool
	connWG  sync.WaitGroup
}

// NewHub builds a Hub. listenURL is used only for Run's dedicated LISTEN connection (a pooled
// database/sql connection cannot hold a LISTEN session across separate queries); sqlDB is the
// pool used for every catch-up and dispatch fetch.
func NewHub(listenURL string, sqlDB *sql.DB, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		subs:              make(map[string]map[*subscriber]struct{}),
		watermark:         make(map[string]int64),
		pendingNotify:     make(map[string]map[int64]struct{}),
		sqlDB:             sqlDB,
		listenURL:         listenURL,
		log:               log,
		replicaID:         db.NewID(),
		KeepaliveInterval: defaultKeepaliveInterval,
		PingTimeout:       defaultPingTimeout,
		ListenIdleTimeout: defaultListenIdleTimeout,
		ListenPingTimeout: defaultListenPingTimeout,
		conns:             make(map[*websocket.Conn]struct{}),
	}
}

// LoadListenPingCount reports how many liveness pings listenLoop has sent so far on this Hub's
// LISTEN session (see listenPingCount's own doc comment) — a test-observability hook, not
// something production code has a reason to call.
func (h *Hub) LoadListenPingCount() int64 {
	return h.listenPingCount.Load()
}

// Subscribe registers a local subscriber for roomKey and returns a buffered channel of
// marshalled frames (see the CONTRACT block above for what a consumer must do with them) plus an
// idempotent unsubscribe func.
//
// Subscribe is exported (other packages in this module can call it) but is not the public API a
// client talks to — Task 2's ServeWS wraps it and is the actual public face.
//
// A room's first local subscriber (on this hub, since its process started) triggers
// initWatermarkFloor: without it, a brand new subscriber to a room with a long history would
// have its very first NOTIFY treat "no watermark yet" as "start from id 0", sweep the room's
// entire history in one deliverSince call, and very likely blow straight through the subscriber's
// bounded channel and get dropped before a single live event arrives — that's a cold start, not a
// slow consumer, and dropping it would be wrong. Catching a subscriber up on history that
// predates it is EventsSince's job, not Subscribe's.
func (h *Hub) Subscribe(roomKey string) (<-chan []byte, func()) {
	sub := &subscriber{ch: make(chan []byte, subscriberBufferSize)}

	h.mu.Lock()
	room := h.subs[roomKey]
	if room == nil {
		room = make(map[*subscriber]struct{})
		h.subs[roomKey] = room
	}
	room[sub] = struct{}{}
	firstLocalSubscriber := len(room) == 1
	_, watermarkKnown := h.watermark[roomKey]
	h.mu.Unlock()

	if firstLocalSubscriber && !watermarkKnown {
		h.initWatermarkFloor(roomKey)
	}

	unsubscribe := func() {
		h.mu.Lock()
		room, ok := h.subs[roomKey]
		if ok {
			delete(room, sub)
		}
		// No `if ok` guard around the prune check below (an earlier version of this closure had
		// one): dispatchLocal can have already deleted h.subs[roomKey] out from under this
		// subscriber — its OWN slow-consumer drop path prunes the room the moment it empties,
		// which can race this exact unsubscribe call for the very same, now-dropped subscriber.
		// When that happens ok is false here, room is nil, and len(nil) is 0 — pruneRoomLocked
		// still needs to run (defensively harmless if dispatchLocal already ran it first; deleting
		// an absent map key is a no-op) so a subscriber that reaches this function via the
		// "dropped, then its own deferred unsubscribe fires anyway" path is never the one case that
		// skips cleanup. See pruneRoomLocked's own doc comment for the full lifecycle this and
		// dispatchLocal's drop path both feed into.
		if !ok || len(room) == 0 {
			h.pruneRoomLocked(roomKey)
		}
		h.mu.Unlock()
		sub.close()
	}
	return sub.ch, unsubscribe
}

// pruneRoomLocked deletes every trace of roomKey — its subs entry, its watermark, and any
// pendingNotify entries — once nothing is locally subscribed to it anymore. Must be called with
// h.mu held.
//
// The full lifecycle these three maps share: Subscribe adds roomKey to subs (and, for its first
// local subscriber, primes watermark via initWatermarkFloor); handleNotify/deliverSince may add or
// advance watermark and populate pendingNotify as NOTIFYs arrive (see listener.go's own doc
// comments for both); and this function is the ONE place all three are torn back down, called from
// the two and only two paths a room's last local subscriber can disappear by: the unsubscribe
// closure above (the ordinary, graceful "the connection closed" path) and dispatchLocal's
// slow-consumer drop path (a subscriber can be removed WITHOUT ever calling unsubscribe itself —
// the hub notices its channel is full and drops it there instead). Missing either call site was
// this design's documented map leak (I3): a room subscribed to even briefly, then abandoned via
// the drop path specifically, used to leave its watermark (and possibly a pendingNotify entry)
// behind forever, since nothing else ever revisits a room with zero subscribers. Neither watermark
// nor pendingNotify means anything with nobody listening regardless of which path got there:
// pruning both here means a future resubscribe gets a fresh initWatermarkFloor rather than
// trusting stale state, and handleNotify's short-circuit (no local subscribers) re-derives a floor
// from whatever NOTIFYs arrive in the meantime.
//
// See also Run's reconnect path (listener.go), which clears pendingNotify wholesale across EVERY
// room on a lost/reconnected LISTEN session — a coarser, session-scoped version of this same
// cleanup, for the same underlying reason: entries recorded before a disconnect can never be
// consumed (their own NOTIFY is gone for good), so nothing revisits them either.
func (h *Hub) pruneRoomLocked(roomKey string) {
	delete(h.subs, roomKey)
	delete(h.watermark, roomKey)
	delete(h.pendingNotify, roomKey)
}

// initWatermarkFloor establishes a cold-start floor for a room at (approximately) "now": the
// room's current max(id), so this hub's first NOTIFY for it doesn't get treated as "replay
// everything since id 0" — see Subscribe's doc comment for why that matters.
//
// A bounded timeout keeps Subscribe from blocking indefinitely if the database is slow or
// unreachable. If the query errors or times out, the floor is simply left unestablished here:
// handleNotify's "unseen room" branch provides the exact same protection reactively, on this
// room's next NOTIFY, so a failure here is degraded (that one notify's row is all a subscriber
// gets rather than everything since "now"), not unsafe.
func (h *Hub) initWatermarkFloor(roomKey string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var floor int64
	err := h.sqlDB.QueryRowContext(ctx,
		`SELECT coalesce(max(id), 0) FROM room_events WHERE room_key = $1`, roomKey,
	).Scan(&floor)
	if err != nil {
		h.log.Error("rooms: establishing cold-start watermark floor", "room_key", roomKey, "error", err)
		return
	}
	h.establishWatermarkFloor(roomKey, floor)
}

// establishWatermarkFloor raises watermark[roomKey] to floor, unless it is already at or past
// floor — it never moves a room's watermark backwards. Used by both initWatermarkFloor and
// handleNotify's "unseen room" and "no local subscribers" branches (listener.go), which can race
// each other arbitrarily; taking the max of whatever each proposes is what makes that safe
// (over-advancing is, at worst, an extra standalone late-committer fetch later — see
// deliverLateCommitter — never a silent loss).
func (h *Hub) establishWatermarkFloor(roomKey string, floor int64) {
	h.mu.Lock()
	if current, ok := h.watermark[roomKey]; !ok || floor > current {
		h.watermark[roomKey] = floor
	}
	h.mu.Unlock()
}

// dispatchLocal delivers frame to every subscriber currently registered for roomKey. A
// subscriber whose channel is full (its consumer isn't keeping up, or has stopped reading
// entirely) is dropped instead of blocking: see subscriberBufferSize's doc comment.
func (h *Hub) dispatchLocal(roomKey string, frame []byte) {
	h.mu.Lock()
	room := h.subs[roomKey]
	var dropped []*subscriber
	for sub := range room {
		select {
		case sub.ch <- frame:
		default:
			dropped = append(dropped, sub)
		}
	}
	for _, sub := range dropped {
		delete(room, sub)
	}
	if len(room) == 0 {
		// See pruneRoomLocked's own doc comment: this is one of the two paths a room's last local
		// subscriber can disappear by, and the one the map-leak fix (I3) was specifically missing.
		h.pruneRoomLocked(roomKey)
	}
	h.mu.Unlock()

	for _, sub := range dropped {
		sub.close()
	}
}

// resyncAll sends resyncFrame to every subscriber of every currently-subscribed room. Called
// once after Run re-establishes its LISTEN session following a connection loss, because
// notifications that fired while disconnected are gone forever (Postgres does not queue NOTIFYs
// for a session that isn't listening) — the client-side fix is a full snapshot refetch, not
// event replay (see the CONTRACT block above), so the nudge alone is the whole story from the
// hub's side.
func (h *Hub) resyncAll() {
	h.mu.Lock()
	roomKeys := make([]string, 0, len(h.subs))
	for roomKey := range h.subs {
		roomKeys = append(roomKeys, roomKey)
	}
	h.mu.Unlock()

	for _, roomKey := range roomKeys {
		h.dispatchLocal(roomKey, resyncFrame)
	}
}

// EventsSince is the catch-up query: events for roomKey after sinceID, oldest first.
//
// This is a plain "id > sinceID" query, which carries the same cursor-visibility hazard the rest
// of this file works around for live delivery (see handleNotify's doc comment): a row whose id is
// numerically below sinceID but which committed *after* the client last saw sinceID will never
// be returned here, because id ordering and commit ordering can disagree. That window is real and
// is not closed by this function alone — the accepted mitigation (and the residual risk) is
// documented at length in the task report; the short version is that a reconnecting client should
// Subscribe (live) before calling EventsSince, so any such row still reaches it through the
// live/late-committer path even though the catch-up query alone would miss it. It is never a
// substitute for a resync's full snapshot refetch — see the CONTRACT block above.
func (h *Hub) EventsSince(ctx context.Context, roomKey string, sinceID int64) ([]Event, error) {
	rows, err := h.sqlDB.QueryContext(ctx,
		`SELECT id, room_key, event FROM room_events WHERE room_key = $1 AND id > $2 ORDER BY id`,
		roomKey, sinceID,
	)
	if err != nil {
		return nil, fmt.Errorf("rooms: query events since %d: %w", sinceID, err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]Event, 0)
	for rows.Next() {
		var (
			ev  Event
			raw []byte
		)
		if err := rows.Scan(&ev.ID, &ev.RoomKey, &raw); err != nil {
			return nil, fmt.Errorf("rooms: scan event: %w", err)
		}
		envelopeType, data, err := unmarshalEnvelope(raw)
		if err != nil {
			return nil, fmt.Errorf("rooms: unmarshal event %d: %w", ev.ID, err)
		}
		ev.Type = envelopeType
		ev.Data = data
		events = append(events, ev)
	}
	return events, rows.Err()
}

// unmarshalEnvelope decodes the {"type","data"} shape Emit wrote to room_events.event.
func unmarshalEnvelope(raw []byte) (eventType string, data json.RawMessage, err error) {
	var envelope struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", nil, err
	}
	return envelope.Type, envelope.Data, nil
}

// buildFrame unwraps the raw room_events.event column bytes (the {"type","data"} envelope Emit
// wrote) into a frame, via buildFrameFromParts. Used by the hub's own live dispatch, which only
// ever has the raw column bytes on hand (see deliverExact/deliverSince in listener.go); Event.Frame
// is the equivalent entry point for callers (Task 2's backfill) that already have it decoded.
func buildFrame(id int64, raw []byte) ([]byte, error) {
	eventType, data, err := unmarshalEnvelope(raw)
	if err != nil {
		return nil, err
	}
	return buildFrameFromParts(id, eventType, data)
}

// buildFrameFromParts is the ONE place the durable {"type","data"} envelope is unwrapped into the
// flat wire frame a client receives: {"type":..., "seq":..., <data's fields flattened at the top
// level>}. data == null (or empty) yields just {"type","seq"}. See the CONTRACT block above for
// what a consumer must (and must not) assume about the "seq" this produces.
//
// "type" and "seq" always win if a data field happens to collide with either name — they are
// protocol-level, not domain data.
func buildFrameFromParts(id int64, eventType string, data json.RawMessage) ([]byte, error) {
	fields := map[string]any{}
	if len(data) > 0 && string(data) != "null" {
		if err := json.Unmarshal(data, &fields); err != nil {
			// Every emitter in this codebase passes a struct or map as Emit's data (see
			// rooms.Emit's `data any` and every plan-4/5 call site), so data is always a JSON
			// object or null in practice. If it's ever something else (a bare scalar/array),
			// nest it verbatim under "data" rather than silently dropping it or failing the
			// whole dispatch.
			fields = map[string]any{"data": json.RawMessage(data)}
		}
	}
	fields["type"] = eventType
	fields["seq"] = id
	return json.Marshal(fields)
}

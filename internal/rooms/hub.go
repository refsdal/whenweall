package rooms

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/refsdal/whenweall/internal/db"
)

// Event is one row of room_events, decoded into typed fields.
type Event struct {
	ID      int64
	RoomKey string
	Type    string
	Data    json.RawMessage
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
// "you may have missed something while we were disconnected — go re-fetch your snapshot" (the
// frontend's response to it is a plan-8 concern).
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
	// (directly or via a batched catch-up fetch) to its current and former local subscribers.
	// See handleNotify for why this is not simply "the highest id notified".
	watermark map[string]int64

	sqlDB     *sql.DB
	listenURL string
	log       *slog.Logger
	replicaID string
}

// NewHub builds a Hub. listenURL is used only for Run's dedicated LISTEN connection (a pooled
// database/sql connection cannot hold a LISTEN session across separate queries); sqlDB is the
// pool used for every catch-up and dispatch fetch.
func NewHub(listenURL string, sqlDB *sql.DB, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.Default()
	}
	return &Hub{
		subs:      make(map[string]map[*subscriber]struct{}),
		watermark: make(map[string]int64),
		sqlDB:     sqlDB,
		listenURL: listenURL,
		log:       log,
		replicaID: db.NewID(),
	}
}

// Subscribe registers a local subscriber for roomKey and returns a buffered channel of
// marshalled frames (see buildFrame for their shape) plus an idempotent unsubscribe func.
//
// Subscribe is exported (other packages in this module can call it) but is not the public API a
// client talks to — Task 2's ServeWS wraps it and is the actual public face.
func (h *Hub) Subscribe(roomKey string) (<-chan []byte, func()) {
	sub := &subscriber{ch: make(chan []byte, subscriberBufferSize)}

	h.mu.Lock()
	if h.subs[roomKey] == nil {
		h.subs[roomKey] = make(map[*subscriber]struct{})
	}
	h.subs[roomKey][sub] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if room, ok := h.subs[roomKey]; ok {
			delete(room, sub)
			if len(room) == 0 {
				delete(h.subs, roomKey)
			}
		}
		h.mu.Unlock()
		sub.close()
	}
	return sub.ch, unsubscribe
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
		delete(h.subs, roomKey)
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
// event replay, so the nudge alone is the whole story from the hub's side.
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
// live/late-committer path even though the catch-up query alone would miss it.
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

// buildFrame is the ONE place the durable {"type","data"} envelope (what Emit writes) is
// unwrapped into the flat wire frame WS clients receive: {"type":..., "seq":..., <data's fields
// flattened at the top level>}. data == null (or empty) yields just {"type","seq"}.
//
// "type" and "seq" always win if a data field happens to collide with either name — they are
// protocol-level, not domain data.
func buildFrame(id int64, raw []byte) ([]byte, error) {
	eventType, data, err := unmarshalEnvelope(raw)
	if err != nil {
		return nil, err
	}

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

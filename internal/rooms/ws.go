// This file is Task 2: ServeWS, the actual public face a client talks to (Subscribe, exported
// from hub.go, is an internal building block — see its own doc comment). Every frame this file
// writes to a client, live or backfilled, must honor the CONTRACT block at the top of hub.go —
// read that first if you haven't. See PROTOCOL.md for the full wire protocol these frames (and the
// snapshot/backfill/keepalive/ping-pong machinery below) implement, with literal JSON examples.
package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/refsdal/whenweall/internal/httpserver"
)

// Sentinel errors an Authorize func (WSOptions.Authorize) can return. ServeWS maps each to an
// HTTP status and writes the standard JSON error envelope (httpserver.Err) BEFORE ever attempting
// the WS upgrade — see statusForAuthzErr. Any other error is still answered (never a bare
// connection drop or a 500 for what is fundamentally a client-side "you can't be here"), just
// with the safest default status: 403.
var (
	ErrUnauthorized = errors.New("rooms: unauthorized")
	ErrForbidden    = errors.New("rooms: forbidden")
	ErrNotFound     = errors.New("rooms: room not found")
)

// SnapshotFunc builds the payload for a connection's first frame. Its return value need not be
// perfectly synchronized with the seq ServeWS attaches to it (see sendSnapshotAndBackfill) — every
// connection goes through the same subscribe-then-backfill-then-live path regardless, which is
// what actually closes any such gap, not precise ordering of this call.
type SnapshotFunc func(ctx context.Context, roomKey string) (data any, err error)

// WSOptions configures one ServeWS-mounted route.
type WSOptions struct {
	// Snapshot builds the payload sent as the connection's very first frame. May be nil for a
	// route with no meaningful "current state" beyond the live event stream itself.
	Snapshot SnapshotFunc

	// Authorize runs BEFORE the WS upgrade is attempted: it resolves the request to the room the
	// caller may join, or an error (one of the sentinels above, ideally) mapped to an HTTP
	// 401/403/404 response instead.
	Authorize func(r *http.Request) (roomKey string, err error)

	// Presence: when true, this connection's whole lifetime (join through close) adjusts
	// ws_presence for its room and broadcasts the room's new total — see presence.go.
	Presence bool
}

// wsWriteTimeout bounds every write this file makes to a client connection (snapshot, backfill,
// and live frames alike) — see writeFrame.
const wsWriteTimeout = 5 * time.Second

// defaultKeepaliveInterval/defaultPingTimeout are Hub.KeepaliveInterval/Hub.PingTimeout's
// production defaults (NewHub, hub.go) — see keepaliveLoop. 30s mirrors presence.go's own
// presenceHeartbeatInterval (the same order of magnitude as this codebase's other liveness
// signals); PingTimeout is comfortably shorter than the interval so one slow-but-alive round trip
// can't cause pings to pile up against each other.
const (
	defaultKeepaliveInterval = 30 * time.Second
	defaultPingTimeout       = 10 * time.Second
)

// pongFrame is this file's entire reply to a client ping — see readPumpLoop.
var pongFrame = []byte(`{"type":"pong"}`)

// ServeWS returns the handler for one WS-backed room route.
func (h *Hub) ServeWS(opts WSOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		roomKey, err := opts.Authorize(r)
		if err != nil {
			status, code := statusForAuthzErr(err)
			httpserver.Err(w, status, code, err.Error(), nil)
			return
		}

		// InsecureSkipVerify is left false: Accept's default behaviour is to require a request's
		// Origin header (when one is present at all — an absent header, e.g. a same-origin request
		// from an older client, is let through unchanged) to match EITHER the request's own Host
		// header exactly, OR one of OriginPatterns (h.OriginPatterns, hub.go — nil by default,
		// preserving the plain same-origin check alone). See OriginPatterns's own doc comment
		// (hub.go) for why a configured allow-list is worth adding on top of the request's own Host
		// header (M8) rather than trusting it alone, the way an earlier version of this comment
		// argued was unconditionally fine.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			// NoContextTakeover: many small JSON frames, across a potentially large number of
			// concurrent connections — bounding each connection's compression window matters more
			// here than the extra compression ratio ContextTakeover would buy.
			CompressionMode: websocket.CompressionNoContextTakeover,
			OriginPatterns:  h.OriginPatterns,
		})
		if err != nil {
			// Accept already wrote the handshake failure response itself.
			return
		}
		defer func() { _ = conn.CloseNow() }()

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// Subscribe BEFORE presenceJoin, and BEFORE building the snapshot or running the
		// ?since= backfill: any event that commits from this point on — including this very
		// connection's own presenceJoin broadcast just below — reaches this connection through
		// the live channel even if the snapshot/backfill queries that follow also happen to
		// observe it too. The CONTRACT block's at-least-once guarantee makes that an accepted,
		// harmless duplicate, not a correctness problem. Doing this any other way around (join or
		// backfill before subscribe) reopens a real gap: presenceJoin before subscribe meant a
		// first-connect client with no ?since= could never learn its own presence count until
		// some other peer moved (fixed here); snapshot/backfill before subscribe is the residual
		// gap Task 1 flagged (an event committing in between would be seen by neither).
		frames, unsubscribe := h.Subscribe(roomKey)
		defer unsubscribe()

		if opts.Presence {
			h.presenceJoin(ctx, roomKey)
			defer func() {
				leaveCtx, leaveCancel := context.WithTimeout(context.Background(), wsWriteTimeout)
				defer leaveCancel()
				h.presenceLeave(leaveCtx, roomKey)
			}()
		}

		if err := h.sendSnapshotAndBackfill(ctx, conn, roomKey, opts.Snapshot, r); err != nil {
			h.log.Error("rooms: ws snapshot/backfill", "room_key", roomKey, "error", err)
			_ = conn.Close(websocket.StatusInternalError, "snapshot failed")
			return
		}

		readDone := make(chan struct{})
		go h.readPumpLoop(ctx, conn, readDone)
		go h.keepaliveLoop(ctx, conn, cancel)

		h.writePumpLoop(ctx, conn, frames, readDone)
	}
}

// statusForAuthzErr maps an Authorize error to an HTTP status and envelope code. Anything that
// isn't one of the three sentinels above defaults to 403 rather than a generic 500: an Authorize
// failure is always a client-caused rejection, never a server error, even when its exact reason
// isn't one this package has a dedicated sentinel for.
func statusForAuthzErr(err error) (status int, code string) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return http.StatusUnauthorized, "unauthorized"
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, "not_found"
	default:
		return http.StatusForbidden, "forbidden"
	}
}

// snapshotFrame is the wire shape of a connection's very first frame. Unlike every other frame
// this file sends (all of which are room_events rows, unwrapped by Event.Frame/buildFrameFromParts
// — see hub.go), a snapshot isn't a room_events row at all, so it gets its own, deliberately
// distinct shape: {"type":"snapshot","seq":<current max room_events.id>,"data":<Snapshot's
// payload, or null>}. "data" nests here — unlike the live/backfill frames' flattened fields —
// precisely because there is no envelope-unwrapping step for it to go through.
type snapshotFrame struct {
	Type string `json:"type"`
	Seq  int64  `json:"seq"`
	Data any    `json:"data"`
}

// sendSnapshotAndBackfill writes the connection's snapshot frame, then — only when the request
// carries a valid ?since= — every room_events row after it via EventsSince, in order, via
// Event.Frame (so a backfilled row and the same row delivered live are byte-for-byte identical —
// see Event.Frame's doc comment). A missing or unparsable ?since= is treated as "no backfill
// wanted" (the ordinary first-connect case), never as "since the beginning of history" — see
// parseSinceParam.
func (h *Hub) sendSnapshotAndBackfill(ctx context.Context, conn *websocket.Conn, roomKey string, snapshot SnapshotFunc, r *http.Request) error {
	seq, err := h.currentSeq(ctx, roomKey)
	if err != nil {
		return err
	}

	var data any
	if snapshot != nil {
		data, err = snapshot(ctx, roomKey)
		if err != nil {
			return err
		}
	}

	frame, err := json.Marshal(snapshotFrame{Type: "snapshot", Seq: seq, Data: data})
	if err != nil {
		return fmt.Errorf("rooms: marshal snapshot frame: %w", err)
	}
	if err := writeFrame(ctx, conn, frame); err != nil {
		return err
	}

	since, ok := parseSinceParam(r)
	if !ok {
		return nil
	}
	events, err := h.EventsSince(ctx, roomKey, since)
	if err != nil {
		return err
	}
	for _, ev := range events {
		evFrame, err := ev.Frame()
		if err != nil {
			return err
		}
		if err := writeFrame(ctx, conn, evFrame); err != nil {
			return err
		}
	}
	return nil
}

// currentSeq returns the current max(id) for roomKey: the point-in-time cursor a freshly built
// snapshot is described against. This deliberately does not touch (or need) Hub's watermark
// bookkeeping — unlike initWatermarkFloor's identical-looking query, which exists purely for the
// hub's own cold-start protection — this is just what a caller of ServeWS needs to know to label
// its snapshot.
func (h *Hub) currentSeq(ctx context.Context, roomKey string) (int64, error) {
	var seq int64
	if err := h.sqlDB.QueryRowContext(ctx,
		`SELECT coalesce(max(id), 0) FROM room_events WHERE room_key = $1`, roomKey,
	).Scan(&seq); err != nil {
		return 0, fmt.Errorf("rooms: query current seq: %w", err)
	}
	return seq, nil
}

// parseSinceParam reads ?since= as a non-negative int64. Both an absent param and an unparsable
// one report ok == false — those two cases must never be told apart from an explicit "since=0",
// which is a real, different request ("replay everything") from "no since was given at all"
// (the ordinary first-connect case, which wants no backfill sweep). See sendSnapshotAndBackfill.
func parseSinceParam(r *http.Request) (since int64, ok bool) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// writeFrame writes one frame under a bounded write timeout — a wedged or wildly slow TCP peer
// must never be able to block this connection's writer indefinitely.
func writeFrame(ctx context.Context, conn *websocket.Conn, frame []byte) error {
	wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, frame)
}

// writePumpLoop is the connection's entire live-delivery loop: every frame the hub hands this
// subscriber — a room_events row, a presence update (itself just another room_events row, see
// presence.go's broadcastPresence), or a resync nudge (hub.go's resyncFrame) — is indistinguishable
// at this layer and forwarded verbatim, in order, until either side ends the conversation.
//
//   - frames closing means the hub dropped this subscriber as a slow consumer (subscriberBufferSize's
//     doc comment): the WS is closed with StatusPolicyViolation so a well-behaved client
//     reconnects — a fresh connection re-subscribes and re-snapshots, the only way to recover
//     from having fallen behind by a full buffer's worth of frames.
//   - ctx ending (the read pump saw the client close/error, or the request context itself ended)
//     or a write erroring both just return, letting ServeWS's deferred cleanup run.
func (h *Hub) writePumpLoop(ctx context.Context, conn *websocket.Conn, frames <-chan []byte, readDone <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return
		case frame, ok := <-frames:
			if !ok {
				_ = conn.Close(websocket.StatusPolicyViolation, "dropped: too slow, reconnect")
				return
			}
			if err := writeFrame(ctx, conn, frame); err != nil {
				return
			}
		}
	}
}

// readPumpLoop owns the connection's entire read side (Conn.Read must never be called from more
// than one goroutine at a time — every other Conn method, Write included, is safe to call
// concurrently with it, per coder/websocket's own doc comment). The only two things a client ever
// sends in this design are a ping (answered inline, on this same goroutine, with a pong) or a
// close frame/dropped connection (which unblocks Read with an error and ends this loop) — closing
// readDone is what tells writePumpLoop to stop pumping live frames at a peer that is gone.
func (h *Hub) readPumpLoop(ctx context.Context, conn *websocket.Conn, readDone chan<- struct{}) {
	defer close(readDone)
	for {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var incoming struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(msg, &incoming) != nil || incoming.Type != "ping" {
			continue
		}
		if err := writeFrame(ctx, conn, pongFrame); err != nil {
			return
		}
	}
}

// keepaliveLoop pings conn every h.KeepaliveInterval to detect a dead peer (I4) — a connection
// whose TCP session never sees a clean close/RST (a client that vanished mid-flight, an
// intervening middlebox that silently drops an idle connection, ...) would otherwise sit in
// h.subs forever, its buffered channel filling with frames nobody will ever read until
// dispatchLocal eventually drops it as "slow" — a real but needlessly delayed way to notice a
// peer that's actually just gone.
//
// coder/websocket's Ping (conn.Ping) sends a WebSocket-protocol ping frame and blocks for the
// peer's pong, which is delivered to it via the SAME mechanism as any other control frame: a
// concurrent Read call processing it. That concurrent reader is readPumpLoop, already running on
// its own goroutine for this connection's entire lifetime (ws.go's own doc comment on Ping's
// needing "the concurrent reader" refers to exactly this). A ping that doesn't get its pong within
// h.PingTimeout means the peer is unresponsive; cancel (the connection's own ctx.CancelFunc) tears
// the connection down the same way losing the client's own read already does — unblocking
// writePumpLoop's ctx.Done() case and readPumpLoop's in-flight conn.Read(ctx) call.
func (h *Hub) keepaliveLoop(ctx context.Context, conn *websocket.Conn, cancel context.CancelFunc) {
	ticker := time.NewTicker(h.KeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, pingCancel := context.WithTimeout(ctx, h.PingTimeout)
			err := conn.Ping(pingCtx)
			pingCancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

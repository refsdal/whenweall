package rooms_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

// dialWS dials path on server and fails the test if the WS handshake itself doesn't succeed.
func dialWS(t *testing.T, server *httptest.Server, path string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, server.URL+path, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", path, err)
	}
	return conn
}

// readWSFrame reads and JSON-decodes the next frame off conn, failing the test if none arrives
// within timeout or the read errors (including an ordinary close).
func readWSFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read ws frame: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ws frame %s: %v", data, err)
	}
	return got
}

// awaitPresenceFrame reads frames off conn (skipping anything that isn't a "presence" frame,
// e.g. the initial snapshot or an unrelated event) until one reports count == want, or fails the
// test after too many frames without finding it.
func awaitPresenceFrame(t *testing.T, conn *websocket.Conn, want int) map[string]any {
	t.Helper()
	for i := 0; i < 20; i++ {
		frame := readWSFrame(t, conn, 5*time.Second)
		if frame["type"] != "presence" {
			continue
		}
		if count, ok := frame["count"].(float64); ok && int(count) == want {
			return frame
		}
	}
	t.Fatalf("did not see a presence frame with count=%d in time", want)
	return nil
}

func TestServeWS_SnapshotIsFirstFrame(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:ws-snapshot"

	// Pre-existing history: the snapshot's seq must reflect it even though this connection never
	// backfills it (no ?since= is given). room_events.id is a single bigserial shared by every
	// room (see migrations/00001_infra.sql), not a per-room counter, so the expected seq is
	// whatever id this room's own last event actually landed on — read back via EventsSince
	// rather than assumed to be "2".
	emitCommitted(t, sqlDB, roomKey, "seed", nil)
	emitCommitted(t, sqlDB, roomKey, "seed", nil)
	seeded, err := hub.EventsSince(context.Background(), roomKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantSeq := seeded[len(seeded)-1].ID

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS(rooms.WSOptions{
		Authorize: func(r *http.Request) (string, error) { return roomKey, nil },
		Snapshot: func(_ context.Context, _ string) (any, error) {
			return map[string]any{"hello": "world"}, nil
		},
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn := dialWS(t, server, "/ws")
	defer func() { _ = conn.CloseNow() }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "snapshot" {
		t.Fatalf("first frame type = %v, want snapshot", frame["type"])
	}
	if seq, ok := frame["seq"].(float64); !ok || int64(seq) != wantSeq {
		t.Errorf("snapshot seq = %v, want %d", frame["seq"], wantSeq)
	}
	data, ok := frame["data"].(map[string]any)
	if !ok || data["hello"] != "world" {
		t.Errorf("snapshot data = %v, want {hello: world}", frame["data"])
	}
}

func TestServeWS_LiveEventArrivesOverTheWire(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:ws-live"

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS(rooms.WSOptions{
		Authorize: func(r *http.Request) (string, error) { return roomKey, nil },
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn := dialWS(t, server, "/ws")
	defer func() { _ = conn.CloseNow() }()

	_ = readWSFrame(t, conn, 5*time.Second) // snapshot

	emitCommitted(t, sqlDB, roomKey, "poll.changed", map[string]any{"pollId": "p1"})

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "poll.changed" {
		t.Errorf("live frame type = %v, want poll.changed", frame["type"])
	}
	if frame["pollId"] != "p1" {
		t.Errorf("live frame pollId = %v, want p1", frame["pollId"])
	}
	if _, ok := frame["seq"]; !ok {
		t.Errorf("live frame missing seq: %v", frame)
	}
}

func TestServeWS_ReconnectReplaysMissedThenLive(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:ws-reconnect"

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS(rooms.WSOptions{
		Authorize: func(r *http.Request) (string, error) { return roomKey, nil },
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn1 := dialWS(t, server, "/ws")
	snap := readWSFrame(t, conn1, 5*time.Second)
	lastSeq := int64(snap["seq"].(float64))
	_ = conn1.CloseNow() // simulate the client going away

	// Missed while "disconnected".
	emitCommitted(t, sqlDB, roomKey, "missed1", map[string]any{"n": 1})
	emitCommitted(t, sqlDB, roomKey, "missed2", map[string]any{"n": 2})

	conn2 := dialWS(t, server, fmt.Sprintf("/ws?since=%d", lastSeq))
	defer func() { _ = conn2.CloseNow() }()

	_ = readWSFrame(t, conn2, 5*time.Second) // snapshot, again

	emitCommitted(t, sqlDB, roomKey, "live", nil)

	// Delivery is at-least-once (PROTOCOL.md): a missed event whose LISTEN dispatch is still in
	// flight when conn2 subscribes arrives twice — once via backfill, once live. Read like a real
	// client: dedupe by a set of seqs, and judge ordering on first occurrences only.
	seen := map[float64]bool{}
	var order []string
	seqOf := map[string]float64{}
	for len(order) < 3 {
		frame := readWSFrame(t, conn2, 5*time.Second)
		seq, _ := frame["seq"].(float64)
		if seen[seq] {
			continue // duplicate delivery — the contract's routine case, a client drops it
		}
		seen[seq] = true
		typ, _ := frame["type"].(string)
		order = append(order, typ)
		seqOf[typ] = seq
	}
	if order[0] != "missed1" || order[1] != "missed2" || order[2] != "live" {
		t.Fatalf("first-occurrence order = %v, want [missed1 missed2 live]", order)
	}
	if seqOf["missed1"] >= seqOf["missed2"] {
		t.Errorf("backfilled seqs not increasing: %v, %v", seqOf["missed1"], seqOf["missed2"])
	}
}

// TestServeWS_PresenceCountJoinAndLeave connects two clients with NO ?since= at all — neither
// needs one. Subscribe now runs before presenceJoin (see ServeWS's doc comment), so each
// connection's own join broadcast is already reaching its own (buffered) subscriber channel by
// the time presenceJoin returns, and arrives as an ordinary live frame shortly after that
// connection's snapshot — this is the regression test for that fix: a first-connect client used
// to be unable to learn its own presence count until some other peer moved.
func TestServeWS_PresenceCountJoinAndLeave(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:ws-presence"

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS(rooms.WSOptions{
		Authorize: func(r *http.Request) (string, error) { return roomKey, nil },
		Presence:  true,
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	conn1 := dialWS(t, server, "/ws")
	_ = readWSFrame(t, conn1, 5*time.Second) // snapshot
	awaitPresenceFrame(t, conn1, 1)          // conn1's own join, live — no ?since= needed

	conn2 := dialWS(t, server, "/ws")
	_ = readWSFrame(t, conn2, 5*time.Second) // snapshot

	awaitPresenceFrame(t, conn1, 2) // conn1 sees conn2's join live
	awaitPresenceFrame(t, conn2, 2) // conn2 sees its own join live too

	_ = conn2.CloseNow()

	awaitPresenceFrame(t, conn1, 1) // conn2 leaving, seen live by conn1

	_ = conn1.CloseNow()
	awaitZeroPresenceRows(t, sqlDB, roomKey) // wait out conn1's own async leave before returning,
	// so the test doesn't race testdb's cleanup closing sqlDB out from under a still-in-flight
	// presenceLeave query (the same class of harmless-but-noisy race hub_test.go's runHub already
	// documents for the hub's own listener goroutine).
}

// awaitZeroPresenceRows polls ws_presence until roomKey's total viewer count reaches 0 (every
// connection's async presenceLeave has landed) or fails the test after a bounded wait.
func awaitZeroPresenceRows(t *testing.T, sqlDB *sql.DB, roomKey string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var total int64
		if err := sqlDB.QueryRowContext(context.Background(),
			`SELECT coalesce(sum(count), 0) FROM ws_presence WHERE room_key = $1`, roomKey,
		).Scan(&total); err != nil {
			t.Fatal(err)
		}
		if total == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for ws_presence to reach 0 for room")
}

// TestServeWS_KeepaliveTearsDownDeadPeer is I4's regression test: a peer that stops reading
// entirely (never issues another conn.Read after its own snapshot) can never answer a WebSocket
// ping — coder/websocket only processes/responds to control frames from inside an active Read
// call — so the server's own keepalive ping must eventually time out and tear the connection down.
// Shrinks Hub.KeepaliveInterval/PingTimeout (test-only fields — see hub.go's own doc comment) so
// this doesn't need to wait out the real 30s production default.
//
// Observed indirectly, through a SECOND connection watching the same room's presence count: rather
// than trying to read anything more from the dead connection itself (which by construction is not
// reading, so nothing it could report is a load-bearing assertion), an observer sees presence rise
// to 2 (both connections' own joins) and then fall back to 1 once the server notices the dead peer
// and runs its ordinary connection-close cleanup (presenceLeave).
func TestServeWS_KeepaliveTearsDownDeadPeer(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	hub.KeepaliveInterval = 150 * time.Millisecond
	hub.PingTimeout = 150 * time.Millisecond
	const roomKey = "poll:ws-keepalive"

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", hub.ServeWS(rooms.WSOptions{
		Authorize: func(r *http.Request) (string, error) { return roomKey, nil },
		Presence:  true,
	}))
	server := httptest.NewServer(mux)
	defer server.Close()

	dead := dialWS(t, server, "/ws")
	defer func() { _ = dead.CloseNow() }()
	_ = readWSFrame(t, dead, 5*time.Second) // snapshot — the ONLY read this connection ever does.

	observer := dialWS(t, server, "/ws")
	defer func() { _ = observer.CloseNow() }()
	_ = readWSFrame(t, observer, 5*time.Second) // snapshot

	awaitPresenceFrame(t, observer, 2) // both connections joined
	awaitPresenceFrame(t, observer, 1) // dead's keepalive ping timed out; server tore it down
}

func TestServeWS_AuthorizeErrorRejectsBeforeUpgrade(t *testing.T) {
	_, sqlDB := testdb.URL(t)
	hub := rooms.NewHub("", sqlDB, nil) // Authorize fails before the hub is ever touched.

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"unauthorized", rooms.ErrUnauthorized, http.StatusUnauthorized},
		{"forbidden", rooms.ErrForbidden, http.StatusForbidden},
		{"not_found", rooms.ErrNotFound, http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/ws", hub.ServeWS(rooms.WSOptions{
				Authorize: func(r *http.Request) (string, error) { return "", tc.err },
			}))
			server := httptest.NewServer(mux)
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, resp, err := websocket.Dial(ctx, server.URL+"/ws", nil)
			if err == nil {
				_ = conn.CloseNow()
				t.Fatal("expected the dial (WS upgrade) to fail, it succeeded")
			}
			if resp == nil {
				t.Fatal("expected an HTTP response even though the dial failed")
			}
			if resp.StatusCode != tc.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if resp.StatusCode < 400 || resp.StatusCode >= 500 {
				t.Errorf("status = %d, want a 4xx", resp.StatusCode)
			}
		})
	}
}

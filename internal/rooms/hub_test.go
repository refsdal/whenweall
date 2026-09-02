package rooms_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

// startHub builds a Hub against url/sqlDB, runs it in the background for the life of the test,
// and blocks until its LISTEN session is confirmed active before returning — see awaitListening.
func startHub(t *testing.T, url string, sqlDB *sql.DB) *rooms.Hub {
	t.Helper()
	hub := rooms.NewHub(url, sqlDB, nil)
	runHub(t, hub)
	awaitListening(t, hub, sqlDB)
	return hub
}

// runHub starts hub.Run in the background for the life of the test, and blocks in cleanup until
// it has actually exited (not merely been asked to, via cancel) before returning. This matters
// because testdb.URL's own cleanup — registered earlier, so it runs after this one (t.Cleanup is
// LIFO) — closes the *sql.DB Run's goroutine still uses for its fetch queries; without this wait
// a notify still being processed when the test ends can race that Close and log a harmless but
// noisy "sql: database is closed".
func runHub(t *testing.T, hub *rooms.Hub) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		if err := hub.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Logf("hub.Run: %v", err)
		}
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("hub.Run did not exit within 5s of cancel")
		}
	})
}

// awaitListening blocks until hub's dedicated LISTEN connection is confirmed active, so a test's
// subsequent Emit calls can't race the initial pgx.Connect + LISTEN setup: a NOTIFY sent before
// LISTEN is established is gone forever (Postgres does not queue notifications for an absent
// listener). It probes with real Emits on a private room rather than a fixed sleep, so it takes
// exactly as long as the environment needs and isn't flaky under load.
func awaitListening(t *testing.T, hub *rooms.Hub, sqlDB *sql.DB) {
	t.Helper()
	ctx := context.Background()
	probeRoom := "probe:" + db.NewID()
	frames, unsubscribe := hub.Subscribe(probeRoom)
	defer unsubscribe()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := rooms.Emit(ctx, tx, probeRoom, "probe", nil); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		select {
		case <-frames:
			return
		case <-time.After(150 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for hub to start listening")
}

// mustReceiveFrame reads and json-decodes the next frame, failing the test if none arrives
// within timeout or the channel is closed first.
func mustReceiveFrame(t *testing.T, frames <-chan []byte, timeout time.Duration) map[string]any {
	t.Helper()
	select {
	case frame, ok := <-frames:
		if !ok {
			t.Fatal("subscriber channel closed unexpectedly")
		}
		var got map[string]any
		if err := json.Unmarshal(frame, &got); err != nil {
			t.Fatalf("unmarshal frame %s: %v", frame, err)
		}
		return got
	case <-time.After(timeout):
		t.Fatal("timed out waiting for frame")
		return nil
	}
}

func emitCommitted(t *testing.T, sqlDB *sql.DB, roomKey, eventType string, data any) {
	t.Helper()
	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.Emit(ctx, tx, roomKey, eventType, data); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestEmitReachesSubscriber(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)

	frames, unsubscribe := hub.Subscribe("poll:p1")
	defer unsubscribe()

	emitCommitted(t, sqlDB, "poll:p1", "poll.changed", map[string]any{"pollId": "p1"})

	got := mustReceiveFrame(t, frames, 5*time.Second)
	if got["type"] != "poll.changed" {
		t.Errorf("frame type = %v, want poll.changed", got["type"])
	}
	if _, ok := got["seq"]; !ok {
		t.Errorf("frame missing seq: %v", got)
	}
	if got["pollId"] != "p1" {
		t.Errorf("frame pollId = %v, want p1", got["pollId"])
	}
}

func TestEmitOtherRoomNotDelivered(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)

	frames, unsubscribe := hub.Subscribe("poll:p1")
	defer unsubscribe()

	emitCommitted(t, sqlDB, "poll:other", "poll.changed", map[string]any{"pollId": "other"})

	select {
	case frame := <-frames:
		t.Fatalf("unexpected frame delivered for unsubscribed room: %s", frame)
	case <-time.After(500 * time.Millisecond):
		// Expected: nothing arrives for a room this subscriber never joined.
	}
}

func TestTwoHubsBothDeliver(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub1 := startHub(t, url, sqlDB)
	hub2 := startHub(t, url, sqlDB) // a second Hub against the same DB simulates a second replica.

	frames1, unsub1 := hub1.Subscribe("poll:p1")
	defer unsub1()
	frames2, unsub2 := hub2.Subscribe("poll:p1")
	defer unsub2()

	emitCommitted(t, sqlDB, "poll:p1", "poll.changed", map[string]any{"pollId": "p1"})

	got1 := mustReceiveFrame(t, frames1, 5*time.Second)
	got2 := mustReceiveFrame(t, frames2, 5*time.Second)
	if got1["type"] != "poll.changed" || got2["type"] != "poll.changed" {
		t.Errorf("frames = %v, %v, want both poll.changed", got1, got2)
	}
	if got1["seq"] != got2["seq"] {
		t.Errorf("seq mismatch across replicas: %v vs %v", got1["seq"], got2["seq"])
	}
}

func TestCatchUpReplaysMissedEvents(t *testing.T) {
	_, sqlDB := testdb.URL(t)
	hub := rooms.NewHub("", sqlDB, nil) // EventsSince only ever touches sqlDB; Run need not be started.
	ctx := context.Background()
	const roomKey = "poll:catchup"

	emitCommitted(t, sqlDB, roomKey, "first", nil)
	emitCommitted(t, sqlDB, roomKey, "second", nil)
	emitCommitted(t, sqlDB, roomKey, "third", nil)

	all, err := hub.EventsSince(ctx, roomKey, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("EventsSince(0) returned %d events, want 3", len(all))
	}

	replayed, err := hub.EventsSince(ctx, roomKey, all[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 {
		t.Fatalf("EventsSince(after first) returned %d events, want 2", len(replayed))
	}
	if replayed[0].Type != "second" || replayed[1].Type != "third" {
		t.Errorf("replayed types = [%s, %s], want [second, third]", replayed[0].Type, replayed[1].Type)
	}
	if replayed[0].ID >= replayed[1].ID {
		t.Errorf("replayed ids not increasing: %d, %d", replayed[0].ID, replayed[1].ID)
	}
}

func TestSlowSubscriberIsDropped(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:slow"

	slowFrames, slowUnsub := hub.Subscribe(roomKey)
	defer slowUnsub() // idempotent even if dispatchLocal already dropped (closed) it.
	fastFrames, fastUnsub := hub.Subscribe(roomKey)
	defer fastUnsub()

	fastCount := make(chan int, 1)
	go func() {
		count := 0
		deadline := time.After(15 * time.Second)
		for count < 100 {
			select {
			case _, ok := <-fastFrames:
				if !ok {
					fastCount <- count
					return
				}
				count++
			case <-deadline:
				fastCount <- count
				return
			}
		}
		fastCount <- count
	}()

	// The slow subscriber never reads. Its channel (cap 64) must fill and get dropped well
	// before all 100 events are emitted.
	for i := 0; i < 100; i++ {
		emitCommitted(t, sqlDB, roomKey, "poll.changed", map[string]any{"n": i})
	}

	// Confirm the slow subscriber was dropped: drain whatever's buffered until the channel
	// reports closed. This only reads AFTER emission is done, so it never "helps" the slow
	// subscriber keep up during the run.
	closed := false
	deadline := time.After(10 * time.Second)
	for !closed {
		select {
		case _, ok := <-slowFrames:
			if !ok {
				closed = true
			}
		case <-deadline:
			t.Fatal("slow subscriber's channel was never closed (never dropped)")
		}
	}

	if got := <-fastCount; got != 100 {
		t.Errorf("fast subscriber received %d frames, want 100", got)
	}
}

// TestLateCommitterIsNotPermanentlyMissed is the mandatory hazard test: it forces the exact
// interleaving handleNotify's doc comment describes — a lower id (A) committing strictly after a
// higher id (B) — and asserts A still reaches the subscriber once it finally commits.
func TestLateCommitterIsNotPermanentlyMissed(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:hazard"

	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	ctx := context.Background()

	// tx1 allocates the lower id (event "A") but does not commit yet.
	tx1, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.Emit(ctx, tx1, roomKey, "A", map[string]any{"which": "A"}); err != nil {
		t.Fatal(err)
	}

	// tx2 allocates the higher id (event "B") and commits first: its NOTIFY fires while A is
	// still open, which is exactly the interleaving that would fool a naive
	// "id > highest id ever notified" cursor into skipping A forever once it finally commits.
	tx2, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.Emit(ctx, tx2, roomKey, "B", map[string]any{"which": "B"}); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}

	// B, the only committed row so far, must be delivered promptly.
	frameB := mustReceiveFrame(t, frames, 5*time.Second)
	if frameB["type"] != "B" {
		t.Fatalf("first delivered frame = %v, want type B", frameB)
	}

	// Now the late committer completes. Its own NOTIFY — carrying its own, lower id — is the
	// only notification the hub will ever get for this row.
	if err := tx1.Commit(); err != nil {
		t.Fatal(err)
	}

	frameA := mustReceiveFrame(t, frames, 5*time.Second)
	if frameA["type"] != "A" {
		t.Fatalf("second delivered frame = %v, want type A (the late committer), got %v instead", frameA, frameA["type"])
	}
	// A's seq must be lower than B's: this is what makes it a "late committer" rather than just
	// the next event in line.
	seqA, okA := frameA["seq"].(float64)
	seqB, okB := frameB["seq"].(float64)
	if !okA || !okB || seqA >= seqB {
		t.Errorf("expected seq(A)=%v < seq(B)=%v", frameA["seq"], frameB["seq"])
	}
}

func TestFrameUnwrapsEntityFields(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:frame"

	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	emitCommitted(t, sqlDB, roomKey, "poll.changed", map[string]any{"pollId": "p1", "status": "closed"})

	got := mustReceiveFrame(t, frames, 5*time.Second)
	if got["type"] != "poll.changed" {
		t.Errorf("type = %v, want poll.changed", got["type"])
	}
	if _, ok := got["seq"]; !ok {
		t.Errorf("frame missing seq: %v", got)
	}
	if got["pollId"] != "p1" {
		t.Errorf("pollId = %v, want p1", got["pollId"])
	}
	if got["status"] != "closed" {
		t.Errorf("status = %v, want closed", got["status"])
	}
	if _, ok := got["data"]; ok {
		t.Errorf("frame should not carry a nested data envelope, got %v", got)
	}
}

func TestFrameNullDataYieldsTypeAndSeqOnly(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "page:frame"

	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	emitCommitted(t, sqlDB, roomKey, "page.changed", nil)

	got := mustReceiveFrame(t, frames, 5*time.Second)
	if got["type"] != "page.changed" {
		t.Errorf("type = %v, want page.changed", got["type"])
	}
	if _, ok := got["seq"]; !ok {
		t.Errorf("frame missing seq: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("frame = %v, want exactly {type, seq}", got)
	}
}

func TestReconnectSendsResyncAndResumesDelivery(t *testing.T) {
	url, sqlDB := testdb.URL(t)

	appName := "rooms_hub_test_" + db.NewID()
	listenURL := url
	if strings.Contains(listenURL, "?") {
		listenURL += "&application_name=" + appName
	} else {
		listenURL += "?application_name=" + appName
	}

	hub := rooms.NewHub(listenURL, sqlDB, nil)
	runHub(t, hub)
	awaitListening(t, hub, sqlDB)

	const roomKey = "poll:reconnect"
	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	// Force the hub's dedicated LISTEN connection to drop, simulating a lost connection.
	var pid int
	if err := sqlDB.QueryRowContext(context.Background(),
		`SELECT pid FROM pg_stat_activity WHERE application_name = $1`, appName,
	).Scan(&pid); err != nil {
		t.Fatalf("finding listener backend pid: %v", err)
	}
	if _, err := sqlDB.ExecContext(context.Background(), `SELECT pg_terminate_backend($1)`, pid); err != nil {
		t.Fatalf("terminating listener backend: %v", err)
	}

	resync := mustReceiveFrame(t, frames, 10*time.Second)
	if resync["type"] != "resync" {
		t.Fatalf("frame after reconnect = %v, want resync", resync)
	}

	// Delivery must keep working once the reconnect completes.
	emitCommitted(t, sqlDB, roomKey, "poll.changed", nil)
	after := mustReceiveFrame(t, frames, 5*time.Second)
	if after["type"] != "poll.changed" {
		t.Fatalf("frame after reconnect delivery = %v, want poll.changed", after)
	}
}

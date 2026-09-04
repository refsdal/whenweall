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

// TestDroppedRoomIsFullyPruned is I3's regression test for the map-leak fix's drop-path half:
// when dispatchLocal drops a room's ONLY subscriber, every trace of that room — its subs entry,
// its watermark, and any pendingNotify bookkeeping — must be gone, not just the subs entry the
// pre-fix code already handled (see pruneRoomLocked's own doc comment on Hub).
//
// This drives dispatchLocal directly (Hub.DispatchLocal, export_test.go) rather than through real
// Postgres NOTIFYs: no LISTEN session is running at all here, which is what lets this assert the
// room stays pruned rather than raced by handleNotify's "no local subscribers" branch legitimately
// re-populating watermark for whatever NOTIFY happens to land next — a real, correct behavior this
// test must not be confused with the leak it targets.
func TestDroppedRoomIsFullyPruned(t *testing.T) {
	_, sqlDB := testdb.URL(t)
	hub := rooms.NewHub("", sqlDB, nil)
	const roomKey = "poll:dropped-room"

	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe() // idempotent even if dispatchLocal already dropped (closed) it.
	hub.SeedPendingNotify(roomKey, 999999)

	if !hub.RoomTracked(roomKey) {
		t.Fatal("room not tracked immediately after Subscribe + SeedPendingNotify")
	}

	// Never read frames: fill its buffer (cap 64) and push it over, so dispatchLocal drops it as a
	// slow consumer — the path pruneRoomLocked must be called from, not just the graceful
	// unsubscribe closure.
	for i := 0; i < 100; i++ {
		hub.DispatchLocal(roomKey, []byte(`{"type":"poll.changed"}`))
	}

	if hub.RoomTracked(roomKey) {
		t.Fatal("room still tracked (subs/watermark/pendingNotify) after its only subscriber was dropped")
	}
	if n := hub.PendingNotifyLen(roomKey); n != 0 {
		t.Errorf("pendingNotify still holds %d entries for a fully pruned room", n)
	}

	// Confirm the subscriber really was dropped (not merely left registered) — same confirmation
	// TestSlowSubscriberIsDropped already makes.
	closed := false
	waitClosed := time.After(5 * time.Second)
	for !closed {
		select {
		case _, ok := <-frames:
			if !ok {
				closed = true
			}
		case <-waitClosed:
			t.Fatal("subscriber's channel was never closed (never dropped)")
		}
	}
}

// TestReconnectClearsPendingNotify is I3's regression test for the map-leak fix's reconnect-path
// half: a pendingNotify entry recorded before a lost LISTEN session is unconsumable (its own
// NOTIFY is gone for good — Postgres does not queue notifications for an absent listener, see
// Run's own doc comment) and must not survive the reconnect. Seeds the entry directly
// (Hub.SeedPendingNotify) rather than via the natural two-Emits-in-one-tx path: that path's
// routine duplicate NOTIFY is typically consumed within milliseconds, racing (and likely losing
// to) a deliberately forced disconnect — seeding removes that race entirely.
func TestReconnectClearsPendingNotify(t *testing.T) {
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

	const roomKey = "poll:pendingnotify-reconnect"
	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	hub.SeedPendingNotify(roomKey, 999999)
	if got := hub.PendingNotifyLen(roomKey); got != 1 {
		t.Fatalf("PendingNotifyLen after seeding = %d, want 1", got)
	}

	// Force the hub's dedicated LISTEN connection to drop, simulating a lost connection — same
	// machinery as TestReconnectSendsResyncAndResumesDelivery.
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

	if got := hub.PendingNotifyLen(roomKey); got != 0 {
		t.Errorf("PendingNotifyLen after reconnect = %d, want 0 (Run must clear pendingNotify wholesale on every reconnect)", got)
	}
}

// TestColdStartDoesNotReplayHistory is the regression test for the cold-start critical: a room
// with a long pre-existing history must not get swept and replayed at a subscriber the moment it
// joins (which would very likely blow through the subscriber's bounded channel — cap 64 — and get
// it dropped before a single live event ever arrived). Subscribe's initWatermarkFloor is what
// prevents this; see its doc comment and handleNotify's "unseen room" branch, its reactive
// fallback.
func TestColdStartDoesNotReplayHistory(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	const roomKey = "poll:coldstart"

	// A substantial history, written before any hub or subscriber for this room exists.
	for i := 0; i < 100; i++ {
		emitCommitted(t, sqlDB, roomKey, "stale", map[string]any{"n": i})
	}

	hub := startHub(t, url, sqlDB) // fresh hub: its watermark map starts out empty.
	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	emitCommitted(t, sqlDB, roomKey, "fresh", nil)

	got := mustReceiveFrame(t, frames, 5*time.Second)
	if got["type"] != "fresh" {
		t.Fatalf("first frame delivered = %v, want type fresh (no history replay)", got)
	}

	// Nothing else should trickle in afterward — exactly the new event, not history-then-new.
	select {
	case frame := <-frames:
		t.Fatalf("unexpected extra frame after cold start (history leaked through): %s", frame)
	case <-time.After(500 * time.Millisecond):
	}
}

// TestTwoEmitsInOneTxDeliverExactlyTwoFrames covers the routine (not the mandatory hazard test's
// deliberately-held-open-transaction) duplicate-delivery case: two Emits inside ONE transaction
// produce two rows and two NOTIFYs from a single commit. Processing the first NOTIFY sweeps both
// rows (both are already committed and visible by the time either NOTIFY is delivered); the
// second NOTIFY, for a row already delivered, must be recognized and suppressed rather than
// redelivering it — see deliverSince's pendingNotify bookkeeping.
func TestTwoEmitsInOneTxDeliverExactlyTwoFrames(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	const roomKey = "poll:duptx"

	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rooms.Emit(ctx, tx, roomKey, "first", nil); err != nil {
		t.Fatal(err)
	}
	if err := rooms.Emit(ctx, tx, roomKey, "second", nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool)
	for i := 0; i < 2; i++ {
		got := mustReceiveFrame(t, frames, 5*time.Second)
		if s, ok := got["type"].(string); ok {
			seen[s] = true
		}
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("expected both events delivered exactly once, got %v", seen)
	}

	// A third frame arriving here would be the routine duplicate this test guards against.
	select {
	case frame := <-frames:
		t.Fatalf("unexpected extra frame (a duplicate delivery): %s", frame)
	case <-time.After(1 * time.Second):
	}
}

// TestEventFrame_WithData and TestEventFrame_NullData exercise Event.Frame directly — the entry
// point Task 2's EventsSince-backed backfill uses — through both envelope shapes buildFrame's
// integration-level siblings (TestFrameUnwrapsEntityFields, TestFrameNullDataYieldsTypeAndSeqOnly)
// already cover via the live path. Both routes share the exact same unwrap implementation
// (buildFrameFromParts), so a backfilled frame and a live one are byte-for-byte identical in
// shape for the same row.
func TestEventFrame_WithData(t *testing.T) {
	ev := rooms.Event{
		ID:      42,
		RoomKey: "poll:p1",
		Type:    "poll.changed",
		Data:    json.RawMessage(`{"pollId":"p1","status":"closed"}`),
	}
	frame, err := ev.Frame()
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(frame, &got); err != nil {
		t.Fatalf("unmarshal frame %s: %v", frame, err)
	}
	if got["type"] != "poll.changed" {
		t.Errorf("type = %v, want poll.changed", got["type"])
	}
	if seq, ok := got["seq"].(float64); !ok || int64(seq) != 42 {
		t.Errorf("seq = %v, want 42", got["seq"])
	}
	if got["pollId"] != "p1" || got["status"] != "closed" {
		t.Errorf("frame = %v, want pollId=p1 status=closed", got)
	}
	if _, ok := got["data"]; ok {
		t.Errorf("frame should not carry a nested data envelope, got %v", got)
	}
}

func TestEventFrame_NullData(t *testing.T) {
	ev := rooms.Event{ID: 7, RoomKey: "page:p1", Type: "page.changed", Data: nil}
	frame, err := ev.Frame()
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(frame, &got); err != nil {
		t.Fatalf("unmarshal frame %s: %v", frame, err)
	}
	if got["type"] != "page.changed" {
		t.Errorf("type = %v, want page.changed", got["type"])
	}
	if seq, ok := got["seq"].(float64); !ok || int64(seq) != 7 {
		t.Errorf("seq = %v, want 7", got["seq"])
	}
	if len(got) != 2 {
		t.Errorf("frame = %v, want exactly {type, seq}", got)
	}
}

// TestListenerIdleTimeoutPingsWithoutReconnecting pins the liveness check's SHAPE: an idle LISTEN
// session is interrupted every ListenIdleTimeout and PINGED — it is NOT torn down and redialed.
// Observable from outside as (a) Hub.LoadListenPingCount actually increasing (fix round 1: the
// original version of this test asserted only the ABSENCE of a reconnect, which a silent revert
// to the unbounded `conn.WaitForNotification(ctx)` — no ping at all — would have passed just as
// well; see this function's own doc history), (b) the backend pid behind the listener staying the
// same across several idle windows, (c) no resync frame reaching a subscriber (a reconnect would
// send one — see Run), and (d) delivery still working afterwards. A genuinely half-open TCP
// session cannot be simulated against a local container, so the ping's failure branch is covered
// by the ordinary connection-loss tests (TestReconnectSendsResyncAndResumesDelivery): a failed
// ping returns an error from listenLoop and takes exactly that path.
func TestListenerIdleTimeoutPingsWithoutReconnecting(t *testing.T) {
	url, sqlDB := testdb.URL(t)

	appName := "rooms_hub_test_" + db.NewID()
	listenURL := url
	if strings.Contains(listenURL, "?") {
		listenURL += "&application_name=" + appName
	} else {
		listenURL += "?application_name=" + appName
	}

	hub := rooms.NewHub(listenURL, sqlDB, nil)
	hub.ListenIdleTimeout = 100 * time.Millisecond
	hub.ListenPingTimeout = 2 * time.Second
	runHub(t, hub)
	awaitListening(t, hub, sqlDB)

	pidOf := func() int {
		t.Helper()
		var pid int
		if err := sqlDB.QueryRowContext(context.Background(),
			`SELECT pid FROM pg_stat_activity WHERE application_name = $1`, appName,
		).Scan(&pid); err != nil {
			t.Fatalf("finding listener backend pid: %v", err)
		}
		return pid
	}
	before := pidOf()
	pingsBefore := hub.LoadListenPingCount()

	const roomKey = "poll:idle-ping"
	frames, unsubscribe := hub.Subscribe(roomKey)
	defer unsubscribe()

	// Seven idle windows with nothing to deliver: each must end in a ping, never a reconnect.
	select {
	case frame := <-frames:
		t.Fatalf("unexpected frame during an idle stretch (a resync means the listener reconnected instead of pinging): %s", frame)
	case <-time.After(700 * time.Millisecond):
	}
	if after := pidOf(); after != before {
		t.Fatalf("listener backend pid changed %d -> %d: the idle timeout reconnected instead of pinging", before, after)
	}
	// The actual proof of a ping, not just the absence of a reconnect: with 100ms idle windows
	// over a 700ms wait, at least a few pings must have gone out.
	if pingsAfter := hub.LoadListenPingCount(); pingsAfter <= pingsBefore {
		t.Fatalf("listen ping count = %d, want > %d: the idle timeout elapsed without ever pinging the connection", pingsAfter, pingsBefore)
	}

	emitCommitted(t, sqlDB, roomKey, "poll.changed", nil)
	got := mustReceiveFrame(t, frames, 5*time.Second)
	if got["type"] != "poll.changed" {
		t.Fatalf("frame after idle pings = %v, want poll.changed", got)
	}
}

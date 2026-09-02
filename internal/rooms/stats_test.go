package rooms_test

// Tests internal/rooms/stats.go: Increment's atomic counter bump + Snapshot's read-back, plus the
// broadcast throttle's own contract — at most a handful of live "stats" frames for a burst of
// rapid increments, with the LAST one always carrying the final, fully-caught-up count (see
// stats.go's maybeBroadcast doc comment for why this differs from StatsRoom.ts's own pure
// leading-edge throttle). Timing-sensitive: run with -count=5 to catch flakiness.

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

// incrementCommitted runs one Increment inside its own committed transaction — the shape every
// real call site (internal/polls's Create/Finalize/AddParticipant/...) uses.
func incrementCommitted(t *testing.T, sqlDB *sql.DB, stats *rooms.StatsService, field string) {
	t.Helper()
	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := stats.Increment(ctx, tx, field); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestStatsFieldForAnswer(t *testing.T) {
	cases := map[string]string{
		"yes":      rooms.StatsResponsesYes,
		"ifneedbe": rooms.StatsResponsesIfNeedBe,
		"no":       rooms.StatsResponsesNo,
	}
	for answer, want := range cases {
		got, ok := rooms.StatsFieldForAnswer(answer)
		if !ok || got != want {
			t.Errorf("StatsFieldForAnswer(%q) = (%q, %v), want (%q, true)", answer, got, ok, want)
		}
	}
	if _, ok := rooms.StatsFieldForAnswer("bogus"); ok {
		t.Error("StatsFieldForAnswer(\"bogus\") ok = true, want false")
	}
}

func TestStatsIncrement_RejectsUnknownField(t *testing.T) {
	_, sqlDB := testdb.URL(t)
	stats := rooms.NewStatsService(sqlDB, nil)

	ctx := context.Background()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := stats.Increment(ctx, tx, "not-a-real-field"); err == nil {
		t.Error("Increment with an unknown field = nil error, want one")
	}
}

func TestStatsSnapshot_ReflectsCounters(t *testing.T) {
	_, sqlDB := testdb.URL(t)
	stats := rooms.NewStatsService(sqlDB, nil)
	ctx := context.Background()

	got, err := stats.Snapshot(ctx, rooms.RoomKeyStats)
	if err != nil {
		t.Fatal(err)
	}
	if zero, ok := got.(rooms.UsageStats); !ok || zero != (rooms.UsageStats{}) {
		t.Errorf("Snapshot before any Increment = %#v, want a zero UsageStats", got)
	}

	incrementCommitted(t, sqlDB, stats, rooms.StatsResponsesYes)
	incrementCommitted(t, sqlDB, stats, rooms.StatsResponsesYes)
	incrementCommitted(t, sqlDB, stats, rooms.StatsResponsesNo)
	incrementCommitted(t, sqlDB, stats, rooms.StatsPollsCreated)

	got, err = stats.Snapshot(ctx, rooms.RoomKeyStats)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got.(rooms.UsageStats)
	if !ok {
		t.Fatalf("Snapshot = %#v (%T), want a UsageStats", got, got)
	}
	if s.ResponsesYes != 2 {
		t.Errorf("ResponsesYes = %d, want 2", s.ResponsesYes)
	}
	if s.ResponsesNo != 1 {
		t.Errorf("ResponsesNo = %d, want 1", s.ResponsesNo)
	}
	if s.PollsCreated != 1 {
		t.Errorf("PollsCreated = %d, want 1", s.PollsCreated)
	}
	if s.PollsFinalized != 0 || s.ResponsesIfNeedBe != 0 {
		t.Errorf("untouched fields should stay zero, got %#v", s)
	}
}

// TestStatsIncrement_ThrottledBroadcastCarriesFinalCount is Task 3's own required proof: a burst
// of rapid Increment calls must not flood every live subscriber with one frame per call, and the
// LAST frame it does send must reflect the burst's true final total, not whatever count happened
// to be current when that frame's own underlying Increment call ran. See stats.go's
// maybeBroadcast doc comment for the leading+trailing throttle design this asserts.
func TestStatsIncrement_ThrottledBroadcastCarriesFinalCount(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	stats := rooms.NewStatsService(sqlDB, nil)

	frames, unsubscribe := hub.Subscribe(rooms.RoomKeyStats)
	defer unsubscribe()

	const burst = 10
	for i := 0; i < burst; i++ {
		incrementCommitted(t, sqlDB, stats, rooms.StatsPollsCreated)
	}

	var received []map[string]any
	deadline := time.After(3 * time.Second)
collect:
	for {
		select {
		case frame, ok := <-frames:
			if !ok {
				break collect
			}
			var decoded map[string]any
			if err := json.Unmarshal(frame, &decoded); err != nil {
				t.Fatalf("unmarshal frame %s: %v", frame, err)
			}
			if decoded["type"] == "stats" {
				received = append(received, decoded)
			}
		case <-deadline:
			break collect
		}
	}

	if len(received) == 0 {
		t.Fatal("received no stats frames at all, want at least one (the leading-edge broadcast)")
	}
	if len(received) > 4 {
		t.Errorf("received %d stats frames for one %d-call burst, want a small number (throttled to <=1 per 2s)", len(received), burst)
	}

	last := received[len(received)-1]
	count, ok := last["pollsCreated"].(float64)
	if !ok || int(count) != burst {
		t.Errorf("last stats frame pollsCreated = %v, want %d (the burst's final total)", last["pollsCreated"], burst)
	}
}

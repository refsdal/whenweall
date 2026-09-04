package rooms_test

// Tests internal/rooms/stats.go: Record's atomic multi-field counter bump + Snapshot's read-back,
// plus the broadcast throttle's own contract — at most a handful of live "stats" frames for a
// burst of rapid Records, with the LAST one always carrying the final, fully-caught-up count (see
// stats.go's maybeBroadcast doc comment for why this differs from StatsRoom.ts's own pure
// leading-edge throttle). Timing-sensitive: run with -count=5 to catch flakiness.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

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

// TestStatsRecord_UnknownFieldIsSkippedNotFatal is Record's counterpart to the old
// Increment-rejects-unknown-field test: Record is best-effort (it logs, it never returns an
// error to a caller — see its own doc comment), so an unrecognized field alongside a recognized
// one must not stop the recognized one from landing.
func TestStatsRecord_UnknownFieldIsSkippedNotFatal(t *testing.T) {
	_, sqlDB := testdb.URL(t)
	stats := rooms.NewStatsService(sqlDB, nil)
	ctx := context.Background()

	stats.Record(ctx, map[string]int64{
		"not-a-real-field":      1,
		rooms.StatsPollsCreated: 1,
	})

	got, err := stats.Snapshot(ctx, rooms.RoomKeyStats)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got.(rooms.UsageStats)
	if !ok || s.PollsCreated != 1 {
		t.Errorf("Snapshot = %#v, want PollsCreated = 1 (the unknown field must not block the known one)", got)
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
		t.Errorf("Snapshot before any Record = %#v, want a zero UsageStats", got)
	}

	stats.Record(ctx, map[string]int64{rooms.StatsResponsesYes: 2})
	stats.Record(ctx, map[string]int64{rooms.StatsResponsesNo: 1})
	// A single Record call applies several fields at once (the multi-answer submission shape
	// internal/polls's AddParticipant/UpdateParticipant now use — tallyAnswerStats, called once
	// per write) — exercised here alongside the single-field calls above.
	stats.Record(ctx, map[string]int64{rooms.StatsPollsCreated: 1, rooms.StatsPollsFinalized: 0})

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

// TestStatsRecord_ThrottledBroadcastCarriesFinalCount is Task 3's own required proof (retargeted
// from Increment to Record for I2): a burst of rapid Record calls must not flood every live
// subscriber with one frame per call, and the LAST frame it does send must reflect the burst's
// true final total, not whatever count happened to be current when that frame's own underlying
// Record call ran. See stats.go's maybeBroadcast doc comment for the leading+trailing throttle
// design this asserts.
func TestStatsRecord_ThrottledBroadcastCarriesFinalCount(t *testing.T) {
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)
	stats := rooms.NewStatsService(sqlDB, nil)
	ctx := context.Background()

	frames, unsubscribe := hub.Subscribe(rooms.RoomKeyStats)
	defer unsubscribe()

	const burst = 10
	for i := 0; i < burst; i++ {
		stats.Record(ctx, map[string]int64{rooms.StatsPollsCreated: 1})
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

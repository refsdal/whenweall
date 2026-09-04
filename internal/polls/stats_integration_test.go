package polls_test

// Integration coverage for I2 (plan 6 final review): internal/rooms.StatsService.Record is called
// AFTER the domain write's own transaction has committed, and its own failures never propagate to
// the caller. TestStatsSnapshot_ReflectsCounters (internal/rooms/stats_test.go) already covers
// Record's own atomic multi-field upsert directly; these two tests cover the polls-package call
// sites that wire it in.

import (
	"context"
	"testing"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestAddParticipant_MultiAnswerRecordsAllFieldsPostCommit is I2's regression test for the
// batched-record shape: a single AddParticipant call answering two different options with two
// different answers ("yes" and "no") must land BOTH counters via Record's one atomic upsert, and
// only once the participant's own write has actually committed — before the call, "stats:global"
// has no row (or a zero one) for either field; only after AddParticipant returns does it reflect
// both deltas together.
func TestAddParticipant_MultiAnswerRecordsAllFieldsPostCommit(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	stats := rooms.NewStatsService(d, nil)
	s.SetStats(stats)

	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	opt1, opt2 := created.Options[0], created.Options[1]

	before, err := stats.Snapshot(ctx, rooms.RoomKeyStats)
	if err != nil {
		t.Fatalf("Snapshot before: %v", err)
	}
	beforeStats := before.(rooms.UsageStats)
	if beforeStats.ResponsesYes != 0 || beforeStats.ResponsesNo != 0 {
		t.Fatalf("stats before AddParticipant = %#v, want all zero", beforeStats)
	}

	if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
		Name:    "Alice",
		Answers: map[string]string{opt1.ID: "yes", opt2.ID: "no"},
	}, polls.Viewer{}); err != nil {
		t.Fatalf("AddParticipant: %v", err)
	}

	after, err := stats.Snapshot(ctx, rooms.RoomKeyStats)
	if err != nil {
		t.Fatalf("Snapshot after: %v", err)
	}
	afterStats := after.(rooms.UsageStats)
	if afterStats.ResponsesYes != 1 {
		t.Errorf("ResponsesYes = %d, want 1", afterStats.ResponsesYes)
	}
	if afterStats.ResponsesNo != 1 {
		t.Errorf("ResponsesNo = %d, want 1", afterStats.ResponsesNo)
	}
}

// TestAddParticipant_StatsRecordFailureDoesNotFailTheWrite is I2's regression test for Record's
// best-effort contract: a StatsService whose own sqlDB is unusable must never fail the domain
// write it's counting. stats is deliberately pointed at an ALREADY-CLOSED pool (a second, separate
// connection to the same database, opened and immediately closed) rather than s's own pool — every
// query against it fails instantly and deterministically ("sql: database is closed"), with no
// network flakiness and no risk of also breaking AddParticipant's own, still-open pool.
func TestAddParticipant_StatsRecordFailureDoesNotFailTheWrite(t *testing.T) {
	ctx := context.Background()
	url, d := testdb.URL(t)
	s := polls.NewService(d)

	brokenDB, err := db.Open(ctx, url, 1)
	if err != nil {
		t.Fatalf("opening the pool to break: %v", err)
	}
	if err := brokenDB.Close(); err != nil {
		t.Fatalf("closing the pool: %v", err)
	}
	s.SetStats(rooms.NewStatsService(brokenDB, nil))

	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	opt1 := created.Options[0]

	result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
		Name:    "Bob",
		Answers: map[string]string{opt1.ID: "yes"},
	}, polls.Viewer{})
	if err != nil {
		t.Fatalf("AddParticipant should succeed despite a broken stats sink, got error: %v", err)
	}
	if result.ParticipantID == "" {
		t.Error("AddParticipant returned an empty ParticipantID")
	}
}

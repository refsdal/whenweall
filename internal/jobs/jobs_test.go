package jobs_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestScheduleAndClaim(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind:    "mail:send",
		RunAt:   time.Now().Add(-time.Second),
		Payload: map[string]string{"to": "a@b.c"},
	})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "replica-1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	if claimed[0].Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", claimed[0].Attempts)
	}
	if !strings.Contains(string(claimed[0].Payload), "a@b.c") {
		t.Errorf("Payload = %s, want to contain a@b.c", claimed[0].Payload)
	}
}

func TestClaimSkipsFutureJobs(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind:  "poll:digest",
		RunAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "replica-1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("len(claimed) = %d, want 0 (job is not due yet)", len(claimed))
	}
}

// TestConcurrentClaimersGetDisjointCompleteSets contends ClaimDue's FOR UPDATE SKIP LOCKED for
// real: eight goroutines claim in batches from the same due set until it is empty. Every job must
// be claimed exactly once, by exactly one worker. (Its sequential predecessor called ClaimDue
// twice in a row, which a bare `UPDATE ... WHERE locked_by IS NULL` would also have passed.)
func TestConcurrentClaimersGetDisjointCompleteSets(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	const total = 40
	for i := 0; i < total; i++ {
		if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
			Kind:  "mail:send",
			RunAt: time.Now().Add(-time.Second),
		}); err != nil {
			t.Fatalf("Schedule[%d]: %v", i, err)
		}
	}

	const workers = 8
	var (
		mu        sync.Mutex
		claimedBy = make(map[string]string, total)
	)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(replica string) {
			defer wg.Done()
			<-start
			for {
				batch, err := jobs.ClaimDue(ctx, d, replica, 3)
				if err != nil {
					t.Errorf("%s: ClaimDue: %v", replica, err)
					return
				}
				if len(batch) == 0 {
					return
				}
				mu.Lock()
				for _, j := range batch {
					if prev, dup := claimedBy[j.ID]; dup {
						t.Errorf("job %s claimed by both %s and %s", j.ID, prev, replica)
					}
					claimedBy[j.ID] = replica
				}
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	close(start)
	wg.Wait()

	if len(claimedBy) != total {
		t.Fatalf("claimed %d distinct jobs, want %d (every due job claimed exactly once)", len(claimedBy), total)
	}
}

func TestRoomKeyUpsertKeepsOneJob(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	room := "room-1"

	first := time.Now().Add(-time.Minute)
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "poll:digest", RoomKey: &room, RunAt: first,
	}); err != nil {
		t.Fatalf("Schedule (first): %v", err)
	}

	// Claim it once so attempts advances past zero, to prove the upsert below resets it
	// rather than merely leaving it alone.
	if _, err := jobs.ClaimDue(ctx, d, "w1", 10); err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}

	second := time.Now().Add(time.Hour)
	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "poll:digest", RoomKey: &room, RunAt: second,
	}); err != nil {
		t.Fatalf("Schedule (second): %v", err)
	}

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (upsert, not append)", count)
	}

	var runAt time.Time
	var attempts int
	if err := d.QueryRowContext(ctx,
		"SELECT run_at, attempts FROM scheduled_jobs WHERE kind = $1 AND room_key = $2",
		"poll:digest", room,
	).Scan(&runAt, &attempts); err != nil {
		t.Fatalf("select: %v", err)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0 (upsert must reset)", attempts)
	}
	if diff := runAt.Sub(second); diff < -time.Second || diff > time.Second {
		t.Errorf("run_at = %v, want ~%v (the second schedule's value)", runAt, second)
	}
}

// TestScheduleIfAbsentInsertsOnlyWhenNothingPending is the regression test for IMPORTANT 4
// (EnsureScheduled clobbering run_at on every boot): unlike Schedule, ScheduleIfAbsent must
// insert the first row for a (kind, room_key) but leave a second call — with a different run_at —
// completely alone.
func TestScheduleIfAbsentInsertsOnlyWhenNothingPending(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	room := "room-absent"

	first := time.Now().Add(time.Hour)
	if err := jobs.ScheduleIfAbsent(ctx, d, jobs.ScheduleInput{
		Kind: "t:seed", RoomKey: &room, RunAt: first,
	}); err != nil {
		t.Fatalf("ScheduleIfAbsent (first): %v", err)
	}

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}

	// A second call with a different run_at must not move it: this is the whole point of
	// ScheduleIfAbsent over Schedule's upsert.
	second := time.Now().Add(-time.Second)
	if err := jobs.ScheduleIfAbsent(ctx, d, jobs.ScheduleInput{
		Kind: "t:seed", RoomKey: &room, RunAt: second,
	}); err != nil {
		t.Fatalf("ScheduleIfAbsent (second): %v", err)
	}

	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs").Scan(&count); err != nil {
		t.Fatalf("count after second call: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (still no duplicate)", count)
	}

	var runAt time.Time
	if err := d.QueryRowContext(ctx,
		"SELECT run_at FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "t:seed", room,
	).Scan(&runAt); err != nil {
		t.Fatalf("select run_at: %v", err)
	}
	if diff := runAt.Sub(first); diff < -time.Second || diff > time.Second {
		t.Errorf("run_at = %v, want ~%v (the first call's value, untouched by the second)", runAt, first)
	}
}

// TestScheduleIfAbsentRequiresRoomKey documents that ScheduleIfAbsent only makes sense for the
// singleton (kind, room_key) jobs it exists for: a nil RoomKey has no upsert target to be absent
// or present under.
func TestScheduleIfAbsentRequiresRoomKey(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	err := jobs.ScheduleIfAbsent(ctx, d, jobs.ScheduleInput{
		Kind: "t:no-room", RunAt: time.Now(),
	})
	if err == nil {
		t.Fatal("want an error for a nil RoomKey, got nil")
	}
}

func TestFailBacksOffThenDeadLetters(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "mail:send", RunAt: time.Now().Add(-time.Second), MaxAttempts: 2,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	id := claimed[0].ID
	if claimed[0].Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", claimed[0].Attempts)
	}

	willRetry, err := jobs.Fail(ctx, d, id, "smtp timeout")
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if !willRetry {
		t.Fatal("willRetry = false, want true (attempts 1 < max 2)")
	}

	var runAt time.Time
	if err := d.QueryRowContext(ctx, "SELECT run_at FROM scheduled_jobs WHERE id = $1", id).Scan(&runAt); err != nil {
		t.Fatalf("select run_at: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("run_at = %v, want pushed into the future (backoff)", runAt)
	}

	// Force run_at back into the past so the job is claimable again without waiting out backoff.
	if _, err := d.ExecContext(ctx, "UPDATE scheduled_jobs SET run_at = now() - interval '1 second' WHERE id = $1", id); err != nil {
		t.Fatalf("force run_at: %v", err)
	}

	claimed2, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue (2nd): %v", err)
	}
	if len(claimed2) != 1 {
		t.Fatalf("len(claimed2) = %d, want 1", len(claimed2))
	}
	if claimed2[0].Attempts != 2 {
		t.Fatalf("Attempts = %d, want 2", claimed2[0].Attempts)
	}

	willRetry2, err := jobs.Fail(ctx, d, id, "smtp timeout again")
	if err != nil {
		t.Fatalf("Fail (2nd): %v", err)
	}
	if willRetry2 {
		t.Error("willRetry = true, want false (attempts 2 >= max 2)")
	}

	dead, err := jobs.Dead(ctx, d, 10)
	if err != nil {
		t.Fatalf("Dead: %v", err)
	}
	found := false
	for _, j := range dead {
		if j.ID == id {
			found = true
		}
	}
	if !found {
		t.Error("dead-lettered job not found in Dead()")
	}

	claimed3, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue (3rd): %v", err)
	}
	for _, j := range claimed3 {
		if j.ID == id {
			t.Error("dead-lettered job should not be claimable")
		}
	}
}

func TestCompleteDeletesRow(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "mail:send", RunAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}

	if err := jobs.Complete(ctx, d, claimed[0].ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs WHERE id = $1", claimed[0].ID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (row deleted)", count)
	}
}

func TestLockTimeoutReclaims(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "mail:send", RunAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	id := claimed[0].ID

	// Simulate w1 dying mid-job: its lock is stale (older than LOCK_TIMEOUT).
	if _, err := d.ExecContext(ctx, "UPDATE scheduled_jobs SET locked_at = now() - interval '6 minutes' WHERE id = $1", id); err != nil {
		t.Fatalf("force stale lock: %v", err)
	}

	claimed2, err := jobs.ClaimDue(ctx, d, "w2", 1)
	if err != nil {
		t.Fatalf("ClaimDue (w2): %v", err)
	}
	if len(claimed2) != 1 {
		t.Fatalf("len(claimed2) = %d, want 1 (reclaim)", len(claimed2))
	}
	if claimed2[0].ID != id {
		t.Errorf("reclaimed id = %s, want %s", claimed2[0].ID, id)
	}
	if claimed2[0].Attempts != 2 {
		t.Errorf("Attempts = %d, want 2", claimed2[0].Attempts)
	}
}

func TestRetryResurrectsDeadJob(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "mail:send", RunAt: time.Now().Add(-time.Second), MaxAttempts: 1,
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	id := claimed[0].ID

	willRetry, err := jobs.Fail(ctx, d, id, "boom")
	if err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if willRetry {
		t.Fatal("willRetry = true, want false (maxAttempts=1 exhausted)")
	}

	dead, err := jobs.Dead(ctx, d, 10)
	if err != nil {
		t.Fatalf("Dead: %v", err)
	}
	found := false
	for _, j := range dead {
		if j.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("job not dead-lettered as expected")
	}

	if err := jobs.Retry(ctx, d, id); err != nil {
		t.Fatalf("Retry: %v", err)
	}

	claimed2, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue (after retry): %v", err)
	}
	found = false
	for _, j := range claimed2 {
		if j.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("retried job not claimable")
	}

	var lastError sql.NullString
	if err := d.QueryRowContext(ctx, "SELECT last_error FROM scheduled_jobs WHERE id = $1", id).Scan(&lastError); err != nil {
		t.Fatalf("select last_error: %v", err)
	}
	if lastError.Valid {
		t.Errorf("last_error = %q, want NULL", lastError.String)
	}
}

func TestFailTruncatesErrorOnRuneBoundary(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "mail:send", RunAt: time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	claimed, err := jobs.ClaimDue(ctx, d, "w1", 10)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	id := claimed[0].ID

	// "€" is 3 bytes; 800 copies is 2400 bytes, and 2000 (maxLastErrorLen) is not a multiple
	// of 3 — a naive errMsg[:2000] byte-slice would land mid-rune here.
	longErr := strings.Repeat("€", 800)

	willRetry, err := jobs.Fail(ctx, d, id, longErr)
	if err != nil {
		t.Fatalf("Fail must not fail on a multi-byte truncation boundary: %v", err)
	}
	if !willRetry {
		t.Fatal("willRetry = false, want true (fresh job, default max_attempts)")
	}

	var lastError string
	if err := d.QueryRowContext(ctx, "SELECT last_error FROM scheduled_jobs WHERE id = $1", id).Scan(&lastError); err != nil {
		t.Fatalf("select last_error: %v", err)
	}
	if !utf8.ValidString(lastError) {
		t.Errorf("last_error is not valid UTF-8: %q", lastError)
	}
	if len(lastError) > 2000 {
		t.Errorf("len(last_error) = %d, want <= 2000", len(lastError))
	}
}

func TestCancelRemovesRoomJob(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	room := "room-2"

	if err := jobs.Schedule(ctx, d, jobs.ScheduleInput{
		Kind: "poll:digest", RoomKey: &room, RunAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	if err := jobs.Cancel(ctx, d, "poll:digest", room); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	var count int
	if err := d.QueryRowContext(ctx, "SELECT count(*) FROM scheduled_jobs WHERE kind = $1 AND room_key = $2", "poll:digest", room).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (cancelled)", count)
	}

	// Cancelling a job that isn't there is not an error.
	if err := jobs.Cancel(ctx, d, "poll:digest", "no-such-room"); err != nil {
		t.Errorf("Cancel (nonexistent): %v, want nil", err)
	}
}

// TestPayloadExpired pins the rule the admin console's 409 "payload_expired" and FailedJobView's
// payloadExpired flag both lean on: only a mail:* job can have had its payload purged (every mail
// kind is enqueued WITH one), so a NULL payload on one means deadletter:sweep cleared it.
func TestPayloadExpired(t *testing.T) {
	cases := []struct {
		kind       string
		hasPayload bool
		want       bool
	}{
		{"mail:send", false, true},
		{"mail:poll", false, true},
		{"mail:booking", false, true},
		{"mail:send", true, false},
		{"poll.digest", false, false},
		{"deadletter:sweep", false, false},
	}
	for _, c := range cases {
		if got := jobs.PayloadExpired(c.kind, c.hasPayload); got != c.want {
			t.Errorf("PayloadExpired(%q, hasPayload=%v) = %v, want %v", c.kind, c.hasPayload, got, c.want)
		}
	}
}

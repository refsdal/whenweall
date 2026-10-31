package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestHealthCountsRecentDeadLetters (#43): a job that dead-lettered in the last 24 hours is
// something a human should hear about; an older one is already on the admin console's to-do list
// and must not keep an alert firing for the 30 days it stays there.
func TestHealthCountsRecentDeadLetters(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()

	h, err := jobs.CheckHealth(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if !h.OK() || h.DeadLetteredLast24h != 0 {
		t.Fatalf("empty queue: %+v, want OK", h)
	}

	seedDeadRow(t, d, "mail:send", nil, "2 hours")
	seedDeadRow(t, d, "mail:send", nil, "3 days")

	h, err = jobs.CheckHealth(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if h.DeadLetteredLast24h != 1 {
		t.Errorf("DeadLetteredLast24h = %d, want 1 (the 3-day-old one is excluded)", h.DeadLetteredLast24h)
	}
	if h.OK() {
		t.Error("OK() = true with a fresh dead letter, want false")
	}
}

// TestHealthReportsStalledHousekeeping: a housekeeping chain can never dead-letter (its attempt
// budget is enormous by design), so a stalled one is invisible on the admin console — the
// retention it enforces silently stops. An overdue chain is how it shows.
func TestHealthReportsStalledHousekeeping(t *testing.T) {
	d := testdb.New(t)
	ctx := context.Background()
	if err := jobs.EnsureScheduled(ctx, d); err != nil {
		t.Fatal(err)
	}

	h, err := jobs.CheckHealth(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if !h.OK() || len(h.StalledHousekeeping) != 0 {
		t.Fatalf("freshly seeded chains: %+v, want OK", h)
	}

	if _, err := d.ExecContext(ctx,
		`UPDATE scheduled_jobs SET run_at = $1 WHERE kind = 'deadletter:sweep'`, time.Now().Add(-time.Hour),
	); err != nil {
		t.Fatal(err)
	}
	h, err = jobs.CheckHealth(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.StalledHousekeeping) != 1 || h.StalledHousekeeping[0] != "deadletter:sweep" || h.OK() {
		t.Errorf("after an hour overdue: %+v, want deadletter:sweep stalled and not OK", h)
	}
}

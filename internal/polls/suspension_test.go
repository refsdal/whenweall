package polls_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestHandlerSuspendedPoll (#48): a poll staff have suspended answers its public URL with 410
// "removed" — distinct from not_found, so the page can say it was taken down — and is gone from
// every other read too (the organiser's dashboard here; every other path goes through the same
// GetPoll/PollExists filter). Clearing suspended_at brings it back untouched.
func TestHandlerSuspendedPoll(t *testing.T) {
	d := testdb.New(t)
	h, a, s := newTestHandler(d, testConfig(t))
	ctx := context.Background()
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	if _, err := d.ExecContext(ctx, `UPDATE polls SET suspended_at = now() WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID, nil, nil)
	if rec.Code != http.StatusGone || errCode(t, rec) != "removed" {
		t.Fatalf("GET suspended poll: status %d code %q, want 410 removed; body=%s", rec.Code, errCode(t, rec), rec.Body)
	}
	// A browser caches a 410 heuristically; without no-store, a visitor who saw the takedown would
	// keep seeing it after staff lift the suspension.
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	rec = doRequest(t, h, "GET", "/api/v1/polls/"+created.ID+"/calendar.ics", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET suspended poll's .ics: status %d, want 404", rec.Code)
	}

	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})
	rec = doRequest(t, h, "GET", "/api/v1/polls", nil, sessHeader(ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d; body=%s", rec.Code, rec.Body)
	}
	if list := decodeBody[[]map[string]any](t, rec); len(list) != 0 {
		t.Errorf("organiser's list has %d polls, want 0 (suspended content is hidden everywhere)", len(list))
	}

	t.Run("a deleted poll stays not_found, not removed", func(t *testing.T) {
		if _, err := d.ExecContext(ctx, `UPDATE polls SET deleted_at = now() WHERE id = $1`, created.ID); err != nil {
			t.Fatal(err)
		}
		rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status %d, want 404", rec.Code)
		}
		if _, err := d.ExecContext(ctx, `UPDATE polls SET deleted_at = NULL WHERE id = $1`, created.ID); err != nil {
			t.Fatal(err)
		}
	})

	if _, err := d.ExecContext(ctx, `UPDATE polls SET suspended_at = NULL WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("unsuspend: %v", err)
	}
	if rec := doRequest(t, h, "GET", "/api/v1/polls/"+created.ID, nil, nil); rec.Code != http.StatusOK {
		t.Errorf("GET after unsuspend: status %d, want 200", rec.Code)
	}
}

// TestDeadlineDuringSuspensionStillClosesThePollAfterwards: the deadline job is one-shot, and a
// suspended poll is invisible to GetPoll, so without deferral a deadline that falls inside the
// suspension would be consumed as "nothing to do" — and once the suspension is lifted the poll
// would stay open, and votable, forever. The job must wait out the suspension instead.
func TestDeadlineDuringSuspensionStillClosesThePollAfterwards(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	past := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &past}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := d.ExecContext(ctx, `UPDATE polls SET suspended_at = now() WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("suspend: %v", err)
	}

	w := jobs.NewWorker(d, "test-replica", slog.Default())
	s.RegisterJobs(w, testMailer("https://whenweall.example"))
	if processed, err := w.RunOnce(ctx); err != nil || processed != 1 {
		t.Fatalf("RunOnce while suspended = %d, %v; want the one poll.deadline job", processed, err)
	}

	var runAt time.Time
	if err := d.QueryRowContext(ctx,
		`SELECT run_at FROM scheduled_jobs WHERE kind = 'poll.deadline' AND room_key = $1`, "poll:"+created.ID,
	).Scan(&runAt); err != nil {
		t.Fatalf("deadline job gone after running during the suspension, want it deferred: %v", err)
	}
	if !runAt.After(time.Now()) {
		t.Errorf("deferred run_at = %v, want in the future", runAt)
	}

	if _, err := d.ExecContext(ctx, `UPDATE polls SET suspended_at = NULL WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("unsuspend: %v", err)
	}
	forceDue(t, d, "poll.deadline")
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce after unsuspend: %v", err)
	}
	view, err := s.GetView(ctx, created.ID, polls.Viewer{})
	if err != nil || view == nil {
		t.Fatalf("GetView: %v, %v", view, err)
	}
	if view.Status != "closed" {
		t.Errorf("Status after the suspension = %q, want closed", view.Status)
	}
}

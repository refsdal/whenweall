package polls_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/refsdal/whenweall/internal/auth"
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

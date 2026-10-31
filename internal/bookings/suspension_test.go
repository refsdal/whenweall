package bookings_test

import (
	"context"
	"net/http"
	"testing"
)

// TestHandlerSuspendedBookingPage (#48): the booking-page counterpart of polls'
// TestHandlerSuspendedPoll — 410 "removed" on both public reads, nothing bookable, and back
// untouched once suspended_at is cleared.
func TestHandlerSuspendedBookingPage(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	ctx := context.Background()
	if _, err := p.d.ExecContext(ctx, `UPDATE booking_pages SET suspended_at = now() WHERE id = $1`, p.pageID); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	base := "/api/v1/book/" + p.orgSlug + "/" + p.slug

	for _, path := range []string{base, base + "/availability"} {
		rec := doRequest(t, p.h, "GET", path, nil, nil)
		if rec.Code != http.StatusGone || errCode(t, rec) != "removed" {
			t.Errorf("GET %s: status %d code %q, want 410 removed; body=%s", path, rec.Code, errCode(t, rec), rec.Body)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET %s: Cache-Control = %q, want no-store (a cached 410 outlives the suspension)", path, cc)
		}
	}

	rec := doRequest(t, p.h, "POST", base+"/bookings", bookBody(futureUTCSlot(3, 9, 0)), nil)
	if rec.Code/100 == 2 {
		t.Errorf("booking a suspended page: status %d, want refused", rec.Code)
	}

	if _, err := p.d.ExecContext(ctx, `UPDATE booking_pages SET suspended_at = NULL WHERE id = $1`, p.pageID); err != nil {
		t.Fatalf("unsuspend: %v", err)
	}
	if rec := doRequest(t, p.h, "GET", base, nil, nil); rec.Code != http.StatusOK {
		t.Errorf("GET after unsuspend: status %d, want 200", rec.Code)
	}
}

package bookings_test

// Ports the behavioral cases from src/server/bookings/__tests__/bookings.workers.test.ts
// case-for-case, adapted to this port's own method signatures — most importantly, Book/Reschedule
// (bookings.go) have no injectable `now` the way createBooking/rescheduleBooking (bookings.ts) do,
// so every case below is built around real wall-clock time: a fixed UTC page (Timezone: "UTC",
// full-day availability) and start times computed as an offset from time.Now(), rather than the
// TS source's own fixed 2026-08-25 calendar date. availability_test.go already exhaustively covers
// timezone/DST-specific Slots behavior; this file exercises the DB-backed integration (locking,
// token hashing, buffers-against-a-real-row, the manage flow) on top of it.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/testdb"
)

// openAvailability is bookable every day of the week, 00:00 up to (not including) 23:30 UTC —
// deliberately timezone/weekday-independent so these DB-integration tests never depend on which
// real calendar date/weekday they happen to run on.
func openAvailability() bookings.Availability {
	day := []bookings.TimeRange{{Start: "00:00", End: "23:30"}}
	return bookings.Availability{"0": day, "1": day, "2": day, "3": day, "4": day, "5": day, "6": day}
}

func openPageInput(mutate func(*bookings.PageInput)) bookings.PageInput {
	in := bookings.PageInput{
		Slug:            "intro-call",
		Title:           "Intro call",
		Timezone:        "UTC",
		SlotDurationMin: 30,
		BufferBeforeMin: 0,
		BufferAfterMin:  0,
		MinNoticeMin:    0,
		MaxDaysAhead:    60,
		Availability:    openAvailability(),
		GoogleSync:      false,
		Reminders:       true,
		Status:          "active",
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

// futureUTCSlot is a fixed hour:minute UTC time daysAhead from today — always in the future,
// always well within the default 60-day MaxDaysAhead, and (since it's a whole UTC hour/half-hour)
// always on openAvailability's slot grid.
func futureUTCSlot(daysAhead, hour, minute int) time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC).AddDate(0, 0, daysAhead)
}

func uniqueHandle() string {
	return fmt.Sprintf("handle-%d", seedSeq.Add(1))
}

// bookablePage bundles everything a test needs to address one seeded, bookable page: the service
// and its own *sql.DB (for planting raw rows via makeBooking, or direct SQL assertions), the
// numeric org id UpdatePage/ListPageBookings take, the org's public handle and page slug Book/
// PublicAvailability take, the page's own id, and the page's creator (ownerID — used by
// handlers_test.go's own authz cases, requirement (a)/(e)).
type bookablePage struct {
	svc     *bookings.Service
	db      *sql.DB
	orgID   string
	ownerID string
	orgSlug string
	pageID  string
	slug    string
}

// setupBookablePage seeds a fresh org+owner, gives the org a unique public handle, and creates one
// bookable page (openPageInput, optionally mutated).
func setupBookablePage(t *testing.T, mutate func(*bookings.PageInput)) bookablePage {
	t.Helper()
	ctx := context.Background()
	d := testdb.New(t)
	s := bookings.NewService(testConfig(t), d)
	orgID, ownerID := seedOrgAndUser(t, d)
	handle := uniqueHandle()
	if err := s.SetOrgSlug(ctx, orgID, handle); err != nil {
		t.Fatalf("SetOrgSlug: %v", err)
	}
	page, err := s.CreatePage(ctx, orgID, ownerID, openPageInput(mutate))
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	return bookablePage{svc: s, db: d, orgID: orgID, ownerID: ownerID, orgSlug: handle, pageID: page.ID, slug: page.Slug}
}

func bookInput(start time.Time, email string) bookings.BookInput {
	return bookings.BookInput{
		StartAt:  start,
		Name:     "Bob",
		Email:    email,
		Timezone: "UTC",
	}
}

// formatISOForTest matches formatISO's own layout (timeutil.go is unexported) so tests can assert
// against a BookingView's string time fields directly.
func formatISOForTest(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func TestBook(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a confirmed booking and returns a 43-char manage token", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)

		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}
		if result.BookingID == "" {
			t.Error("BookingID is empty")
		}
		if len(result.ManageToken) != 43 {
			t.Errorf("len(ManageToken) = %d, want 43", len(result.ManageToken))
		}

		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken, false)
		if err != nil {
			t.Fatalf("ManagedBooking: %v", err)
		}
		if view.StartAt != formatISOForTest(start) {
			t.Errorf("StartAt = %q, want %q", view.StartAt, formatISOForTest(start))
		}
		if view.Status != "confirmed" {
			t.Errorf("Status = %q, want confirmed", view.Status)
		}
	})

	t.Run("throws NOT_FOUND for an unknown org or page", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)

		if _, err := p.svc.Book(ctx, "no-such-org", p.slug, bookInput(start, "a@example.com")); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("unknown org: err = %v, want ErrNotFound", err)
		}
		if _, err := p.svc.Book(ctx, p.orgSlug, "no-such-page", bookInput(start, "a@example.com")); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("unknown page: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("throws PAGE_PAUSED for a paused page", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		if _, err := p.svc.UpdatePage(ctx, p.pageID, p.orgID, openPageInput(func(in *bookings.PageInput) {
			in.Status = "paused"
		})); err != nil {
			t.Fatalf("UpdatePage: %v", err)
		}
		start := futureUTCSlot(3, 9, 0)

		if _, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com")); !errors.Is(err, bookings.ErrPagePaused) {
			t.Errorf("err = %v, want ErrPagePaused", err)
		}
	})

	t.Run("throws BOOKING_PAST when startAt is before now", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		past := time.Now().UTC().Add(-1 * time.Hour)

		if _, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(past, "a@example.com")); !errors.Is(err, bookings.ErrBookingPast) {
			t.Errorf("err = %v, want ErrBookingPast", err)
		}
	})

	t.Run("throws SLOT_UNAVAILABLE outside availability", func(t *testing.T) {
		p := setupBookablePage(t, func(in *bookings.PageInput) {
			in.Availability = bookings.Availability{} // nothing scheduled, ever
		})
		start := futureUTCSlot(3, 9, 0)

		if _, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com")); !errors.Is(err, bookings.ErrSlotTaken) {
			t.Errorf("err = %v, want ErrSlotTaken", err)
		}
	})

	t.Run("throws SLOT_UNAVAILABLE when the candidate collides with an existing booking plus buffer", func(t *testing.T) {
		p := setupBookablePage(t, func(in *bookings.PageInput) {
			in.BufferBeforeMin, in.BufferAfterMin = 15, 15
		})
		start := futureUTCSlot(3, 9, 0) // makeBooking below plants a raw 09:00-09:30 booking
		makeBooking(t, p.db, p.pageID, start, "confirmed")

		// Adjacent slot 09:30-10:00: its own 15-min bufferBeforeMin pads it back to 09:15, which
		// collides with the (unbuffered, stored-raw) 09:00-09:30 booking.
		adjacent := start.Add(30 * time.Minute)
		if _, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(adjacent, "carol@example.com")); !errors.Is(err, bookings.ErrSlotTaken) {
			t.Errorf("err = %v, want ErrSlotTaken", err)
		}
	})

	t.Run("buffers apply once (via the candidate), not twice via the stored busy interval", func(t *testing.T) {
		p := setupBookablePage(t, func(in *bookings.PageInput) {
			in.SlotDurationMin = 15
			in.BufferBeforeMin, in.BufferAfterMin = 15, 15
		})
		tenAM := futureUTCSlot(3, 10, 0)
		makeBooking(t, p.db, p.pageID, tenAM, "confirmed") // raw 10:00-10:30 (makeBooking's own fixed 30-min span)

		attempt := func(offset time.Duration, email string) error {
			_, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(tenAM.Add(offset), email))
			return err
		}

		// A 15-min gap after the booking's end is exactly the configured bufferAfterMin, so a
		// slot starting there succeeds — if buffers were double-applied this would need a 30-min
		// gap and wrongly fail.
		if err := attempt(45*time.Minute, "a@example.com"); err != nil { // 10:45
			t.Errorf("10:45 attempt: err = %v, want nil", err)
		}
		// Touching the booking's end (no gap) still fails: the candidate's own 15-min
		// bufferBeforeMin pads it back into the existing booking.
		if err := attempt(30*time.Minute, "b@example.com"); !errors.Is(err, bookings.ErrSlotTaken) { // 10:30
			t.Errorf("10:30 attempt: err = %v, want ErrSlotTaken", err)
		}
		// Symmetric on the other side: a 15-min gap *before* the booking's start succeeds...
		if err := attempt(-30*time.Minute, "c@example.com"); err != nil { // 09:30
			t.Errorf("09:30 attempt: err = %v, want nil", err)
		}
		// ...but touching the booking's start (no gap) does not.
		if err := attempt(-15*time.Minute, "d@example.com"); !errors.Is(err, bookings.ErrSlotTaken) { // 09:45
			t.Errorf("09:45 attempt: err = %v, want ErrSlotTaken", err)
		}
	})

	t.Run("rejects a slot blocked by a caller-supplied busy interval (e.g. Google Calendar)", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		in := bookInput(start, "a@example.com")
		in.Busy = []bookings.Interval{{Start: start, End: start.Add(30 * time.Minute)}}

		if _, err := p.svc.Book(ctx, p.orgSlug, p.slug, in); !errors.Is(err, bookings.ErrSlotTaken) {
			t.Errorf("err = %v, want ErrSlotTaken", err)
		}
	})

	t.Run("rejects invalid input before ever touching the page/db (ValidationError, not NOT_FOUND)", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		in := bookInput(start, "not-an-email")

		_, err := p.svc.Book(ctx, p.orgSlug, p.slug, in)
		var verr *bookings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v (%T), want *ValidationError", err, err)
		}
		if _, ok := verr.Fields["email"]; !ok {
			t.Errorf("Fields = %+v, want an email entry", verr.Fields)
		}

		// Also proven against an org/page that doesn't even exist: validation runs before any
		// lookup, so this is still ValidationError, never ErrNotFound.
		_, err = p.svc.Book(ctx, "no-such-org", "no-such-page", in)
		if !errors.As(err, &verr) {
			t.Errorf("unknown org/page: err = %v (%T), want *ValidationError", err, err)
		}
	})

	// I4: Book must fail loudly, rather than mint an unverifiable manage token, when the service
	// was built with no manage-token secret at all (config.Load's own AuthSecret >= 32 chars rule
	// means this can't happen in a real running server — but Book still checks it explicitly).
	t.Run("I4: fails when the manage token secret is not configured", func(t *testing.T) {
		ctx := context.Background()
		d := testdb.New(t)
		s := bookings.NewService(&config.Config{}, d)
		orgID, ownerID := seedOrgAndUser(t, d)
		handle := uniqueHandle()
		if err := s.SetOrgSlug(ctx, orgID, handle); err != nil {
			t.Fatalf("SetOrgSlug: %v", err)
		}
		page, err := s.CreatePage(ctx, orgID, ownerID, openPageInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}

		if _, err := s.Book(ctx, handle, page.Slug, bookInput(futureUTCSlot(3, 9, 0), "a@example.com")); err == nil {
			t.Error("Book with an empty manage secret: err = nil, want an error")
		}
	})
}

func TestBookedIntervals(t *testing.T) {
	ctx := context.Background()

	t.Run("returns confirmed bookings as raw intervals and excludes cancelled ones", func(t *testing.T) {
		p := setupBookablePage(t, func(in *bookings.PageInput) {
			in.BufferBeforeMin, in.BufferAfterMin = 5, 10
		})
		start := futureUTCSlot(3, 9, 0)
		makeBooking(t, p.db, p.pageID, start, "confirmed")
		makeBooking(t, p.db, p.pageID, start.Add(30*time.Minute), "cancelled")

		intervals, err := p.svc.BookedIntervals(ctx, p.pageID, futureUTCSlot(2, 0, 0), futureUTCSlot(4, 0, 0))
		if err != nil {
			t.Fatalf("BookedIntervals: %v", err)
		}
		if len(intervals) != 1 {
			t.Fatalf("len(intervals) = %d, want 1", len(intervals))
		}
		if !intervals[0].Start.Equal(start) || !intervals[0].End.Equal(start.Add(30*time.Minute)) {
			t.Errorf("intervals[0] = %+v, want [%v, %v)", intervals[0], start, start.Add(30*time.Minute))
		}
	})

	t.Run("returns an empty slice for an unknown page", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		intervals, err := p.svc.BookedIntervals(ctx, "missing", time.Now(), time.Now())
		if err != nil {
			t.Fatalf("BookedIntervals: %v", err)
		}
		if len(intervals) != 0 {
			t.Errorf("intervals = %v, want empty", intervals)
		}
	})
}

func TestCancel(t *testing.T) {
	ctx := context.Background()

	t.Run("cancels a confirmed booking via its manage token and is idempotent on a second call", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if err := p.svc.Cancel(ctx, result.BookingID, result.ManageToken, false); err != nil {
			t.Fatalf("Cancel: %v", err)
		}
		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken, false)
		if err != nil {
			t.Fatalf("ManagedBooking: %v", err)
		}
		if view.Status != "cancelled" {
			t.Errorf("Status = %q, want cancelled", view.Status)
		}
		if view.CancelledBy == nil || *view.CancelledBy != "visitor" {
			t.Errorf("CancelledBy = %v, want visitor", view.CancelledBy)
		}

		// Idempotent: cancelling again (even as the organiser) is a no-op, not an error.
		if err := p.svc.Cancel(ctx, result.BookingID, "", true); err != nil {
			t.Errorf("second Cancel: err = %v, want nil", err)
		}
	})

	t.Run("throws NOT_FOUND for an unknown booking", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		if err := p.svc.Cancel(ctx, "missing", "whatever", false); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a wrong manage token returns the distinct INVALID_TOKEN error (task 6 requirement d)", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if err := p.svc.Cancel(ctx, result.BookingID, "wrong-token", false); !errors.Is(err, bookings.ErrInvalidToken) {
			t.Errorf("err = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("byOrganiser bypasses the token check", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if err := p.svc.Cancel(ctx, result.BookingID, "", true); err != nil {
			t.Fatalf("Cancel(byOrganiser): %v", err)
		}
		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken, false)
		if err != nil {
			t.Fatalf("ManagedBooking: %v", err)
		}
		if view.CancelledBy == nil || *view.CancelledBy != "organiser" {
			t.Errorf("CancelledBy = %v, want organiser", view.CancelledBy)
		}
	})
}

func TestReschedule(t *testing.T) {
	ctx := context.Background()

	t.Run("moves the booking, keeps the manage token, and does not block on its own prior slot", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		newStart := futureUTCSlot(3, 11, 0)
		resched, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, newStart, false)
		if err != nil {
			t.Fatalf("Reschedule: %v", err)
		}
		if !resched.Changed {
			t.Error("Changed = false, want true")
		}
		if !resched.PreviousStartAt.Equal(start) {
			t.Errorf("PreviousStartAt = %v, want %v", resched.PreviousStartAt, start)
		}
		if resched.ManageToken != "" {
			t.Errorf("ManageToken = %q, want empty (Reschedule mints no new token)", resched.ManageToken)
		}

		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken, false)
		if err != nil {
			t.Fatalf("ManagedBooking: %v", err)
		}
		if view.StartAt != formatISOForTest(newStart) {
			t.Errorf("StartAt = %q, want %q", view.StartAt, formatISOForTest(newStart))
		}
	})

	t.Run("rescheduling to the exact same slot succeeds (its own interval does not self-block)", func(t *testing.T) {
		p := setupBookablePage(t, func(in *bookings.PageInput) {
			in.BufferBeforeMin, in.BufferAfterMin = 15, 15
		})
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		resched, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, start, false)
		if err != nil {
			t.Fatalf("Reschedule(same slot): %v", err)
		}
		if !resched.Changed || !resched.PreviousStartAt.Equal(start) {
			t.Errorf("resched = %+v, want Changed=true PreviousStartAt=%v", resched, start)
		}
	})

	t.Run("throws SLOT_UNAVAILABLE when the new slot collides with another booking", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}
		blocked := futureUTCSlot(3, 11, 0)
		makeBooking(t, p.db, p.pageID, blocked, "confirmed")

		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, blocked, false); !errors.Is(err, bookings.ErrSlotTaken) {
			t.Errorf("err = %v, want ErrSlotTaken", err)
		}
	})

	t.Run("a wrong manage token returns the distinct INVALID_TOKEN error (task 6 requirement d)", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if _, err := p.svc.Reschedule(ctx, result.BookingID, "wrong-token", futureUTCSlot(3, 11, 0), false); !errors.Is(err, bookings.ErrInvalidToken) {
			t.Errorf("err = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("throws CONFLICT for an already-cancelled booking", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}
		if err := p.svc.Cancel(ctx, result.BookingID, result.ManageToken, false); err != nil {
			t.Fatalf("Cancel: %v", err)
		}

		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, futureUTCSlot(3, 11, 0), false); !errors.Is(err, bookings.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
	})

	t.Run("throws BOOKING_PAST for a new start in the past", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		past := time.Now().UTC().Add(-1 * time.Hour)
		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, past, false); !errors.Is(err, bookings.ErrBookingPast) {
			t.Errorf("err = %v, want ErrBookingPast", err)
		}
	})

	t.Run("throws PAGE_PAUSED once the page has been paused", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}
		if _, err := p.svc.UpdatePage(ctx, p.pageID, p.orgID, openPageInput(func(in *bookings.PageInput) {
			in.Status = "paused"
		})); err != nil {
			t.Fatalf("UpdatePage: %v", err)
		}

		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, futureUTCSlot(3, 11, 0), false); !errors.Is(err, bookings.ErrPagePaused) {
			t.Errorf("err = %v, want ErrPagePaused", err)
		}
	})

	// I6: byOrganiser bypasses the token check entirely — the same shape Cancel's own
	// byOrganiser flag already has (see TestCancel's identical case above).
	t.Run("I6: byOrganiser bypasses the token check", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		newStart := futureUTCSlot(3, 11, 0)
		if _, err := p.svc.Reschedule(ctx, result.BookingID, "wrong-token-doesnt-matter", newStart, true); err != nil {
			t.Fatalf("Reschedule(byOrganiser): %v", err)
		}
		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken, false)
		if err != nil {
			t.Fatalf("ManagedBooking: %v", err)
		}
		if view.StartAt != formatISOForTest(newStart) {
			t.Errorf("StartAt = %q, want %q", view.StartAt, formatISOForTest(newStart))
		}
	})
}

func TestManagedBooking(t *testing.T) {
	ctx := context.Background()

	t.Run("resolves by token and carries the page handle, slug, duration and owner name", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken, false)
		if err != nil {
			t.Fatalf("ManagedBooking: %v", err)
		}
		if view.Page.Slug != p.slug {
			t.Errorf("Page.Slug = %q, want %q", view.Page.Slug, p.slug)
		}
		if view.Page.Handle == nil || *view.Page.Handle != p.orgSlug {
			t.Errorf("Page.Handle = %v, want %q", view.Page.Handle, p.orgSlug)
		}
		if view.Page.SlotDurationMin != 30 {
			t.Errorf("Page.SlotDurationMin = %d, want 30", view.Page.SlotDurationMin)
		}
		if view.Page.Owner.Name != "Test Org" {
			t.Errorf("Page.Owner.Name = %q, want Test Org", view.Page.Owner.Name)
		}
	})

	t.Run("a wrong token returns the distinct INVALID_TOKEN error (task 6 requirement d)", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if _, err := p.svc.ManagedBooking(ctx, result.BookingID, "wrong-token", false); !errors.Is(err, bookings.ErrInvalidToken) {
			t.Errorf("err = %v, want ErrInvalidToken", err)
		}
	})

	t.Run("an unknown booking id returns NOT_FOUND", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		if _, err := p.svc.ManagedBooking(ctx, "missing", "whatever", false); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	// I4: a booking's manage token is now derived from its own id (HMAC-SHA256(secret,
	// "booking-manage:"+id)) rather than a random value stored per-row — this proves that
	// derivation is actually keyed by id: booking A's token must never open booking B, even on
	// the same page/service (same secret) and even though both tokens are the same 43-character
	// shape.
	t.Run("I4: booking A's manage token is rejected for booking B", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		startA := futureUTCSlot(3, 9, 0)
		startB := futureUTCSlot(3, 11, 0)
		a, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(startA, "a@example.com"))
		if err != nil {
			t.Fatalf("Book A: %v", err)
		}
		b, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(startB, "b@example.com"))
		if err != nil {
			t.Fatalf("Book B: %v", err)
		}
		if a.ManageToken == b.ManageToken {
			t.Fatalf("A and B minted the same manage token: %q", a.ManageToken)
		}

		if _, err := p.svc.ManagedBooking(ctx, b.BookingID, a.ManageToken, false); !errors.Is(err, bookings.ErrInvalidToken) {
			t.Errorf("B with A's token: err = %v, want ErrInvalidToken", err)
		}
		if _, err := p.svc.ManagedBooking(ctx, a.BookingID, b.ManageToken, false); !errors.Is(err, bookings.ErrInvalidToken) {
			t.Errorf("A with B's token: err = %v, want ErrInvalidToken", err)
		}
		// Each still opens its own.
		if _, err := p.svc.ManagedBooking(ctx, a.BookingID, a.ManageToken, false); err != nil {
			t.Errorf("A with A's own token: err = %v, want nil", err)
		}
		if _, err := p.svc.ManagedBooking(ctx, b.BookingID, b.ManageToken, false); err != nil {
			t.Errorf("B with B's own token: err = %v, want nil", err)
		}
	})

	// I6: byOrganiser bypasses the token check entirely — the same shape Cancel's/Reschedule's
	// own byOrganiser flag already has.
	t.Run("I6: byOrganiser bypasses the token check", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		view, err := p.svc.ManagedBooking(ctx, result.BookingID, "wrong-token-doesnt-matter", true)
		if err != nil {
			t.Fatalf("ManagedBooking(byOrganiser): %v", err)
		}
		if view.ID != result.BookingID {
			t.Errorf("ID = %q, want %q", view.ID, result.BookingID)
		}
	})
}

func TestPublicAvailability(t *testing.T) {
	ctx := context.Background()

	t.Run("generates slots against the page's own live bookings", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		booked := futureUTCSlot(3, 9, 0)
		makeBooking(t, p.db, p.pageID, booked, "confirmed")

		from, to := futureUTCSlot(3, 0, 0), futureUTCSlot(3, 23, 0)
		slots, err := p.svc.PublicAvailability(ctx, p.orgSlug, p.slug, from, to)
		if err != nil {
			t.Fatalf("PublicAvailability: %v", err)
		}
		for _, s := range slots {
			if s.Equal(booked) {
				t.Errorf("slots include the already-booked %v", booked)
			}
		}
		wantSomeAt := futureUTCSlot(3, 9, 30)
		found := false
		for _, s := range slots {
			if s.Equal(wantSomeAt) {
				found = true
			}
		}
		if !found {
			t.Errorf("slots = %v, want to include the unbooked %v", slots, wantSomeAt)
		}
	})

	t.Run("returns nil for an unknown org or page", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		from, to := futureUTCSlot(3, 0, 0), futureUTCSlot(3, 23, 0)

		slots, err := p.svc.PublicAvailability(ctx, "no-such-org", p.slug, from, to)
		if err != nil || slots != nil {
			t.Errorf("unknown org: slots=%v err=%v, want nil, nil", slots, err)
		}
		slots, err = p.svc.PublicAvailability(ctx, p.orgSlug, "no-such-page", from, to)
		if err != nil || slots != nil {
			t.Errorf("unknown page: slots=%v err=%v, want nil, nil", slots, err)
		}
	})
}

func TestListPageBookings(t *testing.T) {
	ctx := context.Background()

	t.Run("returns bookings within range", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		inRange := futureUTCSlot(3, 9, 0)
		outOfRange := futureUTCSlot(90, 9, 0)
		makeBooking(t, p.db, p.pageID, inRange, "confirmed")
		makeBooking(t, p.db, p.pageID, outOfRange, "confirmed")

		rows, err := p.svc.ListPageBookings(ctx, p.pageID, p.orgID, futureUTCSlot(0, 0, 0), futureUTCSlot(10, 0, 0))
		if err != nil {
			t.Fatalf("ListPageBookings: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("len(rows) = %d, want 1", len(rows))
		}
		if rows[0].StartAt != formatISOForTest(inRange) {
			t.Errorf("StartAt = %q, want %q", rows[0].StartAt, formatISOForTest(inRange))
		}
	})

	t.Run("a different org gets NOT_FOUND", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		otherOrgID, _ := seedOrgAndUser(t, p.db)

		if _, err := p.svc.ListPageBookings(ctx, p.pageID, otherOrgID, time.Now(), time.Now().Add(time.Hour)); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("an unknown page gets NOT_FOUND", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		if _, err := p.svc.ListPageBookings(ctx, "missing", p.orgID, time.Now(), time.Now().Add(time.Hour)); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// TestBookRacingClaimsExactlyOneWinner is spec §9's double-book proof, this package's analog of
// internal/polls/claims_test.go's TestClaimLastSlotExactlyOneWinner: 16 goroutines all try to Book
// the exact same slot on the same page concurrently. The page-row FOR UPDATE lock (see bookings.go's
// package doc comment) must serialize them so exactly one wins and the rest see ErrSlotTaken. CI
// runs this under `go test -race`; `-count=5` locally widens the window further.
func TestBookRacingClaimsExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)

	const racers = 16
	var wins atomic.Int32
	var wg sync.WaitGroup
	begin := make(chan struct{})

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-begin
			_, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, fmt.Sprintf("racer-%d@example.com", i)))
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, bookings.ErrSlotTaken) {
				t.Errorf("racer %d: err = %v, want nil or ErrSlotTaken", i, err)
			}
		}(i)
	}
	close(begin)
	wg.Wait()

	if wins.Load() != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins.Load())
	}

	var n int
	if err := p.db.QueryRowContext(ctx,
		"SELECT count(*) FROM bookings WHERE page_id = $1 AND status = 'confirmed'", p.pageID,
	).Scan(&n); err != nil {
		t.Fatalf("counting bookings: %v", err)
	}
	if n != 1 {
		t.Fatalf("confirmed booking rows = %d, want exactly 1", n)
	}
}

// TestBookRaceAdjacentSlotBufferCollision is M7's own buffer-collision race proof:
// TestBookRacingClaimsExactlyOneWinner above races two Book calls onto the EXACT SAME slot; this
// races two ADJACENT slots that only collide once each candidate's own buffer padding is applied
// (see "throws SLOT_UNAVAILABLE when the candidate collides with an existing booking plus buffer"
// in TestBook, above, for the single-threaded version of the same rule) — proving the page lock's
// serialization (this file's package doc comment) catches a buffer-only collision under real
// concurrency too, not just an identical-start collision.
func TestBookRaceAdjacentSlotBufferCollision(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, func(in *bookings.PageInput) {
		in.BufferBeforeMin, in.BufferAfterMin = 15, 15
	})
	slotA := futureUTCSlot(3, 9, 0)      // 09:00-09:30
	slotB := slotA.Add(30 * time.Minute) // 09:30-10:00 — adjacent, no raw overlap with slotA

	var wg sync.WaitGroup
	begin := make(chan struct{})
	errA, errB := new(error), new(error)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-begin
		_, *errA = p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(slotA, "a@example.com"))
	}()
	go func() {
		defer wg.Done()
		<-begin
		_, *errB = p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(slotB, "b@example.com"))
	}()
	close(begin)
	wg.Wait()

	wins := 0
	for _, e := range []error{*errA, *errB} {
		if e == nil {
			wins++
		} else if !errors.Is(e, bookings.ErrSlotTaken) {
			t.Errorf("err = %v, want nil or ErrSlotTaken", e)
		}
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1 (adjacent slots collide via buffer padding)", wins)
	}

	var n int
	if err := p.db.QueryRowContext(ctx,
		"SELECT count(*) FROM bookings WHERE page_id = $1 AND status = 'confirmed'", p.pageID,
	).Scan(&n); err != nil {
		t.Fatalf("counting bookings: %v", err)
	}
	if n != 1 {
		t.Fatalf("confirmed booking rows = %d, want exactly 1", n)
	}
}

// TestBookVsRescheduleRaceConsistentOccupancy is M7's Book-vs-Reschedule race: a fresh Book onto
// slot T races a Reschedule of a DIFFERENT, already-confirmed booking onto that SAME slot T.
// Exactly one of the two must win; the slot must end up with exactly one confirmed occupant
// either way — proving Book and Reschedule serialize against EACH OTHER (not just against their
// own kind) via the same page lock.
func TestBookVsRescheduleRaceConsistentOccupancy(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)

	existingStart := futureUTCSlot(3, 9, 0)
	existing, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(existingStart, "existing@example.com"))
	if err != nil {
		t.Fatalf("Book(existing): %v", err)
	}
	target := futureUTCSlot(3, 11, 0)

	var wg sync.WaitGroup
	begin := make(chan struct{})
	var bookErr, reschedErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-begin
		_, bookErr = p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(target, "new@example.com"))
	}()
	go func() {
		defer wg.Done()
		<-begin
		_, reschedErr = p.svc.Reschedule(ctx, existing.BookingID, existing.ManageToken, target, false)
	}()
	close(begin)
	wg.Wait()

	wins := 0
	if bookErr == nil {
		wins++
	} else if !errors.Is(bookErr, bookings.ErrSlotTaken) {
		t.Errorf("Book: err = %v, want nil or ErrSlotTaken", bookErr)
	}
	if reschedErr == nil {
		wins++
	} else if !errors.Is(reschedErr, bookings.ErrSlotTaken) {
		t.Errorf("Reschedule: err = %v, want nil or ErrSlotTaken", reschedErr)
	}
	if wins != 1 {
		t.Fatalf("winners = %d, want exactly 1 (Book and Reschedule racing onto the same slot)", wins)
	}

	var n int
	if err := p.db.QueryRowContext(ctx,
		`SELECT count(*) FROM bookings WHERE page_id = $1 AND status = 'confirmed' AND start_at = $2`,
		p.pageID, target.UTC(),
	).Scan(&n); err != nil {
		t.Fatalf("counting bookings at target: %v", err)
	}
	if n != 1 {
		t.Fatalf("confirmed bookings occupying target slot = %d, want exactly 1", n)
	}
}

// TestCancelRaceEnqueuesOneMailJobPerRecipient is M2's own race proof: several concurrent Cancel
// calls for the SAME booking must all succeed (Cancel is idempotent — none of them ever error),
// the booking ends up cancelled exactly once, and — the actual bug this fixes — exactly ONE
// "cancelled" mail:booking job PER RECIPIENT (visitor + organiser = 2) is enqueued, never one pair
// per racer. See Cancel's own doc comment (bookings.go) for why this needs a row lock, not just
// Cancel's own pre-existing idempotency check.
func TestCancelRaceEnqueuesOneMailJobPerRecipient(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	const racers = 8
	var wg sync.WaitGroup
	begin := make(chan struct{})
	errs := make([]error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-begin
			errs[i] = p.svc.Cancel(ctx, result.BookingID, result.ManageToken, false)
		}(i)
	}
	close(begin)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("racer %d: Cancel err = %v, want nil (idempotent)", i, err)
		}
	}

	var status string
	if err := p.db.QueryRowContext(ctx, `SELECT status FROM bookings WHERE id = $1`, result.BookingID).Scan(&status); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", status)
	}

	payloads := decodeMailBookingJobs(t, listJobs(t, p.db, "mail:booking"))
	cancelled := 0
	for _, pl := range payloads {
		if pl.Kind == "cancelled" {
			cancelled++
		}
	}
	if cancelled != 2 {
		t.Fatalf(`"cancelled" mail:booking jobs = %d, want exactly 2 (one per recipient, among %+v) — a race would enqueue a pair per racer`, cancelled, payloads)
	}
}

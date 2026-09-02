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
// PublicAvailability take, and the page's own id.
type bookablePage struct {
	svc     *bookings.Service
	db      *sql.DB
	orgID   string
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
	s := bookings.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	handle := uniqueHandle()
	if err := s.SetOrgSlug(ctx, orgID, handle); err != nil {
		t.Fatalf("SetOrgSlug: %v", err)
	}
	page, err := s.CreatePage(ctx, orgID, ownerID, openPageInput(mutate))
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	return bookablePage{svc: s, db: d, orgID: orgID, orgSlug: handle, pageID: page.ID, slug: page.Slug}
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

		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken)
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
		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken)
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

	t.Run("a wrong manage token returns NOT_FOUND, not a distinct invalid-token error", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if err := p.svc.Cancel(ctx, result.BookingID, "wrong-token", false); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
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
		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken)
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
		resched, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, newStart)
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

		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken)
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

		resched, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, start)
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

		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, blocked); !errors.Is(err, bookings.ErrSlotTaken) {
			t.Errorf("err = %v, want ErrSlotTaken", err)
		}
	})

	t.Run("a wrong manage token returns NOT_FOUND", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if _, err := p.svc.Reschedule(ctx, result.BookingID, "wrong-token", futureUTCSlot(3, 11, 0)); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
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

		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, futureUTCSlot(3, 11, 0)); !errors.Is(err, bookings.ErrConflict) {
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
		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, past); !errors.Is(err, bookings.ErrBookingPast) {
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

		if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, futureUTCSlot(3, 11, 0)); !errors.Is(err, bookings.ErrPagePaused) {
			t.Errorf("err = %v, want ErrPagePaused", err)
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

		view, err := p.svc.ManagedBooking(ctx, result.BookingID, result.ManageToken)
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

	t.Run("a wrong token returns NOT_FOUND", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		start := futureUTCSlot(3, 9, 0)
		result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "a@example.com"))
		if err != nil {
			t.Fatalf("Book: %v", err)
		}

		if _, err := p.svc.ManagedBooking(ctx, result.BookingID, "wrong-token"); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("an unknown booking id returns NOT_FOUND", func(t *testing.T) {
		p := setupBookablePage(t, nil)
		if _, err := p.svc.ManagedBooking(ctx, "missing", "whatever"); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
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
// package doc comment) must serialize them so exactly one wins and the rest see ErrSlotTaken — run
// with `-count=5` (no `-race`: this environment has CGO_ENABLED=0 and no cgo toolchain, and -race
// requires cgo).
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

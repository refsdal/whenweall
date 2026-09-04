package bookings_test

// Ports the behavioral cases from src/server/bookings/__tests__/pages.workers.test.ts case-for-
// case, adapted to this port's own method signatures (see pages.go's doc comments for the
// deliberate deviations: UpdatePage/DeletePage/GetOwnedPage carry no userID, so the "same-org
// non-manager gets FORBIDDEN" half of the TS requireManagedPage test isn't reachable through this
// package's public surface — the id/org/slug NOT_FOUND half is still ported).
//
// One TS case is deliberately not ported: "defaults memberUserId to createdBy, but an explicit
// memberUserId (or null) overrides it" — this port's CreatePage signature has no memberUserId
// override parameter at all (see PageInput's/CreatePage's doc comments), so there is nothing to
// assert beyond "it defaults to the creator", which every other test already exercises implicitly.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/testdb"
)

var seedSeq atomic.Int64

// seedOrgAndUser inserts one organization and one user directly via SQL, mirroring
// internal/polls/service_test.go's own helper of the same name — a Limen-shaped row is just
// users(email, updated_at) and organizations(name, slug, updated_at), both BIGSERIAL-keyed
// (migrations/00002_auth.sql) — and returns their ids stringified, matching the seam's convention
// every bookings service method expects.
func seedOrgAndUser(t *testing.T, d *sql.DB) (orgID, userID string) {
	t.Helper()
	return seedOrgAndUserNamed(t, d, "Test Org")
}

func seedOrgAndUserNamed(t *testing.T, d *sql.DB, orgName string) (orgID, userID string) {
	t.Helper()
	n := seedSeq.Add(1)
	ctx := context.Background()

	var uid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users (email, updated_at) VALUES ($1, now()) RETURNING id`,
		fmt.Sprintf("user-%d@example.com", n),
	).Scan(&uid); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	var oid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO organizations (name, slug, updated_at) VALUES ($1, $2, now()) RETURNING id`,
		orgName, fmt.Sprintf("test-org-%d", n),
	).Scan(&oid); err != nil {
		t.Fatalf("seeding organization: %v", err)
	}

	return fmt.Sprint(oid), fmt.Sprint(uid)
}

func weekdayAvailability() bookings.Availability {
	weekday := []bookings.TimeRange{{Start: "09:00", End: "17:00"}}
	return bookings.Availability{"1": weekday, "2": weekday, "3": weekday, "4": weekday, "5": weekday}
}

func baseInput(mutate func(*bookings.PageInput)) bookings.PageInput {
	in := bookings.PageInput{
		Slug:            "intro-call",
		Title:           "15 min intro",
		Timezone:        "Europe/Oslo",
		SlotDurationMin: 30,
		BufferBeforeMin: 0,
		BufferAfterMin:  0,
		MinNoticeMin:    0,
		MaxDaysAhead:    60,
		Availability:    weekdayAvailability(),
		GoogleSync:      false,
		Reminders:       true,
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

func TestCreatePage(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a page and rejects a second one with the same (org, slug)", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		view, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		if view.ID == "" {
			t.Fatal("view.ID is empty")
		}

		got, err := s.GetOwnedPage(ctx, view.ID, orgID)
		if err != nil {
			t.Fatalf("GetOwnedPage: %v", err)
		}
		if got.Slug != "intro-call" {
			t.Errorf("Slug = %q, want intro-call", got.Slug)
		}
		if got.Status != "active" {
			t.Errorf("Status = %q, want active", got.Status)
		}
		if len(got.Availability["1"]) != 1 || got.Availability["1"][0] != (bookings.TimeRange{Start: "09:00", End: "17:00"}) {
			t.Errorf("Availability[1] = %+v, want the weekday window", got.Availability["1"])
		}

		if _, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil)); !errors.Is(err, bookings.ErrSlugTaken) {
			t.Errorf("second create error = %v, want ErrSlugTaken", err)
		}
	})

	t.Run("allows the same slug for two different orgs", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		org1, owner1 := seedOrgAndUser(t, d)
		org2, owner2 := seedOrgAndUser(t, d)

		if _, err := s.CreatePage(ctx, org1, owner1, baseInput(nil)); err != nil {
			t.Fatalf("CreatePage(org1): %v", err)
		}
		if _, err := s.CreatePage(ctx, org2, owner2, baseInput(nil)); err != nil {
			t.Errorf("CreatePage(org2) with the same slug: %v", err)
		}
	})

	t.Run("allows reusing a slug after the page that held it is soft-deleted", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		first, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage(first): %v", err)
		}
		if err := s.DeletePage(ctx, first.ID, orgID); err != nil {
			t.Fatalf("DeletePage: %v", err)
		}

		second, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage(second): %v", err)
		}
		if second.ID == first.ID {
			t.Error("second.ID == first.ID, want a fresh id")
		}

		// Two live pages with the same (org, slug) still collide.
		if _, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil)); !errors.Is(err, bookings.ErrSlugTaken) {
			t.Errorf("third create error = %v, want ErrSlugTaken", err)
		}
	})

	t.Run("rejects invalid input with field-level ValidationError", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		_, err := s.CreatePage(ctx, orgID, ownerID, baseInput(func(in *bookings.PageInput) {
			in.Timezone = "Not/AZone"
			in.SlotDurationMin = 3
		}))
		var verr *bookings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want *ValidationError", err)
		}
		if _, ok := verr.Fields["timezone"]; !ok {
			t.Errorf("Fields = %+v, want a timezone entry", verr.Fields)
		}
		if _, ok := verr.Fields["slotDurationMin"]; !ok {
			t.Errorf("Fields = %+v, want a slotDurationMin entry", verr.Fields)
		}
	})
}

func TestUpdatePage(t *testing.T) {
	ctx := context.Background()

	t.Run("updates fields and enforces NOT_FOUND/SLUG_TAKEN", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		if _, err := s.CreatePage(ctx, orgID, ownerID, baseInput(func(in *bookings.PageInput) {
			in.Slug = "other-slug"
		})); err != nil {
			t.Fatalf("CreatePage(other-slug): %v", err)
		}

		updated, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
			in.Title = "New title"
			in.Status = "paused"
		}))
		if err != nil {
			t.Fatalf("UpdatePage: %v", err)
		}
		if updated.Title != "New title" {
			t.Errorf("Title = %q, want %q", updated.Title, "New title")
		}
		if updated.Status != "paused" {
			t.Errorf("Status = %q, want paused", updated.Status)
		}

		if _, err := s.UpdatePage(ctx, "missing", orgID, baseInput(nil)); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("UpdatePage(missing) error = %v, want ErrNotFound", err)
		}

		otherOrgID, _ := seedOrgAndUser(t, d)
		if _, err := s.UpdatePage(ctx, page.ID, otherOrgID, baseInput(nil)); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("UpdatePage(wrong org) error = %v, want ErrNotFound", err)
		}

		if _, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
			in.Slug = "other-slug"
		})); !errors.Is(err, bookings.ErrSlugTaken) {
			t.Errorf("UpdatePage(slug collision) error = %v, want ErrSlugTaken", err)
		}
	})
}

func TestDeletePage(t *testing.T) {
	ctx := context.Background()

	t.Run("soft-deletes so the page disappears from ListMyPages and GetOwnedPage", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		if err := s.DeletePage(ctx, page.ID, orgID); err != nil {
			t.Fatalf("DeletePage: %v", err)
		}

		if _, err := s.GetOwnedPage(ctx, page.ID, orgID); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("GetOwnedPage after delete error = %v, want ErrNotFound", err)
		}
		list, err := s.ListMyPages(ctx, orgID)
		if err != nil {
			t.Fatalf("ListMyPages: %v", err)
		}
		if len(list) != 0 {
			t.Errorf("ListMyPages = %+v, want empty", list)
		}
	})

	t.Run("wrong org gets NOT_FOUND, not a silent no-op", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)
		otherOrgID, _ := seedOrgAndUser(t, d)

		page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		if err := s.DeletePage(ctx, page.ID, otherOrgID); !errors.Is(err, bookings.ErrNotFound) {
			t.Errorf("DeletePage(wrong org) error = %v, want ErrNotFound", err)
		}
	})
}

func TestListMyPages(t *testing.T) {
	ctx := context.Background()

	t.Run("reports upcomingCount from confirmed future bookings", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUser(t, d)

		page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}

		future := time.Now().Add(24 * time.Hour)
		past := time.Now().Add(-24 * time.Hour)
		makeBooking(t, d, page.ID, future, "confirmed")
		makeBooking(t, d, page.ID, past, "confirmed")
		makeBooking(t, d, page.ID, time.Now().Add(48*time.Hour), "cancelled")

		list, err := s.ListMyPages(ctx, orgID)
		if err != nil {
			t.Fatalf("ListMyPages: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("len(list) = %d, want 1", len(list))
		}
		if list[0].UpcomingCount != 1 {
			t.Errorf("UpcomingCount = %d, want 1", list[0].UpcomingCount)
		}
	})
}

func TestGetPublicPage(t *testing.T) {
	ctx := context.Background()

	t.Run("resolves by handle (org slug) + page slug and includes the owner name and status", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUserNamed(t, d, "Grace")
		if err := s.SetOrgSlug(ctx, orgID, "grace"); err != nil {
			t.Fatalf("SetOrgSlug: %v", err)
		}
		if _, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil)); err != nil {
			t.Fatalf("CreatePage: %v", err)
		}

		page, err := s.GetPublicPage(ctx, "grace", "intro-call")
		if err != nil {
			t.Fatalf("GetPublicPage: %v", err)
		}
		if page == nil {
			t.Fatal("page = nil, want a page")
		}
		if page.Title != "15 min intro" {
			t.Errorf("Title = %q, want %q", page.Title, "15 min intro")
		}
		if page.Owner.Name != "Grace" {
			t.Errorf("Owner.Name = %q, want Grace", page.Owner.Name)
		}
		if page.Status != "active" {
			t.Errorf("Status = %q, want active", page.Status)
		}
		if page.Handle != "grace" {
			t.Errorf("Handle = %q, want grace", page.Handle)
		}
		if page.Slug != "intro-call" {
			t.Errorf("Slug = %q, want intro-call", page.Slug)
		}
	})

	t.Run("still returns a paused page (so the route can show a paused message)", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUserNamed(t, d, "Hedy")
		if err := s.SetOrgSlug(ctx, orgID, "hedy"); err != nil {
			t.Fatalf("SetOrgSlug: %v", err)
		}
		page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
		if _, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
			in.Status = "paused"
		})); err != nil {
			t.Fatalf("UpdatePage: %v", err)
		}

		got, err := s.GetPublicPage(ctx, "hedy", "intro-call")
		if err != nil {
			t.Fatalf("GetPublicPage: %v", err)
		}
		if got == nil {
			t.Fatal("got = nil, want a page")
		}
		if got.Status != "paused" {
			t.Errorf("Status = %q, want paused", got.Status)
		}
	})

	t.Run("returns nil for a deleted page, unknown handle, or unknown slug", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUserNamed(t, d, "Ida")
		if err := s.SetOrgSlug(ctx, orgID, "ida"); err != nil {
			t.Fatalf("SetOrgSlug: %v", err)
		}
		page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
		if err != nil {
			t.Fatalf("CreatePage: %v", err)
		}

		if got, err := s.GetPublicPage(ctx, "unknown-handle", "intro-call"); err != nil || got != nil {
			t.Errorf("unknown handle: got=%v err=%v, want nil, nil", got, err)
		}
		if got, err := s.GetPublicPage(ctx, "ida", "unknown-slug"); err != nil || got != nil {
			t.Errorf("unknown slug: got=%v err=%v, want nil, nil", got, err)
		}

		if err := s.DeletePage(ctx, page.ID, orgID); err != nil {
			t.Fatalf("DeletePage: %v", err)
		}
		if got, err := s.GetPublicPage(ctx, "ida", "intro-call"); err != nil || got != nil {
			t.Errorf("deleted page: got=%v err=%v, want nil, nil", got, err)
		}
	})

	t.Run("only exposes the fields the public client actually uses", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, ownerID := seedOrgAndUserNamed(t, d, "Iris")
		if err := s.SetOrgSlug(ctx, orgID, "iris"); err != nil {
			t.Fatalf("SetOrgSlug: %v", err)
		}
		if _, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil)); err != nil {
			t.Fatalf("CreatePage: %v", err)
		}

		page, err := s.GetPublicPage(ctx, "iris", "intro-call")
		if err != nil || page == nil {
			t.Fatalf("GetPublicPage: %v, %v", page, err)
		}
		// PublicPageView's own Go struct definition is the exhaustive-fields guarantee (no
		// availability/buffers/minNotice/org id fields exist on the type at all) — this is a
		// smoke check that the populated view still carries its own required data.
		if page.ID == "" || page.Owner.Name == "" {
			t.Errorf("page = %+v, want populated id and owner name", page)
		}
	})
}

func TestSetOrgSlug(t *testing.T) {
	ctx := context.Background()

	t.Run("sets the org slug and rejects a second org taking the same one", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		org1, _ := seedOrgAndUser(t, d)
		org2, _ := seedOrgAndUser(t, d)

		if err := s.SetOrgSlug(ctx, org1, "shared-handle"); err != nil {
			t.Fatalf("SetOrgSlug(org1): %v", err)
		}
		if err := s.SetOrgSlug(ctx, org2, "shared-handle"); !errors.Is(err, bookings.ErrHandleTaken) {
			t.Errorf("SetOrgSlug(org2) error = %v, want ErrHandleTaken", err)
		}
	})

	t.Run("rejects an invalid handle with ValidationError", func(t *testing.T) {
		d := testdb.New(t)
		s := bookings.NewService(testConfig(t), d)
		orgID, _ := seedOrgAndUser(t, d)

		err := s.SetOrgSlug(ctx, orgID, "AB")
		var verr *bookings.ValidationError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want *ValidationError", err)
		}
	})
}

// makeBooking inserts a booking row directly via SQL — Task 3's CreateBooking doesn't exist yet,
// so listMyPages' upcomingCount test seeds bookings the way the TS test's own test/helpers.ts
// makeBooking would, bypassing the (not yet ported) service layer.
func makeBooking(t *testing.T, d *sql.DB, pageID string, startAt time.Time, status string) {
	t.Helper()
	n := seedSeq.Add(1)
	id := fmt.Sprintf("booking-%d", n)
	now := time.Now().UTC()
	if _, err := d.ExecContext(context.Background(), `
		INSERT INTO bookings (
			id, page_id, start_at, end_at, visitor_name, visitor_email, visitor_timezone,
			status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
		id, pageID, startAt.UTC(), startAt.UTC().Add(30*time.Minute), "Visitor", "visitor@example.com",
		"Europe/Oslo", status, now,
	); err != nil {
		t.Fatalf("seeding booking: %v", err)
	}
}

// pageChangedEvents counts the "page.changed" room_events rows in pageID's own room — the
// broadcast a visitor sitting on the public page (web/src/lib/use-live-page.ts) refetches on.
func pageChangedEvents(t *testing.T, d *sql.DB, pageID string) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(),
		`SELECT count(*) FROM room_events WHERE room_key = $1 AND event->>'type' = 'page.changed'`,
		"booking:"+pageID,
	).Scan(&n); err != nil {
		t.Fatalf("count room_events for %s: %v", pageID, err)
	}
	return n
}

// TestUpdatePageEmitsPageChanged ports pages.functions.ts's notifyPageChanged after updatePage:
// an organiser pausing/editing a page must wake every open public page.
func TestUpdatePageEmitsPageChanged(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := bookings.NewService(testConfig(t), d)
	orgID, ownerID := seedOrgAndUser(t, d)

	page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if n := pageChangedEvents(t, d, page.ID); n != 0 {
		t.Fatalf("events before update = %d, want 0", n)
	}

	if _, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
		in.Status = "paused"
	})); err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if n := pageChangedEvents(t, d, page.ID); n != 1 {
		t.Errorf("events after update = %d, want 1", n)
	}

	t.Run("a rejected update emits nothing (write and emit share one transaction)", func(t *testing.T) {
		if _, err := s.CreatePage(ctx, orgID, ownerID, baseInput(func(in *bookings.PageInput) {
			in.Slug = "taken-slug"
		})); err != nil {
			t.Fatalf("CreatePage(taken-slug): %v", err)
		}
		if _, err := s.UpdatePage(ctx, page.ID, orgID, baseInput(func(in *bookings.PageInput) {
			in.Slug = "taken-slug"
		})); !errors.Is(err, bookings.ErrSlugTaken) {
			t.Fatalf("UpdatePage(slug collision) error = %v, want ErrSlugTaken", err)
		}
		if n := pageChangedEvents(t, d, page.ID); n != 1 {
			t.Errorf("events after rejected update = %d, want still 1", n)
		}
	})
}

// TestDeletePageEmitsPageChanged ports notifyPageChanged after deletePage: an open public page
// refetches, gets the 404, and shows "not found" instead of a live-looking grid for a dead page.
func TestDeletePageEmitsPageChanged(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := bookings.NewService(testConfig(t), d)
	orgID, ownerID := seedOrgAndUser(t, d)

	page, err := s.CreatePage(ctx, orgID, ownerID, baseInput(nil))
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}
	if err := s.DeletePage(ctx, page.ID, orgID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if n := pageChangedEvents(t, d, page.ID); n != 1 {
		t.Errorf("events after delete = %d, want 1", n)
	}
}

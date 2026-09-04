// Package bookings (this file, ws.go) is Task 3's (plan 6) adapter between this package's own
// RequireManageablePage/GetOwnedPage/ListPageBookings and internal/rooms.Register's narrow
// BookingService seam. Lives here rather than in internal/rooms for the same reason
// internal/polls/ws.go does: this package already imports internal/rooms (for Emit, see
// bookings.go), so the reverse edge would be an import cycle — rooms.BookingService is declared
// using only primitive/rooms-native types so *Service can satisfy it with no shared vocabulary.
package bookings

import (
	"context"
	"errors"
	"time"

	"github.com/refsdal/whenweall/internal/rooms"
)

// AuthorizeManagePage checks the booking WS route's gate — session + RequireManageablePage
// (manager-only) — and pre-maps this package's own ErrNotFound/ErrForbidden sentinels onto
// rooms's, so rooms.Register (which cannot import this package's error vocabulary — see this
// file's doc comment) still gets its own 404/403 mapping (statusForAuthzErr) for free. Any other
// error (a real DB failure) passes through unchanged, matching every other DomainErrorMapper's
// fallthrough in this codebase.
func (s *Service) AuthorizeManagePage(ctx context.Context, pageID, orgID, userID string) error {
	err := s.RequireManageablePage(ctx, pageID, orgID, userID)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrNotFound):
		return rooms.ErrNotFound
	case errors.Is(err, ErrForbidden):
		return rooms.ErrForbidden
	default:
		return err
	}
}

// BookingSnapshot builds the booking WS route's snapshot payload: pageID's bookings across its
// own visible horizon (now through the page's own MaxDaysAhead), the same window the owner
// dashboard's calendar shows — src/do/BookingRoom.ts's own `fetch` sends no join payload at all
// (its DO accepts the socket and returns; every visible-state read goes through a separate REST
// call), so this is a deliberate improvement this port's snapshot-on-connect design makes
// possible, not a literal port of an existing payload. Called only after AuthorizeManagePage has
// already verified the caller manages pageID, so any error here is a genuine failure, never an
// authz one.
func (s *Service) BookingSnapshot(ctx context.Context, pageID, orgID string) (any, error) {
	page, err := s.GetOwnedPage(ctx, pageID, orgID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	horizon := now.Add(time.Duration(page.MaxDaysAhead) * 24 * time.Hour)
	return s.ListPageBookings(ctx, pageID, orgID, now, horizon)
}

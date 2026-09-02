// Package polls (this file, ws.go) is Task 3's (plan 6) adapter between this package's own
// GetView and internal/rooms.Register's narrow PollService seam. It lives here — not in
// internal/rooms — because internal/rooms cannot import this package: this package already
// imports internal/rooms (for Emit, see service.go/participants.go/claims.go), and the reverse
// edge would be a compile-time import cycle. rooms.PollService is declared using only
// rooms-native types (rooms.PollViewer, any) for exactly this reason; this file is what makes
// *Service satisfy it, translating to/from this package's own Viewer/PollView types.
package polls

import (
	"context"

	"github.com/refsdal/whenweall/internal/rooms"
)

// PollSnapshot builds the poll WS route's snapshot payload — the same *PollView JSON as GET
// /api/v1/polls/{id} (handleGetView), scoped to viewer exactly like that handler's own
// viewerFromRequest + GetView call. Returns (nil, nil) — not an error — for a missing or
// soft-deleted poll, mirroring GetView's own contract: rooms.Register interprets that nil as
// "poll not found" (a 404) itself, so this method deliberately does NOT return a typed *PollView
// boxed into the any return when view is nil (a typed-nil interface value is never == nil to the
// caller) — the explicit nil/nil branch below is what avoids that trap.
func (s *Service) PollSnapshot(ctx context.Context, pollID string, viewer rooms.PollViewer) (any, error) {
	view, err := s.GetView(ctx, pollID, Viewer{UserID: viewer.UserID, GuestParticipantID: viewer.GuestParticipantID})
	if err != nil {
		return nil, err
	}
	if view == nil {
		return nil, nil //nolint:nilnil // mirrors GetView's own missing/deleted contract; see doc comment above
	}
	return view, nil
}

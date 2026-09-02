// This file is Task 3's (plan 6) public HTTP surface for this package: the three WS routes
// (poll/booking/stats) a plan-8 client actually dials, each just a thin Authorize+Snapshot
// configuration over Hub.ServeWS (ws.go, Task 2). See PROTOCOL.md for every route's URL, query
// params, and auth rule in one place, alongside the frame shapes ws.go/stats.go produce.
//
// Register's signature deliberately does NOT take *polls.Service/*bookings.Service directly (the
// task brief's own sketch does) — both of those packages already import this one (for Emit; see
// internal/polls/service.go's package doc comment), so the reverse edge would be a compile-time
// import cycle. PollService/BookingService below are this package's OWN narrow seams, declared
// using only primitive/rooms-native types (context.Context, string, any, error, PollViewer);
// internal/polls/ws.go and internal/bookings/ws.go are the small adapter methods that make
// *polls.Service and *bookings.Service satisfy them, translating to/from each package's own
// richer domain types on their own side of the boundary. This is the same "narrow interface,
// built the direction that avoids the cycle" shape internal/httpserver's own Auth interface
// already uses for the identical reason (polls/bookings depend on httpserver, never the other way
// around) — see httpserver/domainauth.go's doc comment.
package rooms

import (
	"context"
	"net/http"
	"time"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
)

// wsConnectLimit/wsConnectWindow bound this package's two PUBLIC (no session required) WS
// routes' own connect rate — I5. Each connect attempt costs a real DB round trip before the
// handshake can even be evaluated (PollExists's existence query; stats has no gate at all, so its
// cost is Accept itself), so an unauthenticated caller opening connections in a tight loop is a
// real, if modest, resource-exhaustion vector otherwise. 30/min per IP is generous for a real
// browser (at most a handful of tabs, each reconnecting on its own backoff) while still bounding a
// flood.
const (
	wsConnectLimit  = 30
	wsConnectWindow = time.Minute
)

// PollViewer identifies the caller connecting to a poll's WS room — the same shape polls.Viewer
// carries (UserID / GuestParticipantID), redeclared here so PollService's signature never needs
// to import internal/polls itself (see this file's package doc comment). internal/polls/ws.go's
// PollSnapshot converts between the two structurally, field-for-field.
type PollViewer struct {
	UserID             string
	GuestParticipantID string
}

// PollService is the narrow seam Register needs from polls.Service for the poll WS route.
//
// PollExists is the route's Authorize gate (pollWSHandler): a cheap existence check (respecting
// soft-delete) run BEFORE Hub.Subscribe, so it must never build (or cache) a full snapshot itself
// — see the C1 fix below for why that distinction is load-bearing.
//
// PollSnapshot builds pollID's WS snapshot for viewer — the same *PollView JSON as GET
// /api/v1/polls/{id} — or (nil, nil), NOT an error, for a missing or soft-deleted poll (mirroring
// polls.Service.GetView's own contract exactly). pollWSHandler calls this FRESH, every time, from
// ws.go's Snapshot hook — which ServeWS only ever invokes after Subscribe has already registered
// this connection's listener (see ws.go's own ordering comment). Memoizing this result across the
// Authorize and Snapshot calls (as an earlier version of pollWSHandler did) would reopen a real
// gap: an update committing between Authorize and Subscribe is invisible to a stale cached
// snapshot AND invisible to the live channel (this connection wasn't subscribed yet), so only a
// snapshot query that runs fresh, after Subscribe, can still observe it.
type PollService interface {
	PollExists(ctx context.Context, pollID string) (bool, error)
	PollSnapshot(ctx context.Context, pollID string, viewer PollViewer) (any, error)
}

// BookingService is the narrow seam Register needs from bookings.Service for the booking WS
// route. AuthorizeManagePage (internal/bookings/ws.go) is bookings.Service.RequireManageablePage
// with its own ErrNotFound/ErrForbidden sentinels pre-mapped onto this package's (ErrNotFound/
// ErrForbidden, ws.go) — the direction that mapping can happen in without an import cycle, since
// bookings already imports this package.
type BookingService interface {
	AuthorizeManagePage(ctx context.Context, pageID, orgID, userID string) error
	BookingSnapshot(ctx context.Context, pageID, orgID string) (any, error)
}

// Register mounts this package's three realtime WS routes on mux:
//
//   - GET /api/v1/polls/{id}/ws — public (a session, a guest participant token, or a fully
//     anonymous caller may all connect); 404 for a missing/soft-deleted poll. roomKey "poll:"+id.
//     Presence on. Connect-rate-limited (wsConnectLimit/wsConnectWindow, I5).
//   - GET /api/v1/booking-pages/{pageId}/ws — session required (401), and the session's caller
//     must manage pageId (403) — managers only, mirroring handleListPageBookings's own gate
//     (handlers.go). roomKey "booking:"+pageId. Presence OFF (M4): booking-protocol.ts's own
//     frontend contract has no presence UI for the organiser dashboard this route serves, unlike
//     the poll room's public viewer count — so there is nothing here to broadcast a count for.
//     Renamed from /api/v1/bookings/{pageId}/ws (M5) to match this package's own
//     /api/v1/booking-pages/* REST surface (handlers.go) rather than the /api/v1/bookings/*
//     namespace that surface reserves for an individual booking's own manage/cancel/reschedule
//     routes. Deliberately NOT wrapped in the same connect limiter as the two public routes below:
//     this route is already gated behind a signed-in session that must also manage the target
//     page, a meaningfully higher bar than any anonymous per-IP budget would add on top of it —
//     its caller is by construction an authenticated org member, not an anonymous flood vector. A
//     documented decision, not an oversight.
//   - GET /api/v1/stats/ws — fully public, no gate at all. roomKey "stats:global". Presence OFF —
//     a global anonymous counter has no per-viewer identity worth counting, and StatsRoom.ts's
//     own DO never tracked one either. Connect-rate-limited, same as polls above.
func Register(mux *http.ServeMux, h *Hub, a httpserver.Auth, polls PollService, bookings BookingService, stats *StatsService, cfg *config.Config) {
	// h.sqlDB (not a separate parameter): Register lives in the same package as Hub, so it can
	// reach the pool the hub itself already holds rather than asking every caller to pass it again
	// — main.go's own rooms.NewHub(cfg.DatabaseURL, sqlDB, ...) call is the same sqlDB either way.
	connectLimit := httpserver.PublicRateLimit(h.sqlDB, "rooms", "ws_connect", wsConnectLimit, wsConnectWindow, cfg.TrustProxy)

	mux.Handle("GET /api/v1/polls/{id}/ws", connectLimit(pollWSHandler(h, a, polls)))
	mux.HandleFunc("GET /api/v1/booking-pages/{pageId}/ws", bookingWSHandler(h, a, bookings))
	mux.Handle("GET /api/v1/stats/ws", connectLimit(statsWSHandler(h, stats)))
}

// pollWSHandler builds one poll WS connection's handler per request (rather than a single
// Hub.ServeWS built once at Register time): the Authorize/Snapshot closures below need per-
// request data (the path's poll id, the caller's resolved viewer identity), which WSOptions has
// nowhere else to carry.
//
// Authorize and Snapshot deliberately do NOT share a memoized result (an earlier version of this
// handler did, via a `load` cache) — see PollService's own doc comment for why that was a bug, not
// an optimization: Snapshot must run its own fresh PollSnapshot query, since ws.go's ServeWS only
// calls it after Subscribe has already registered this connection's listener, which is what
// closes the authorize-window gap. Authorize's PollExists call and Snapshot's PollSnapshot call
// are two genuinely separate queries as a result — the same trade-off bookingWSHandler already
// makes below for the same reason (its own doc comment).
func pollWSHandler(h *Hub, a httpserver.Auth, svc PollService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")
		viewer := PollViewer{GuestParticipantID: httpserver.GuestParticipantID(a, r)}
		if sess, ok := a.FromContext(r.Context()); ok {
			viewer.UserID = sess.UserID
		}

		h.ServeWS(WSOptions{
			Authorize: func(rq *http.Request) (string, error) {
				exists, err := svc.PollExists(rq.Context(), pollID)
				if err != nil {
					return "", err
				}
				if !exists {
					return "", ErrNotFound
				}
				return "poll:" + pollID, nil
			},
			Snapshot: func(ctx context.Context, _ string) (any, error) {
				return svc.PollSnapshot(ctx, pollID, viewer)
			},
			Presence: true,
		})(w, r)
	}
}

// bookingWSHandler builds one booking WS connection's handler per request — same shape as
// pollWSHandler, minus the cross-callback caching (AuthorizeManagePage and BookingSnapshot do
// genuinely different queries, so there is nothing to share between them).
func bookingWSHandler(h *Hub, a httpserver.Auth, svc BookingService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pageID := r.PathValue("pageId")

		h.ServeWS(WSOptions{
			Authorize: func(rq *http.Request) (string, error) {
				sess, ok := a.FromContext(rq.Context())
				if !ok {
					return "", ErrUnauthorized
				}
				if sess.ActiveOrgID == "" {
					return "", ErrForbidden
				}
				if err := svc.AuthorizeManagePage(rq.Context(), pageID, sess.ActiveOrgID, sess.UserID); err != nil {
					return "", err
				}
				return "booking:" + pageID, nil
			},
			Snapshot: func(ctx context.Context, _ string) (any, error) {
				// Authorize has already run and succeeded by the time ServeWS calls Snapshot
				// (ws.go), so FromContext succeeding here is not re-checked defensively — the
				// same session that passed the gate above is still on ctx (sendSnapshotAndBackfill
				// derives its ctx from the same request via context.WithCancel, which preserves
				// context values).
				sess, _ := a.FromContext(ctx)
				return svc.BookingSnapshot(ctx, pageID, sess.ActiveOrgID)
			},
			// Presence OFF (M4) — see Register's own doc comment for why: the organiser dashboard
			// this route serves has no presence UI to feed, unlike the poll room's public viewer
			// count.
			Presence: false,
		})(w, r)
	}
}

// statsWSHandler needs no per-request state at all (the stats room is fully public and global),
// so — unlike the two above — it builds its one WSOptions/ServeWS handler once, at Register time.
func statsWSHandler(h *Hub, stats *StatsService) http.HandlerFunc {
	return h.ServeWS(WSOptions{
		Authorize: func(_ *http.Request) (string, error) { return RoomKeyStats, nil },
		Snapshot:  stats.Snapshot,
		Presence:  false,
	})
}

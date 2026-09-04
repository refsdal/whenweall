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

// statsReadLimit/statsReadWindow bound the plain REST stats read (GET /api/v1/stats) added
// alongside the WS route — a fix-round finding (go-rewrite-08 T6/T7 review) that the REST read
// shipped with none of the throttling its WS sibling has, despite costing the identical
// stats.Snapshot -> readCurrent DB round trip. The landing route's loader fires this once per page
// load, so 60/min per IP leaves ample headroom for a real visitor (including a hard refresh loop)
// while still bounding a flood the same way every other PublicRateLimit bucket in this package
// does; double wsConnectLimit's budget since a plain GET costs less than a WS handshake+upgrade.
const (
	statsReadLimit  = 60
	statsReadWindow = time.Minute
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
// bookings already imports this package. PageExists is the route's own public Authorize gate — see
// bookingWSHandler's doc comment for why the route needs one at all now.
type BookingService interface {
	PageExists(ctx context.Context, pageID string) (bool, error)
	AuthorizeManagePage(ctx context.Context, pageID, orgID, userID string) error
	BookingSnapshot(ctx context.Context, pageID, orgID string) (any, error)
}

// Register mounts this package's three realtime WS routes on mux:
//
//   - GET /api/v1/polls/{id}/ws — public (a session, a guest participant token, or a fully
//     anonymous caller may all connect); 404 for a missing/soft-deleted poll. roomKey "poll:"+id.
//     Presence on. Connect-rate-limited (wsConnectLimit/wsConnectWindow, I5).
//   - GET /api/v1/booking-pages/{pageId}/ws — public (existence/soft-delete check only, mirroring
//     the poll room's own gate — and src/routes/api/bookings/$pageId/ws.ts's identical pre-rewrite
//     behavior: an existence check, nothing more). roomKey "booking:"+pageId. Presence OFF (M4):
//     booking-protocol.ts's own frontend contract has no presence UI for this route, unlike the
//     poll room's public viewer count — so there is nothing here to broadcast a count for.
//     Renamed from /api/v1/bookings/{pageId}/ws (M5) to match this package's own
//     /api/v1/booking-pages/* REST surface (handlers.go) rather than the /api/v1/bookings/*
//     namespace that surface reserves for an individual booking's own manage/cancel/reschedule
//     routes.
//
//     Review fix (go-rewrite-08 T7): originally gated session-required + manager-only, on the
//     theory that this route only serves the organiser's own dashboard. It doesn't — its one
//     actual caller, `web/src/routes/book/$handle/$slug.tsx`'s `useLivePage`, is the PUBLIC
//     /book/{org}/{page} page, run by an anonymous visitor with no session at all; the manager
//     dashboard (`bookings/$id/index.tsx`) never calls `useLivePage` in the first place. The old
//     gate meant a public visitor's own websocket 401'd immediately, silently killing every
//     "watch this page for live availability changes" feature the frontend actually shipped — the
//     symptom an e2e spec caught (a booked slot never disappeared from a second visitor's screen).
//     Snapshot below still keeps the owner's *data* private (a real booking list, with visitor
//     PII) — only the *connection* is now public, exactly matching what a plain GET
//     /api/v1/book/{org}/{page}/availability already discloses to anyone regardless.
//     Connect-rate-limited now, same as the two public routes below, for the same I5 reasoning
//     (existsCheck's own DB round trip) — no longer exempt now that an anonymous caller is the
//     expected, common case rather than an edge case.
//   - GET /api/v1/stats/ws — fully public, no gate at all. roomKey "stats:global". Presence OFF —
//     a global anonymous counter has no per-viewer identity worth counting, and StatsRoom.ts's
//     own DO never tracked one either. Connect-rate-limited, same as polls above.
//
// Register also mounts one plain REST route alongside those three: GET /api/v1/stats (T7), the
// stats room's snapshot as JSON for the landing page's first paint. Public like its WS sibling,
// and — a fix-round finding, T6/T7 review — rate-limited like it too (statsRead, its own
// "rooms.stats_read" bucket, distinct from ws_connect's): it costs the identical
// stats.Snapshot -> readCurrent DB round trip the WS route's own connect-time frame does, so
// leaving it unmetered would have been exactly the traffic shape PublicRateLimit exists to bound
// everywhere else in this codebase.
func Register(mux *http.ServeMux, h *Hub, a httpserver.Auth, polls PollService, bookings BookingService, stats *StatsService, cfg *config.Config) {
	// h.sqlDB (not a separate parameter): Register lives in the same package as Hub, so it can
	// reach the pool the hub itself already holds rather than asking every caller to pass it again
	// — main.go's own rooms.NewHub(cfg.DatabaseURL, sqlDB, ...) call is the same sqlDB either way.
	connectLimit := httpserver.PublicRateLimit(h.sqlDB, cfg, "rooms", "ws_connect", wsConnectLimit, wsConnectWindow)
	// statsReadLimit's own bucket, distinct from connectLimit's "rooms.ws_connect" (a different
	// namespace/name pair, so the two never share a budget) — see statsReadLimit's own doc comment.
	statsRead := httpserver.PublicRateLimit(h.sqlDB, cfg, "rooms", "stats_read", statsReadLimit, statsReadWindow)

	mux.Handle("GET /api/v1/polls/{id}/ws", connectLimit(pollWSHandler(h, a, polls)))
	mux.Handle("GET /api/v1/booking-pages/{pageId}/ws", connectLimit(bookingWSHandler(h, a, bookings)))
	mux.Handle("GET /api/v1/stats/ws", connectLimit(statsWSHandler(h, stats)))

	// The stats room's REST read: the landing route's loader fetches this for first paint, then
	// the WS route above takes over for live updates. Unauthenticated, like the WS route, but NOT
	// unmetered — it costs the identical stats.Snapshot -> readCurrent DB round trip the WS route's
	// own connect-time snapshot frame does, so it gets its own PublicRateLimit bucket (statsRead)
	// rather than being the one public mutating-or-reading route in this codebase left unbounded.
	mux.Handle("GET /api/v1/stats", statsRead(statsSnapshotHandler(stats)))
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
// pollWSHandler: existence-only Authorize (PageExists, mirroring PollExists), a Snapshot that
// runs its own fresh query after Subscribe (see pollWSHandler's own doc comment for why that
// ordering matters).
//
// Snapshot's manager check is NOT what gates the connection (Authorize already let any caller,
// session or none, through) — it only decides whether THIS caller gets the real, private
// BookingSnapshot payload or nothing. Safe to leave "nothing" for everyone else: `useLivePage`'s
// `onSnapshot` (web/src/lib/use-live-page.ts) never reads the snapshot's `data` at all, only that
// a frame arrived, so the public page (this room's actual consumer — see Register's own doc
// comment) never notices its snapshot carries no payload.
func bookingWSHandler(h *Hub, a httpserver.Auth, svc BookingService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pageID := r.PathValue("pageId")

		h.ServeWS(WSOptions{
			Authorize: func(rq *http.Request) (string, error) {
				exists, err := svc.PageExists(rq.Context(), pageID)
				if err != nil {
					return "", err
				}
				if !exists {
					return "", ErrNotFound
				}
				return "booking:" + pageID, nil
			},
			Snapshot: func(ctx context.Context, _ string) (any, error) {
				sess, ok := a.FromContext(ctx)
				if !ok || sess.ActiveOrgID == "" {
					return nil, nil
				}
				if err := svc.AuthorizeManagePage(ctx, pageID, sess.ActiveOrgID, sess.UserID); err != nil {
					// Signed in, but not a manager of this page — same as anonymous: no data,
					// never an error (an error here would fail the whole connection, not just
					// withhold the payload — svc.AuthorizeManagePage's own ErrForbidden/ErrNotFound
					// are exactly the "not a manager" cases this deliberately swallows).
					return nil, nil
				}
				return svc.BookingSnapshot(ctx, pageID, sess.ActiveOrgID)
			},
			// Presence OFF (M4) — see Register's own doc comment for why: this route has no
			// presence UI to feed, unlike the poll room's public viewer count.
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

// statsSnapshotHandler serves StatsService.Snapshot as plain JSON — byte-for-byte the object the
// stats WS route nests under "data" in its snapshot frame (PROTOCOL.md), so the frontend's
// UsageStats type covers both. no-store: a cached counter is exactly the stale number this
// endpoint exists to avoid.
func statsSnapshotHandler(stats *StatsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := stats.Snapshot(r.Context(), RoomKeyStats)
		if err != nil {
			httpserver.Err(w, http.StatusInternalServerError, "internal", "internal error", nil)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		httpserver.JSON(w, http.StatusOK, snapshot)
	}
}

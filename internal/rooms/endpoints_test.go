package rooms_test

// Tests internal/rooms/endpoints.go's auth matrix: the poll WS route is public (anonymous,
// guest-token, and signed-in callers may all connect) but 404s an unknown poll; the booking WS
// route is public in the same shape (404s an unknown page, connects anyone else) but withholds
// its Snapshot's real data unless the caller's session manages the page. fakePollService/
// fakeBookingService/fakeWSAuth below are small test doubles for rooms.PollService/
// rooms.BookingService/httpserver.Auth — the same "narrow fake standing in for the real domain
// service" pattern internal/polls/handlers_test.go's own fakeAuth already uses, kept local to this
// file since this package's real callers (internal/polls, internal/bookings) can't be imported
// here without recreating the very import cycle endpoints.go's own doc comment explains.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

// fakePollService is a minimal rooms.PollService: PollSnapshot returns whatever byID holds for
// pollID, or (nil, nil) for an unknown id — mirroring polls.Service.PollSnapshot's own
// missing/soft-deleted contract (internal/polls/ws.go) exactly, which is what lets
// pollWSHandler's nil-check turn "unknown poll" into a 404 with no special-casing here.
//
// onExists, when set, runs as a side effect of PollExists (Authorize's own gate) BEFORE it
// returns — TestPollWS_SnapshotObservesChangeDuringAuthorize uses it to simulate a write landing
// exactly in the authorize-window C1 closes: between Authorize returning and Subscribe running.
type fakePollService struct {
	byID     map[string]any
	onExists func(pollID string)
}

func (f *fakePollService) PollExists(_ context.Context, pollID string) (bool, error) {
	if f.onExists != nil {
		f.onExists(pollID)
	}
	view, ok := f.byID[pollID]
	return ok && view != nil, nil
}

func (f *fakePollService) PollSnapshot(_ context.Context, pollID string, _ rooms.PollViewer) (any, error) {
	return f.byID[pollID], nil
}

// fakeBookingService is a minimal rooms.BookingService: PageExists returns false only for
// missingPageID (default "", so every real test page id — always non-empty — exists by default);
// AuthorizeManagePage succeeds only for managerUserID, matching AuthorizeManagePage's own contract
// of returning rooms.ErrForbidden for anyone else (internal/bookings/ws.go).
type fakeBookingService struct {
	missingPageID string
	managerUserID string
	snapshot      any
}

func (f *fakeBookingService) PageExists(_ context.Context, pageID string) (bool, error) {
	return pageID != f.missingPageID, nil
}

func (f *fakeBookingService) AuthorizeManagePage(_ context.Context, _, _, userID string) error {
	if userID != f.managerUserID {
		return rooms.ErrForbidden
	}
	return nil
}

func (f *fakeBookingService) BookingSnapshot(_ context.Context, _, _ string) (any, error) {
	return f.snapshot, nil
}

// fakeWSAuth implements httpserver.Auth without touching Limen — the same role
// internal/polls/handlers_test.go's own fakeAuth plays for that package's handler tests. Sessions
// are keyed by an X-Test-Session header value (this harness's stand-in for a real session
// cookie); guest tokens are a trivially invertible pair, since this file never re-verifies
// MintGuestToken/VerifyGuestToken's own cryptography (internal/auth/guest_test.go already does).
type fakeWSAuth struct {
	sessions map[string]*auth.Session
}

func newFakeWSAuth() *fakeWSAuth {
	return &fakeWSAuth{sessions: map[string]*auth.Session{}}
}

func (f *fakeWSAuth) login(sess *auth.Session) string {
	f.sessions[sess.UserID] = sess
	return sess.UserID
}

type fakeWSSessionKey struct{}

// middleware resolves X-Test-Session into context for every request — mirrors auth.Service.
// Middleware wrapping the whole mux in production (httpserver.New), which is what lets ws.go's
// Authorize/Snapshot closures find a session via a.FromContext at all.
func (f *fakeWSAuth) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Test-Session"); id != "" {
			if sess, ok := f.sessions[id]; ok {
				r = r.WithContext(context.WithValue(r.Context(), fakeWSSessionKey{}, sess))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (f *fakeWSAuth) RequireSession(next http.HandlerFunc) http.HandlerFunc { return next }

func (f *fakeWSAuth) FromContext(ctx context.Context) (*auth.Session, bool) {
	sess, ok := ctx.Value(fakeWSSessionKey{}).(*auth.Session)
	return sess, ok
}

func (f *fakeWSAuth) VerifyGuestToken(token string) (string, bool) {
	const prefix = "guest-token-for-"
	if len(token) <= len(prefix) || token[:len(prefix)] != prefix {
		return "", false
	}
	return token[len(prefix):], true
}

func (f *fakeWSAuth) MintGuestToken(participantID string) string {
	return "guest-token-for-" + participantID
}

// dialWSExpectSuccess dials path, failing the test if the handshake itself doesn't succeed.
func dialWSExpectSuccess(t *testing.T, server *httptest.Server, path string, header http.Header) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, server.URL+path, &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", path, err, status)
	}
	return conn
}

// dialWSExpectStatus dials path, failing the test unless the handshake is rejected with exactly
// wantStatus — the same "resp is populated even on a failed dial" assertion
// TestServeWS_AuthorizeErrorRejectsBeforeUpgrade already established for ws_test.go.
func dialWSExpectStatus(t *testing.T, server *httptest.Server, path string, header http.Header, wantStatus int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, server.URL+path, &websocket.DialOptions{HTTPHeader: header})
	if err == nil {
		_ = conn.CloseNow()
		t.Fatalf("dial %s: expected the handshake to fail, it succeeded", path)
	}
	if resp == nil {
		t.Fatalf("dial %s: expected an HTTP response even though the dial failed", path)
	}
	if resp.StatusCode != wantStatus {
		t.Errorf("dial %s: status = %d, want %d", path, resp.StatusCode, wantStatus)
	}
}

// newTestMux builds a fresh Hub against a testdb clone, mounts rooms.Register on a ServeMux
// wrapped in auth's session-resolving middleware (mirroring how httpserver.New wires the real
// auth.Service.Middleware around the whole mux in production), and returns the running
// httptest.Server plus every dependency a test might need to configure.
func newTestMux(t *testing.T) (server *httptest.Server, a *fakeWSAuth, polls *fakePollService, bookings *fakeBookingService) {
	t.Helper()
	url, sqlDB := testdb.URL(t)
	hub := startHub(t, url, sqlDB)

	a = newFakeWSAuth()
	polls = &fakePollService{byID: map[string]any{}}
	bookings = &fakeBookingService{}
	stats := rooms.NewStatsService(sqlDB, nil)

	mux := http.NewServeMux()
	rooms.Register(mux, hub, a, polls, bookings, stats, &config.Config{})

	server = httptest.NewServer(a.middleware(mux))
	t.Cleanup(server.Close)
	return server, a, polls, bookings
}

func TestPollWS_AnonymousConnectsToPublicPoll(t *testing.T) {
	server, _, polls, _ := newTestMux(t)
	polls.byID["p1"] = map[string]any{"id": "p1", "title": "Q1 planning"}

	conn := dialWSExpectSuccess(t, server, "/api/v1/polls/p1/ws", nil)
	defer func() { _ = conn.CloseNow() }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "snapshot" {
		t.Fatalf("frame type = %v, want snapshot", frame["type"])
	}
	data, ok := frame["data"].(map[string]any)
	if !ok {
		t.Fatalf("frame data = %v, want an object", frame["data"])
	}
	if data["id"] != "p1" {
		t.Errorf("snapshot data id = %v, want p1", data["id"])
	}
}

func TestPollWS_UnknownPollIs404(t *testing.T) {
	server, _, _, _ := newTestMux(t)
	dialWSExpectStatus(t, server, "/api/v1/polls/does-not-exist/ws", nil, http.StatusNotFound)
}

func TestPollWS_SignedInAndGuestTokenAlsoConnect(t *testing.T) {
	server, a, polls, _ := newTestMux(t)
	polls.byID["p1"] = map[string]any{"id": "p1"}

	userID := a.login(&auth.Session{UserID: "u1"})
	conn := dialWSExpectSuccess(t, server, "/api/v1/polls/p1/ws", http.Header{"X-Test-Session": {userID}})
	_ = conn.CloseNow()

	guestHeader := http.Header{"X-Guest-Token": {"guest-token-for-participant-1"}}
	conn2 := dialWSExpectSuccess(t, server, "/api/v1/polls/p1/ws", guestHeader)
	_ = conn2.CloseNow()
}

// TestBookingWS_AnonymousConnectsToPublicPage is the review-fix regression test (endpoints.go's
// own doc comment on Register): the booking WS route's actual, sole consumer —
// web/src/routes/book/$handle/$slug.tsx's useLivePage — is an anonymous visitor on the public
// /book/{org}/{page} page, so an anonymous connect (no X-Test-Session at all) must succeed, not
// 401. Its snapshot carries no data (useLivePage never reads it, only that a frame arrived), never
// the real BookingSnapshot payload — that's the privacy half of this same fix: an anonymous
// caller must never see it.
func TestBookingWS_AnonymousConnectsToPublicPage(t *testing.T) {
	server, _, _, bookings := newTestMux(t)
	bookings.managerUserID = "manager-1"
	bookings.snapshot = []map[string]any{{"id": "b1", "name": "Visitor One"}}

	conn := dialWSExpectSuccess(t, server, "/api/v1/booking-pages/page-1/ws", nil)
	defer func() { _ = conn.CloseNow() }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "snapshot" {
		t.Fatalf("frame type = %v, want snapshot", frame["type"])
	}
	if frame["data"] != nil {
		t.Errorf("snapshot data = %v, want nil (an anonymous caller must never see the owner's BookingSnapshot payload)", frame["data"])
	}
}

// TestBookingWS_NonManagerConnectsWithNoSnapshotData mirrors the anonymous case above for a
// signed-in caller who just isn't this page's manager: the connection still succeeds (this route
// no longer gates on manager status at all — only on the page existing), but the snapshot is
// still withheld, exactly as for an anonymous caller.
func TestBookingWS_NonManagerConnectsWithNoSnapshotData(t *testing.T) {
	server, a, _, bookings := newTestMux(t)
	bookings.managerUserID = "manager-1"
	bookings.snapshot = []map[string]any{{"id": "b1", "name": "Visitor One"}}

	otherUser := a.login(&auth.Session{UserID: "someone-else", ActiveOrgID: "org-1"})
	conn := dialWSExpectSuccess(t, server, "/api/v1/booking-pages/page-1/ws",
		http.Header{"X-Test-Session": {otherUser}})
	defer func() { _ = conn.CloseNow() }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["data"] != nil {
		t.Errorf("snapshot data = %v, want nil (not this page's manager)", frame["data"])
	}
}

func TestBookingWS_UnknownPageIs404(t *testing.T) {
	server, _, _, bookings := newTestMux(t)
	bookings.missingPageID = "does-not-exist"
	dialWSExpectStatus(t, server, "/api/v1/booking-pages/does-not-exist/ws", nil, http.StatusNotFound)
}

func TestBookingWS_ManagerConnects(t *testing.T) {
	server, a, _, bookings := newTestMux(t)
	bookings.managerUserID = "manager-1"
	bookings.snapshot = []map[string]any{{"id": "b1"}}

	managerID := a.login(&auth.Session{UserID: "manager-1", ActiveOrgID: "org-1"})
	conn := dialWSExpectSuccess(t, server, "/api/v1/booking-pages/page-1/ws",
		http.Header{"X-Test-Session": {managerID}})
	defer func() { _ = conn.CloseNow() }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "snapshot" {
		t.Fatalf("frame type = %v, want snapshot", frame["type"])
	}
	data, ok := frame["data"].([]any)
	if !ok || len(data) == 0 {
		t.Fatalf("snapshot data = %v, want the manager's real BookingSnapshot payload", frame["data"])
	}
}

// TestPollWS_SnapshotObservesChangeDuringAuthorize is C1's regression test: a write that lands
// while Authorize (PollExists) is still running — before ws.go's ServeWS ever calls Subscribe —
// must still be visible to this connection. It can only be visible through a FRESH Snapshot call
// (PollSnapshot queried again, after Subscribe), never through a memoized value computed at
// Authorize-time: this connection wasn't subscribed yet when the write landed, so the live channel
// never had a chance to deliver it either. A pollWSHandler that still cached Authorize's own
// PollSnapshot lookup for Snapshot to reuse (the pre-C1 shape) would serve the STALE title here.
func TestPollWS_SnapshotObservesChangeDuringAuthorize(t *testing.T) {
	server, _, polls, _ := newTestMux(t)
	polls.byID["p1"] = map[string]any{"id": "p1", "title": "before"}
	polls.onExists = func(pollID string) {
		polls.byID[pollID] = map[string]any{"id": pollID, "title": "after"}
	}

	conn := dialWSExpectSuccess(t, server, "/api/v1/polls/p1/ws", nil)
	defer func() { _ = conn.CloseNow() }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "snapshot" {
		t.Fatalf("frame type = %v, want snapshot", frame["type"])
	}
	data, ok := frame["data"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot data = %v, want an object", frame["data"])
	}
	if data["title"] != "after" {
		t.Errorf("snapshot title = %v, want after (Snapshot must query fresh, post-Subscribe, not reuse a value memoized during Authorize)", data["title"])
	}
}

// TestPollWS_ConnectRateLimited is I5's regression test: the poll WS route's connect budget
// (wsConnectLimit/wsConnectWindow, endpoints.go) rejects a caller once it exceeds 30 connects in
// the window with 429, the standard rate_limited envelope — the same status/code shape
// httpserver.RateLimit already uses for every other rate-limited route in this codebase.
func TestPollWS_ConnectRateLimited(t *testing.T) {
	server, _, polls, _ := newTestMux(t)
	polls.byID["p1"] = map[string]any{"id": "p1"}

	for i := 0; i < rooms.TestWSConnectLimit; i++ {
		conn := dialWSExpectSuccess(t, server, "/api/v1/polls/p1/ws", nil)
		_ = conn.CloseNow()
	}
	dialWSExpectStatus(t, server, "/api/v1/polls/p1/ws", nil, http.StatusTooManyRequests)
}

// TestBookingWS_PresenceOff is M4's regression test: unlike the poll room (where a second
// connection's own join broadcasts a live "presence" frame every other connected viewer sees —
// TestServeWS_PresenceCountJoinAndLeave), a second manager connecting to the SAME booking page
// must produce no frame at all for the first — bookingWSHandler's Presence is off.
func TestBookingWS_PresenceOff(t *testing.T) {
	server, a, _, bookings := newTestMux(t)
	bookings.managerUserID = "manager-1"
	bookings.snapshot = []map[string]any{}

	managerID := a.login(&auth.Session{UserID: "manager-1", ActiveOrgID: "org-1"})
	conn1 := dialWSExpectSuccess(t, server, "/api/v1/booking-pages/page-1/ws",
		http.Header{"X-Test-Session": {managerID}})
	defer func() { _ = conn1.CloseNow() }()
	_ = readWSFrame(t, conn1, 5*time.Second) // conn1's own snapshot

	conn2 := dialWSExpectSuccess(t, server, "/api/v1/booking-pages/page-1/ws",
		http.Header{"X-Test-Session": {managerID}})
	defer func() { _ = conn2.CloseNow() }()
	_ = readWSFrame(t, conn2, 5*time.Second) // conn2's own snapshot

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, _, err := conn1.Read(ctx); err == nil {
		t.Fatal("booking ws connection received a frame after a second manager connected, want none (Presence must be off)")
	}
}

func TestStatsWS_PublicNoPresence(t *testing.T) {
	server, _, _, _ := newTestMux(t)
	conn := dialWSExpectSuccess(t, server, "/api/v1/stats/ws", nil)
	defer func() { _ = conn.CloseNow() }()

	frame := readWSFrame(t, conn, 5*time.Second)
	if frame["type"] != "snapshot" {
		t.Fatalf("frame type = %v, want snapshot", frame["type"])
	}
}

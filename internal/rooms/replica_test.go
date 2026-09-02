package rooms_test

// Task 4: the cross-replica integration proof — the concrete claim that this design "replaces
// Durable Objects" only holds if two independent server processes, each with their own Hub and
// their own *sql.DB pool, fan out the SAME event to clients connected on EITHER one, purely
// through Postgres LISTEN/NOTIFY (no in-process shortcut). This test builds two full stacks (two
// Hubs, two httptest servers, two *polls.Service/*bookings.Service instances) against ONE shared
// testdb database — simulating two application replicas behind a load balancer, both talking to
// the same Postgres.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/rooms"
	"github.com/refsdal/whenweall/internal/testdb"
)

var replicaSeedSeq atomic.Int64

// seedOrgAndOwner inserts one organization, one user, and an organization_members row linking
// them — a Limen-shaped fixture (migrations/00002_auth.sql), replicated here rather than reused
// from internal/polls's or internal/bookings's own test helpers of the same shape: those are
// unexported to their own _test packages, and this file (package rooms_test) can't reach them
// without the same import-cycle problem endpoints.go's doc comment already explains for
// non-test code. The membership row is what canManageContent (bookings/authz.go) needs before
// its creator-match check even runs — see RequireManageablePage's own doc comment.
func seedOrgAndOwner(t *testing.T, d *sql.DB) (orgID, orgSlug, userID string) {
	t.Helper()
	n := replicaSeedSeq.Add(1)
	ctx := context.Background()

	var uid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users (email, updated_at) VALUES ($1, now()) RETURNING id`,
		fmt.Sprintf("replica-owner-%d@example.com", n),
	).Scan(&uid); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	slug := fmt.Sprintf("replica-org-%d", n)
	var oid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO organizations (name, slug, updated_at) VALUES ($1, $2, now()) RETURNING id`,
		"Replica Test Org", slug,
	).Scan(&oid); err != nil {
		t.Fatalf("seeding organization: %v", err)
	}

	if _, err := d.ExecContext(ctx,
		`INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2)`,
		oid, uid,
	); err != nil {
		t.Fatalf("seeding organization_member: %v", err)
	}

	return fmt.Sprint(oid), slug, fmt.Sprint(uid)
}

// bookingsAndPollsServer bundles one "replica"'s full set of dependencies: its own Hub (own
// LISTEN session), its own *sql.DB pool, its own domain services bound to that pool, and the
// httptest server exposing all three WS routes over them.
type bookingsAndPollsServer struct {
	httptestServer *httptest.Server
	polls          *polls.Service
	bookings       *bookings.Service
}

func newReplicaStack(t *testing.T, url string, sqlDB *sql.DB, a *fakeWSAuth) *bookingsAndPollsServer {
	t.Helper()
	hub := startHub(t, url, sqlDB)

	cfg := &config.Config{AuthSecret: "replica-test-secret-32-characters!!"}
	pollsSvc := polls.NewService(sqlDB)
	bookingsSvc := bookings.NewService(cfg, sqlDB)
	stats := rooms.NewStatsService(sqlDB, nil)

	mux := http.NewServeMux()
	rooms.Register(mux, hub, a, pollsSvc, bookingsSvc, stats, cfg)

	server := httptest.NewServer(a.middleware(mux))
	t.Cleanup(server.Close)

	return &bookingsAndPollsServer{httptestServer: server, polls: pollsSvc, bookings: bookingsSvc}
}

// awaitFrameOfType reads frames off conn (skipping any that don't match wantType — the initial
// snapshot, a presence update, ...) until one does, or fails the test after too long.
func awaitFrameOfType(t *testing.T, conn *websocket.Conn, wantType string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		frame := readWSFrame(t, conn, timeout)
		if frame["type"] == wantType {
			return frame
		}
	}
	t.Fatalf("did not see a %q frame within %s", wantType, timeout)
	return nil
}

func TestReplicaFanOut_VotePresenceAndBookingCrossReplicas(t *testing.T) {
	ctx := context.Background()

	// One shared testdb database; sqlDB1 is testdb.URL's own pool (cleaned up/dropped by its own
	// t.Cleanup), sqlDB2 is a second, independent pool against the SAME url — exactly what two
	// separate replica processes connecting to the same Postgres would each hold.
	url, sqlDB1 := testdb.URL(t)
	sqlDB2, err := db.Open(ctx, url, 5)
	if err != nil {
		t.Fatalf("opening second replica's pool: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB2.Close() })

	a := newFakeWSAuth()
	stack1 := newReplicaStack(t, url, sqlDB1, a)
	stack2 := newReplicaStack(t, url, sqlDB2, a)

	orgID, orgSlug, ownerID := seedOrgAndOwner(t, sqlDB1)
	ownerSessionID := a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	// ---- poll: vote cast through stack 1 must reach a client on EITHER stack with the same seq.
	view, err := stack1.polls.Create(ctx, orgID, ownerID, polls.CreatePollInput{
		Type:     polls.PollTypeDatetime,
		Title:    "Cross-replica test poll",
		Timezone: "UTC",
		Options: []polls.OptionInput{
			{Kind: polls.OptionKindDatetime, StartAt: futureUTCTime(1, 10, 0)},
			{Kind: polls.OptionKindDatetime, StartAt: futureUTCTime(1, 14, 0)},
		},
	})
	if err != nil {
		t.Fatalf("Create poll: %v", err)
	}
	optionID := view.Options[0].ID

	connA := dialWSExpectSuccess(t, stack1.httptestServer, "/api/v1/polls/"+view.ID+"/ws", nil)
	defer func() { _ = connA.CloseNow() }()
	_ = readWSFrame(t, connA, 5*time.Second) // snapshot

	connB := dialWSExpectSuccess(t, stack2.httptestServer, "/api/v1/polls/"+view.ID+"/ws", nil)
	defer func() { _ = connB.CloseNow() }()
	_ = readWSFrame(t, connB, 5*time.Second) // snapshot

	// Both connections joined the SAME poll room across two different replicas: presence must
	// count both. Each Hub aggregates ws_presence across every replica's own row (presence.go's
	// broadcastPresence), so a connect on stack2 must bump the total BOTH connections observe to
	// 2, not just stack2's own local count.
	awaitPresenceFrame(t, connA, 2)
	awaitPresenceFrame(t, connB, 2)

	if _, err := stack1.polls.AddParticipant(ctx, view.ID, polls.ParticipantInput{
		Name:    "Cross-replica voter",
		Answers: map[string]string{optionID: "yes"},
	}, polls.Viewer{}); err != nil {
		t.Fatalf("AddParticipant (vote) via stack 1: %v", err)
	}

	frameA := awaitFrameOfType(t, connA, "poll.changed", 5*time.Second)
	frameB := awaitFrameOfType(t, connB, "poll.changed", 5*time.Second)
	if frameA["seq"] == nil || frameB["seq"] == nil {
		t.Fatalf("expected both frames to carry a seq, got A=%v B=%v", frameA["seq"], frameB["seq"])
	}
	if frameA["seq"] != frameB["seq"] {
		t.Errorf("poll.changed seq differs across replicas: stack1 conn saw %v, stack2 conn saw %v", frameA["seq"], frameB["seq"])
	}

	// ---- booking: a booking made through stack 2 must reach stack 1's watcher (the organiser
	// dashboard, subscribed to the booking room over stack1's own Hub/httptest server).
	page, err := stack1.bookings.CreatePage(ctx, orgID, ownerID, bookings.PageInput{
		Slug:            "cross-replica-page",
		Title:           "Cross-replica booking page",
		Timezone:        "UTC",
		SlotDurationMin: 30,
		MaxDaysAhead:    60,
		Availability:    allDayAvailability(),
		Reminders:       false,
	})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	watcher := dialWSExpectSuccess(t, stack1.httptestServer, "/api/v1/booking-pages/"+page.ID+"/ws",
		http.Header{"X-Test-Session": {ownerSessionID}})
	defer func() { _ = watcher.CloseNow() }()
	_ = readWSFrame(t, watcher, 5*time.Second) // snapshot

	if _, err := stack2.bookings.Book(ctx, orgSlug, page.Slug, bookings.BookInput{
		StartAt:  futureUTCSlotStart(2, 10, 0),
		Name:     "Cross-replica visitor",
		Email:    "visitor@example.com",
		Timezone: "UTC",
	}); err != nil {
		t.Fatalf("Book via stack 2: %v", err)
	}

	_ = awaitFrameOfType(t, watcher, "page.changed", 5*time.Second)
}


// futureUTCTime formats a whole-hour UTC instant daysAhead from now as the ISO datetime string
// polls' OptionInput.StartAt expects.
func futureUTCTime(daysAhead, hour, minute int) string {
	now := time.Now().UTC()
	d := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC).AddDate(0, 0, daysAhead)
	return d.Format("2006-01-02T15:04:05.000Z")
}

// futureUTCSlotStart is futureUTCTime's time.Time-typed twin for bookings.BookInput.StartAt.
func futureUTCSlotStart(daysAhead, hour, minute int) time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, time.UTC).AddDate(0, 0, daysAhead)
}

// allDayAvailability is open every day, all day — no weekday-dependent flakiness for whatever day
// this test happens to run on.
func allDayAvailability() bookings.Availability {
	day := []bookings.TimeRange{{Start: "00:00", End: "23:30"}}
	return bookings.Availability{"0": day, "1": day, "2": day, "3": day, "4": day, "5": day, "6": day}
}

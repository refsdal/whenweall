package bookings_test

// Ports this task's own TDD list (task-5-brief.md) against an httptest Google Calendar API stub
// (busy merge, insert stores google_event_id, refresh-on-401-then-retry, hard failure -> a
// "sync_failed" mail:booking job) plus delete-on-cancel and the "member has no Google account"
// silent-skip case (google-sync.ts's own contract, re-verified here rather than assumed).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/testdb"
)

// testGoogleConfig is a Config with the Google capability on — the client id/secret values
// themselves are never checked by the httptest stubs below, only that some non-empty pair was
// configured (config.Load's own pair() rule).
func testGoogleConfig() *config.Config {
	return &config.Config{
		Capabilities:       config.Capabilities{Google: true},
		GoogleClientID:     "test-client-id",
		GoogleClientSecret: "test-client-secret",
	}
}

// withGoogleAPIStub points bookings.GoogleCalendarBaseURL at ts's URL for the duration of the
// test, restoring the real Google endpoint on cleanup — mirrors
// internal/httpserver/turnstile_test.go's withSiteverifyStub precedent for the same reason
// (GoogleCalendarBaseURL is a package var so production code can't accidentally point at a test
// server, but that also means every test using it must clean up after itself).
func withGoogleAPIStub(t *testing.T, ts *httptest.Server) {
	t.Helper()
	orig := bookings.GoogleCalendarBaseURL
	bookings.GoogleCalendarBaseURL = ts.URL
	t.Cleanup(func() { bookings.GoogleCalendarBaseURL = orig })
	t.Cleanup(ts.Close)
}

// withGoogleTokenStub is withGoogleAPIStub's own counterpart for the oauth2 token endpoint
// (refresh-on-401 tests).
func withGoogleTokenStub(t *testing.T, ts *httptest.Server) {
	t.Helper()
	orig := bookings.GoogleOAuthTokenURL
	bookings.GoogleOAuthTokenURL = ts.URL
	t.Cleanup(func() { bookings.GoogleOAuthTokenURL = orig })
	t.Cleanup(ts.Close)
}

// insertGoogleAccount plants a Limen accounts row (migrations/00002_auth.sql) for userID under
// provider "google" — the row GetGoogleAccount (google.go) reads. refreshToken == "" stores SQL
// NULL, matching an account that never got offline access.
func insertGoogleAccount(t *testing.T, d *sql.DB, userID, accessToken, refreshToken string) {
	t.Helper()
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		t.Fatalf("parse userID %q: %v", userID, err)
	}
	var refresh sql.NullString
	if refreshToken != "" {
		refresh = sql.NullString{String: refreshToken, Valid: true}
	}
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO accounts (user_id, provider, provider_account_id, access_token, refresh_token, scope, updated_at)
		 VALUES ($1, 'google', $2, $3, $4, 'https://www.googleapis.com/auth/calendar.readonly https://www.googleapis.com/auth/calendar.events', now())`,
		uid, fmt.Sprintf("google-sub-%d", uid), accessToken, refresh,
	); err != nil {
		t.Fatalf("insert google account for user %s: %v", userID, err)
	}
}

// googleAccountAccessToken reads back the access_token currently stored for userID's google
// account row — used to assert a refresh actually persisted the new token.
func googleAccountAccessToken(t *testing.T, d *sql.DB, userID string) string {
	t.Helper()
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		t.Fatalf("parse userID %q: %v", userID, err)
	}
	var tok string
	if err := d.QueryRowContext(context.Background(),
		`SELECT access_token FROM accounts WHERE user_id = $1 AND provider = 'google'`, uid,
	).Scan(&tok); err != nil {
		t.Fatalf("read access_token for user %s: %v", userID, err)
	}
	return tok
}

// bookingGoogleEventID reads bookings.google_event_id (NULL -> "").
func bookingGoogleEventID(t *testing.T, d *sql.DB, bookingID string) string {
	t.Helper()
	var eventID sql.NullString
	if err := d.QueryRowContext(context.Background(),
		`SELECT google_event_id FROM bookings WHERE id = $1`, bookingID,
	).Scan(&eventID); err != nil {
		t.Fatalf("read google_event_id for booking %s: %v", bookingID, err)
	}
	if !eventID.Valid {
		return ""
	}
	return eventID.String
}

// runAllJobs drains every pending job (of any kind) registered on w against d, looping RunOnce
// until nothing's left — mirrors emails_test.go's own TestMailBookingDeliversRealMail loop, here
// factored out since several cases in this file need to run a "google:sync" job and then whatever
// "mail:booking" job it might enqueue in turn.
func runAllJobs(t *testing.T, ctx context.Context, d *sql.DB, w *jobs.Worker) {
	t.Helper()
	for {
		n, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n == 0 {
			return
		}
	}
}

func TestNewGoogleSyncNilWhenCapabilityOff(t *testing.T) {
	d := testdb.New(t)
	if got := bookings.NewGoogleSync(&config.Config{}, d); got != nil {
		t.Fatalf("NewGoogleSync with Capabilities.Google off = %v, want nil", got)
	}
}

func TestGoogleSyncBusyMergesGoogleFreebusyIntoAvailability(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, func(in *bookings.PageInput) { in.GoogleSync = true })

	blockedSlot := futureUTCSlot(3, 10, 0)
	openSlot := futureUTCSlot(3, 11, 0)

	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/freeBusy" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"calendars":{"primary":{"busy":[{"start":%q,"end":%q}]}}}`,
			blockedSlot.Format(time.RFC3339), blockedSlot.Add(30*time.Minute).Format(time.RFC3339),
		)
	}))
	withGoogleAPIStub(t, ts)

	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	from := blockedSlot.Add(-time.Hour)
	to := openSlot.Add(time.Hour)
	slots, err := p.svc.PublicAvailability(ctx, p.orgSlug, p.slug, from, to)
	if err != nil {
		t.Fatalf("PublicAvailability: %v", err)
	}

	for _, s := range slots {
		if s.Equal(blockedSlot) {
			t.Errorf("blockedSlot %s present in availability, want it subtracted by Google freebusy", blockedSlot)
		}
	}
	foundOpen := false
	for _, s := range slots {
		if s.Equal(openSlot) {
			foundOpen = true
		}
	}
	if !foundOpen {
		t.Errorf("openSlot %s missing from availability", openSlot)
	}
}

// TestRescheduleRejectsSlotBlockedByGoogleFreebusy is I1's own regression coverage for
// Reschedule's Google Calendar busy merge: the freebusy lookup moved to a read-only prefetch
// BEFORE Reschedule ever opens its transaction/takes the page lock (bookings.go), so this proves
// the merge still reaches IsSlotAvailable correctly after that refactor, not just that Book's
// (sibling, but separately hoisted) prefetch does.
func TestRescheduleRejectsSlotBlockedByGoogleFreebusy(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, func(in *bookings.PageInput) { in.GoogleSync = true })
	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	original := futureUTCSlot(3, 9, 0)
	blockedSlot := futureUTCSlot(3, 10, 0)
	openSlot := futureUTCSlot(3, 11, 0)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"calendars":{"primary":{"busy":[{"start":%q,"end":%q}]}}}`,
			blockedSlot.Format(time.RFC3339), blockedSlot.Add(30*time.Minute).Format(time.RFC3339),
		)
	}))
	withGoogleAPIStub(t, ts)
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(original, "a@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, blockedSlot, false); !errors.Is(err, bookings.ErrSlotTaken) {
		t.Errorf("reschedule onto Google-busy slot: err = %v, want ErrSlotTaken", err)
	}
	if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, openSlot, false); err != nil {
		t.Errorf("reschedule onto an open slot: err = %v, want nil", err)
	}
}

func TestBookInsertsGoogleEventAndStoresEventID(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, func(in *bookings.PageInput) { in.GoogleSync = true })
	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	var insertCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/freeBusy" {
			// Book's own pre-insert availability re-check (googleBusyForPage) hits this too —
			// answer with an empty busy list, this test only cares about the insert call.
			_, _ = w.Write([]byte(`{"calendars":{"primary":{"busy":[]}}}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/calendars/primary/events" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		insertCalls.Add(1)
		_, _ = w.Write([]byte(`{"id":"evt-abc123"}`))
	}))
	withGoogleAPIStub(t, ts)
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	if n := countJobs(t, p.db, "google:sync"); n != 1 {
		t.Fatalf(`countJobs("google:sync") = %d, want 1`, n)
	}

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	runAllJobs(t, ctx, p.db, w)

	if got := insertCalls.Load(); got != 1 {
		t.Fatalf("insert calls = %d, want 1", got)
	}
	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "evt-abc123" {
		t.Errorf("booking.google_event_id = %q, want %q", got, "evt-abc123")
	}
	// No hard failure -> no "sync_failed" mail job.
	if n := countJobs(t, p.db, "mail:booking"); n != 1 {
		t.Fatalf(`countJobs("mail:booking") = %d, want 1 (just "confirmed")`, n)
	}
}

func TestGoogleSyncRefreshesAccessTokenOn401ThenRetriesOnce(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	_, ownerID := seedOrgAndUser(t, d)
	insertGoogleAccount(t, d, ownerID, "stale-access-tok", "good-refresh-tok")

	var calendarCalls atomic.Int32
	calendarTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calendarCalls.Add(1)
		if r.Header.Get("Authorization") == "Bearer stale-access-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fresh-access-tok" {
			t.Errorf("call %d: unexpected Authorization header %q", n, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"calendars":{"primary":{"busy":[]}}}`))
	}))
	withGoogleAPIStub(t, calendarTS)

	var tokenCalls atomic.Int32
	tokenTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-access-tok","token_type":"Bearer","expires_in":3600}`))
	}))
	withGoogleTokenStub(t, tokenTS)

	sync := bookings.NewGoogleSync(testGoogleConfig(), d)
	from := time.Now().UTC()
	to := from.Add(24 * time.Hour)
	if _, err := sync.Busy(ctx, ownerID, from, to); err != nil {
		t.Fatalf("Busy: %v", err)
	}

	if got := calendarCalls.Load(); got != 2 {
		t.Fatalf("calendar API calls = %d, want 2 (initial 401 + one retry)", got)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("token endpoint calls = %d, want 1", got)
	}
	if got := googleAccountAccessToken(t, d, ownerID); got != "fresh-access-tok" {
		t.Errorf("persisted access_token = %q, want %q", got, "fresh-access-tok")
	}
}

func TestBookHardGoogleFailureEnqueuesSyncFailedMailJob(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, func(in *bookings.PageInput) { in.GoogleSync = true })
	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	withGoogleAPIStub(t, ts)
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	// The mailer points at an unreachable host on purpose: a "sync_failed" mail:booking job must
	// be enqueued regardless of whether the notice itself can later be delivered — actual
	// delivery is covered end-to-end by TestMailBookingDeliversRealMail (emails_test.go) for
	// every kind already. RunOnce also claims the "confirmed" job Book itself enqueued in the same
	// batch; its own SMTP attempt against the bogus host fails and backs off (logged, harmless) —
	// only the "google:sync" outcome below is asserted on.
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "" {
		t.Errorf("booking.google_event_id = %q, want empty (insert failed)", got)
	}

	jobsRows := listJobs(t, p.db, "mail:booking")
	payloads := decodeMailBookingJobs(t, jobsRows)
	found := false
	for _, pl := range payloads {
		if pl.Kind == "sync_failed" && pl.BookingID == result.BookingID {
			found = true
		}
	}
	if !found {
		t.Fatalf(`no "sync_failed" mail:booking job found for booking %s among %+v`, result.BookingID, payloads)
	}
}

func TestCancelDeletesGoogleEventViaJob(t *testing.T) {
	ctx := context.Background()
	// GoogleSync starts off so Book doesn't try to insert an event of its own — this booking's
	// googleEventId is planted directly, simulating one created in an earlier session.
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if _, err := p.db.ExecContext(ctx,
		`UPDATE bookings SET google_event_id = 'evt-to-delete' WHERE id = $1`, result.BookingID,
	); err != nil {
		t.Fatalf("plant google_event_id: %v", err)
	}

	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	var deleteCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deleteCalls.Add(1)
		if r.Method != http.MethodDelete || r.URL.Path != "/calendars/primary/events/evt-to-delete" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	withGoogleAPIStub(t, ts)
	// Cancel deletes a known event unconditionally, regardless of the page's CURRENT googleSync
	// toggle (spec finding 7) — SetGoogleSync is enough, the page itself is left with sync off.
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	if err := p.svc.Cancel(ctx, result.BookingID, result.ManageToken, false); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	runAllJobs(t, ctx, p.db, w)

	if got := deleteCalls.Load(); got != 1 {
		t.Fatalf("delete calls = %d, want 1", got)
	}
	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "" {
		t.Errorf("booking.google_event_id = %q, want empty (deleted)", got)
	}
}

func TestGoogleSyncSilentlySkipsWhenMemberHasNoGoogleAccount(t *testing.T) {
	ctx := context.Background()
	// GoogleSync on, but no accounts row is ever planted for the page's member.
	p := setupBookablePage(t, func(in *bookings.PageInput) { in.GoogleSync = true })

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected Google API call to %s — there is no linked account to call with", r.URL.Path)
	}))
	withGoogleAPIStub(t, ts)
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	runAllJobs(t, ctx, p.db, w)

	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "" {
		t.Errorf("booking.google_event_id = %q, want empty (no account -> silent skip)", got)
	}
	jobsRows := listJobs(t, p.db, "mail:booking")
	payloads := decodeMailBookingJobs(t, jobsRows)
	for _, pl := range payloads {
		if pl.Kind == "sync_failed" {
			t.Fatalf("unexpected sync_failed mail job for booking %s — no account means silent skip, not a hard failure", pl.BookingID)
		}
	}
}

// TestGoogleSyncInsertIsIdempotent is I5's own regression test (triage 2+5): a retried "insert"
// job for a booking that already has a known GoogleEventID must make no HTTP call at all, not
// create a second Google Calendar event.
func TestGoogleSyncInsertIsIdempotent(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, func(in *bookings.PageInput) { in.GoogleSync = true })
	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	var insertCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/freeBusy" {
			_, _ = w.Write([]byte(`{"calendars":{"primary":{"busy":[]}}}`))
			return
		}
		insertCalls.Add(1)
		_, _ = w.Write([]byte(`{"id":"evt-first"}`))
	}))
	withGoogleAPIStub(t, ts)
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	runAllJobs(t, ctx, p.db, w)

	if got := insertCalls.Load(); got != 1 {
		t.Fatalf("insert calls after the first run = %d, want 1", got)
	}
	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "evt-first" {
		t.Fatalf("booking.google_event_id = %q, want %q", got, "evt-first")
	}

	// A second "insert" job for the SAME booking — a job-system retry, or a duplicate enqueue —
	// arriving after the first one already succeeded and stored a google_event_id.
	if err := jobs.Schedule(ctx, p.db, jobs.ScheduleInput{
		Kind: "google:sync", RunAt: time.Now(),
		Payload:     map[string]any{"kind": "insert", "bookingId": result.BookingID},
		MaxAttempts: 5,
	}); err != nil {
		t.Fatalf("schedule a second insert job: %v", err)
	}
	runAllJobs(t, ctx, p.db, w)

	if got := insertCalls.Load(); got != 1 {
		t.Errorf("insert calls after the second (idempotent) run = %d, want still 1 (no HTTP call)", got)
	}
	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "evt-first" {
		t.Errorf("booking.google_event_id = %q, want unchanged %q", got, "evt-first")
	}
}

// TestGoogleSyncRescheduleDeletesThenInserts is I5's reschedule-path proof: the naive "insert
// no-ops whenever GoogleEventID is already set" guard, applied blindly, would also make the
// RESCHEDULE path's own insert-after-delete silently do nothing (the booking's row still shows
// the OLD event id at that point) — googleSyncDelete's in-place clear (google.go) is what keeps
// this working. DELETE fires for the old event, then POST for the new one, and the new id is
// what ends up stored.
func TestGoogleSyncRescheduleDeletesThenInserts(t *testing.T) {
	ctx := context.Background()
	// GoogleSync off at Book time (so Book itself enqueues no "insert" job to race against the
	// planted event id below) — turned on via UpdatePage before Reschedule, so Reschedule's own
	// "reschedule" job is the only "google:sync" job this booking ever gets.
	p := setupBookablePage(t, nil)
	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if _, err := p.db.ExecContext(ctx,
		`UPDATE bookings SET google_event_id = 'evt-old' WHERE id = $1`, result.BookingID,
	); err != nil {
		t.Fatalf("plant google_event_id: %v", err)
	}
	if _, err := p.svc.UpdatePage(ctx, p.pageID, p.orgID, openPageInput(func(in *bookings.PageInput) {
		in.GoogleSync = true
	})); err != nil {
		t.Fatalf("UpdatePage (turn GoogleSync on): %v", err)
	}

	newStart := futureUTCSlot(3, 14, 0)
	if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, newStart, false); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	var deleteCalls, insertCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/freeBusy":
			_, _ = w.Write([]byte(`{"calendars":{"primary":{"busy":[]}}}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/calendars/primary/events/evt-old":
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/calendars/primary/events":
			insertCalls.Add(1)
			_, _ = w.Write([]byte(`{"id":"evt-new"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	withGoogleAPIStub(t, ts)
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	runAllJobs(t, ctx, p.db, w)

	if got := deleteCalls.Load(); got != 1 {
		t.Errorf("delete calls = %d, want 1", got)
	}
	if got := insertCalls.Load(); got != 1 {
		t.Errorf("insert calls = %d, want 1", got)
	}
	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "evt-new" {
		t.Errorf("booking.google_event_id = %q, want %q", got, "evt-new")
	}
}

// TestGoogleSyncRescheduleDeleteFailureSkipsInsert is I5's failure-path proof: a hard DELETE
// failure must never be followed by an insert (that would silently orphan the booking from its
// real, still-existing old event), must leave the stored google_event_id unchanged, and must
// still enqueue exactly one "sync_failed" notice.
func TestGoogleSyncRescheduleDeleteFailureSkipsInsert(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil) // GoogleSync off at Book time — the old event is planted directly
	memberUserID := ownerUserID(t, p.db, p.pageID)
	insertGoogleAccount(t, p.db, memberUserID, "access-tok", "refresh-tok")

	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if _, err := p.db.ExecContext(ctx,
		`UPDATE bookings SET google_event_id = 'evt-old' WHERE id = $1`, result.BookingID,
	); err != nil {
		t.Fatalf("plant google_event_id: %v", err)
	}

	newStart := futureUTCSlot(3, 14, 0)
	if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, newStart, false); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	var insertCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		insertCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"evt-should-never-exist"}`))
	}))
	withGoogleAPIStub(t, ts)
	p.svc.SetGoogleSync(bookings.NewGoogleSync(testGoogleConfig(), p.db))

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, testBookingMailer("https://whenweall.example"))
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if got := insertCalls.Load(); got != 0 {
		t.Errorf("insert calls = %d, want 0 (delete failed, insert must not run)", got)
	}
	if got := bookingGoogleEventID(t, p.db, result.BookingID); got != "evt-old" {
		t.Errorf("booking.google_event_id = %q, want unchanged %q", got, "evt-old")
	}

	payloads := decodeMailBookingJobs(t, listJobs(t, p.db, "mail:booking"))
	found := 0
	for _, pl := range payloads {
		if pl.Kind == "sync_failed" && pl.BookingID == result.BookingID {
			found++
		}
	}
	if found != 1 {
		t.Fatalf(`"sync_failed" mail:booking jobs for booking %s = %d, want 1 (among %+v)`, result.BookingID, found, payloads)
	}
}

// ownerUserID reads back the numeric member_user_id a booking page is assigned to (CreatePage
// defaults it to the creator — see ownerEmail's own doc comment, emails_test.go) as the string
// form GoogleSync's userID parameter expects.
func ownerUserID(t *testing.T, d *sql.DB, pageID string) string {
	t.Helper()
	var id int64
	if err := d.QueryRowContext(context.Background(),
		`SELECT member_user_id FROM booking_pages WHERE id = $1`, pageID,
	).Scan(&id); err != nil {
		t.Fatalf("ownerUserID(%s): %v", pageID, err)
	}
	return strconv.FormatInt(id, 10)
}

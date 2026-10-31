package httpserver_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// seedExportFixture builds, around the signed-in user (email), everything GET /api/v1/me/export
// has to tell apart: their own poll with a stranger's response on it, a stranger-organised poll
// they answered as a guest (by e-mail only, different case), a booking they made as a visitor,
// and a booking a stranger made on their page.
func seedExportFixture(t *testing.T, d *sql.DB, email string) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := d.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}

	var userID, orgID int64
	if err := d.QueryRowContext(ctx, `
		SELECT u.id, m.organization_id FROM users u
		JOIN organization_members m ON m.user_id = u.id
		WHERE u.email = $1 LIMIT 1`, email).Scan(&userID, &orgID); err != nil {
		t.Fatalf("lookup user/org: %v", err)
	}

	// Their own poll, with one stranger's vote and comment on it.
	exec(`INSERT INTO polls (id, organization_id, created_by, type, title, timezone, created_at, updated_at)
		VALUES ('own-poll', $1, $2, 'dates', 'My team dinner', 'UTC', now(), now())`, orgID, userID)
	exec(`INSERT INTO poll_options (id, poll_id, position, kind, label) VALUES ('own-opt', 'own-poll', 0, 'text', 'Friday')`)
	exec(`INSERT INTO participants (id, poll_id, name, email, edit_token_hash, created_at, updated_at)
		VALUES ('bob', 'own-poll', 'Bob Stranger', 'bob@stranger.example', 'SECRET-EDIT-HASH', now(), now())`)
	exec(`INSERT INTO votes (participant_id, option_id, answer) VALUES ('bob', 'own-opt', 'yes')`)
	exec(`INSERT INTO comments (id, poll_id, author_name, participant_id, body, created_at)
		VALUES ('bob-c', 'own-poll', 'Bob Stranger', 'bob', 'Bob private remark', now())`)

	// A stranger's poll in another organization, answered by our user as a guest.
	exec(`INSERT INTO organizations (name, slug) VALUES ('Elsewhere', 'elsewhere-export')`)
	exec(`INSERT INTO polls (id, organization_id, type, title, timezone, created_at, updated_at)
		SELECT 'their-poll', id, 'dates', 'Book club', 'UTC', now(), now() FROM organizations WHERE slug = 'elsewhere-export'`)
	exec(`INSERT INTO poll_options (id, poll_id, position, kind, label) VALUES ('their-opt', 'their-poll', 0, 'text', 'Monday')`)
	exec(`INSERT INTO participants (id, poll_id, name, email, created_at, updated_at)
		VALUES ('me-guest', 'their-poll', 'Me As Guest', $1, now(), now())`, strings.ToUpper(email))
	exec(`INSERT INTO votes (participant_id, option_id, answer) VALUES ('me-guest', 'their-opt', 'ifneedbe')`)
	exec(`INSERT INTO comments (id, poll_id, author_name, participant_id, body, created_at)
		VALUES ('me-c', 'their-poll', 'Me As Guest', 'me-guest', 'My own remark', now())`)

	// Another ACCOUNT answering that same poll while typing our user's address: the row belongs to
	// that account (user_id set), so the e-mail match must not claim it.
	var daveID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users (email, updated_at) VALUES ('dave@stranger.example', now()) RETURNING id`,
	).Scan(&daveID); err != nil {
		t.Fatalf("seed dave: %v", err)
	}
	exec(`INSERT INTO participants (id, poll_id, name, email, user_id, created_at, updated_at)
		VALUES ('dave', 'their-poll', 'Dave Signed In', $1, $2, now(), now())`, email, daveID)
	exec(`INSERT INTO comments (id, poll_id, author_name, participant_id, user_id, body, created_at)
		VALUES ('dave-c', 'their-poll', 'Dave Signed In', 'dave', $1, 'Dave private remark', now())`, daveID)

	// A booking our user made as a visitor on someone else's page...
	exec(`INSERT INTO booking_pages (id, organization_id, slug, title, timezone, slot_duration_min,
			buffer_before_min, buffer_after_min, min_notice_min, max_days_ahead, availability, created_at, updated_at)
		SELECT 'their-page', id, 'intro', 'Their intro call', 'UTC', 30, 0, 0, 0, 30, '{}'::jsonb, now(), now()
		FROM organizations WHERE slug = 'elsewhere-export'`)
	exec(`INSERT INTO bookings (id, page_id, start_at, end_at, visitor_name, visitor_email, visitor_note,
			visitor_timezone, created_at, updated_at)
		VALUES ('my-booking', 'their-page', now(), now() + interval '30 minutes', 'Me Visiting', $1,
			'My own booking note', 'UTC', now(), now())`, email)

	// ...and a stranger's booking on our user's own page.
	exec(`INSERT INTO booking_pages (id, organization_id, created_by, slug, title, timezone, slot_duration_min,
			buffer_before_min, buffer_after_min, min_notice_min, max_days_ahead, availability, created_at, updated_at)
		VALUES ('own-page', $1, $2, 'mine', 'My office hours', 'UTC', 30, 0, 0, 0, 30, '{}'::jsonb, now(), now())`, orgID, userID)
	exec(`INSERT INTO bookings (id, page_id, start_at, end_at, visitor_name, visitor_email, visitor_note,
			visitor_timezone, created_at, updated_at)
		VALUES ('carol-booking', 'own-page', now(), now() + interval '30 minutes', 'Carol Visitor',
			'carol@stranger.example', 'Carol private note', 'UTC', now(), now())`)
}

// TestExportMe (#50): GET /api/v1/me/export hands a verified user one JSON document with their
// own data in full — account, their own polls and pages, their own answers, comments and
// bookings elsewhere (matched by their verified e-mail too) — and other people's data on their
// content only as counts. No credential of any kind is ever in it.
func TestExportMe(t *testing.T) {
	h := newAccountHarness(t)
	email := "exporter@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)
	_ = h.me() // resolves the session, which creates the personal organization
	seedExportFixture(t, h.db, email)

	resp := h.do(http.MethodGet, "/api/v1/me/export", nil)
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, raw)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="whenweall-export-`) {
		t.Errorf("Content-Disposition = %q, want an attachment", cd)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	body := string(raw)

	for _, want := range []string{
		email, "My team dinner", "Friday", // account, own poll and its options
		"My office hours",                      // own booking page
		"Book club", "Me As Guest", "ifneedbe", // own guest answer elsewhere, matched by e-mail
		"My own remark",       // own comment
		"My own booking note", // own booking as a visitor
	} {
		if !strings.Contains(body, want) {
			t.Errorf("export is missing %q", want)
		}
	}
	for _, leak := range []string{
		"Bob Stranger", "bob@stranger.example", "Bob private remark", // stranger on own poll
		"Carol Visitor", "carol@stranger.example", "Carol private note", // stranger on own page
		"Dave Signed In", "Dave private remark", // another account that typed our address
		"SECRET-EDIT-HASH", "$argon2", // credentials
	} {
		if strings.Contains(body, leak) {
			t.Errorf("export contains %q, which is not the user's to take", leak)
		}
	}

	// A password account lists "password" as a sign-in method — the method, never the hash.
	methods, _ := doc["signInMethods"].([]any)
	if len(methods) != 1 || methods[0].(map[string]any)["provider"] != "password" {
		t.Errorf("signInMethods = %v, want exactly [password]", doc["signInMethods"])
	}

	// Strangers' responses still show up, as counts, on the user's own poll and page.
	polls, _ := doc["pollsCreated"].([]any)
	if len(polls) != 1 {
		t.Fatalf("pollsCreated = %v, want one poll", doc["pollsCreated"])
	}
	if got := polls[0].(map[string]any)["participantCount"]; got != float64(1) {
		t.Errorf("own poll participantCount = %v, want 1", got)
	}
	pages, _ := doc["bookingPagesCreated"].([]any)
	if len(pages) != 1 {
		t.Fatalf("bookingPagesCreated = %v, want one page", doc["bookingPagesCreated"])
	}
	if got := pages[0].(map[string]any)["bookingCount"]; got != float64(1) {
		t.Errorf("own page bookingCount = %v, want 1", got)
	}
}

// TestExportMe_RequiresAVerifiedSession: the export matches guest answers and bookings by
// e-mail address, so an address the caller has not proven must never unlock it.
func TestExportMe_RequiresAVerifiedSession(t *testing.T) {
	h := newAccountHarness(t)
	h.mustStatus(h.do(http.MethodGet, "/api/v1/me/export", nil), http.StatusUnauthorized, "anonymous export")

	email := "unverified-exporter@example.com"
	h.signUp(email)
	h.signIn(email)
	body := h.mustStatus(h.do(http.MethodGet, "/api/v1/me/export", nil), http.StatusForbidden, "unverified export")
	if h.errorCode(body) != "email_unverified" {
		t.Errorf("error.code = %q, want email_unverified", h.errorCode(body))
	}
}

// TestExportMe_IsRateLimited: the export is the most expensive read in the app.
func TestExportMe_IsRateLimited(t *testing.T) {
	h := newAccountHarness(t)
	email := "busy-exporter@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)
	for i := 0; i < 5; i++ {
		h.mustStatus(h.do(http.MethodGet, "/api/v1/me/export", nil), http.StatusOK, "export within budget")
	}
	h.mustStatus(h.do(http.MethodGet, "/api/v1/me/export", nil), http.StatusTooManyRequests, "export over budget")
}

// TestExportMe_RefusesCrossSiteRequests: a hostile page could otherwise navigate a signed-in
// victim to the export (SameSite=Lax sends the cookie on top-level GETs), dropping copies of their
// data into Downloads and burning their hourly budget. The SPA always fetches it same-origin.
func TestExportMe_RefusesCrossSiteRequests(t *testing.T) {
	h := newAccountHarness(t)
	email := "cross-site-exporter@example.com"
	h.signUp(email)
	h.verify(email)
	h.signIn(email)

	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/api/v1/me/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	resp, err := h.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	h.mustStatus(resp, http.StatusForbidden, "cross-site export")

	// Refused requests must not count against the budget: five real ones still succeed.
	for i := 0; i < 5; i++ {
		h.mustStatus(h.do(http.MethodGet, "/api/v1/me/export", nil), http.StatusOK, "same-origin export")
	}
}

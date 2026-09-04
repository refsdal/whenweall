package bookings_test

// Tests internal/bookings/handlers.go: httptest through the full middleware chain (session
// resolution -> route -> handler), using a fakeAuth standing in for *auth.Service via the
// bookings.Auth seam — the same rationale internal/polls/handlers_test.go's own identical fake
// gives (a real signup/signin flow through Limen for every case here would be prohibitively slow
// and would mostly test Limen, not this package's handlers). One test per endpoint-table row, plus
// a dedicated case (folded into the row it gates, and noted there) for each of this task's
// accumulated requirements (a)-(e).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// ---- test harness -------------------------------------------------------------------------

// fakeAuth implements bookings.Auth without touching Limen — the same shape
// internal/polls/handlers_test.go's own fakeAuth already established for the identical seam.
type fakeAuth struct {
	sessions map[string]*auth.Session
}

func newFakeAuth() *fakeAuth {
	return &fakeAuth{sessions: map[string]*auth.Session{}}
}

func (f *fakeAuth) login(sess *auth.Session) string {
	f.sessions[sess.UserID] = sess
	return sess.UserID
}

type fakeSessionKey struct{}

func (f *fakeAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := r.Header.Get("X-Test-Session"); id != "" {
			if sess, ok := f.sessions[id]; ok {
				r = r.WithContext(context.WithValue(r.Context(), fakeSessionKey{}, sess))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (f *fakeAuth) RequireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := f.FromContext(r.Context()); !ok {
			httpserver.Err(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
			return
		}
		next(w, r)
	}
}

func (f *fakeAuth) FromContext(ctx context.Context) (*auth.Session, bool) {
	sess, ok := ctx.Value(fakeSessionKey{}).(*auth.Session)
	return sess, ok
}

func (f *fakeAuth) VerifyGuestToken(token string) (string, bool) {
	const prefix = "guest-token-for-"
	if !strings.HasPrefix(token, prefix) {
		return "", false
	}
	return strings.TrimPrefix(token, prefix), true
}

func (f *fakeAuth) MintGuestToken(participantID string) string {
	return "guest-token-for-" + participantID
}

func sessHeader(userID string) map[string]string {
	return map[string]string{"X-Test-Session": userID}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "https://whenweall.example", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// testConfigWithTurnstile is testConfig plus a configured (fake) Turnstile key pair, so
// cfg.Capabilities.Turnstile is on — used by the captcha-gating tests.
func testConfigWithTurnstile(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "https://whenweall.example", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
		"TURNSTILE_SITE_KEY": "site-key", "TURNSTILE_SECRET_KEY": "secret-key",
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// newTestHandler builds a *bookings.Service bound to d, registers its whole HTTP surface on a
// fresh mux via a fakeAuth, and wraps that mux in the fake's own Middleware — the same shape
// internal/polls/handlers_test.go's own newTestHandler builds for the sibling package.
func newTestHandler(d *sql.DB, cfg *config.Config) (http.Handler, *fakeAuth, *bookings.Service) {
	s := bookings.NewService(cfg, d)
	a := newFakeAuth()
	mux := http.NewServeMux()
	s.Register(mux, a, cfg)
	return a.Middleware(mux), a, s
}

func doRequest(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response body %s: %v", rec.Body.String(), err)
	}
	return out
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope %s: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func errFields(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var body struct {
		Error struct {
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error envelope %s: %v", rec.Body.String(), err)
	}
	return body.Error.Fields
}

// addOrgMember inserts an organization_members row (+ its organization_member_roles row) directly
// via SQL — mirrors internal/polls/participants_test.go's own helper of the same name (needed here
// for the same reason: canManageContent's role check, authz.go, is a real DB query, not a
// precomputed value the caller hands in).
func addOrgMember(t *testing.T, d *sql.DB, orgID, userID, role string) {
	t.Helper()
	ctx := context.Background()
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		t.Fatalf("parse orgID: %v", err)
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		t.Fatalf("parse userID: %v", err)
	}

	var memberID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2) RETURNING id`,
		orgIDInt, userIDInt,
	).Scan(&memberID); err != nil {
		t.Fatalf("seeding organization_member: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO organization_member_roles (member_id, organization_id, role) VALUES ($1, $2, $3)`,
		memberID, orgIDInt, role,
	); err != nil {
		t.Fatalf("seeding organization_member_role: %v", err)
	}
}

// setupHandlerPage builds a fresh httptest handler over a freshly seeded, bookable page — the
// handler-layer analog of bookings_test.go's own setupBookablePage, additionally registering the
// page's own creator as an "owner" org member (every owner-facing route needs SOME managing role
// to succeed at all) and returning the org id as a bare numeric string (Limen's own convention)
// alongside the *http.Handler/fakeAuth/creator id this file's tests actually drive requests
// through.
type handlerPage struct {
	h       http.Handler
	a       *fakeAuth
	s       *bookings.Service
	d       *sql.DB
	orgID   string
	ownerID string
	orgSlug string
	pageID  string
	slug    string
}

func setupHandlerPage(t *testing.T, cfg *config.Config) handlerPage {
	t.Helper()
	ctx := context.Background()
	d := testdb.New(t)
	h, a, s := newTestHandler(d, cfg)

	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "owner")
	a.login(&auth.Session{UserID: ownerID, ActiveOrgID: orgID})

	handle := fmt.Sprintf("handle-%d", seedSeq.Add(1))
	if err := s.SetOrgSlug(ctx, orgID, handle); err != nil {
		t.Fatalf("SetOrgSlug: %v", err)
	}
	page, err := s.CreatePage(ctx, orgID, ownerID, openPageInput(nil))
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	return handlerPage{h: h, a: a, s: s, d: d, orgID: orgID, ownerID: ownerID, orgSlug: handle, pageID: page.ID, slug: page.Slug}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// dateOnly formats t as the bare "YYYY-MM-DD" parseQueryDate expects — handlePublicAvailability's
// own ?from&to shape, distinct from every other route's own RFC3339 ?from&to (rfc3339, above).
func dateOnly(t time.Time) string { return t.UTC().Format("2006-01-02") }

// seedUser inserts a standalone user with no organization membership yet — mirrors
// internal/polls/service_test.go's own helper of the same name.
func seedUser(t *testing.T, d *sql.DB) string {
	t.Helper()
	n := seedSeq.Add(1)
	var uid int64
	if err := d.QueryRowContext(context.Background(),
		`INSERT INTO users (email, updated_at) VALUES ($1, now()) RETURNING id`,
		fmt.Sprintf("handler-user-%d@example.com", n),
	).Scan(&uid); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return fmt.Sprint(uid)
}

// ---- row 1: POST /api/v1/booking-pages (auth -> CreatePage) --------------------------------

func TestHandlerCreatePage(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, _ := newTestHandler(d, cfg)
	orgID, userID := seedOrgAndUser(t, d)
	a.login(&auth.Session{UserID: userID, ActiveOrgID: orgID})

	body := map[string]any{
		"slug": "intro-call", "title": "Intro call", "timezone": "UTC",
		"slotDurationMin": 30, "bufferBeforeMin": 0, "bufferAfterMin": 0,
		"minNoticeMin": 0, "maxDaysAhead": 60,
		"availability": map[string]any{"1": []map[string]string{{"start": "09:00", "end": "17:00"}}},
		"googleSync":   false, "reminders": true,
	}
	rec := doRequest(t, h, "POST", "/api/v1/booking-pages", body, sessHeader(userID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	view := decodeBody[bookings.PageView](t, rec)
	if view.Slug != "intro-call" || view.Title != "Intro call" {
		t.Errorf("view = %+v, want slug/title set", view)
	}

	t.Run("401 without a session", func(t *testing.T) {
		rec := doRequest(t, h, "POST", "/api/v1/booking-pages", body, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("422 invalid with field errors for a bad slug", func(t *testing.T) {
		bad := map[string]any{}
		for k, v := range body {
			bad[k] = v
		}
		bad["slug"] = "AB"
		rec := doRequest(t, h, "POST", "/api/v1/booking-pages", bad, sessHeader(userID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "invalid" {
			t.Errorf("code = %q, want invalid", errCode(t, rec))
		}
	})
}

// ---- row 2: GET /api/v1/booking-pages (auth -> ListMyPages) --------------------------------

func TestHandlerListMyPages(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages", nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	summaries := decodeBody[[]bookings.PageSummary](t, rec)
	if len(summaries) != 1 || summaries[0].ID != p.pageID {
		t.Errorf("summaries = %+v, want exactly the one seeded page", summaries)
	}
}

// ---- row 3: GET /api/v1/booking-pages/{id} (auth+org -> GetOwnedPage) ----------------------
//
// Also this task's accumulated requirement (a): a plain member (no owner/admin role, not the
// page's creator) is forbidden; the page's own creator, and a separate admin, both succeed; an
// unknown/wrong-org page id is a consistent 404 either way (never leaking whether it exists in
// another org).

func TestHandlerGetOwnedPage(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view := decodeBody[bookings.PageView](t, rec)
	if view.ID != p.pageID {
		t.Errorf("ID = %q, want %q", view.ID, p.pageID)
	}

	t.Run("requirement (a): a plain member is forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "forbidden" {
			t.Errorf("code = %q, want forbidden", errCode(t, rec))
		}
	})

	t.Run("requirement (a): an admin (not the creator) succeeds", func(t *testing.T) {
		adminID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, adminID, "admin")
		p.a.login(&auth.Session{UserID: adminID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(adminID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("401 without a session", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID, nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("404 for an unknown page id, same as a wrong-org one", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/missing", nil, sessHeader(p.ownerID))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}

		otherOrgID, otherUserID := seedOrgAndUser(t, p.d)
		addOrgMember(t, p.d, otherOrgID, otherUserID, "owner")
		p.a.login(&auth.Session{UserID: otherUserID, ActiveOrgID: otherOrgID})
		rec2 := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(otherUserID))
		if rec2.Code != http.StatusNotFound {
			t.Fatalf("wrong-org status = %d, want 404; body=%s", rec2.Code, rec2.Body)
		}
	})
}

// ---- row 4: PATCH /api/v1/booking-pages/{id} (auth+org -> UpdatePage) ----------------------

func TestHandlerUpdatePage(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	body := map[string]any{
		"slug": p.slug, "title": "Renamed", "description": "Detailed notes", "timezone": "UTC",
		"slotDurationMin": 45, "bufferBeforeMin": 0, "bufferAfterMin": 0,
		"minNoticeMin": 0, "maxDaysAhead": 60,
		"availability": map[string]any{"1": []map[string]string{{"start": "09:00", "end": "17:00"}}},
		"googleSync":   false, "reminders": true, "status": "active",
	}
	rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, body, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view := decodeBody[bookings.PageView](t, rec)
	if view.Title != "Renamed" || view.SlotDurationMin != 45 {
		t.Errorf("view = %+v, want Title=Renamed SlotDurationMin=45", view)
	}
	if view.Description == nil || *view.Description != "Detailed notes" {
		t.Fatalf("Description = %v, want \"Detailed notes\"", view.Description)
	}

	// Requirement (c): full-replace semantics — a second PATCH that simply omits "description"
	// (rather than sending it back unchanged) clears it, rather than leaving the value the FIRST
	// PATCH just set alone. This is what "the client rounds-trips GetOwnedPage first" (this
	// endpoint's own doc comment) is for: a real caller wanting to change one field must resend
	// every other field's current value, or lose it, exactly as demonstrated here.
	t.Run("requirement (c): omitting a previously-set field on a later PATCH clears it", func(t *testing.T) {
		noDescription := map[string]any{}
		for k, v := range body {
			noDescription[k] = v
		}
		delete(noDescription, "description")

		rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, noDescription, sessHeader(p.ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		view := decodeBody[bookings.PageView](t, rec)
		if view.Description != nil {
			t.Errorf("Description = %v, want nil (full-replace, not merged)", *view.Description)
		}
	})

	t.Run("requirement (a): a plain member is forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, body, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("422 when status is omitted (no default to active)", func(t *testing.T) {
		noStatus := map[string]any{}
		for k, v := range body {
			noStatus[k] = v
		}
		delete(noStatus, "status")

		rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, noStatus, sessHeader(p.ownerID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		if errFields(t, rec)["status"] == "" {
			t.Errorf("fields = %+v, want a status entry", errFields(t, rec))
		}
	})

	t.Run("422 when availability is omitted (never stored as JSON null)", func(t *testing.T) {
		noAvailability := map[string]any{}
		for k, v := range body {
			noAvailability[k] = v
		}
		delete(noAvailability, "availability")

		rec := doRequest(t, p.h, "PATCH", "/api/v1/booking-pages/"+p.pageID, noAvailability, sessHeader(p.ownerID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		if errFields(t, rec)["availability"] == "" {
			t.Errorf("fields = %+v, want an availability entry", errFields(t, rec))
		}
	})
}

// ---- row 5: DELETE /api/v1/booking-pages/{id} (auth+org -> DeletePage) ---------------------

func TestHandlerDeletePage(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	t.Run("requirement (a): a plain member is forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "DELETE", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	rec := doRequest(t, p.h, "DELETE", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var out struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ID != p.pageID || !out.Deleted {
		t.Errorf("out = %+v, want ID=%q Deleted=true", out, p.pageID)
	}

	// The page is gone now — a follow-up GetOwnedPage 404s.
	rec2 := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID, nil, sessHeader(p.ownerID))
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("status after delete = %d, want 404", rec2.Code)
	}
}

// ---- row 6: GET /api/v1/booking-pages/{id}/bookings (auth+org -> ListPageBookings) ---------

func TestHandlerListPageBookings(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	start := futureUTCSlot(3, 9, 0)
	makeBooking(t, p.d, p.pageID, start, "confirmed")

	from := rfc3339(start.Add(-time.Hour))
	to := rfc3339(start.Add(time.Hour))
	rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/bookings?from="+from+"&to="+to, nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	views := decodeBody[[]bookings.BookingView](t, rec)
	if len(views) != 1 {
		t.Fatalf("len(views) = %d, want 1", len(views))
	}

	t.Run("400 invalid for a malformed from/to", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/bookings?from=not-a-date&to="+to, nil, sessHeader(p.ownerID))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("requirement (a): a plain member is forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/bookings?from="+from+"&to="+to, nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})
}

// ---- row 7: POST /api/v1/org/handle (auth -> SetOrgSlug) -----------------------------------
//
// Also this task's accumulated requirement (b): a validation failure reports its field under the
// key "handle", not "slug".

func TestHandlerSetOrgSlug(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig(t)
	h, a, _ := newTestHandler(d, cfg)
	orgID, userID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, userID, "owner")
	a.login(&auth.Session{UserID: userID, ActiveOrgID: orgID})

	// Owner-only (review fix, not requirement (a)'s wider creator-or-admin-or-owner gate): the
	// org's OWNER succeeds — see the "an admin is forbidden" case below for the other half.
	rec := doRequest(t, h, "POST", "/api/v1/org/handle", map[string]any{"handle": "my-handle"}, sessHeader(userID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	t.Run("requirement (b): a bad handle reports field \"handle\"", func(t *testing.T) {
		rec := doRequest(t, h, "POST", "/api/v1/org/handle", map[string]any{"handle": "AB"}, sessHeader(userID))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body)
		}
		fields := errFields(t, rec)
		if _, ok := fields["handle"]; !ok {
			t.Errorf("fields = %+v, want a \"handle\" entry", fields)
		}
		if _, ok := fields["slug"]; ok {
			t.Errorf("fields = %+v, want no \"slug\" entry", fields)
		}
	})

	t.Run("a plain member is forbidden", func(t *testing.T) {
		memberID := seedUser(t, d)
		addOrgMember(t, d, orgID, memberID, "member")
		a.login(&auth.Session{UserID: memberID, ActiveOrgID: orgID})

		rec := doRequest(t, h, "POST", "/api/v1/org/handle", map[string]any{"handle": "another-handle"}, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("review fix: an admin (not just a plain member) is forbidden — owner-only, not owner-or-admin", func(t *testing.T) {
		adminID := seedUser(t, d)
		addOrgMember(t, d, orgID, adminID, "admin")
		a.login(&auth.Session{UserID: adminID, ActiveOrgID: orgID})

		rec := doRequest(t, h, "POST", "/api/v1/org/handle", map[string]any{"handle": "admin-set-handle"}, sessHeader(adminID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "forbidden" {
			t.Errorf("code = %q, want forbidden", errCode(t, rec))
		}
	})
}

// ---- row 8: GET /api/v1/book/{org}/{page} (public -> GetPublicPage) ------------------------

func TestHandlerGetPublicPage(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	rec := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/"+p.slug, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view := decodeBody[bookings.PublicPageView](t, rec)
	if view.Slug != p.slug || view.Handle != p.orgSlug {
		t.Errorf("view = %+v, want Slug=%q Handle=%q", view, p.slug, p.orgSlug)
	}

	t.Run("404 JSON for an unknown handle/slug", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/no-such-page", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "not_found" {
			t.Errorf("code = %q, want not_found", errCode(t, rec))
		}
	})

	// Reads (page, availability, manage, .ics) have their own, roomier bucket: the SPA fetches
	// page + availability per month view, again after the timezone correction, and again on every
	// page.changed — a visitor flipping through months must never hit the 20/min budget that
	// exists to slow down booking/cancel/reschedule abuse.
	t.Run("read bucket: PublicReadRateLimit per minute, separate from the mutating bucket", func(t *testing.T) {
		p2 := setupHandlerPage(t, testConfig(t))
		var last *httptest.ResponseRecorder
		for i := 0; i < bookings.PublicReadRateLimit; i++ {
			last = doRequest(t, p2.h, "GET", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug, nil, nil)
			if last.Code != http.StatusOK {
				t.Fatalf("read %d: status = %d, want 200; body=%s", i+1, last.Code, last.Body)
			}
		}
		last = doRequest(t, p2.h, "GET", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug, nil, nil)
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("read %d: status = %d, want 429; body=%s", bookings.PublicReadRateLimit+1, last.Code, last.Body)
		}
		if errCode(t, last) != "rate_limited" {
			t.Errorf("code = %q, want rate_limited", errCode(t, last))
		}

		// Exhausting the read bucket leaves the mutating bucket untouched: a booking still lands.
		rec := doRequest(t, p2.h, "POST", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug+"/bookings", bookBody(futureUTCSlot(3, 9, 0)), nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("Book after read-bucket exhaustion: status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("mutating bucket: PublicBookRateLimit per minute, and exhausting it leaves reads alone", func(t *testing.T) {
		p2 := setupHandlerPage(t, testConfig(t))
		var last *httptest.ResponseRecorder
		for i := 0; i < bookings.PublicBookRateLimit; i++ {
			// A cancel against an unknown id is a cheap, always-404 mutating request that still
			// counts against the bucket (the limiter runs before the handler).
			last = doRequest(t, p2.h, "POST", "/api/v1/bookings/missing/cancel?t=x", nil, nil)
			if last.Code != http.StatusNotFound {
				t.Fatalf("cancel %d: status = %d, want 404; body=%s", i+1, last.Code, last.Body)
			}
		}
		last = doRequest(t, p2.h, "POST", "/api/v1/bookings/missing/cancel?t=x", nil, nil)
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("cancel %d: status = %d, want 429; body=%s", bookings.PublicBookRateLimit+1, last.Code, last.Body)
		}

		rec := doRequest(t, p2.h, "GET", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET page after book-bucket exhaustion: status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
	})
}

// ---- row 9: GET /api/v1/book/{org}/{page}/availability (public -> PublicAvailability) ------

func TestHandlerPublicAvailability(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	from := dateOnly(time.Now().UTC())
	to := dateOnly(time.Now().UTC().Add(72 * time.Hour))

	rec := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/availability?from="+from+"&to="+to, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Slots []string `json:"slots"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Slots) == 0 {
		t.Errorf("Slots is empty, want at least one bookable slot in the next 72h")
	}

	t.Run("404 for an unknown page", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/no-such-page/availability?from="+from+"&to="+to, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("400 invalid for a malformed from/to", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/availability?from=nope&to="+to, nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
	})

	// I3: publicAvailabilityQuerySchema's own window cap (schemas.ts, LIMITS.publicWindowDays).
	t.Run("I3: 400 invalid for a window wider than 62 days", func(t *testing.T) {
		now := time.Now().UTC()
		wideTo := dateOnly(now.Add(63 * 24 * time.Hour))
		rec := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/availability?from="+dateOnly(now)+"&to="+wideTo, nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "invalid" {
			t.Errorf("code = %q, want invalid", errCode(t, rec))
		}

		// Exactly 62 days is still accepted.
		exactTo := dateOnly(now.Add(62 * 24 * time.Hour))
		rec2 := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/availability?from="+dateOnly(now)+"&to="+exactTo, nil, nil)
		if rec2.Code != http.StatusOK {
			t.Fatalf("62-day window: status = %d, want 200; body=%s", rec2.Code, rec2.Body)
		}
	})

	t.Run("I3: 400 invalid when to is before from", func(t *testing.T) {
		now := time.Now().UTC()
		rec := doRequest(t, p.h, "GET", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/availability?from="+dateOnly(now)+"&to="+dateOnly(now.Add(-48*time.Hour)), nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body)
		}
	})
}

// ---- row 10: POST /api/v1/book/{org}/{page}/bookings (public+captcha -> Book) --------------

func bookBody(start time.Time) map[string]any {
	return map[string]any{
		"startAt": rfc3339(start), "name": "Visitor", "email": "visitor@example.com", "timezone": "UTC",
	}
}

func TestHandlerBook(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	start := futureUTCSlot(3, 9, 0)

	rec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Booking     bookings.BookingView `json:"booking"`
		ManageToken string               `json:"manageToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Booking.VisitorEmail != "visitor@example.com" || len(out.ManageToken) != 43 {
		t.Errorf("out = %+v, want a matching booking + a 43-char manageToken", out)
	}

	t.Run("409 slot_taken for a double-booked slot", func(t *testing.T) {
		rec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "slot_taken" {
			t.Errorf("code = %q, want slot_taken", errCode(t, rec))
		}
	})

	t.Run("403 captcha_failed for an anonymous caller with no/bad token when Turnstile is on", func(t *testing.T) {
		p2 := setupHandlerPage(t, testConfigWithTurnstile(t))
		rec := doRequest(t, p2.h, "POST", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug+"/bookings", bookBody(futureUTCSlot(4, 9, 0)), nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "captcha_failed" {
			t.Errorf("code = %q, want captcha_failed", errCode(t, rec))
		}
	})
}

// ---- row 11: GET /api/v1/bookings/{id}/manage (public(token) -> ManagedBooking) ------------
//
// Also this task's accumulated requirement (d): a wrong manage token is 403 invalid_token, not
// 404.

func TestHandlerManagedBooking(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	start := futureUTCSlot(3, 9, 0)
	bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
	if bookRec.Code != http.StatusCreated {
		t.Fatalf("Book: status = %d; body=%s", bookRec.Code, bookRec.Body)
	}
	booked := decodeBody[struct {
		Booking     bookings.BookingView `json:"booking"`
		ManageToken string               `json:"manageToken"`
	}](t, bookRec)

	rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/manage?t="+booked.ManageToken, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	view := decodeBody[bookings.ManagedBookingView](t, rec)
	if view.ID != booked.Booking.ID || view.Page.Slug != p.slug {
		t.Errorf("view = %+v, want ID=%q Page.Slug=%q", view, booked.Booking.ID, p.slug)
	}

	t.Run("requirement (d): a wrong token is 403 invalid_token", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/manage?t=wrong-token", nil, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "invalid_token" {
			t.Errorf("code = %q, want invalid_token", errCode(t, rec))
		}
	})

	t.Run("404 for an unknown booking id", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/missing/manage?t=whatever", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
	})

	// I6: the same no-token-but-a-session organiser fallback handleCancel's own requirement (e)
	// already has — see this file's Register doc comment's own I6 note.
	t.Run("I6: the page's own creator views with no token, byOrganiser", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/manage", nil, sessHeader(p.ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		view := decodeBody[bookings.ManagedBookingView](t, rec)
		if view.ID != booked.Booking.ID {
			t.Errorf("ID = %q, want %q", view.ID, booked.Booking.ID)
		}
	})

	t.Run("I6: a plain member with no token is forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/manage", nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("I6: no token and no session is 401", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/manage", nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body)
		}
	})

	// M1: this endpoint was unmetered — an anonymous caller could brute-force a booking's
	// 43-character manage token with no rate limit at all. It sits in the read bucket
	// (PublicReadRateLimit/min, Register's own doc comment), separate from the Book call that
	// created the booking, so exactly PublicReadRateLimit lookups succeed and the next one 429s.
	t.Run("M1: the read rate limiter applies", func(t *testing.T) {
		p2 := setupHandlerPage(t, testConfig(t))
		start2 := futureUTCSlot(3, 9, 0)
		bookRec2 := doRequest(t, p2.h, "POST", "/api/v1/book/"+p2.orgSlug+"/"+p2.slug+"/bookings", bookBody(start2), nil)
		booked2 := decodeBody[struct {
			Booking     bookings.BookingView `json:"booking"`
			ManageToken string               `json:"manageToken"`
		}](t, bookRec2)

		var last *httptest.ResponseRecorder
		for i := 0; i < bookings.PublicReadRateLimit; i++ {
			last = doRequest(t, p2.h, "GET", "/api/v1/bookings/"+booked2.Booking.ID+"/manage?t="+booked2.ManageToken, nil, nil)
			if last.Code != http.StatusOK {
				t.Fatalf("lookup %d: status = %d, want 200; body=%s", i+1, last.Code, last.Body)
			}
		}
		last = doRequest(t, p2.h, "GET", "/api/v1/bookings/"+booked2.Booking.ID+"/manage?t="+booked2.ManageToken, nil, nil)
		if last.Code != http.StatusTooManyRequests {
			t.Fatalf("lookup %d: status = %d, want 429; body=%s", bookings.PublicReadRateLimit+1, last.Code, last.Body)
		}
		if errCode(t, last) != "rate_limited" {
			t.Errorf("code = %q, want rate_limited", errCode(t, last))
		}
	})
}

// ---- row 11b: GET /api/v1/bookings/{id}/calendar.ics (public(token) -> BookingICS) ---------
//
// Carried fix: restores the .ics parity the TS backend had — a visitor who lost the mailed
// attachment can still re-download the same invite off their manage link.

func TestHandlerBookingICS(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	start := futureUTCSlot(3, 9, 0)
	bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
	if bookRec.Code != http.StatusCreated {
		t.Fatalf("Book: status = %d; body=%s", bookRec.Code, bookRec.Body)
	}
	booked := decodeBody[struct {
		Booking     bookings.BookingView `json:"booking"`
		ManageToken string               `json:"manageToken"`
	}](t, bookRec)

	rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics?t="+booked.ManageToken, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/calendar; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/calendar; charset=utf-8", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "BEGIN:VCALENDAR") || !strings.Contains(body, "BEGIN:VEVENT") {
		t.Errorf("body missing VCALENDAR/VEVENT: %q", body)
	}
	if !strings.Contains(body, "UID:"+booked.Booking.ID+"@whenweall") {
		t.Errorf("body missing booking UID: %q", body)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="whenweall-booking-`+booked.Booking.ID+`.ics"` {
		t.Errorf("Content-Disposition = %q, want the whenweall-booking-{id}.ics attachment filename", cd)
	}

	t.Run("a wrong token is 403 invalid_token", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics?t=wrong-token", nil, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "invalid_token" {
			t.Errorf("code = %q, want invalid_token", errCode(t, rec))
		}
	})

	t.Run("no token and no session is 401 unauthenticated", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "unauthenticated" {
			t.Errorf("code = %q, want unauthenticated", errCode(t, rec))
		}
	})

	t.Run("an organiser session downloads without a token (the dashboard's own 'Add to calendar')", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, sessHeader(p.ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/calendar; charset=utf-8" {
			t.Errorf("Content-Type = %q, want text/calendar; charset=utf-8", ct)
		}
		if !strings.Contains(rec.Body.String(), "UID:"+booked.Booking.ID+"@whenweall") {
			t.Errorf("body missing booking UID: %q", rec.Body.String())
		}
	})

	t.Run("a same-org plain member (not creator, no managing role) is 403 forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "forbidden" {
			t.Errorf("code = %q, want forbidden", errCode(t, rec))
		}
	})

	t.Run("a session from another org is 404 (no cross-org existence leak)", func(t *testing.T) {
		otherOrgID, otherUserID := seedOrgAndUser(t, p.d)
		addOrgMember(t, p.d, otherOrgID, otherUserID, "owner")
		p.a.login(&auth.Session{UserID: otherUserID, ActiveOrgID: otherOrgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/"+booked.Booking.ID+"/calendar.ics", nil, sessHeader(otherUserID))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("404 for an unknown booking id", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/bookings/missing/calendar.ics?t=whatever", nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
	})
}

// ---- row 12: POST /api/v1/bookings/{id}/cancel (public(token)|auth+org -> Cancel) ----------
//
// Also this task's accumulated requirement (e): without a token but with a session, the caller
// must manage the booking's page (creator-or-org-manager) before Cancel(byOrganiser: true) runs.

func TestHandlerCancel(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	t.Run("a visitor's manage token cancels", func(t *testing.T) {
		start := futureUTCSlot(3, 9, 0)
		bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
		booked := decodeBody[struct {
			Booking     bookings.BookingView `json:"booking"`
			ManageToken string               `json:"manageToken"`
		}](t, bookRec)

		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/cancel?t="+booked.ManageToken, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("requirement (d): a wrong token is 403 invalid_token", func(t *testing.T) {
		start := futureUTCSlot(3, 10, 0)
		bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
		booked := decodeBody[struct {
			Booking bookings.BookingView `json:"booking"`
		}](t, bookRec)

		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/cancel?t=wrong-token", nil, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "invalid_token" {
			t.Errorf("code = %q, want invalid_token", errCode(t, rec))
		}
	})

	t.Run("requirement (e): the page's own creator cancels with no token, byOrganiser", func(t *testing.T) {
		start := futureUTCSlot(3, 11, 0)
		bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
		booked := decodeBody[struct {
			Booking bookings.BookingView `json:"booking"`
		}](t, bookRec)

		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/cancel", nil, sessHeader(p.ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("requirement (e): a plain member with no token is forbidden", func(t *testing.T) {
		start := futureUTCSlot(3, 12, 0)
		bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
		booked := decodeBody[struct {
			Booking bookings.BookingView `json:"booking"`
		}](t, bookRec)

		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/cancel", nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("requirement (e): no token and no session is 401", func(t *testing.T) {
		start := futureUTCSlot(3, 13, 0)
		bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
		booked := decodeBody[struct {
			Booking bookings.BookingView `json:"booking"`
		}](t, bookRec)

		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/cancel", nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body)
		}
	})
}

// ---- row 13: POST /api/v1/bookings/{id}/reschedule (public(token) -> Reschedule) -----------

func TestHandlerReschedule(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))
	start := futureUTCSlot(3, 9, 0)
	bookRec := doRequest(t, p.h, "POST", "/api/v1/book/"+p.orgSlug+"/"+p.slug+"/bookings", bookBody(start), nil)
	booked := decodeBody[struct {
		Booking     bookings.BookingView `json:"booking"`
		ManageToken string               `json:"manageToken"`
	}](t, bookRec)

	newStart := futureUTCSlot(3, 11, 0)
	rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/reschedule?t="+booked.ManageToken,
		map[string]any{"startAt": rfc3339(newStart)}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Booking bookings.BookingView `json:"booking"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Booking.StartAt == booked.Booking.StartAt {
		t.Errorf("StartAt unchanged, want the rescheduled time")
	}

	t.Run("requirement (d): a wrong token is 403 invalid_token", func(t *testing.T) {
		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/reschedule?t=wrong-token",
			map[string]any{"startAt": rfc3339(futureUTCSlot(3, 13, 0))}, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
		if errCode(t, rec) != "invalid_token" {
			t.Errorf("code = %q, want invalid_token", errCode(t, rec))
		}
	})

	// I6: the same no-token-but-a-session organiser fallback handleCancel's own requirement (e)
	// already has — see this file's Register doc comment's own I6 note.
	t.Run("I6: the page's own creator reschedules with no token, byOrganiser", func(t *testing.T) {
		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/reschedule",
			map[string]any{"startAt": rfc3339(futureUTCSlot(3, 14, 0))}, sessHeader(p.ownerID))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("I6: a plain member with no token is forbidden", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/reschedule",
			map[string]any{"startAt": rfc3339(futureUTCSlot(3, 15, 0))}, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("I6: no token and no session is 401", func(t *testing.T) {
		rec := doRequest(t, p.h, "POST", "/api/v1/bookings/"+booked.Booking.ID+"/reschedule",
			map[string]any{"startAt": rfc3339(futureUTCSlot(3, 16, 0))}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body)
		}
	})
}

// ---- row 14: GET /api/v1/booking-pages/{id}/google-status (auth+org -> RequireManageablePage) --
//
// I2: this row was wired as a.RequireSession only (a plain session check, no org-scoping at all)
// — any signed-in user could probe whether ANY page id (in any org) has Google Calendar synced.
// Now gated by RequireManageablePage, the same requirement (a) check every other owner-facing
// route over a page id in this file uses.

func TestHandlerGoogleStatus(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	// The Google capability is off in testConfig (no client id/secret) — s.google stays nil, so
	// every page reports {"available":false} regardless of its own memberUserId.
	rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/google-status", nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var out struct {
		Available bool `json:"available"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Available {
		t.Errorf("Available = true, want false (nil GoogleSync)")
	}

	t.Run("401 without a session", func(t *testing.T) {
		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/google-status", nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})

	t.Run("403 for a plain member (not the creator, no managing role)", func(t *testing.T) {
		memberID := seedUser(t, p.d)
		addOrgMember(t, p.d, p.orgID, memberID, "member")
		p.a.login(&auth.Session{UserID: memberID, ActiveOrgID: p.orgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/google-status", nil, sessHeader(memberID))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
		}
	})

	t.Run("404 for a page belonging to a different org (I2: cross-org probing)", func(t *testing.T) {
		otherOrgID, otherUserID := seedOrgAndUser(t, p.d)
		addOrgMember(t, p.d, otherOrgID, otherUserID, "owner")
		p.a.login(&auth.Session{UserID: otherUserID, ActiveOrgID: otherOrgID})

		rec := doRequest(t, p.h, "GET", "/api/v1/booking-pages/"+p.pageID+"/google-status", nil, sessHeader(otherUserID))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
	})
}

// ---- row 15: POST /api/v1/me/google/disconnect (auth) --------------------------------------

func TestHandlerDisconnectGoogle(t *testing.T) {
	p := setupHandlerPage(t, testConfig(t))

	rec := doRequest(t, p.h, "POST", "/api/v1/me/google/disconnect", nil, sessHeader(p.ownerID))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	t.Run("401 without a session", func(t *testing.T) {
		rec := doRequest(t, p.h, "POST", "/api/v1/me/google/disconnect", nil, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

// TestHandlerCreatePageRateLimited is internal/polls's TestHandlerCreateRateLimited for booking
// pages: POST /api/v1/booking-pages has its own 20/min per-IP 'create' bucket, outside the
// session gate.
func TestHandlerCreatePageRateLimited(t *testing.T) {
	d := testdb.New(t)
	h, _, _ := newTestHandler(d, testConfig(t))

	for i := 0; i < 20; i++ {
		rec := doRequest(t, h, "POST", "/api/v1/booking-pages", map[string]any{}, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401 (under budget, no session)", i+1, rec.Code)
		}
	}

	over := doRequest(t, h, "POST", "/api/v1/booking-pages", map[string]any{}, nil)
	if over.Code != http.StatusTooManyRequests {
		t.Fatalf("21st create: status = %d, want 429; body=%s", over.Code, over.Body)
	}
	if errCode(t, over) != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", errCode(t, over))
	}
}

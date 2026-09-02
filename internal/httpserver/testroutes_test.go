package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

// testConfigWithTestRoutes mirrors this file's sibling server_test.go's testConfig, plus
// EnableTestRoutes — Task 5's own seam is gated on it (RegisterTestRoutes is only ever called by
// cmd/whenweall when it's set, and config.Load already hard-fails APP_ENV=production alongside
// it, so leaving APP_ENV at its "development" default here is exactly the state cmd/whenweall's
// own gate allows).
func testConfigWithTestRoutes(t *testing.T) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "http://localhost:3000", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
		"ENABLE_TEST_ROUTES": "true",
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return cfg
}

// seedTestServer wires the same four pieces cmd/whenweall's serve() does (minus the realtime hub
// and jobs worker, which this route never touches) — a real Postgres via testdb, a real
// auth.Service (real Limen), and real polls/bookings Services — so RegisterTestRoutes drives its
// signup/session/poll/booking-page calls against the genuine seams, not stand-ins.
func seedTestServer(t *testing.T) (srv *httpserver.Server, authSvc *auth.Service, pollsSvc *polls.Service, bookingsSvc *bookings.Service) {
	t.Helper()
	d := testdb.New(t)
	cfg := testConfigWithTestRoutes(t)

	authSvc, err := auth.New(cfg, d)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	pollsSvc = polls.NewService(d)
	bookingsSvc = bookings.NewService(cfg, d)

	srv = httpserver.New(cfg, d, authSvc)
	srv.RegisterAPI(func(mux *http.ServeMux) {
		pollsSvc.Register(mux, authSvc, cfg)
		bookingsSvc.Register(mux, authSvc, cfg)
		httpserver.RegisterTestRoutes(mux, cfg, authSvc, pollsSvc, bookingsSvc)
	})
	return srv, authSvc, pollsSvc, bookingsSvc
}

func postSeed(t *testing.T, srv *httpserver.Server, body map[string]any) map[string]any {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal seed body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/test/seed", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /api/test/seed: status %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode seed response: %v: %s", err, rec.Body.String())
	}
	return out
}

// signIn drives POST /api/v1/auth/signin/credential through the full mounted stack (exactly the
// route a real e2e spec's login form submits to) and returns the session cookies it sets — the
// "user can sign in" half of Task 5's own test brief.
func signIn(t *testing.T, srv *httpserver.Server, email, password string) []*http.Cookie {
	t.Helper()
	body, err := json.Marshal(map[string]string{"credential": email, "password": password})
	if err != nil {
		t.Fatalf("marshal signin body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/signin/credential", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("signin/credential: status %d: %s", rec.Code, rec.Body.String())
	}
	return rec.Result().Cookies()
}

func stringField(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, _ := m[key].(string)
	if v == "" {
		t.Fatalf("seed response missing %q: %#v", key, m)
	}
	return v
}

func TestSeed_DefaultsToAVerifiedSignInableUser(t *testing.T) {
	srv, _, _, _ := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{})

	email := stringField(t, seeded, "email")
	password := stringField(t, seeded, "password")
	if got, _ := seeded["name"].(string); got != "Test User" {
		t.Errorf("name = %q, want the default %q", got, "Test User")
	}
	for _, key := range []string{"pollId", "pageId", "handle", "slug"} {
		if seeded[key] != nil {
			t.Errorf("%s = %v, want null for a plain seed with no with* flags set", key, seeded[key])
		}
	}

	// The whole point of this route: the returned credentials actually sign in.
	if cookies := signIn(t, srv, email, password); len(cookies) == 0 {
		t.Fatalf("signin/credential set no cookies")
	}
}

func TestSeed_WithPollCreatesAPollTheUserOwns(t *testing.T) {
	srv, _, pollsSvc, _ := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{"withPoll": true})
	pollID := stringField(t, seeded, "pollId")

	view, err := pollsSvc.GetView(context.Background(), pollID, polls.Viewer{})
	if err != nil {
		t.Fatalf("GetView(%q): %v", pollID, err)
	}
	if view.Title != "Seeded test poll" {
		t.Errorf("poll title = %q, want %q", view.Title, "Seeded test poll")
	}
	if len(view.Options) != 2 {
		t.Errorf("len(options) = %d, want 2", len(view.Options))
	}
}

func TestSeed_WithSignupCreatesASignUpSheet(t *testing.T) {
	srv, _, pollsSvc, _ := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{"withSignup": true})
	pollID := stringField(t, seeded, "pollId")

	view, err := pollsSvc.GetView(context.Background(), pollID, polls.Viewer{})
	if err != nil {
		t.Fatalf("GetView(%q): %v", pollID, err)
	}
	if view.Type != string(polls.PollTypeSignup) {
		t.Errorf("poll type = %q, want %q", view.Type, polls.PollTypeSignup)
	}
}

func TestSeed_WithBookingPageCreatesAPublicPage(t *testing.T) {
	srv, _, _, bookingsSvc := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{"withBookingPage": true})
	pageID := stringField(t, seeded, "pageId")
	handle := stringField(t, seeded, "handle")
	slug := stringField(t, seeded, "slug")
	if slug != "intro-call" {
		t.Errorf("slug = %q, want %q", slug, "intro-call")
	}

	public, err := bookingsSvc.GetPublicPage(context.Background(), handle, slug)
	if err != nil {
		t.Fatalf("GetPublicPage(%q, %q): %v", handle, slug, err)
	}
	if public == nil {
		t.Fatalf("GetPublicPage(%q, %q) = nil, want the seeded page", handle, slug)
	}
	_ = pageID
}

// TestSeed_StaffRoleForwarded is Task 5's own regression test for the a442f9f lesson: a seeded
// "staff" user must actually pass RequireStaff once signed in, not just carry the role in the
// seed response.
func TestSeed_StaffRoleForwarded(t *testing.T) {
	srv, authSvc, _, _ := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{"role": "staff"})
	email := stringField(t, seeded, "email")
	password := stringField(t, seeded, "password")

	cookies := signIn(t, srv, email, password)

	rec := requireStaffProbe(authSvc, cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("RequireStaff status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestSeed_OrdinaryUserFailsRequireStaff is TestSeed_StaffRoleForwarded's contrast case: without
// `role: "staff"`, the same probe must reject — proving the previous test isn't passing simply
// because RequireStaff lets everyone through.
func TestSeed_OrdinaryUserFailsRequireStaff(t *testing.T) {
	srv, authSvc, _, _ := seedTestServer(t)

	seeded := postSeed(t, srv, map[string]any{})
	email := stringField(t, seeded, "email")
	password := stringField(t, seeded, "password")

	cookies := signIn(t, srv, email, password)

	rec := requireStaffProbe(authSvc, cookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("RequireStaff status = %d, want 403 for a non-staff user", rec.Code)
	}
}

func requireStaffProbe(authSvc *auth.Service, cookies []*http.Cookie) *httptest.ResponseRecorder {
	handler := authSvc.Middleware(authSvc.RequireStaff(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/probe/staff", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestSeed_NotMountedWithoutRegisterTestRoutes proves the gate lives in cmd/whenweall's own call
// site (RegisterTestRoutes is only ever invoked when cfg.EnableTestRoutes is true) rather than
// inside this route itself: a server that never calls it has no /api/test/seed at all, same 404
// any other unmatched /api/ path gets.
func TestSeed_NotMountedWithoutRegisterTestRoutes(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfigWithTestRoutes(t)
	authSvc, err := auth.New(cfg, d)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	srv := httpserver.New(cfg, d, authSvc)
	// No RegisterTestRoutes call.

	req := httptest.NewRequest(http.MethodPost, "/api/test/seed", bytes.NewReader([]byte("{}")))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when the route was never registered", rec.Code)
	}
}

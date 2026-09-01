package httpserver_test

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

func testConfig() *config.Config {
	cfg, _, err := config.Load(map[string]string{
		"APP_URL": "http://localhost:3000", "DATABASE_URL": "postgres://unused/unused",
		"AUTH_SECRET": strings.Repeat("s", 32), "SMTP_HOST": "localhost",
	})
	if err != nil {
		panic(err)
	}
	return cfg
}

// testAuthService builds a real auth.Service against d, the same way cmd/whenweall does.
func testAuthService(t *testing.T, cfg *config.Config, d *sql.DB) *auth.Service {
	t.Helper()
	svc, err := auth.New(cfg, d)
	if err != nil {
		t.Fatalf("auth.New: %v", err)
	}
	return svc
}

func TestHealthzOK(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff header = %q", got)
	}
}

// TestAPIOnlySkipsSessionResolutionOutsideAPI is I9's regression test: a request to a path
// outside /api/ must never have a session resolved for it, even carrying a perfectly valid
// session cookie — static assets and the SPA shell need no identity, and everything that does
// (including plan 4's websockets) lives under /api/, so resolveSession's database work has no
// reason to run on every asset request a browser makes. This builds its own small mux (rather
// than a full Server, which has no route to probe FromContext through) mirroring exactly how
// Server.Handler wires httpserver.APIOnly around authSvc.Middleware.
func TestAPIOnlySkipsSessionResolutionOutsideAPI(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	authSvc := testAuthService(t, cfg, d)

	probe := func(w http.ResponseWriter, r *http.Request) {
		_, ok := auth.FromContext(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"sessionResolved": ok})
	}

	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/", authSvc.Handler())
	mux.HandleFunc("/probe", probe)     // outside /api/ — must never see a session
	mux.HandleFunc("/api/probe", probe) // under /api/ — must see one

	ts := httptest.NewServer(httpserver.APIOnly(authSvc.Middleware)(mux))
	defer ts.Close()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}

	email := "gate-check@example.com"
	const password = "Str0ngPassw0rd"

	postJSON := func(path string, body map[string]any) *http.Response {
		t.Helper()
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		resp, err := client.Post(ts.URL+path, "application/json", strings.NewReader(string(b)))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return resp
	}
	requireOK := func(resp *http.Response, what string) {
		t.Helper()
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode/100 != 2 {
			respBody, _ := io.ReadAll(resp.Body)
			t.Fatalf("%s: status %d: %s", what, resp.StatusCode, respBody)
		}
	}

	requireOK(postJSON("/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": password,
	}), "signup")
	requireOK(postJSON("/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": password,
	}), "signin")

	// The cookie jar now carries a valid session cookie for every following request on this
	// client, same as a real browser.
	decode := func(path string) bool {
		t.Helper()
		resp, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body map[string]bool
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("GET %s: decode response: %v", path, err)
		}
		return body["sessionResolved"]
	}

	if got := decode("/probe"); got {
		t.Error("GET /probe (outside /api/) resolved a session; want none — APIOnly should have skipped Middleware entirely")
	}
	if got := decode("/api/probe"); !got {
		t.Error("GET /api/probe resolved no session; want the valid cookie's session to be resolved")
	}
}

func TestHealthzDegradedWhenDBDown(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	authSvc := testAuthService(t, cfg, d)
	_ = d.Close() // kill the pool
	srv := httpserver.New(cfg, d, authSvc)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

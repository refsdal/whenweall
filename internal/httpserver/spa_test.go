package httpserver_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestSPAFallback(t *testing.T) {
	d := testdb.New(t)
	srv := httpserver.New(testConfig(), d)
	for _, path := range []string{"/", "/dashboard", "/p/abc123"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Errorf("%s status = %d", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "whenweall") {
			t.Errorf("%s did not serve index.html", path)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("%s Cache-Control = %q", path, cc)
		}
	}
}

func TestUnknownAPIPathIs404NotSPA(t *testing.T) {
	d := testdb.New(t)
	srv := httpserver.New(testConfig(), d)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/nope", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestAssetMissIs404NotSPA guards against a missing built asset silently falling back to
// index.html: that would look like a stale build succeeding instead of loudly failing.
func TestAssetMissIs404NotSPA(t *testing.T) {
	d := testdb.New(t)
	srv := httpserver.New(testConfig(), d)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/assets/does-not-exist.js", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "whenweall") {
		t.Error("missing asset served index.html instead of a 404")
	}
}

// TestIndexHTMLExactMatchGetsNoCache guards against GET /index.html (the exact-file branch, not
// the fallback branch) being served without a Cache-Control header — a client could otherwise
// cache the app shell indefinitely.
func TestIndexHTMLExactMatchGetsNoCache(t *testing.T) {
	d := testdb.New(t)
	srv := httpserver.New(testConfig(), d)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/index.html", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

// TestDotfileBasenameIs404 guards against the embedded dist/.gitignore (present because the
// "all:dist" embed directive pulls in the whole dist/ tree) ever being served to a client.
func TestDotfileBasenameIs404(t *testing.T) {
	d := testdb.New(t)
	srv := httpserver.New(testConfig(), d)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/.gitignore", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

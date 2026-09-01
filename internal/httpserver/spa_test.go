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

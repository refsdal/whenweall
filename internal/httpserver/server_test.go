package httpserver_test

import (
	"net/http/httptest"
	"strings"
	"testing"

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

func TestHealthzOK(t *testing.T) {
	d := testdb.New(t)
	srv := httpserver.New(testConfig(), d)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("nosniff header = %q", got)
	}
}

func TestHealthzDegradedWhenDBDown(t *testing.T) {
	d := testdb.New(t)
	d.Close() // kill the pool
	srv := httpserver.New(testConfig(), d)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 503 {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestAPIBodyLimitedToOneMiB drives a probe route through the real Server.Handler() chain: a
// small JSON body decodes normally, a body one byte over 1 MiB is answered 413 with the
// payload_too_large envelope (not a 400 "malformed JSON", which is what a bare MaxBytesReader
// error looks like to json.Decoder).
func TestAPIBodyLimitedToOneMiB(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	srv.RegisterAPI(func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/v1/probe-body", func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if !httpserver.DecodeJSON(w, r, &body) {
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})
	h := srv.Handler()

	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/probe-body", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := post(`{"ok":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("small body: status = %d, want 204; body=%s", rec.Code, rec.Body)
	}

	huge := `{"pad":"` + strings.Repeat("a", 1<<20) + `"}` // > 1 MiB once the wrapper is counted
	rec := post(huge)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: status = %d, want 413; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"code":"payload_too_large"`) {
		t.Errorf("oversized body envelope = %s, want code payload_too_large", rec.Body)
	}
}

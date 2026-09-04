package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
)

func TestCheckOriginCrossOriginPostRejected(t *testing.T) {
	h := httpserver.CheckOrigin("https://whenweall.example")(okHandler())

	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"code":"bad_origin"`) {
		t.Errorf("body = %q, want code bad_origin", got)
	}
}

func TestCheckOriginSameOriginPostPasses(t *testing.T) {
	h := httpserver.CheckOrigin("https://whenweall.example")(okHandler())

	r := httptest.NewRequest("POST", "/x", nil)
	r.Header.Set("Origin", "https://whenweall.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestCheckOriginCrossOriginGetPasses(t *testing.T) {
	h := httpserver.CheckOrigin("https://whenweall.example")(okHandler())

	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (GET is never checked)", rec.Code)
	}
}

func TestCheckOriginNoHeaderPostPasses(t *testing.T) {
	h := httpserver.CheckOrigin("https://whenweall.example")(okHandler())

	r := httptest.NewRequest("POST", "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (no Origin header is not evidence of forgery)", rec.Code)
	}
}

func TestCheckOriginOtherMutatingMethods(t *testing.T) {
	h := httpserver.CheckOrigin("https://whenweall.example")(okHandler())

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := httptest.NewRequest(method, "/x", nil)
		r.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s cross-origin: status = %d, want 403", method, rec.Code)
		}
	}
}

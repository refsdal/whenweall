package httpserver_test

// Ports src/server/http/__tests__/turnstile.workers.test.ts's cases (verifyTurnstile succeeds/
// fails/no-token-no-request, requireTurnstile forwards remoteip and throws CAPTCHA_FAILED) onto
// VerifyTurnstile/RequireCaptcha, plus the task brief's own required timeout->fail-closed case
// (not present in the TS suite, which runs against a mocked network with no real timeout to hit).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
)

// withSiteverifyStub points httpserver.TurnstileSiteverifyURL at ts's URL for the duration of the
// test, restoring the real Cloudflare endpoint on cleanup — TurnstileSiteverifyURL exists
// specifically so an external test package (this one) has somewhere to inject a stub.
func withSiteverifyStub(t *testing.T, ts *httptest.Server) {
	t.Helper()
	orig := httpserver.TurnstileSiteverifyURL
	httpserver.TurnstileSiteverifyURL = ts.URL
	t.Cleanup(func() {
		httpserver.TurnstileSiteverifyURL = orig
		ts.Close()
	})
}

func jsonSuccess(success bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": success})
	}
}

func TestVerifyTurnstileSucceeds(t *testing.T) {
	ts := httptest.NewServer(jsonSuccess(true))
	withSiteverifyStub(t, ts)

	if err := httpserver.VerifyTurnstile(context.Background(), "secret", "tok", ""); err != nil {
		t.Errorf("VerifyTurnstile = %v, want nil", err)
	}
}

func TestVerifyTurnstileFails(t *testing.T) {
	ts := httptest.NewServer(jsonSuccess(false))
	withSiteverifyStub(t, ts)

	if err := httpserver.VerifyTurnstile(context.Background(), "secret", "tok", ""); err == nil {
		t.Error("VerifyTurnstile = nil, want an error for success:false")
	}
}

func TestVerifyTurnstileNoTokenSkipsRequest(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		jsonSuccess(true)(w, r)
	}))
	withSiteverifyStub(t, ts)

	if err := httpserver.VerifyTurnstile(context.Background(), "secret", "", ""); err == nil {
		t.Error("VerifyTurnstile = nil, want an error for an empty token")
	}
	if called {
		t.Error("siteverify was called despite an empty token")
	}
}

func TestVerifyTurnstileForwardsRemoteIP(t *testing.T) {
	var body url.Values
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body, _ = url.ParseQuery(string(raw))
		jsonSuccess(true)(w, r)
	}))
	withSiteverifyStub(t, ts)

	if err := httpserver.VerifyTurnstile(context.Background(), "secret", "tok", "203.0.113.9"); err != nil {
		t.Fatalf("VerifyTurnstile: %v", err)
	}
	if got := body.Get("remoteip"); got != "203.0.113.9" {
		t.Errorf("remoteip = %q, want %q", got, "203.0.113.9")
	}
	if got := body.Get("secret"); got != "secret" {
		t.Errorf("secret = %q, want %q", got, "secret")
	}
	if got := body.Get("response"); got != "tok" {
		t.Errorf("response = %q, want %q", got, "tok")
	}
}

func TestVerifyTurnstileTimeoutFailsClosed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		jsonSuccess(true)(w, r)
	}))
	withSiteverifyStub(t, ts)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := httpserver.VerifyTurnstile(ctx, "secret", "tok", ""); err == nil {
		t.Error("VerifyTurnstile = nil, want an error when siteverify times out")
	}
}

func TestRequireCaptchaPassesThroughWhenCapabilityOff(t *testing.T) {
	cfg := &config.Config{}
	h := httpserver.RequireCaptcha(cfg)(okHandler())

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (capability off is a pass-through)", rec.Code)
	}
}

func TestRequireCaptchaSucceedsWithValidToken(t *testing.T) {
	ts := httptest.NewServer(jsonSuccess(true))
	withSiteverifyStub(t, ts)

	cfg := &config.Config{
		TurnstileSecretKey: "secret",
		Capabilities:       config.Capabilities{Turnstile: true},
	}
	h := httpserver.RequireCaptcha(cfg)(okHandler())

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("X-Captcha-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestRequireCaptchaRejectsMissingToken(t *testing.T) {
	ts := httptest.NewServer(jsonSuccess(true))
	withSiteverifyStub(t, ts)

	cfg := &config.Config{
		TurnstileSecretKey: "secret",
		Capabilities:       config.Capabilities{Turnstile: true},
	}
	h := httpserver.RequireCaptcha(cfg)(okHandler())

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"code":"captcha_failed"`) {
		t.Errorf("body = %q, want code captcha_failed", got)
	}
}

func TestRequireCaptchaRejectsFailedVerification(t *testing.T) {
	ts := httptest.NewServer(jsonSuccess(false))
	withSiteverifyStub(t, ts)

	cfg := &config.Config{
		TurnstileSecretKey: "secret",
		Capabilities:       config.Capabilities{Turnstile: true},
	}
	h := httpserver.RequireCaptcha(cfg)(okHandler())

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("X-Captcha-Token", "tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"code":"captcha_failed"`) {
		t.Errorf("body = %q, want code captcha_failed", got)
	}
}

func TestRequireCaptchaFailsClosedOnTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		jsonSuccess(true)(w, r)
	}))
	withSiteverifyStub(t, ts)

	cfg := &config.Config{
		TurnstileSecretKey: "secret",
		Capabilities:       config.Capabilities{Turnstile: true},
	}
	h := httpserver.RequireCaptcha(cfg)(okHandler())

	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set("X-Captcha-Token", "tok")
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Millisecond)
	defer cancel()
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (fail-closed on timeout)", rec.Code)
	}
	if got := rec.Body.String(); !strings.Contains(got, `"code":"captcha_failed"`) {
		t.Errorf("body = %q, want code captcha_failed", got)
	}
}

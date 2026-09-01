package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/testdb"
)

// TestAuthSigninRateLimitedAfter10 is the end-to-end proof that Server actually wires RateLimit
// in front of the mounted auth handler at /api/v1/auth/signin/credential, not just that the
// RateLimit middleware works in isolation.
//
// Limen itself also rate-limits this route with its own in-memory default (confirmed
// separately: it trips after 5 requests within roughly a 10s window, returning its own
// `{"message":...}` body with no "code" field) — that's a pre-existing, per-process limiter
// Task 5 doesn't touch. To prove OUR outer, DB-backed limiter is the thing guarding the route
// (rather than merely riding along behind Limen's), this sends 10 requests, sleeps past Limen's
// internal window so Limen would itself allow an 11th request through, then sends the 11th: our
// middleware must still reject it — with our envelope and Retry-After — because it counts up to
// 10 in its own 1-minute window and blocks the 11th before ever calling Limen's handler.
func TestAuthSigninRateLimitedAfter10(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	h := srv.Handler()

	body := `{"email":"nobody@example.com","password":"wrong-password"}`
	post := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/auth/signin/credential", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}

	for i := 0; i < 10; i++ {
		post()
	}

	// Let Limen's own in-memory limiter's window lapse, so only our own counter (60s window,
	// same since the first of the 10 requests above) can still be the reason an 11th is blocked.
	time.Sleep(11 * time.Second)

	last := post()
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("11th request: status = %d, want 429", last.Code)
	}
	if !strings.Contains(last.Body.String(), `"code":"rate_limited"`) {
		t.Errorf("11th request body = %q, want our envelope with code rate_limited", last.Body.String())
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("11th request: Retry-After header missing")
	}
}

// TestAuthSigninTrailingSlashStillRateLimited is C1's regression test: the trailing-slash
// spelling of a rate-limited auth route must count against, and be blocked by, the exact same
// budget as the canonical (no trailing slash) spelling. Before the fix, four exact ServeMux
// patterns (one per rate-limited route) only ever matched the no-trailing-slash spelling, so
// "POST .../signin/credential/" fell through to the unmetered "/api/v1/auth/" mount and reached
// Limen's own handler with no budget in front of it at all — this proves that gap is closed by
// authRateLimitMiddleware canonicalizing the path before its route lookup.
func TestAuthSigninTrailingSlashStillRateLimited(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	h := srv.Handler()

	body := `{"email":"nobody@example.com","password":"wrong-password"}`
	post := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		h.ServeHTTP(rec, req)
		return rec
	}

	// Exhaust the budget via the canonical (no trailing slash) path — our counter increments on
	// every request regardless of what Limen itself returns underneath, so this reaches 10 hits
	// whether or not Limen's own (separate, in-memory) limiter has also tripped by now.
	for i := 0; i < 10; i++ {
		post("/api/v1/auth/signin/credential")
	}

	trailing := post("/api/v1/auth/signin/credential/")
	if trailing.Code != http.StatusTooManyRequests {
		t.Fatalf("trailing-slash signin as the 11th request: status = %d, want 429", trailing.Code)
	}
	if !strings.Contains(trailing.Body.String(), `"code":"rate_limited"`) {
		t.Errorf("trailing-slash signin body = %q, want our envelope with code rate_limited", trailing.Body.String())
	}
}

// TestAuthSigninDoubleSlashVariantRedirectsRatherThanBypassing documents net/http.ServeMux's own
// path-cleaning behavior for a "//"-style variant of a rate-limited route: ServeMux redirects to
// the canonical path before any handler — ours or Limen's — ever sees the request at all, so this
// spelling can't bypass the limiter either, for a completely different reason than the
// trailing-slash case above (which does reach a handler, just the right one now). Asserted
// explicitly, per the brief, rather than assumed: if a future Go version stops redirecting here,
// this test catches that a bypass has reopened rather than silently passing.
func TestAuthSigninDoubleSlashVariantRedirectsRatherThanBypassing(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	h := srv.Handler()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/auth/signin/credential//", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	if rec.Code/100 != 3 {
		t.Fatalf("double-slash signin path: status = %d, want a 3xx mux redirect", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/api/v1/auth/signin/credential/" {
		t.Errorf("double-slash signin redirect Location = %q, want the single-trailing-slash cleaned path", got)
	}
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func fixedKey(key string) func(*http.Request) string {
	return func(*http.Request) string { return key }
}

func TestRateLimitAllowsUpToLimitThen429(t *testing.T) {
	d := testdb.New(t)
	h := httpserver.RateLimit(d, "test.limit", 10, time.Minute, fixedKey("k1"))(okHandler())

	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th request: status = %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"rate_limited"`) {
		t.Errorf("11th request body = %q, want code rate_limited", rec.Body.String())
	}
	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("11th request: Retry-After header missing")
	}
	if n, err := strconv.Atoi(retryAfter); err != nil || n < 1 {
		t.Errorf("Retry-After = %q, want a positive integer", retryAfter)
	}
}

func TestRateLimitWindowExpiryResets(t *testing.T) {
	d := testdb.New(t)
	h := httpserver.RateLimit(d, "test.window", 1, time.Minute, fixedKey("k2"))(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("1st request: status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("2nd request (over limit): status = %d, want 429", rec.Code)
	}

	// Force the window to have already closed, the same way the brief's test list does.
	if _, err := d.Exec(`UPDATE rate_limits SET reset_at = now() - interval '1s' WHERE key = 'test.window:k2'`); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("request after window expiry: status = %d, want 200", rec.Code)
	}
}

func TestRateLimitFailsOpenWhenStoreUnavailable(t *testing.T) {
	d := testdb.New(t)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	h := httpserver.RateLimit(d, "test.closed", 1, time.Minute, fixedKey("k3"))(okHandler())

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d with closed DB: status = %d, want 200 (fail open)", i+1, rec.Code)
		}
	}
}

func TestRateLimitSkipsWhenKeyFnReturnsEmpty(t *testing.T) {
	d := testdb.New(t)
	h := httpserver.RateLimit(d, "test.skip", 0, time.Minute, fixedKey(""))(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (limiting skipped)", rec.Code)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		trustProxy bool
		xff        string
		remoteAddr string
		want       string
	}{
		{
			name:       "trust proxy, XFF present, single IP",
			trustProxy: true,
			xff:        "203.0.113.5",
			remoteAddr: "10.0.0.1:1234",
			want:       "203.0.113.5",
		},
		{
			name:       "trust proxy, XFF present, multiple hops uses rightmost (nearest trusted proxy)",
			trustProxy: true,
			xff:        "203.0.113.5, 10.0.0.2",
			remoteAddr: "10.0.0.1:1234",
			want:       "10.0.0.2",
		},
		{
			name:       "trust proxy, forged leftmost entry doesn't change the key: rightmost still wins",
			trustProxy: true,
			xff:        "10.0.0.9, 203.0.113.5, 10.0.0.2",
			remoteAddr: "10.0.0.1:1234",
			want:       "10.0.0.2",
		},
		{
			name:       "trust proxy, no XFF falls back to RemoteAddr",
			trustProxy: true,
			xff:        "",
			remoteAddr: "10.0.0.1:1234",
			want:       "10.0.0.1",
		},
		{
			name:       "proxy not trusted, XFF present but ignored",
			trustProxy: false,
			xff:        "203.0.113.5",
			remoteAddr: "10.0.0.1:1234",
			want:       "10.0.0.1",
		},
		{
			name:       "RemoteAddr with no port",
			trustProxy: false,
			xff:        "",
			remoteAddr: "10.0.0.1",
			want:       "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if got := httpserver.ClientIP(r, tt.trustProxy); got != tt.want {
				t.Errorf("ClientIP(trustProxy=%v) = %q, want %q", tt.trustProxy, got, tt.want)
			}
		})
	}
}

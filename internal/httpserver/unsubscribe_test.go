package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/testdb"
)

// The secret testConfig() builds its AUTH_SECRET from, so a test can mint the same token the
// server will verify.
var unsubSecret = strings.Repeat("s", 32)

// One-click, RFC 8058: the mail client POSTs on the recipient's behalf, with no session, no
// Origin header and a body we never read.
func TestUnsubscribeOneClickPost(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ctx := context.Background()

	token := mailer.UnsubscribeToken(unsubSecret, "ada@example.com")
	req := httptest.NewRequest("POST", "/api/v1/unsubscribe?token="+token,
		strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a non-2xx makes the provider retry or flag us): %s", rec.Code, rec.Body)
	}
	suppressed, err := mailer.IsSuppressed(ctx, d, "ada@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Error("address is not suppressed after a one-click POST")
	}
}

// Someone can click twice, and a provider can retry its POST; neither may fail.
func TestUnsubscribeIsIdempotent(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))

	token := mailer.UnsubscribeToken(unsubSecret, "ada@example.com")
	for i := range 2 {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/unsubscribe?token="+token, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d, want 200: %s", i+1, rec.Code, rec.Body)
		}
	}
}

// Undoing it needs no account either — the same link, which is the only credential the person
// has. Without this, one accidental click is permanent.
func TestResubscribeWithTheSameToken(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ctx := context.Background()

	if err := mailer.Suppress(ctx, d, "ada@example.com", mailer.SourceLink); err != nil {
		t.Fatalf("Suppress: %v", err)
	}

	token := mailer.UnsubscribeToken(unsubSecret, "ada@example.com")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/unsubscribe?token="+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	suppressed, err := mailer.IsSuppressed(ctx, d, "ada@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("address is still suppressed after a resubscribe")
	}
}

// The security property the whole scheme exists for: holding your own link must not let you
// unsubscribe anybody else.
func TestUnsubscribeRejectsBadTokens(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ctx := context.Background()

	forged := strings.SplitN(mailer.UnsubscribeToken(unsubSecret, "victim@example.com"), ".", 2)[0] +
		"." + strings.SplitN(mailer.UnsubscribeToken(unsubSecret, "ada@example.com"), ".", 2)[1]

	for name, token := range map[string]string{
		"missing":           "",
		"garbage":           "not-a-token",
		"another's address": forged,
		"foreign signature": mailer.UnsubscribeToken("some-other-secret-thats-32-chars!", "victim@example.com"),
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/unsubscribe?token="+token, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}

	suppressed, err := mailer.IsSuppressed(ctx, d, "victim@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("a forged token suppressed someone else's address")
	}
}

// Some clients render List-Unsubscribe as a plain link and follow it with a GET. That must reach
// the confirmation page rather than 405, and must NOT unsubscribe on its own — a GET is fetched
// by link scanners and prefetchers that no person ever clicked.
func TestUnsubscribeGetRedirectsToThePageWithoutActing(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))
	ctx := context.Background()

	token := mailer.UnsubscribeToken(unsubSecret, "ada@example.com")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/unsubscribe?token="+token, nil))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/unsubscribe?token="+token {
		t.Errorf("Location = %q, want the SPA page carrying the token", got)
	}

	suppressed, err := mailer.IsSuppressed(ctx, d, "ada@example.com")
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if suppressed {
		t.Error("a GET unsubscribed the address; a scanner following the link would silence someone")
	}
}

// The page itself carries the token in its URL, so it must never be indexed.
func TestUnsubscribePageIsNoindex(t *testing.T) {
	d := testdb.New(t)
	cfg := testConfig()
	srv := httpserver.New(cfg, d, testAuthService(t, cfg, d))

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/unsubscribe?token=abc", nil))
	if got := rec.Header().Get("X-Robots-Tag"); got != "noindex" {
		t.Errorf("X-Robots-Tag = %q, want noindex", got)
	}
}

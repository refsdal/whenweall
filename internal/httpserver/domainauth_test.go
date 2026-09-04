package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/httpserver"
)

// fixedSessionAuth is a minimal httpserver.Auth stand-in for testing RequireCaptchaIfAnon's own
// decision logic in isolation, without a real Limen session or a database: FromContext always
// answers with whatever session (or none) the test configured, and the other three methods of the
// interface are never called by RequireCaptchaIfAnon so they're trivial stubs.
type fixedSessionAuth struct {
	sess *auth.Session
}

func (f fixedSessionAuth) RequireSession(next http.HandlerFunc) http.HandlerFunc { return next }

func (f fixedSessionAuth) FromContext(context.Context) (*auth.Session, bool) {
	if f.sess == nil {
		return nil, false
	}
	return f.sess, true
}

func (f fixedSessionAuth) VerifyGuestToken(string) (string, bool) { return "", false }
func (f fixedSessionAuth) MintGuestToken(string) string           { return "" }

// TestRequireCaptchaIfAnon_TreatsUnverifiedSessionAsAnonymous is the reviewer's finding 4:
// RequireCaptchaIfAnon used to skip the captcha check for ANY session carrying a non-empty
// UserID, verified or not — so an account that signed up and never verified its email could vote/
// comment/claim/book with no captcha at all, contradicting this plan's own binding decision
// ("unverified accounts cannot use the app"). A verified session must still skip the check
// (captcha is only ever for guests); an unverified one must be treated exactly like no session at
// all.
func TestRequireCaptchaIfAnon_TreatsUnverifiedSessionAsAnonymous(t *testing.T) {
	cfg := turnstileConfig(t) // captcha_test.go: Turnstile capability on, no real network call made
	req := httptest.NewRequest(http.MethodPost, "/x", nil) // no X-Captcha-Token header

	t.Run("verified session skips the check", func(t *testing.T) {
		a := fixedSessionAuth{sess: &auth.Session{UserID: "1", EmailVerified: true}}
		if err := httpserver.RequireCaptchaIfAnon(cfg, a, req); err != nil {
			t.Errorf("RequireCaptchaIfAnon = %v, want nil for a verified session", err)
		}
	})

	t.Run("unverified session is treated as anonymous and fails with no token", func(t *testing.T) {
		a := fixedSessionAuth{sess: &auth.Session{UserID: "1", EmailVerified: false}}
		if err := httpserver.RequireCaptchaIfAnon(cfg, a, req); err == nil {
			t.Error("RequireCaptchaIfAnon = nil, want an error for an unverified session with no captcha token")
		}
	})

	t.Run("no session at all fails with no token, same as unverified", func(t *testing.T) {
		a := fixedSessionAuth{sess: nil}
		if err := httpserver.RequireCaptchaIfAnon(cfg, a, req); err == nil {
			t.Error("RequireCaptchaIfAnon = nil, want an error for an anonymous caller with no captcha token")
		}
	})
}

package auth

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// requireErrorCode asserts resp is `status` with our envelope's error.code == code.
func requireErrorCode(t *testing.T, resp *http.Response, status int, code, what string) {
	t.Helper()
	if resp.StatusCode != status {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("%s: status %d, want %d: %s", what, resp.StatusCode, status, body)
	}
	body := decodeJSON(t, resp)
	errObj, _ := body["error"].(map[string]any)
	if got, _ := errObj["code"].(string); got != code {
		t.Fatalf("%s: error.code = %q, want %q (body %+v)", what, got, code, body)
	}
}

// verifyToken extracts the token from a captured verify_email mail's URL.
func verifyToken(t *testing.T, ts *testService, index int) string {
	t.Helper()
	var seen int
	for _, m := range ts.mail.all() {
		if m.Template != "verify_email" {
			continue
		}
		if seen == index {
			url, _ := m.Data["URL"].(string)
			const marker = "/verify-email?token="
			i := strings.Index(url, marker)
			if i < 0 || url[i+len(marker):] == "" {
				t.Fatalf("verify_email URL %q carries no token", url)
			}
			return url[i+len(marker):]
		}
		seen++
	}
	t.Fatalf("no verify_email mail at index %d (have %d)", index, seen)
	return ""
}

// TestEmailVerificationGate re-expresses the old auth.workers.test.ts assertion ("sign-in is
// blocked until the verification token is consumed") on this stack: a fresh signup mints no
// session, signing in works but every gated surface answers 403 email_unverified until Limen's
// POST /verify-email consumes the mailed token, after which the same session passes.
func TestEmailVerificationGate(t *testing.T) {
	ts := newTestService(t)
	email := "gate@example.com"

	signupResp := ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": signupPassword,
	})
	if signupResp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(signupResp.Body)
		t.Fatalf("signup: status %d: %s", signupResp.StatusCode, body)
	}
	for _, c := range signupResp.Header.Values("Set-Cookie") {
		if strings.Contains(c, "limen_session=") {
			t.Fatalf("signup set a session cookie (%q); auto sign-in must be off", c)
		}
	}
	_ = signupResp.Body.Close()

	// Signing in is allowed (the user needs a session to resend the mail) …
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": signupPassword,
	}), "signin")

	// … but nothing gated is.
	requireErrorCode(t, ts.get(t, "/probe/session"), http.StatusForbidden, "email_unverified", "RequireSession before verify")
	requireErrorCode(t, ts.get(t, "/api/v1/auth/organizations/active"), http.StatusForbidden, "email_unverified", "Limen mount before verify")
	if err := ts.svc.MakeStaff(context.Background(), email); err != nil {
		t.Fatalf("MakeStaff: %v", err)
	}
	requireErrorCode(t, ts.get(t, "/probe/staff"), http.StatusForbidden, "email_unverified", "RequireStaff before verify")

	// The exemptions the SPA needs to get out of this state.
	requireStatus2xx(t, ts.get(t, "/api/v1/auth/me"), "GET /me while unverified")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/email-verifications", map[string]any{}), "resend while unverified")
	second := verifyToken(t, ts, 1) // the resend enqueued a second verify_email mail
	if second == "" {
		t.Fatal("resend produced no token")
	}

	// And a wrong token does nothing.
	bad := ts.postJSON(t, "/api/v1/auth/verify-email", map[string]any{"token": "nope"})
	if bad.StatusCode/100 == 2 {
		t.Fatal("POST /verify-email accepted a bogus token")
	}
	_ = bad.Body.Close()

	// Consume the original token (Limen's own route; the SPA's /verify-email page does exactly this).
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/verify-email", map[string]any{
		"token": verifyToken(t, ts, 0),
	}), "verify-email")

	var verified bool
	if err := ts.svc.db.QueryRowContext(context.Background(),
		"SELECT email_verified_at IS NOT NULL FROM users WHERE email = $1", email).Scan(&verified); err != nil {
		t.Fatalf("reading email_verified_at: %v", err)
	}
	if !verified {
		t.Fatal("email_verified_at still NULL after POST /verify-email")
	}

	// Same session, now allowed everywhere — the gate reads the row on every request.
	requireStatus2xx(t, ts.get(t, "/probe/session"), "RequireSession after verify")
	requireStatus2xx(t, ts.get(t, "/probe/staff"), "RequireStaff after verify")
	requireStatus2xx(t, ts.get(t, "/api/v1/auth/organizations/active"), "Limen mount after verify")

	probe := decodeJSON(t, ts.get(t, "/probe"))
	if v, _ := probe["EmailVerified"].(bool); !v {
		t.Errorf("Session.EmailVerified = false after verify: %+v", probe)
	}
}

// TestUnverifiedSessionIsStillASession: FromContext (not RequireSession) keeps reporting the user —
// handlers that want to treat unverified callers specially can — and RequireSessionAllowUnverified
// lets the two account routes that must work before verification (PATCH/DELETE /api/v1/me) through.
func TestUnverifiedSessionIsStillASession(t *testing.T) {
	ts := newTestService(t)
	email := "still-a-session@example.com"
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": signupPassword,
	}), "signup")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": signupPassword,
	}), "signin")

	probe := decodeJSON(t, ts.get(t, "/probe"))
	if anon, _ := probe["anonymous"].(bool); anon {
		t.Fatalf("probe reported anonymous for an unverified session: %+v", probe)
	}
	if v, _ := probe["EmailVerified"].(bool); v {
		t.Errorf("Session.EmailVerified = true for a fresh signup")
	}
	requireStatus2xx(t, ts.get(t, "/probe/session-unverified-ok"), "RequireSessionAllowUnverified")
}

// TestAuthMountUnverifiedAllowedCredentialRoutesActuallyWork is the regression test for the four
// session-less entries in authMountUnverifiedAllowed — signin, signup, request-reset and reset.
// Every other test that hits these routes does so either with NO cookie at all (a fresh
// testService/client), which never reaches the map lookup because AuthMountGuard's earlier "no
// valid Limen session" branch already let the request through, or with an ALREADY-VERIFIED
// user's cookie, which never reaches the `EmailVerifiedAt == nil` branch either. Neither exercises
// the map itself. This test drives all four routes while ts.client is carrying an UNVERIFIED
// user's own session cookie — the exact scenario the guard's own doc comment describes ("a
// browser that still carries an unverified session cookie must be able to sign in as someone else
// or reset a password") — so removing any one of the four entries from authMountUnverifiedAllowed
// turns this test red (verified by hand: deleting each entry in turn reproduces a 403
// email_unverified here where a 2xx is asserted).
func TestAuthMountUnverifiedAllowedCredentialRoutesActuallyWork(t *testing.T) {
	ts := newTestService(t)
	email := "still-unverified-creds@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": email, "password": signupPassword,
	}), "signup")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": signupPassword,
	}), "signin")
	// ts.client's jar now carries email's own UNVERIFIED session cookie for every request below.

	// POST /passwords/request-reset needs no authentication of its own, but AuthMountGuard still
	// resolves the caller's own (unverified) cookie first — that must not turn into a 403.
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/passwords/request-reset", map[string]any{
		"email": email,
	}), "request-reset while carrying an unverified session cookie")

	msg, ok := ts.mail.find("reset_password")
	if !ok {
		t.Fatal("no reset_password mail captured")
	}
	url, _ := msg.Data["URL"].(string)
	const marker = "token="
	idx := strings.Index(url, marker)
	if idx < 0 {
		t.Fatalf("reset_password URL = %q, missing %q", url, marker)
	}
	token := url[idx+len(marker):]
	if token == "" {
		t.Fatal("reset_password URL carries an empty token")
	}

	newPassword := "Ev3nStrongerPassw0rd"
	// POST /passwords/reset: same reasoning as request-reset above — must actually run, not 403.
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/passwords/reset", map[string]any{
		"token":        token,
		"new_password": newPassword,
	}), "reset while carrying an unverified session cookie")

	// POST /signin/credential: re-entering credentials on a stale tab that still carries the same
	// (still unverified) user's old session cookie must not be blocked either.
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email, "password": newPassword,
	}), "signin again while carrying an unverified session cookie")

	// POST /signup/credential: signing up as someone else entirely from the same browser/cookie
	// jar — which still carries the first, unverified user's session cookie — must not be blocked.
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email": "second-signup-same-cookie@example.com", "password": signupPassword,
	}), "signup while carrying an unverified session cookie")
}

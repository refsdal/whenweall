package httpserver

// Ports src/server/http/turnstile.ts's verifyTurnstile (POSTs a Turnstile token to Cloudflare's
// siteverify endpoint). requireTurnstile (throws CAPTCHA_FAILED when verification doesn't
// succeed) is ported twice: RequireCaptchaIfAnon (domainauth.go) for guest votes/comments/
// bookings, and Server.authCaptchaMiddleware (captcha.go) for the sign-in/sign-up/password-reset
// routes under the Limen mount.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// TurnstileSiteverifyURL is Cloudflare Turnstile's verification endpoint. Exported as a mutable
// package-level var — rather than threaded through VerifyTurnstile's own parameters — purely so
// tests (in package httpserver_test, which can't reach an unexported var) can point it at an
// httptest.Server stub instead of the real endpoint; VerifyTurnstile's signature is otherwise
// fixed to (ctx, secretKey, token, remoteIP). Production code must never assign to this.
var TurnstileSiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"

// turnstileHTTPClient's Timeout is a fail-safe upper bound on the siteverify round trip: a hung
// Cloudflare (or stub) response must not block the request holding it open forever. A caller
// wanting a tighter bound (as the timeout test case here does) can still get one by giving
// VerifyTurnstile a context with its own, shorter deadline — context cancellation and this
// Timeout both abort the same underlying request, whichever fires first.
var turnstileHTTPClient = &http.Client{Timeout: 5 * time.Second}

// turnstileSiteverifyResponse is the subset of Cloudflare's siteverify response body this needs.
type turnstileSiteverifyResponse struct {
	Success bool `json:"success"`
}

// VerifyTurnstile POSTs token (Cloudflare's `response` field) and, when non-empty, remoteIP
// (`remoteip`) to TurnstileSiteverifyURL using secretKey, and reports whether Cloudflare accepted
// it. Ports verifyTurnstile (turnstile.ts): a missing token fails immediately with no network
// round trip. Every other failure mode — a network error, a context timeout/cancellation, a
// non-2xx or unparseable response body, or a well-formed `{"success":false}` — returns a non-nil
// error; requireCaptchaIfAnon (internal/polls/handlers.go) treats all of them identically (403
// captcha_failed), matching verifyTurnstile's own single boolean return with no distinct error
// cases exposed to the caller.
func VerifyTurnstile(ctx context.Context, secretKey, token, remoteIP string) error {
	if token == "" {
		return errors.New("turnstile: token required")
	}

	body := url.Values{"secret": {secretKey}, "response": {token}}
	if remoteIP != "" {
		body.Set("remoteip", remoteIP)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, TurnstileSiteverifyURL, strings.NewReader(body.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := turnstileHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("turnstile: siteverify returned a non-2xx status")
	}

	var out turnstileSiteverifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if !out.Success {
		return errors.New("turnstile: verification failed")
	}
	return nil
}

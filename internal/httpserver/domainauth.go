package httpserver

// Domain-agnostic handler helpers, promoted out of internal/polls/handlers.go (Task 7) once a
// second HTTP-surfaced domain package needed the same session/guest-token/rate-limit/error-
// envelope plumbing internal/polls had already built for itself: the Auth seam, the org-session
// gate, guest-token resolution, the anonymous-caller captcha gate, JSON decoding, the generic
// "map a domain error to the standard envelope" core, and the public (per-IP) rate limiter.
// Nothing here is polls-specific — every one of these signatures is unchanged from its original
// internal/polls home, just relocated (and, where noted, generalized) so a later domain package
// can reuse them instead of re-deriving its own copies.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
)

// Auth is the narrow seam an HTTP-surfaced domain package needs from auth.Service —
// RequireSession/FromContext/VerifyGuestToken/MintGuestToken — kept as an interface (rather than
// importing *auth.Service directly into every handler signature) so tests can substitute a fake
// session/guest-token source instead of driving a real signup/signin flow through Limen for every
// handler test case. auth.Service satisfies this with no adapter needed (FromContext is a plain
// delegation method — see internal/auth/session.go).
type Auth interface {
	RequireSession(next http.HandlerFunc) http.HandlerFunc
	FromContext(ctx context.Context) (*auth.Session, bool)
	VerifyGuestToken(token string) (string, bool)
	MintGuestToken(participantID string) string
}

// WithOrgSession requires a valid session (401 otherwise) AND a caller with an active
// organization (403 "no_active_org" otherwise — practically unreachable once signed in, since
// auth.Service's own session resolution always defaults ActiveOrgID to the caller's personal org,
// but an org-scoped handler needs an orgID to pass to its domain service, so this is checked
// explicitly rather than assumed).
func WithOrgSession(a Auth, next func(w http.ResponseWriter, r *http.Request, sess *auth.Session)) http.HandlerFunc {
	return a.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.FromContext(r.Context())
		if !ok {
			Err(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
			return
		}
		if sess.ActiveOrgID == "" {
			Err(w, http.StatusForbidden, "no_active_org", "no active organization", nil)
			return
		}
		next(w, r, sess)
	})
}

// ExtractGuestToken resolves a request's guest edit token — the X-Guest-Token header first, then
// ?token= — WITHOUT verifying it; "" means neither was present. Split out from GuestParticipantID
// so a caller that needs the raw token (rather than an already-verified participant id) has
// somewhere to get it without re-deriving this same header/query precedence itself.
func ExtractGuestToken(r *http.Request) string {
	if token := r.Header.Get("X-Guest-Token"); token != "" {
		return token
	}
	return r.URL.Query().Get("token")
}

// GuestParticipantID resolves the caller's guest edit token (ExtractGuestToken) into a verified
// participant id via a.VerifyGuestToken, or "" for no/invalid token.
func GuestParticipantID(a Auth, r *http.Request) string {
	token := ExtractGuestToken(r)
	if token == "" {
		return ""
	}
	pid, ok := a.VerifyGuestToken(token)
	if !ok {
		return ""
	}
	return pid
}

// RequireCaptchaIfAnon ports participants.functions.ts's own `if (!userId) await
// requireTurnstile(...)` branch: captcha is only ever demanded of an anonymous caller, and only
// when Turnstile is actually configured (cfg.Capabilities.Turnstile) — a deployment with no
// Turnstile keys has no way to verify a token, so it must not block on one (mirrors
// config.functions.ts's capability gating elsewhere). The token travels in the X-Captcha-Token
// header — this REST surface's own convention, rather than the TS source's server-function body
// field.
//
// sess.EmailVerified is checked alongside UserID: signing in is allowed while unverified (so the
// account can resend/complete verification), but this plan's binding decision is "unverified
// accounts cannot use the app" — an unverified session must not get the signed-in caller's free
// pass on a public mutating route just because it carries a UserID. Treated as anonymous, exactly
// like no session at all.
func RequireCaptchaIfAnon(cfg *config.Config, a Auth, r *http.Request) error {
	if sess, ok := a.FromContext(r.Context()); ok && sess.UserID != "" && sess.EmailVerified {
		return nil
	}
	if !cfg.Capabilities.Turnstile {
		return nil
	}
	token := r.Header.Get("X-Captcha-Token")
	remoteIP := ClientIP(r, cfg.TrustProxy)
	return VerifyTurnstile(r.Context(), cfg.TurnstileSecretKey, token, remoteIP)
}

// DecodeJSON decodes r's JSON body into dst, writing the standard "invalid" envelope and
// returning false on any decode failure (including a missing body). A body over the /api/ cap
// (Server.Handler's http.MaxBytesHandler, maxAPIBodyBytes) surfaces here as *http.MaxBytesError
// and is reported as 413 payload_too_large rather than a misleading "malformed JSON".
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		Err(w, http.StatusBadRequest, "invalid", "request body is required", nil)
		return false
	}
	defer func() { _ = r.Body.Close() }()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			Err(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body exceeds 1 MiB", nil)
			return false
		}
		Err(w, http.StatusBadRequest, "invalid", "malformed JSON body", nil)
		return false
	}
	return true
}

// DomainErrorMapper maps one domain package's own sentinel errors to the standard HTTP envelope's
// pieces (status, code, message, and — for a validation failure — its field errors), returning ok
// == false for anything it doesn't recognize (a generic 500 territory it leaves to
// WriteDomainError).
type DomainErrorMapper func(err error) (status int, code, message string, fields map[string]string, ok bool)

// WriteDomainError is the generic "map a domain error to the standard envelope, or log-and-500"
// core every domain package's own writeServiceError-shaped function delegates to: mapper gets
// first look at err, and its result is written as-is when ok; otherwise this logs the error
// (unrecognized == a bug, not a client-caused condition) and reports a generic 500. The actual
// sentinel-to-envelope mapping is deliberately NOT here — each domain package's own errors are its
// own vocabulary, so mapper (typically a small package-level function next to that package's
// sentinels) is the only thing that changes between domains; this function is the shared plumbing
// around it.
func WriteDomainError(w http.ResponseWriter, err error, mapper DomainErrorMapper) {
	if status, code, message, fields, ok := mapper(err); ok {
		Err(w, status, code, message, fields)
		return
	}
	slog.Default().Error("httpserver: unhandled domain error", "error", err)
	Err(w, http.StatusInternalServerError, "internal", "internal error", nil)
}

// PublicRateLimit builds a per-IP rate limiter over db — the same fixed-window counter RateLimit
// uses for the auth surface, namespaced "<namespace>.<name>" (e.g. "polls.vote") so different
// domains' — and different routes within the same domain's — rate limits never share a bucket.
func PublicRateLimit(db *sql.DB, namespace, name string, limit int, window time.Duration, trustProxy bool) func(http.Handler) http.Handler {
	return RateLimit(db, namespace+"."+name, limit, window, func(r *http.Request) string {
		return ClientIP(r, trustProxy)
	})
}

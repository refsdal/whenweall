package httpserver

import (
	"net/http"
	"path"
	"strings"
)

// authCaptchaRoutes are the three unauthenticated, bot-attractive Limen routes that must carry a
// solved Turnstile challenge when the capability is on — the same set Better-Auth's captcha
// plugin protected in the TS stack (sign-up, sign-in, password-reset request). The e-mail
// verification resend is protected by a session instead and needs no captcha. Keyed exactly like
// authRateLimitedRoutes: "METHOD canonical-path".
var authCaptchaRoutes = map[string]struct{}{
	"POST /api/v1/auth/signin/credential":       {},
	"POST /api/v1/auth/signup/credential":       {},
	"POST /api/v1/auth/passwords/request-reset": {},
}

// authCaptchaMiddleware verifies X-Captcha-Token (Cloudflare Turnstile's response token — the
// same header RequireCaptchaIfAnon reads for guest votes/comments/bookings) on authCaptchaRoutes
// and answers 403 captcha_failed when it is missing or rejected. Returns next unchanged when
// Turnstile is not configured: a deployment without the key pair cannot verify anything, so it
// must not demand it (spec §8: an unset capability is invisible, never broken). The SPA mirrors
// this with useCaptchaEnabled(): no site key → no widget → no header.
func (s *Server) authCaptchaMiddleware(next http.Handler) http.Handler {
	if !s.cfg.Capabilities.Turnstile {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleaned := path.Clean(strings.TrimSuffix(r.URL.Path, "/"))
		if _, ok := authCaptchaRoutes[r.Method+" "+cleaned]; !ok {
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-Captcha-Token")
		if err := VerifyTurnstile(r.Context(), s.cfg.TurnstileSecretKey, token, ClientIP(r, s.cfg.TrustProxy)); err != nil {
			Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

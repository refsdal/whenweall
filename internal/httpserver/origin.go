package httpserver

import (
	"net/http"
	"net/url"
)

// mutatingMethods are the ones CheckOrigin polices; GET/HEAD/OPTIONS never mutate state, so a
// cross-origin request using one of those (e.g. a `<link>` prefetch, an image tag) is never a
// forgery risk this check needs to guard against.
var mutatingMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

// CheckOrigin is our own defense-in-depth Origin check for mutating /api/v1/* requests (spec §2):
// SameSite=Lax cookies (Limen's default) already stop most cross-site form submissions, but a
// SameSite=Lax cookie still rides along on a simple cross-site GET, and older browsers/agents
// treat some cross-site requests as "same-site enough" in edge cases the spec has since tightened
// — this closes that gap explicitly for our own routes rather than trusting the cookie attribute
// alone. Limen has its own CSRF protection for its mount (internal/auth/routes.txt), so this is
// belt-and-suspenders there and the sole guard for any other mutating /api/v1/* route.
//
// A POST/PUT/PATCH/DELETE request that carries an Origin header must have it match appURL's
// origin (scheme + host) exactly, or the request is rejected with 403 {"error":{"code":"bad_origin",...}}.
// GET/HEAD/OPTIONS requests, and any request with no Origin header at all (curl, same-origin
// fetches from older browsers/agents that omit it), pass through unchecked — an absent header is
// not evidence of forgery, just evidence the client didn't send one.
func CheckOrigin(appURL string) func(http.Handler) http.Handler {
	wantOrigin := appURL
	if u, err := url.Parse(appURL); err == nil && u.Scheme != "" && u.Host != "" {
		wantOrigin = u.Scheme + "://" + u.Host
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if mutatingMethods[r.Method] {
				if origin := r.Header.Get("Origin"); origin != "" && origin != wantOrigin {
					writeErrorEnvelope(w, http.StatusForbidden, "bad_origin", "origin mismatch")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

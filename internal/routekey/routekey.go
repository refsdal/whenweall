// Package routekey builds the one canonical "METHOD /cleaned/path" string every security-relevant
// route-table lookup in this codebase matches a request against: internal/auth's AuthMountGuard
// (authMountSignoutMethodAndPath, authMountUnverifiedAllowed), internal/httpserver's
// authCaptchaMiddleware (authCaptchaRoutes) and authRateLimitMiddleware (authRateLimitedRoutes).
// All three used to write their own copy of path.Clean(strings.TrimSuffix(r.URL.Path, "/")) — see
// authRateLimitMiddleware's own doc comment in internal/httpserver/server.go for exactly why the
// cleaning matters (matching whatever spelling of a path Limen's own router will eventually
// resolve the request to, not just the one exact string a naive comparison would). Three
// independent copies of a security matcher is a bug waiting to happen the moment one of them
// drifts from the other two, so this is the one shared implementation all three import.
//
// This is its own package, rather than living in internal/httpserver where the pattern was born,
// for the same reason internal/clientip is its own package: internal/auth must never import
// internal/httpserver (see that package's own doc comment), so a helper both sides need has to
// live somewhere neither of them is.
package routekey

import (
	"net/http"
	"path"
	"strings"
)

// Of returns r's canonical route key: its method, a space, and its path cleaned the same way
// path.Clean(strings.TrimSuffix(p, "/")) always has been here — trim exactly one trailing slash,
// then collapse "." / ".." segments and repeated slashes. "POST /foo/" and "POST /foo//" both
// key identically to "POST /foo".
func Of(r *http.Request) string {
	return r.Method + " " + Clean(r.URL.Path)
}

// Clean canonicalizes just a path (no method), for a caller that already has the two apart — a
// route-table literal, or a test asserting on the path half directly.
func Clean(p string) string {
	return path.Clean(strings.TrimSuffix(p, "/"))
}

// Package clientip derives the client address a request should be attributed to, honouring the
// app's TRUST_PROXY setting. It is its own package (rather than living in internal/httpserver,
// where ClientIP was born) so internal/auth can key Limen's built-in rate limiter on the exact
// same value without importing internal/httpserver — which imports internal/auth, so that edge
// would be a cycle.
package clientip

import (
	"net"
	"net/http"
	"strings"
)

// FromRequest returns the request's client IP: the rightmost entry of X-Forwarded-For when
// trustProxy is true (set from the app's TRUST_PROXY config — true only when a reverse proxy in
// front of us is trusted to set that header honestly), otherwise the host portion of RemoteAddr.
//
// Rightmost, not leftmost: X-Forwarded-For is a client-supplied header up until the first proxy
// that actually terminates the request touches it, and every hop after that only ever *appends*
// its own observed peer address to the end of the list — it never rewrites what's already there.
// So the leftmost entry is whatever the original client claimed for itself (trivially spoofed by
// sending an X-Forwarded-For header of their own choosing), while the rightmost entry is the
// address our own trusted proxy saw the connection come from — the only entry in the list this
// process didn't just take the client's word for.
func FromRequest(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if last != "" {
				return last
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

package httpserver

import (
	"database/sql"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// rateLimitSQL is a fixed-window counter: one statement so the read-modify-write of a window
// cannot interleave with another request's. The CASE arms handle the window rolling over — a row
// whose reset_at has already passed starts a fresh window rather than being treated as exhausted.
// This mirrors src/server/http/rate-limit-store.ts's `consume`, backed by the same UNLOGGED
// `rate_limits` table (migrations/00001_infra.sql).
const rateLimitSQL = `
INSERT INTO rate_limits (key, count, reset_at)
VALUES ($1, 1, now() + $2)
ON CONFLICT (key) DO UPDATE SET
  count = CASE WHEN rate_limits.reset_at < now() THEN 1 ELSE rate_limits.count + 1 END,
  reset_at = CASE WHEN rate_limits.reset_at < now() THEN excluded.reset_at ELSE rate_limits.reset_at END
RETURNING count, reset_at
`

// RateLimit allows `limit` hits per `window` per key; over-limit responds 429 with the standard
// JSON error envelope (code "rate_limited") and a Retry-After header in seconds.
//
// The counter key is `name+":"+keyFn(r)` — `name` namespaces the counter per rate-limited route
// (e.g. "auth.signin") so different routes never share a bucket even if keyFn returns the same
// identity (typically the client IP) for both. keyFn returning "" skips rate limiting entirely
// for that request — used when no meaningful identity can be derived.
//
// Errors reading the store (including a closed/unreachable database) fail OPEN: a rate limiter
// that locks everyone out because Postgres hiccupped is worse than one that briefly stops
// limiting, and this sits in front of every auth attempt.
func RateLimit(sqlDB *sql.DB, name string, limit int, window time.Duration, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := keyFn(r)
			if id == "" {
				next.ServeHTTP(w, r)
				return
			}

			key := name + ":" + id
			var count int
			var resetAt time.Time
			err := sqlDB.QueryRowContext(r.Context(), rateLimitSQL, key, window).Scan(&count, &resetAt)
			if err != nil {
				slog.Warn("rate limit store unavailable, failing open",
					"name", name, "error", err)
				next.ServeHTTP(w, r)
				return
			}

			if count > limit {
				retryAfter := int(math.Ceil(time.Until(resetAt).Seconds()))
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeErrorEnvelope(w, http.StatusTooManyRequests, "rate_limited", "rate limit exceeded")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the request's client IP: the rightmost entry of X-Forwarded-For when
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
func ClientIP(r *http.Request, trustProxy bool) string {
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

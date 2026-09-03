package httpserver

import (
	"database/sql"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/refsdal/whenweall/internal/clientip"
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

// ClientIP is clientip.FromRequest under its historical name — every rate limiter and captcha
// check in this package keys on it. The implementation moved to internal/clientip so
// internal/auth can hand Limen's own limiter the identical key (see auth.httpConfigOptions).
func ClientIP(r *http.Request, trustProxy bool) string {
	return clientip.FromRequest(r, trustProxy)
}

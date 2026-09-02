package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/thecodearcher/limen/plugins/organization"
)

// Session is the seam's view of "who is making this request" — every later plan reads this
// (via FromContext or RequireSession) instead of touching Limen's *limen.ValidatedSession.
type Session struct {
	UserID      string
	Email       string
	ActiveOrgID string // "" when the user has no active organization
	Staff       bool
}

// sessionCtxKey is unexported so only this package can set/read the context value it names —
// FromContext is the only way another package reads a Session back out of a context.
type sessionCtxKey struct{}

// Middleware resolves the Limen session cookie (if any) on every request and stashes the result
// — a *Session, or nil for an anonymous request — in the request context. It never rejects an
// unauthenticated request itself; RequireSession/RequireStaff do that. This must wrap the whole
// mux (not just /api/v1/auth/*), since later plans' handlers everywhere need FromContext to work.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sess := s.resolveSession(w, r)
		ctx := context.WithValue(r.Context(), sessionCtxKey{}, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveSession validates the request's Limen session cookie and, on success, builds the
// Session the rest of the app sees: the active organization (if any) and the staff flag both
// require a database round trip, so an anonymous request (no cookie, or an invalid/expired one)
// short-circuits before either. w is only used to forward a re-issued session cookie to the
// client when Limen (or this function's own SetActiveOrganization call below) reports one; it is
// never the vehicle for anything else here.
func (s *Service) resolveSession(w http.ResponseWriter, r *http.Request) *Session {
	validated, err := s.limen.GetSession(r)
	if err != nil || validated == nil || validated.User == nil {
		return nil
	}

	// A locked user (internal/admin.LockUser) is treated as anonymous from here on, same as an
	// invalid session — see locked_users' own doc comment in migrations/00007_admin_locks.sql for
	// why this check exists alongside LockUser's own RevokeUserSessions call: the revoke clears
	// out sessions that already existed when the lock was applied, but a locked user could still
	// sign back in afterwards (Limen's credential-password plugin has no concept of a lock) and
	// mint a brand new, otherwise perfectly valid session — this check is what stops *that* one
	// too, on every request, for as long as the lock stands.
	//
	// Fails closed: a lock control that fails open on its own query error (treating "couldn't
	// tell" as "not locked") defeats the point of having it, so an error here is treated the same
	// as "locked" — the request is anonymous — rather than falling through to a normal session.
	var locked bool
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)", validated.User.ID,
	).Scan(&locked); err != nil {
		s.logger.Error("auth: locked_users check failed; treating session as anonymous", "user_id", fmt.Sprint(validated.User.ID), "error", err)
		return nil
	} else if locked {
		return nil
	}

	// Limen extends a session's expiration lazily, on whichever request first crosses its
	// UpdateAge threshold (ValidatedSession.Refreshed), re-issuing the session cookie with a new
	// expiry. That cookie has to reach the client the same as it would from one of Limen's own
	// handlers — this is the one place every request (not just ones Limen's own routes handle)
	// passes through, so it's the only place that can deliver it.
	if validated.Refreshed != nil && validated.Refreshed.Cookie != nil {
		http.SetCookie(w, validated.Refreshed.Cookie)
	}

	sess := &Session{
		UserID: fmt.Sprint(validated.User.ID),
		Email:  validated.User.Email,
	}

	// Every authenticated request is where the personal-org invariant gets enforced (lazily,
	// once per user per process) — see ensurePersonalOrgOnce's doc comment in auth.go for why
	// this replaced an earlier Limen-hook-based attempt.
	s.ensurePersonalOrgOnce(r.Context(), validated.User)

	if validated.Session != nil {
		activeOrgID, err := s.orgs.GetActiveOrganizationID(r.Context(), validated.Session)
		switch {
		case err == nil && activeOrgID != nil:
			sess.ActiveOrgID = fmt.Sprint(activeOrgID)
		case err == nil:
			// No active org yet — true for every fresh signup, since nothing ever calls
			// organizations/switch on their behalf. Default to the user's first membership (the
			// personal org ensurePersonalOrgOnce just guaranteed exists) rather than leaving
			// ActiveOrgID "" until they happen to switch manually.
			if org, ok := s.firstOrganization(r.Context(), validated.User); ok {
				result, err := s.orgs.SetActiveOrganization(r.Context(), validated.Session, org)
				if err != nil {
					s.logger.Error("auth: set default active organization failed",
						"user_id", sess.UserID, "error", err)
				} else {
					sess.ActiveOrgID = fmt.Sprint(org.ID)
					// The opaque session manager's UpdateSession (what SetActiveOrganization
					// calls underneath) always returns a nil *SessionResult — no new token, no
					// cookie to re-issue, confirmed against the pinned Limen source. This handles
					// a non-nil result anyway so a future session-manager plugin (e.g. JWT
					// sessions, which do mint a new token here) doesn't silently drop its cookie.
					if result != nil && result.Cookie != nil {
						http.SetCookie(w, result.Cookie)
					}
				}
			}
		}
	}

	var staff bool
	if err := s.db.QueryRowContext(r.Context(),
		"SELECT EXISTS(SELECT 1 FROM staff_users WHERE user_id = $1)", validated.User.ID,
	).Scan(&staff); err == nil {
		sess.Staff = staff
	}

	return sess
}

// FromContext returns the Session Middleware stashed on ctx, and whether one was present (false
// for an anonymous request).
func FromContext(ctx context.Context) (*Session, bool) {
	sess, ok := ctx.Value(sessionCtxKey{}).(*Session)
	if !ok || sess == nil {
		return nil, false
	}
	return sess, true
}

// FromContext is a method wrapper around the package-level function of the same name, so a
// package that only ever sees *Service through a narrower interface (internal/polls's Auth seam)
// can still read the caller's Session back out of a context without importing package auth's own
// top-level function — the interface can only name methods, not free functions. Purely a
// delegation; it carries no state of its own.
func (s *Service) FromContext(ctx context.Context) (*Session, bool) {
	return FromContext(ctx)
}

// RequireSession rejects an anonymous request with 401 {"error":{"code":"unauthenticated",...}}
// before calling next; a request with a valid session passes through unchanged (next reads the
// Session back out via FromContext same as any other handler).
func (s *Service) RequireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r)
	}
}

// RequireStaff rejects an anonymous request with 401 and a non-staff request with 403, both
// {"error":{"code":"forbidden"|"unauthenticated",...}}.
func (s *Service) RequireStaff(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := FromContext(r.Context())
		if !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if !sess.Staff {
			writeErrorEnvelope(w, http.StatusForbidden, "forbidden", "staff access required")
			return
		}
		next(w, r)
	}
}

// RequireOrgMember returns the caller's Session after verifying membership of orgID. Unlike
// RequireSession/RequireStaff this is not an http.HandlerFunc wrapper — later plans' handlers
// call it directly (after resolving orgID from a path/body) and translate the returned error:
// ErrUnauthenticated -> 401, ErrForbidden -> 403, ErrInternal -> 500.
func (s *Service) RequireOrgMember(ctx context.Context, orgID string) (*Session, error) {
	sess, ok := FromContext(ctx)
	if !ok {
		return nil, ErrUnauthenticated
	}
	if err := s.orgs.CheckMemberExistsInOrganization(ctx, parseLimenID(orgID), parseLimenID(sess.UserID)); err != nil {
		// Only "you're genuinely not a member" maps to 403. Anything else (a closed/unreachable
		// database, say) is not the caller's fault and shouldn't look like an authorization
		// decision — it gets ErrInternal instead, wrapped with %v (not %w) so the underlying
		// Limen error is preserved for logging but never reachable via errors.Is/As on the
		// returned error, keeping Limen types out of this package's public surface.
		if errors.Is(err, organization.ErrMemberNotInOrganization) {
			return nil, ErrForbidden
		}
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	return sess, nil
}

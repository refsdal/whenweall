package auth

import (
	"context"
	"fmt"
	"net/http"
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
		sess := s.resolveSession(r)
		ctx := context.WithValue(r.Context(), sessionCtxKey{}, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// resolveSession validates the request's Limen session cookie and, on success, builds the
// Session the rest of the app sees: the active organization (if any) and the staff flag both
// require a database round trip, so an anonymous request (no cookie, or an invalid/expired one)
// short-circuits before either.
func (s *Service) resolveSession(r *http.Request) *Session {
	validated, err := s.limen.GetSession(r)
	if err != nil || validated == nil || validated.User == nil {
		return nil
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
		if activeOrgID, err := s.orgs.GetActiveOrganizationID(r.Context(), validated.Session); err == nil && activeOrgID != nil {
			sess.ActiveOrgID = fmt.Sprint(activeOrgID)
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
// ErrUnauthenticated -> 401, ErrForbidden -> 403.
func (s *Service) RequireOrgMember(ctx context.Context, orgID string) (*Session, error) {
	sess, ok := FromContext(ctx)
	if !ok {
		return nil, ErrUnauthenticated
	}
	if err := s.orgs.CheckMemberExistsInOrganization(ctx, parseLimenID(orgID), parseLimenID(sess.UserID)); err != nil {
		return nil, ErrForbidden
	}
	return sess, nil
}

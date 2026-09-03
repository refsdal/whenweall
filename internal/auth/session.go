package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/thecodearcher/limen/plugins/organization"
)

// Session is the seam's view of "who is making this request" — every later plan reads this
// (via FromContext or RequireSession) instead of touching Limen's *limen.ValidatedSession.
type Session struct {
	UserID      string
	Email       string
	ActiveOrgID string // "" when the user has no active organization
	Staff       bool
	// EmailVerified mirrors users.email_verified_at IS NOT NULL. RequireSession/RequireStaff (and
	// AuthMountGuard for Limen's own routes) refuse an unverified session with 403
	// email_unverified; RequireSessionAllowUnverified is the explicit opt-out for the two account
	// routes that must work before verification (PATCH/DELETE /api/v1/me).
	EmailVerified bool
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
	// This is only half of the containment, though: it controls what auth.FromContext returns for
	// *this application's own* handlers (everything internal/polls, internal/bookings, internal/admin
	// etc. register), never what Limen's own mounted routes do — organization's invitations,
	// Limen's own /me, and every other route under /api/v1/auth/ authenticate against Limen's own
	// session validation, which never calls FromContext at all. A locked user with a fresh,
	// Limen-valid session could still call those directly, right past this check. The second half —
	// AuthMountGuard below, wrapped around the whole /api/v1/auth/ mount by
	// internal/httpserver — is what closes that gap; see its own doc comment for the full two-layer
	// picture (also described in migrations/00007_admin_locks.sql).
	//
	// Fails closed: a lock control that fails open on its own query error (treating "couldn't
	// tell" as "not locked") defeats the point of having it, so an error here is treated the same
	// as "locked" — the request is anonymous — rather than falling through to a normal session.
	//
	// This runs as one query with the staff_users check below (two EXISTS subselects, one round
	// trip) rather than two separate ones — folded together since both are per-request lookups
	// keyed on the same validated.User.ID, with no reason to pay for two round trips instead of
	// one. The two checks fail differently on their own, though (asymmetric on purpose, not an
	// oversight of the fold): a locked_users error must fail CLOSED (deny — see above), while a
	// staff_users error has always failed to false (an ordinary, non-staff session, not an error
	// the caller has to handle — RequireStaff's own 403 already covers "not staff" whether that's
	// because the row is genuinely absent or because this query couldn't tell). Combined into one
	// query, though, an error is one error for both columns at once — there is no scanning "only
	// the locked column failed." Resolving that the same way locked_users' own asymmetry already
	// requires — fail closed — is what the switch below does: a query error blanks the whole
	// session (anonymous), which satisfies locked_users' fail-closed contract outright, and, as a
	// side effect, also can never leave staff looking true on an error (an anonymous request has
	// no Staff field for any caller to read at all) — it just does so via a stricter path (no
	// session) than staff_users' own the-row-must-be-missing-or-existing "fails to false" used to
	// take on its own (a valid, non-staff session). That's a real behavior change for a
	// staff_users-only failure (previously: a normal non-staff session; now: anonymous) — one
	// judged acceptable because it's still no less safe (RequireStaff already denies both), and no
	// existing test exercises that exact failure mode in isolation.
	var locked, staff bool
	switch err := s.db.QueryRowContext(r.Context(), `
		SELECT
			EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1),
			EXISTS(SELECT 1 FROM staff_users WHERE user_id = $1)
	`, validated.User.ID).Scan(&locked, &staff); {
	case err != nil:
		s.logger.Error("auth: locked_users/staff_users check failed; treating session as anonymous", "user_id", fmt.Sprint(validated.User.ID), "error", err)
		return nil
	case locked:
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
		UserID:        fmt.Sprint(validated.User.ID),
		Email:         validated.User.Email,
		Staff:         staff,
		EmailVerified: validated.User.EmailVerifiedAt != nil,
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

	return sess
}

// isUserLocked reports whether userID (Limen's `any` user id, same value resolveSession scans
// off validated.User.ID) has a locked_users row. Used by AuthMountGuard below, which only
// ever needs the lock half of resolveSession's own combined locked_users/staff_users query (that
// query folds both into one round trip for its own reasons — see its doc comment — but this
// middleware has no use for the staff flag at all, so there is no reason to fold it in here too).
// The caller, not this helper, decides how to fail on err != nil; AuthMountGuard fails
// closed (treats "couldn't tell" as locked), matching resolveSession's own locked_users half.
func (s *Service) isUserLocked(ctx context.Context, userID any) (bool, error) {
	var locked bool
	err := s.db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM locked_users WHERE user_id = $1)", userID,
	).Scan(&locked)
	return locked, err
}

// authMountSignoutMethodAndPath is the one exception AuthMountGuard carves out of the auth mount
// for a LOCKED user: signing out. A locked user has no legitimate reason to reach any other Limen
// route, but blocking signout too would leave them holding a cookie they can never clear.
const authMountSignoutMethodAndPath = "POST /api/v1/auth/signout"

// authMountUnverifiedAllowed lists the Limen routes an UNVERIFIED (but valid) session may still
// reach: reading itself, completing/resending verification, and the credential routes that never
// depend on the caller's session at all (a browser that still carries an unverified session
// cookie must be able to sign in as someone else or reset a password). Everything else under
// /api/v1/auth/ — organizations, invitations, oauth linking, password change — is refused with
// 403 email_unverified until POST /verify-email has run.
//
// Signing out is NOT listed here even though an unverified session obviously needs it too: it is
// handled entirely by authMountSignoutMethodAndPath's exact-match check above, which returns
// before s.limen.GetSession is even called (so it also covers a LOCKED session, which never
// reaches this map at all). A "POST /api/v1/auth/signout" entry here would be unreachable dead
// code — one exemption, one mechanism.
var authMountUnverifiedAllowed = map[string]struct{}{
	"GET /api/v1/auth/me":                       {},
	"POST /api/v1/auth/verify-email":            {},
	"POST /api/v1/auth/email-verifications":     {},
	"POST /api/v1/auth/signin/credential":       {},
	"POST /api/v1/auth/signup/credential":       {},
	"POST /api/v1/auth/passwords/request-reset": {},
	"POST /api/v1/auth/passwords/reset":         {},
}

// AuthMountGuard wraps Limen's own handler — mounted at /api/v1/auth/ by internal/httpserver — and
// applies the two per-user restrictions Limen itself knows nothing about, in this order:
//
//  1. Lock (locked_users): a locked user's otherwise-valid Limen session cannot reach any route
//     under the mount except signout. This is the second, narrower layer resolveSession's own
//     locked check (above) can't provide on its own: that check only ever controls what
//     auth.FromContext returns for *this application's* handlers, because Limen's own plugin
//     routes (organization's invitations, Limen's own GET /me, an OAuth callback, ...)
//     authenticate against Limen's *own* session validation and never call FromContext at all.
//     Concretely: a locked user can still complete a fresh credential sign-in or an OAuth callback
//     — none of Limen's plugins know locked_users exists — minting a brand new, perfectly valid
//     Limen session; without this middleware they could then use that session against any Limen
//     route directly. The fresh session still gets minted (there is no hook early enough to stop
//     that part), but it is useless the moment it tries to do anything except sign out.
//  2. Verification (users.email_verified_at): an unverified session may only reach the routes in
//     authMountUnverifiedAllowed. Same two-layer reasoning — RequireSession covers our handlers,
//     this covers Limen's — and it is why an unverified user cannot, say, accept an invitation
//     addressed to an email they never proved they own.
//
// Fails closed like resolveSession: a locked_users query error is treated as "locked" rather than
// "couldn't tell, so let it through" — see migrations/00007_admin_locks.sql for the full picture.
func (s *Service) AuthMountGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cleaned := path.Clean(strings.TrimSuffix(r.URL.Path, "/"))
		route := r.Method + " " + cleaned
		if route == authMountSignoutMethodAndPath {
			next.ServeHTTP(w, r)
			return
		}

		validated, err := s.limen.GetSession(r)
		if err != nil || validated == nil || validated.User == nil {
			// No valid Limen session at all — nothing for this middleware to restrict; whatever
			// happens next (a signin attempt, an anonymous 401 from Limen itself, ...) is
			// unaffected by locked_users or verification.
			next.ServeHTTP(w, r)
			return
		}

		locked, err := s.isUserLocked(r.Context(), validated.User.ID)
		if err != nil {
			s.logger.Error("auth: locked_users check failed on auth mount; blocking request", "user_id", fmt.Sprint(validated.User.ID), "error", err)
			writeErrorEnvelope(w, http.StatusForbidden, "forbidden", "account is locked")
			return
		}
		if locked {
			writeErrorEnvelope(w, http.StatusForbidden, "forbidden", "account is locked")
			return
		}

		if validated.User.EmailVerifiedAt == nil {
			if _, ok := authMountUnverifiedAllowed[route]; !ok {
				writeUnverified(w)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
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

// writeUnverified is the shared 403 for a session whose email is not yet verified. The SPA maps
// the code to its verify-email flow (web/src/lib/session-guard.ts redirects there before a route
// ever hits this; this is the server-side truth behind that redirect).
func writeUnverified(w http.ResponseWriter) {
	writeErrorEnvelope(w, http.StatusForbidden, "email_unverified", "email address not verified")
}

// RequireSession rejects an anonymous request with 401 {"error":{"code":"unauthenticated",...}}
// and a session whose email is unverified with 403 {"error":{"code":"email_unverified",...}}
// before calling next; a verified session passes through unchanged (next reads the Session back
// out via FromContext same as any other handler). Unverified accounts cannot use the app — this
// is the one gate every domain handler inherits (httpserver.WithOrgSession is built on it).
func (s *Service) RequireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := FromContext(r.Context())
		if !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if !sess.EmailVerified {
			writeUnverified(w)
			return
		}
		next(w, r)
	}
}

// RequireSessionAllowUnverified is RequireSession without the verification gate: 401 for an
// anonymous request, otherwise next. Only for routes an unverified user legitimately needs —
// setting their locale, deleting the account they just made — never for anything that reads or
// writes shared content.
func (s *Service) RequireSessionAllowUnverified(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		next(w, r)
	}
}

// RequireStaff rejects an anonymous request with 401, an unverified one with 403 email_unverified,
// and a non-staff one with 403 forbidden, all as {"error":{"code":...}}.
func (s *Service) RequireStaff(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := FromContext(r.Context())
		if !ok {
			writeErrorEnvelope(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if !sess.EmailVerified {
			writeUnverified(w)
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

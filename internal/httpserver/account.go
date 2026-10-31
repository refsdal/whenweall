package httpserver

// The signed-in user's own account: profile edits, deletion, and the organization switcher. Thin
// handlers over auth.Service — the seam owns validation, cascades and Limen access; this file only
// maps HTTP to those methods and their errors to the standard envelope. Mounted here (not in
// internal/auth) so the handlers can use this package's JSON/Err/DecodeJSON helpers; internal/auth
// must not import internal/httpserver.

import (
	"errors"
	"net/http"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/dataexport"
)

type updateMeRequest struct {
	Name   *string `json:"name"`
	Locale *string `json:"locale"`
}

type deleteMeRequest struct {
	Password string `json:"password"`
}

type switchOrganizationRequest struct {
	OrgID string `json:"orgId"`
}

// registerAccountRoutes mounts the /api/v1/me* routes. PATCH and DELETE deliberately use
// RequireSessionAllowUnverified: an unverified user must be able to set their locale (so the
// verification mail we resend is in their language) and to delete an account they cannot
// otherwise use. The organization routes are verified-only like everything else.
//
// DELETE carries its own rate limit (5/min per client IP, the same shape authRateLimit builds for
// the auth surface): handleDeleteMe's CheckOwnPassword runs Limen's Argon2id hasher
// (t=3, m=64 MiB, p=4) against caller-supplied input, and — unlike every other password-verifying
// entry point in this codebase (POST /signin/credential gets both authRateLimitMiddleware and
// credential-password's own budget) — this route sits outside the Limen mount entirely, so
// nothing metered it at all. Any session, verified or not (RequireSessionAllowUnverified), could
// otherwise spend unlimited 64 MiB Argon2id computations per second: both a memory-exhaustion DoS
// and an unmetered password-guessing oracle for anyone holding a stolen session cookie.
func (s *Server) registerAccountRoutes() {
	deleteMeRateLimit := RateLimit(s.db, "account.delete", 5, time.Minute, func(r *http.Request) string {
		return ClientIP(r, s.cfg.TrustProxy)
	})
	s.mux.HandleFunc("PATCH /api/v1/me", s.authSvc.RequireSessionAllowUnverified(s.handleUpdateMe))
	s.mux.Handle("DELETE /api/v1/me", deleteMeRateLimit(s.authSvc.RequireSessionAllowUnverified(s.handleDeleteMe)))
	s.mux.HandleFunc("GET /api/v1/me/organizations", s.authSvc.RequireSession(s.handleListMyOrganizations))
	s.mux.HandleFunc("POST /api/v1/me/active-organization", s.authSvc.RequireSession(s.handleSwitchOrganization))

	// GET /api/v1/me/export (#50) is the most expensive read in the app — every table the user
	// touches, in one snapshot — so it gets its own budget, keyed by account rather than IP so a
	// shared address cannot starve anyone else's. RequireSession (verified only, unlike PATCH and
	// DELETE above) is load-bearing: the export matches guest answers and bookings by e-mail
	// address, which must be one the caller has proven they own. Session middleware runs before
	// the mux, so the key function can read the session.
	exportRateLimit := RateLimit(s.db, "account.export", 5, time.Hour, func(r *http.Request) string {
		if sess, ok := auth.FromContext(r.Context()); ok {
			return sess.UserID
		}
		return ClientIP(r, s.cfg.TrustProxy)
	})
	s.mux.Handle("GET /api/v1/me/export", sameOriginOnly(exportRateLimit(s.authSvc.RequireSession(s.handleExportMe))))
}

// sameOriginOnly refuses a request the browser marks as coming from another site. CheckOrigin
// (origin.go) only guards mutating methods, and the session cookie is SameSite=Lax, so a hostile
// page can still send a signed-in visitor on a top-level GET. For the export that would drop
// copies of their data into Downloads and burn their hourly budget (it sits outside the rate
// limiter so refusals do not count). The SPA always fetches the export same-origin; a client that
// sends no Sec-Fetch-Site header at all (curl, an older browser) is let through.
func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
			Err(w, http.StatusForbidden, "forbidden", "cross-site request refused", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleExportMe serves the caller's personal data as one JSON download (internal/dataexport for
// what is and is not in it). no-store: it is a full copy of someone's account, and must never sit
// in a shared or browser cache.
func (s *Server) handleExportMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	export, err := dataexport.Build(r.Context(), s.db, sess.UserID)
	if errors.Is(err, dataexport.ErrNoSuchUser) {
		Err(w, http.StatusNotFound, "not_found", "user not found", nil)
		return
	}
	if err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition",
		`attachment; filename="whenweall-export-`+export.ExportedAt.Format("2006-01-02")+`.json"`)
	JSON(w, http.StatusOK, export)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	var req updateMeRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil && req.Locale == nil {
		Err(w, http.StatusBadRequest, "invalid", "nothing to update: send name and/or locale", nil)
		return
	}
	if err := s.authSvc.SetProfile(r.Context(), sess.UserID, req.Name, req.Locale); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteMe(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	var req deleteMeRequest
	// The body is optional (an OAuth-only account has no password to send), so only decode when
	// one was actually sent — DecodeJSON's "request body is required" 400 would be wrong here.
	if r.ContentLength != 0 {
		if !DecodeJSON(w, r, &req) {
			return
		}
	}
	if err := s.authSvc.CheckOwnPassword(r.Context(), sess.UserID, req.Password); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	if err := s.authSvc.DeleteOwnAccount(r.Context(), sess.UserID); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListMyOrganizations(w http.ResponseWriter, r *http.Request) {
	sess, _ := auth.FromContext(r.Context())
	orgs, err := s.authSvc.ListUserOrganizations(r.Context(), sess)
	if err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	JSON(w, http.StatusOK, orgs)
}

func (s *Server) handleSwitchOrganization(w http.ResponseWriter, r *http.Request) {
	var req switchOrganizationRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	if err := s.authSvc.SwitchOrganization(w, r, req.OrgID); err != nil {
		WriteDomainError(w, err, mapAccountError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mapAccountError maps the auth seam's account errors to the envelope (see WriteDomainError).
func mapAccountError(err error) (status int, code, message string, fields map[string]string, ok bool) {
	var verr *auth.ProfileValidationError
	switch {
	case errors.As(err, &verr):
		return http.StatusUnprocessableEntity, "invalid", "validation failed", map[string]string{verr.Field: verr.Message}, true
	case errors.Is(err, auth.ErrNoSuchUser):
		return http.StatusNotFound, "not_found", "user not found", nil, true
	case errors.Is(err, auth.ErrPasswordRequired):
		return http.StatusBadRequest, "password_required", "your current password is required", nil, true
	case errors.Is(err, auth.ErrPasswordMismatch):
		return http.StatusForbidden, "invalid_password", "your current password is incorrect", nil, true
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, "forbidden", "you are not a member of that organization", nil, true
	case errors.Is(err, auth.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated", "authentication required", nil, true
	}
	return 0, "", "", nil, false
}

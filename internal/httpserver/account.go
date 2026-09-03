package httpserver

// The signed-in user's own account: profile edits, deletion, and the organization switcher. Thin
// handlers over auth.Service — the seam owns validation, cascades and Limen access; this file only
// maps HTTP to those methods and their errors to the standard envelope. Mounted here (not in
// internal/auth) so the handlers can use this package's JSON/Err/DecodeJSON helpers; internal/auth
// must not import internal/httpserver.

import (
	"errors"
	"net/http"

	"github.com/refsdal/whenweall/internal/auth"
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
func (s *Server) registerAccountRoutes() {
	s.mux.HandleFunc("PATCH /api/v1/me", s.authSvc.RequireSessionAllowUnverified(s.handleUpdateMe))
	s.mux.HandleFunc("DELETE /api/v1/me", s.authSvc.RequireSessionAllowUnverified(s.handleDeleteMe))
	s.mux.HandleFunc("GET /api/v1/me/organizations", s.authSvc.RequireSession(s.handleListMyOrganizations))
	s.mux.HandleFunc("POST /api/v1/me/active-organization", s.authSvc.RequireSession(s.handleSwitchOrganization))
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

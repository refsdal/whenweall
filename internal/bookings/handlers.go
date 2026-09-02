// Package bookings (this file, handlers.go) is Task 6's HTTP surface — the frontend contract
// plan 8/5 builds against — a thin decode -> Validate -> service -> respond layer over
// src/server/bookings/{pages,bookings}.functions.ts, following internal/polls/handlers.go's own
// wiring pattern exactly (Register mounting on *http.ServeMux via the promoted
// internal/httpserver helpers: Auth, WithOrgSession, DecodeJSON, WriteDomainError,
// RequireCaptchaIfAnon, PublicRateLimit — none of that plumbing is re-derived here).
//
// This task also folds in five requirements accumulated from Tasks 2-5's own code reviews, each
// only reachable once an actual caller identity (auth.Session) exists to check it against:
//
//  1. RequireManageable-equivalent (a): GetOwnedPage/UpdatePage/DeletePage/ListPageBookings/
//     google-status and the organiser half of Cancel are gated behind a canManageContent-shaped
//     check (authz.go's RequireManageablePage/RequireManageableBooking) before the underlying
//     service call — those methods' own brief-pinned signatures (pages.go/bookings.go/google.go)
//     carry an orgID but no userID/role to check against themselves, mirroring internal/polls's
//     own RequireManageable retrofit exactly. (google-status shipped without this gate — a plain
//     a.RequireSession, no org-scoping at all — until a later fix added it; see
//     handleGoogleStatus's own doc comment.) SetOrgSlug (org/handle) is gated separately, by the
//     stricter RequireOwnerRole (authz.go) — see handleSetOrgSlug's own doc comment for why.
//  2. SetOrgSlug's validation field key (b): "handle", not "slug" — fixed at the source
//     (schemas.go's validateHandle), not patched here; see that function's own doc comment.
//  3. UpdatePage's full-replace semantics (c): see handleUpdatePage's own doc comment.
//  4. Wrong manage token -> 403 invalid_token (d): mapServiceError below maps the new
//     ErrInvalidToken sentinel (errors.go) to its own envelope code, distinct from ErrNotFound's
//     404 — see ErrInvalidToken's own doc comment for why Cancel/Reschedule/ManagedBooking now
//     return it instead of the ErrNotFound they used to.
//  5. Organiser Cancel (e): POST .../cancel with no manage token but a session verifies the
//     caller manages the booking's page (RequireManageableBooking) before calling
//     Cancel(byOrganiser: true) — see handleCancel's own doc comment.
//
// I6 (a later fix, past this task's original five): GET .../manage and POST .../reschedule got
// the SAME organiser fallback as (e) above — ManagedBooking/Reschedule (bookings.go) each grew
// their own byOrganiser flag, and handleManagedBooking/handleReschedule each grew the identical
// no-token-but-a-session-that-manages-the-page branch handleCancel already had. Before this fix,
// an organiser with no manage token in hand (a common case — the token lives in a visitor-facing
// mail, not the dashboard) could cancel a booking from the org's own booking-management UI but
// could neither view nor reschedule it the same way, an inconsistency ts parity didn't call for.
package bookings

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
)

// Auth is an alias for httpserver.Auth — see internal/polls/handlers.go's identical alias for why
// (the narrow RequireSession/FromContext/VerifyGuestToken/MintGuestToken seam this package needs
// from auth.Service, kept as an interface so tests can substitute a fake session source). This
// package's own manage-token flow (Book/Cancel/Reschedule/ManagedBooking) never touches
// VerifyGuestToken/MintGuestToken at all — those are polls' guest-*participant* token scheme; a
// booking's manage token is this package's own credential, deterministically derived from its
// booking id (bookings.go's Service.manageToken/verifyManageToken), carried over HTTP as this
// file's own `?t=` query parameter (see manageTokenFromQuery) rather than through the Auth seam.
type Auth = httpserver.Auth

// Register mounts this package's whole HTTP surface on mux, following internal/polls/
// handlers.go's Register exactly: thin handlers, a single shared public rate limiter
// (bookLimit) for every visitor-facing endpoint on this public booking-flow surface — mirroring
// src/server/bookings/bookings.functions.ts's own SERVER_FN_MIDDLEWARE, whose every
// visitor-facing entry point (getPublicAvailability, bookSlot, cancelBooking, rescheduleBooking)
// shares the single 'book' rate-limit bucket — plus GetPublicPage, which the TS source has no
// standalone route for at all (it's only ever called internally by getPublicAvailability there);
// this Go port's own REST split gives it a dedicated endpoint, so it gets the same bookLimit
// rather than being an unmetered slug-enumeration surface. GET .../bookings/{id}/manage
// (handleManagedBooking) is the same shape — an anonymous, token-authenticated lookup an
// attacker could otherwise use to brute-force a booking's 43-character manage token unmetered —
// so it shares bookLimit too (a gap in this row's original wiring, fixed alongside the
// google-status gate above). Captcha-if-anon (RequireCaptchaIfAnon)
// is narrower than the rate limiter: only Book actually calls requireTurnstile in the TS source
// (bookSlot), so only handleBook checks it here — cancelBooking/rescheduleBooking are already
// authenticated by the manage token itself and never call requireTurnstile in the TS source
// either, so this port doesn't add a captcha gate to handleCancel/handleReschedule.
func (s *Service) Register(mux *http.ServeMux, a Auth, cfg *config.Config) {
	bookLimit := httpserver.PublicRateLimit(s.db, "bookings", "book", 20, time.Minute, cfg.TrustProxy)

	mux.Handle("POST /api/v1/booking-pages", httpserver.WithOrgSession(a, s.handleCreatePage))
	mux.Handle("GET /api/v1/booking-pages", httpserver.WithOrgSession(a, s.handleListMyPages))
	mux.Handle("GET /api/v1/booking-pages/{id}", httpserver.WithOrgSession(a, s.handleGetOwnedPage))
	mux.Handle("PATCH /api/v1/booking-pages/{id}", httpserver.WithOrgSession(a, s.handleUpdatePage))
	mux.Handle("DELETE /api/v1/booking-pages/{id}", httpserver.WithOrgSession(a, s.handleDeletePage))
	mux.Handle("GET /api/v1/booking-pages/{id}/bookings", httpserver.WithOrgSession(a, s.handleListPageBookings))
	mux.Handle("GET /api/v1/booking-pages/{id}/google-status", httpserver.WithOrgSession(a, s.handleGoogleStatus))

	mux.Handle("POST /api/v1/org/handle", httpserver.WithOrgSession(a, s.handleSetOrgSlug))
	mux.Handle("POST /api/v1/me/google/disconnect", s.handleDisconnectGoogle(a))

	mux.Handle("GET /api/v1/book/{org}/{page}", bookLimit(http.HandlerFunc(s.handleGetPublicPage)))
	mux.Handle("GET /api/v1/book/{org}/{page}/availability", bookLimit(http.HandlerFunc(s.handlePublicAvailability)))
	mux.Handle("POST /api/v1/book/{org}/{page}/bookings", bookLimit(http.HandlerFunc(s.handleBook(a, cfg))))

	mux.Handle("GET /api/v1/bookings/{id}/manage", bookLimit(http.HandlerFunc(s.handleManagedBooking(a))))
	mux.Handle("GET /api/v1/bookings/{id}/calendar.ics", bookLimit(http.HandlerFunc(s.handleBookingICS(cfg))))
	mux.Handle("POST /api/v1/bookings/{id}/cancel", bookLimit(http.HandlerFunc(s.handleCancel(a))))
	mux.Handle("POST /api/v1/bookings/{id}/reschedule", bookLimit(http.HandlerFunc(s.handleReschedule(a))))
}

// manageTokenFromQuery resolves the visitor manage-token credential from this request's own `?t=`
// query parameter — the frontend's own convention for this exact credential (see
// src/routes/booking/$id/index.tsx's `searchSchema`/its own doc comment: "`?t=` is the manage
// token from the confirmation email"), deliberately NOT httpserver.ExtractGuestToken's `?token=`/
// X-Guest-Token convention: that seam is polls' guest-*participant* token, verified through
// auth.Service; this one is verified entirely inside bookings.go's own verifyManageToken. "" means no
// token was supplied — every one of this file's token-consuming handlers (handleManagedBooking,
// handleCancel, handleReschedule) treats that as "try the owner-session path instead" (I6 —
// each has one).
func manageTokenFromQuery(r *http.Request) string {
	return r.URL.Query().Get("t")
}

// writeServiceError maps this package's own sentinels to the standard HTTP error envelope, via
// httpserver.WriteDomainError's shared "map or log-and-500" plumbing — see mapServiceError below
// for the actual mapping, and internal/polls/handlers.go's identical writeServiceError for why
// this small per-package wrapper exists at all (the envelope-writing core is domain-agnostic and
// lives in internal/httpserver; only the sentinel vocabulary below is this package's own).
func writeServiceError(w http.ResponseWriter, err error) {
	httpserver.WriteDomainError(w, err, mapServiceError)
}

// mapServiceError is this package's own DomainErrorMapper. Ordering matters: every specific
// ErrConflict-wrapping sentinel (ErrSlugTaken/ErrHandleTaken/ErrSlotTaken/ErrPagePaused/
// ErrBookingPast/ErrGoogleNotConnected — errors.go) is checked BEFORE the bare ErrConflict case,
// since each of them also satisfies errors.Is(err, ErrConflict); a bare ErrConflict (none of the
// six) falls through to a generic "conflict". ErrInvalidToken -> 403 "invalid_token" is Task 6's
// own accumulated requirement (d) — see ErrInvalidToken's doc comment in errors.go.
func mapServiceError(err error) (status int, code, message string, fields map[string]string, ok bool) {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return http.StatusUnprocessableEntity, "invalid", "validation failed", verr.Fields, true
	case errors.Is(err, ErrSlugTaken):
		return http.StatusConflict, "slug_taken", "this slug is already in use", nil, true
	case errors.Is(err, ErrHandleTaken):
		return http.StatusConflict, "handle_taken", "this handle is already in use", nil, true
	case errors.Is(err, ErrSlotTaken):
		return http.StatusConflict, "slot_taken", "this slot is no longer available", nil, true
	case errors.Is(err, ErrPagePaused):
		return http.StatusConflict, "page_paused", "this page is not currently accepting bookings", nil, true
	case errors.Is(err, ErrBookingPast):
		return http.StatusConflict, "booking_past", "this slot has already passed", nil, true
	case errors.Is(err, ErrGoogleNotConnected):
		return http.StatusConflict, "google_not_connected", "google calendar is not connected", nil, true
	case errors.Is(err, ErrConflict):
		return http.StatusConflict, "conflict", "this booking's current state does not allow this", nil, true
	case errors.Is(err, ErrInvalidToken):
		return http.StatusForbidden, "invalid_token", "invalid manage token", nil, true
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound, "not_found", "not found", nil, true
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden, "forbidden", "forbidden", nil, true
	default:
		return 0, "", "", nil, false
	}
}

func respondOK(w http.ResponseWriter) {
	httpserver.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// requireOwnerSession resolves the signed-in owner branch's own session gate — the same two
// failure shapes httpserver.WithOrgSession itself would produce (401 "unauthenticated" for no
// session at all, 403 "no_active_org" for a session with no active organization), reached here
// conditionally (handleManagedBooking/handleCancel/handleReschedule each only take this branch
// once they already know no manage token was supplied — I6) rather than unconditionally the way
// WithOrgSession's own wrapper does. ok == false
// means the response has already been written; the caller must return immediately.
func requireOwnerSession(w http.ResponseWriter, r *http.Request, a Auth) (*auth.Session, bool) {
	sess, ok := a.FromContext(r.Context())
	if !ok {
		httpserver.Err(w, http.StatusUnauthorized, "unauthenticated", "a manage token or an active-org session is required", nil)
		return nil, false
	}
	if sess.ActiveOrgID == "" {
		httpserver.Err(w, http.StatusForbidden, "no_active_org", "no active organization", nil)
		return nil, false
	}
	return sess, true
}

// parseQueryTime parses r's param query parameter as an RFC3339 datetime, writing the standard
// "invalid" envelope and returning ok == false on any parse failure (including an absent/blank
// parameter). Used by handleListPageBookings's own ?from&to — web/src/routes/bookings/$id/index.tsx's
// listPageBookings caller sends `Date.toISOString()` values, so this stays strict.
func parseQueryTime(w http.ResponseWriter, r *http.Request, param string) (time.Time, bool) {
	raw := r.URL.Query().Get(param)
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		httpserver.Err(w, http.StatusBadRequest, "invalid", param+" must be an RFC3339 datetime",
			map[string]string{param: "must be an RFC3339 datetime"})
		return time.Time{}, false
	}
	return t, true
}

// parseQueryDate parses r's param query parameter as a bare "YYYY-MM-DD" date (UTC midnight),
// writing the standard "invalid" envelope and returning ok == false on any parse failure.
//
// Used only by handlePublicAvailability's own ?from&to — src/server/bookings/schemas.ts's old
// availabilityQuerySchema validated these two with `z.iso.date()` (no time component) and built
// UTC-midnight Dates from them (`new Date(`${data.from}T00:00:00Z`)`), and
// web/src/routes/book/$handle/$slug.tsx's monthWindow still sends exactly that shape
// (`date.toISOString().slice(0, 10)`) — never updated for a Go backend that used to require a
// full RFC3339 datetime here instead, which made every real /book/{org}/{page} page load 400 on
// its own availability fetch. UTC midnight is the right anchor to parse into, not just a
// convenient one: Slots (availability.go) only ever reduces from/to to a local *date* string via
// localDateStr, and monthWindow already pads its own window by a day either side specifically so
// a UTC-midnight instant lands on the intended local date even when the page's own timezone is
// hours off UTC.
func parseQueryDate(w http.ResponseWriter, r *http.Request, param string) (time.Time, bool) {
	raw := r.URL.Query().Get(param)
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		httpserver.Err(w, http.StatusBadRequest, "invalid", param+" must be a YYYY-MM-DD date",
			map[string]string{param: "must be a YYYY-MM-DD date"})
		return time.Time{}, false
	}
	return t, true
}

// ---- request DTOs -------------------------------------------------------------------------

// pageRequest ports pageSchema's request shape (CreatePage/UpdatePage share it — see PageInput's
// own doc comment on why UpdatePage is a full replacement, not a partial one). DateOverrides has
// no JSON tag override needed: an absent key decodes to a nil map (PageInput's own "omitted"
// convention), a present-but-empty object decodes to a non-nil empty map.
type pageRequest struct {
	Slug            string        `json:"slug"`
	Title           string        `json:"title"`
	Description     *string       `json:"description"`
	Location        *string       `json:"location"`
	Timezone        string        `json:"timezone"`
	SlotDurationMin int           `json:"slotDurationMin"`
	BufferBeforeMin int           `json:"bufferBeforeMin"`
	BufferAfterMin  int           `json:"bufferAfterMin"`
	MinNoticeMin    int           `json:"minNoticeMin"`
	MaxDaysAhead    int           `json:"maxDaysAhead"`
	Availability    Availability  `json:"availability"`
	DateOverrides   DateOverrides `json:"dateOverrides"`
	GoogleSync      bool          `json:"googleSync"`
	Reminders       bool          `json:"reminders"`
	// Status is only meaningful on an update request; CreatePage's own service method ignores it
	// (always writes "active" — see PageInput's doc comment).
	Status string `json:"status"`
}

// toInput converts to PageInput via a plain type conversion rather than a field-by-field struct
// literal (staticcheck's own S1016) — pageRequest's fields are declared in exactly the same order
// and types as PageInput's (schemas.go), purely so this conversion is valid; keep the two in sync
// if either one's shape ever changes.
func (r pageRequest) toInput() PageInput {
	return PageInput(r)
}

type setOrgSlugRequest struct {
	Handle string `json:"handle"`
}

type bookRequest struct {
	StartAt  string  `json:"startAt"`
	Name     string  `json:"name"`
	Email    string  `json:"email"`
	Note     *string `json:"note"`
	Locale   *string `json:"locale"`
	Timezone string  `json:"timezone"`
}

type rescheduleRequest struct {
	StartAt string `json:"startAt"`
}

// ---- handlers: booking pages (owner-facing) --------------------------------------------------

func (s *Service) handleCreatePage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var req pageRequest
	if !httpserver.DecodeJSON(w, r, &req) {
		return
	}
	view, err := s.CreatePage(r.Context(), sess.ActiveOrgID, sess.UserID, req.toInput())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, view)
}

func (s *Service) handleListMyPages(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	summaries, err := s.ListMyPages(r.Context(), sess.ActiveOrgID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, summaries)
}

func (s *Service) handleGetOwnedPage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pageID := r.PathValue("id")
	if err := s.RequireManageablePage(r.Context(), pageID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := s.GetOwnedPage(r.Context(), pageID, sess.ActiveOrgID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, view)
}

// handleUpdatePage ports updateBookingPage (pages.functions.ts). Accumulated requirement (c): this
// is a FULL replacement of every editable field (PageInput's own doc comment — there is no
// PATCH-style "omitted means unchanged" here, despite the HTTP method being PATCH), so a client
// changing one field must round-trip GetOwnedPage first to get the page's current values for
// every other field, then send the whole shape back.
func (s *Service) handleUpdatePage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pageID := r.PathValue("id")
	if err := s.RequireManageablePage(r.Context(), pageID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	var req pageRequest
	if !httpserver.DecodeJSON(w, r, &req) {
		return
	}
	view, err := s.UpdatePage(r.Context(), pageID, sess.ActiveOrgID, req.toInput())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, view)
}

func (s *Service) handleDeletePage(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pageID := r.PathValue("id")
	if err := s.RequireManageablePage(r.Context(), pageID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	if err := s.DeletePage(r.Context(), pageID, sess.ActiveOrgID); err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"id": pageID, "deleted": true})
}

func (s *Service) handleListPageBookings(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pageID := r.PathValue("id")
	if err := s.RequireManageablePage(r.Context(), pageID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	from, ok := parseQueryTime(w, r, "from")
	if !ok {
		return
	}
	to, ok := parseQueryTime(w, r, "to")
	if !ok {
		return
	}
	views, err := s.ListPageBookings(r.Context(), pageID, sess.ActiveOrgID, from, to)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, views)
}

// handleGoogleStatus ports getGoogleCalendarStatus (pages.functions.ts) at Task 6's own
// per-page shape — see Service.GoogleStatus's own doc comment (google.go) for how this
// simplifies the TS source. Gated by RequireManageablePage, the same canManageContent-shaped
// check every other owner-facing route over a page id in this file uses (requirement (a)) — a
// page belonging to a different org (or a caller who neither created it nor manages the org)
// must 404/403 before GoogleStatus ever runs, the same leak-avoidance rule requireOrgPage's own
// doc comment gives (pages.go): a page id's existence, and now whether it has Google sync wired
// up, must never be revealed outside its own org.
func (s *Service) handleGoogleStatus(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pageID := r.PathValue("id")
	if err := s.RequireManageablePage(r.Context(), pageID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	available, err := s.GoogleStatus(r.Context(), pageID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]bool{"available": available})
}

// handleSetOrgSlug ports setHandle (pages.functions.ts) — gated by RequireOwnerRole (authz.go),
// not the wider RequireManageablePage-shaped canManageContent check every other owner-facing
// route in this file uses: renaming the org's public handle isn't "manage everything" territory
// for an admin (spec §1 — see RequireOwnerRole's own doc comment), so only the org's owner may
// call this.
func (s *Service) handleSetOrgSlug(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	if err := s.RequireOwnerRole(r.Context(), sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	var req setOrgSlugRequest
	if !httpserver.DecodeJSON(w, r, &req) {
		return
	}
	if err := s.SetOrgSlug(r.Context(), sess.ActiveOrgID, req.Handle); err != nil {
		writeServiceError(w, err)
		return
	}
	respondOK(w)
}

// handleDisconnectGoogle ports disconnectGoogleCalendar (pages.functions.ts) — "auth" only (a
// plain session, not an org), matching Service.DisconnectGoogleSync's own signature (userID,
// no org at all).
func (s *Service) handleDisconnectGoogle(a Auth) http.HandlerFunc {
	return a.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.FromContext(r.Context())
		if !ok {
			httpserver.Err(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
			return
		}
		if err := s.DisconnectGoogleSync(r.Context(), sess.UserID); err != nil {
			writeServiceError(w, err)
			return
		}
		respondOK(w)
	})
}

// ---- handlers: public booking flow -------------------------------------------------------------

func (s *Service) handleGetPublicPage(w http.ResponseWriter, r *http.Request) {
	orgSlug, pageSlug := r.PathValue("org"), r.PathValue("page")
	page, err := s.GetPublicPage(r.Context(), orgSlug, pageSlug)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if page == nil {
		httpserver.Err(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	httpserver.JSON(w, http.StatusOK, page)
}

// handlePublicAvailability ports the availability half of getPublicAvailability
// (bookings.functions.ts). The page-existence check runs through GetPublicPage first (rather than
// trusting PublicAvailability's own (nil, nil) return) because Slots (availability.go) can
// legitimately return an empty/nil slot list for a perfectly valid page too — GetPublicPage's own
// nil is the only unambiguous "no such page" signal.
//
// I3: ports publicAvailabilityQuerySchema's own window refinement (schemas.ts) — `to` before
// `from`, or a span past LimitPublicWindowDays (62 days), is rejected with the standard 400
// "invalid" envelope (field "to", mirroring the TS source's own `path: ['to']`) before
// PublicAvailability ever runs a query against it. Without this cap a caller could ask for an
// arbitrarily large window (say, 50 years) and force PublicAvailability/Slots to walk every day
// in it — Slots (availability.go) now also has its own defensive horizon break as a second line
// of defense, but this is the cheap rejection that should catch it first.
func (s *Service) handlePublicAvailability(w http.ResponseWriter, r *http.Request) {
	orgSlug, pageSlug := r.PathValue("org"), r.PathValue("page")
	page, err := s.GetPublicPage(r.Context(), orgSlug, pageSlug)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if page == nil {
		httpserver.Err(w, http.StatusNotFound, "not_found", "not found", nil)
		return
	}

	from, ok := parseQueryDate(w, r, "from")
	if !ok {
		return
	}
	to, ok := parseQueryDate(w, r, "to")
	if !ok {
		return
	}
	if to.Before(from) || to.Sub(from) > LimitPublicWindowDays*24*time.Hour {
		httpserver.Err(w, http.StatusBadRequest, "invalid",
			fmt.Sprintf("window must be between 0 and %d days", LimitPublicWindowDays),
			map[string]string{"to": fmt.Sprintf("window must be between 0 and %d days", LimitPublicWindowDays)})
		return
	}

	slots, err := s.PublicAvailability(r.Context(), orgSlug, pageSlug, from, to)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	out := make([]string, 0, len(slots))
	for _, t := range slots {
		out = append(out, formatISO(t))
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"slots": out})
}

// handleBook ports bookSlot (bookings.functions.ts): captcha-if-anon (a session always bypasses
// it, same as every other public mutation in this codebase — see this file's own Register doc
// comment), then decode -> Book -> 201 {booking, manageToken}. The freshly created booking is
// re-read (s.q.GetBooking) rather than synthesized from the request, so the response always
// reflects exactly what was persisted (e.g. the server-computed endAt).
func (s *Service) handleBook(a Auth, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgSlug, pageSlug := r.PathValue("org"), r.PathValue("page")

		if err := httpserver.RequireCaptchaIfAnon(cfg, a, r); err != nil {
			httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}

		var req bookRequest
		if !httpserver.DecodeJSON(w, r, &req) {
			return
		}
		startAt, err := time.Parse(time.RFC3339, req.StartAt)
		if err != nil {
			httpserver.Err(w, http.StatusBadRequest, "invalid", "startAt must be an RFC3339 datetime",
				map[string]string{"startAt": "must be an RFC3339 datetime"})
			return
		}

		result, err := s.Book(r.Context(), orgSlug, pageSlug, BookInput{
			StartAt: startAt, Name: req.Name, Email: req.Email, Note: req.Note,
			Locale: req.Locale, Timezone: req.Timezone,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}

		booking, err := s.q.GetBooking(r.Context(), result.BookingID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusCreated, map[string]any{
			"booking":     toBookingView(booking),
			"manageToken": result.ManageToken,
		})
	}
}

// handleManagedBooking ports getManagedBooking (bookings.functions.ts), plus I6's own organiser
// fallback: without a manage token, the caller must be signed in AND manage the booking's page
// (RequireManageableBooking, authz.go's creator-or-org-manager gate) — only then is
// ManagedBooking(byOrganiser: true) reached, the exact same shape handleCancel's own requirement
// (e) already has.
func (s *Service) handleManagedBooking(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := r.PathValue("id")

		if token := manageTokenFromQuery(r); token != "" {
			view, err := s.ManagedBooking(r.Context(), bookingID, token, false)
			if err != nil {
				writeServiceError(w, err)
				return
			}
			httpserver.JSON(w, http.StatusOK, view)
			return
		}

		sess, ok := requireOwnerSession(w, r, a)
		if !ok {
			return
		}
		if err := s.RequireManageableBooking(r.Context(), bookingID, sess.ActiveOrgID, sess.UserID); err != nil {
			writeServiceError(w, err)
			return
		}
		view, err := s.ManagedBooking(r.Context(), bookingID, "", true)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, view)
	}
}

// handleBookingICS serves bookingID's own .ics download over ?t=<manage token> — the standalone
// re-download surface BookingICS (bookings.go) backs; see that method's own doc comment for why
// this route has no organiser-session fallback the way handleManagedBooking/handleCancel/
// handleReschedule each do. Content-Disposition is deliberately omitted: this response is meant to
// be opened by the browser's own calendar-import flow (the same reason the mailed attachment's own
// filename, "calendar.ics", is enough context on its own), not saved to disk under a
// server-chosen name.
func (s *Service) handleBookingICS(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := r.PathValue("id")
		token := manageTokenFromQuery(r)

		ics, err := s.BookingICS(r.Context(), bookingID, token, cfg.AppURL)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(ics)
	}
}

// handleCancel ports cancelBooking (bookings.functions.ts), plus this task's accumulated
// requirement (e): without a manage token, the caller must be signed in AND manage the booking's
// page (RequireManageableBooking, authz.go's creator-or-org-manager gate) — only then is
// Cancel(byOrganiser: true) reached. No captcha gate here (unlike handleBook): a token-bearing
// visitor is already authenticated by the manage token itself, and cancelBooking never calls
// requireTurnstile in the TS source either (only bookSlot does) — see this file's Register doc
// comment.
func (s *Service) handleCancel(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := r.PathValue("id")

		if token := manageTokenFromQuery(r); token != "" {
			if err := s.Cancel(r.Context(), bookingID, token, false); err != nil {
				writeServiceError(w, err)
				return
			}
			respondOK(w)
			return
		}

		sess, ok := requireOwnerSession(w, r, a)
		if !ok {
			return
		}
		if err := s.RequireManageableBooking(r.Context(), bookingID, sess.ActiveOrgID, sess.UserID); err != nil {
			writeServiceError(w, err)
			return
		}
		if err := s.Cancel(r.Context(), bookingID, "", true); err != nil {
			writeServiceError(w, err)
			return
		}
		respondOK(w)
	}
}

// handleReschedule ports rescheduleBooking (bookings.functions.ts), plus I6's own organiser
// fallback (the same shape handleCancel's requirement (e) and handleManagedBooking's own I6 fix
// above both have): without a manage token, the caller must be signed in AND manage the booking's
// page (RequireManageableBooking) before Reschedule(byOrganiser: true) is reached. No captcha
// gate here either, for the same reason handleCancel has none: the manage token already
// authenticates a token-bearing caller, and rescheduleBooking never calls requireTurnstile in the
// TS source; the organiser path is authenticated by its own session instead.
func (s *Service) handleReschedule(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := r.PathValue("id")

		var req rescheduleRequest
		if !httpserver.DecodeJSON(w, r, &req) {
			return
		}
		newStart, err := time.Parse(time.RFC3339, req.StartAt)
		if err != nil {
			httpserver.Err(w, http.StatusBadRequest, "invalid", "startAt must be an RFC3339 datetime",
				map[string]string{"startAt": "must be an RFC3339 datetime"})
			return
		}

		var result *BookingResult
		if token := manageTokenFromQuery(r); token != "" {
			result, err = s.Reschedule(r.Context(), bookingID, token, newStart, false)
		} else {
			sess, ok := requireOwnerSession(w, r, a)
			if !ok {
				return
			}
			if err := s.RequireManageableBooking(r.Context(), bookingID, sess.ActiveOrgID, sess.UserID); err != nil {
				writeServiceError(w, err)
				return
			}
			result, err = s.Reschedule(r.Context(), bookingID, "", newStart, true)
		}
		if err != nil {
			writeServiceError(w, err)
			return
		}

		booking, err := s.q.GetBooking(r.Context(), bookingID)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		httpserver.JSON(w, http.StatusOK, map[string]any{
			"booking":         toBookingView(booking),
			"previousStartAt": formatISO(result.PreviousStartAt),
		})
	}
}

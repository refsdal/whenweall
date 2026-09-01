package polls

// Task 7: the poll/sign-up-sheet HTTP API surface — the frontend contract plan 8 builds against.
// Ports src/server/polls/polls.functions.ts + participants.functions.ts + config.functions.ts +
// roster.ts's HTTP-facing wrappers around the Service methods Tasks 2-4 already built (Create,
// GetView, Update, ..., Claim, Unclaim, ...): decode -> validate -> call the already-tested
// service method -> respond. Every request/response shape and error mapping below is new to this
// task; the domain logic it calls into is not.
//
// Five requirements accumulated from Task 2-4 code reviews are folded in here, at this layer,
// because this is the first layer with an actual caller identity (auth.Session) to check them
// against:
//
//  1. Authz retrofit (a): Update/SetStatus/Finalize/Delete/Duplicate/roster.csv are gated behind
//     Service.RequireManageable (service.go) — creator-or-org-manager — before the underlying
//     call, since those five T2 methods' own brief-pinned signatures carry only an orgID, never a
//     userID/role to check that against themselves.
//  2. Manager force-unclaim (b): DELETE .../claims/{oid} accepts an optional participantId (query
//     or body) — Service.UnclaimFor (claims.go).
//  3. Wrong-org -> 404 (c): requireOrgPoll (service.go) now maps a wrong-org poll to ErrNotFound,
//     not ErrForbidden — see its doc comment. This handler layer's writeServiceError reflects
//     that directly (ErrNotFound -> 404) with no special-casing needed here.
//  4. Digest/event wiring (d): AddParticipant/UpdateParticipant/RemoveParticipant/AddComment/Claim/
//     Unclaim enqueue a digest item (Service.EnqueueDigestItem, T4's "currently uncalled"
//     capability) the same way participants.functions.ts's emitPollEvent call sites do, plus the
//     signup.full check (PollRoom's #emitIfSheetFilled) after a claim that filled the last slot.
//  5. Finalize actor (e): handleFinalize passes the caller's own userID through to
//     Service.Finalize (service.go), which now excludes them from the poll.finalized subscriber
//     notification it enqueues.
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/refsdal/whenweall/internal/auth"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/httpserver"
	"github.com/refsdal/whenweall/internal/polls/queries"
)

// Auth is the narrow seam this package needs from auth.Service — RequireSession/FromContext/
// VerifyGuestToken/MintGuestToken — kept as an interface (rather than importing *auth.Service
// directly into every signature below) so tests can substitute a fake session/guest-token source
// instead of driving a real signup/signin flow through Limen for every one of this file's tests.
// auth.Service satisfies this with no adapter needed (FromContext is a plain delegation method
// added alongside this interface — see internal/auth/session.go).
type Auth interface {
	RequireSession(next http.HandlerFunc) http.HandlerFunc
	FromContext(ctx context.Context) (*auth.Session, bool)
	VerifyGuestToken(token string) (string, bool)
	MintGuestToken(participantID string) string
}

// Register mounts this package's whole HTTP surface on mux. Handler rules throughout: thin
// (decode -> Validate -> service -> respond); guest identity via X-Guest-Token header or ?token=;
// captcha (Cloudflare Turnstile, cfg-gated) on every public mutating endpoint's anonymous callers
// only — a signed-in caller never needs it, mirroring participants.functions.ts's own
// `if (!userId) await requireTurnstile(...)` branch; a light per-IP rate limit on the same public
// mutating endpoints, keyed like internal/httpserver's own authRateLimit.
func (s *Service) Register(mux *http.ServeMux, a Auth, cfg *config.Config) {
	voteLimit := s.publicRateLimit(cfg, "vote", 30, time.Minute)
	commentLimit := s.publicRateLimit(cfg, "comment", 20, time.Minute)

	mux.Handle("POST /api/v1/polls", withOrgSession(a, s.handleCreate))
	mux.HandleFunc("GET /api/v1/polls/{id}", s.handleGetView(a))
	mux.Handle("PATCH /api/v1/polls/{id}", withOrgSession(a, s.handleUpdate))
	mux.Handle("POST /api/v1/polls/{id}/status", withOrgSession(a, s.handleSetStatus))
	mux.Handle("POST /api/v1/polls/{id}/finalize", withOrgSession(a, s.handleFinalize))
	mux.Handle("DELETE /api/v1/polls/{id}", withOrgSession(a, s.handleDelete))
	mux.Handle("POST /api/v1/polls/{id}/duplicate", withOrgSession(a, s.handleDuplicate))
	mux.Handle("GET /api/v1/polls", withOrgSession(a, s.handleListMine))

	mux.Handle("POST /api/v1/polls/{id}/participants", voteLimit(http.HandlerFunc(s.handleAddParticipant(a, cfg))))
	mux.HandleFunc("PATCH /api/v1/polls/{id}/participants/{pid}", s.handleUpdateParticipant(a))
	mux.HandleFunc("DELETE /api/v1/polls/{id}/participants/{pid}", s.handleRemoveParticipant(a))

	mux.Handle("POST /api/v1/polls/{id}/comments", commentLimit(http.HandlerFunc(s.handleAddComment(a, cfg))))
	mux.HandleFunc("DELETE /api/v1/polls/{id}/comments/{cid}", s.handleDeleteComment(a))

	mux.Handle("POST /api/v1/polls/{id}/claims", voteLimit(http.HandlerFunc(s.handleClaim(a, cfg))))
	mux.HandleFunc("DELETE /api/v1/polls/{id}/claims/{oid}", s.handleUnclaim(a))

	mux.HandleFunc("GET /api/v1/polls/{id}/calendar.ics", s.handleCalendarICS)
	mux.Handle("GET /api/v1/polls/{id}/roster.csv", withOrgSession(a, s.handleRosterCSV))

	mux.Handle("POST /api/v1/me/notification-prefs", withOrgSession(a, s.handleUpdateNotificationPrefs))
	mux.Handle("POST /api/v1/polls/{id}/following", withOrgSession(a, s.handleSetFollowing))

	mux.HandleFunc("GET /api/v1/config", handleConfig(cfg))
}

// publicRateLimit builds a per-IP rate limiter over this Service's own *sql.DB — the same fixed-
// window counter internal/httpserver.RateLimit uses for the auth surface, just namespaced
// "polls.<name>" so the two never share a bucket.
func (s *Service) publicRateLimit(cfg *config.Config, name string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return httpserver.RateLimit(s.db, "polls."+name, limit, window, func(r *http.Request) string {
		return httpserver.ClientIP(r, cfg.TrustProxy)
	})
}

// withOrgSession requires a valid session (401 otherwise) AND a caller with an active
// organization (403 "no_active_org" otherwise — practically unreachable once signed in, since
// auth.Service's own session resolution always defaults ActiveOrgID to the caller's personal org,
// but every "auth+org" row in the brief's table needs an orgID to pass to the Service methods
// below, so this is checked explicitly rather than assumed).
func withOrgSession(a Auth, next func(w http.ResponseWriter, r *http.Request, sess *auth.Session)) http.HandlerFunc {
	return a.RequireSession(func(w http.ResponseWriter, r *http.Request) {
		sess, ok := a.FromContext(r.Context())
		if !ok {
			httpserver.Err(w, http.StatusUnauthorized, "unauthenticated", "authentication required", nil)
			return
		}
		if sess.ActiveOrgID == "" {
			httpserver.Err(w, http.StatusForbidden, "no_active_org", "no active organization", nil)
			return
		}
		next(w, r, sess)
	})
}

// guestParticipantID resolves the caller's guest edit token — X-Guest-Token header first, then
// ?token= — into a verified participant id, or "" for no/invalid token.
func guestParticipantID(a Auth, r *http.Request) string {
	token := r.Header.Get("X-Guest-Token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		return ""
	}
	pid, ok := a.VerifyGuestToken(token)
	if !ok {
		return ""
	}
	return pid
}

// viewerFromRequest resolves a Viewer for a public(token)|auth endpoint: the caller's own userID
// if signed in (never required), plus any verified guest participant id.
func viewerFromRequest(a Auth, r *http.Request) Viewer {
	v := Viewer{GuestParticipantID: guestParticipantID(a, r)}
	if sess, ok := a.FromContext(r.Context()); ok {
		v.UserID = sess.UserID
	}
	return v
}

// requireCaptchaIfAnon ports participants.functions.ts's own `if (!userId) await
// requireTurnstile(...)` branch: captcha is only ever demanded of an anonymous caller, and only
// when Turnstile is actually configured (cfg.Capabilities.Turnstile) — see
// internal/httpserver.RequireCaptcha's doc comment for the same capability gate. The token travels
// in the X-Captcha-Token header, matching this codebase's existing convention (RequireCaptcha),
// rather than the TS source's own body field (a server-function-specific concern this REST surface
// doesn't share).
func requireCaptchaIfAnon(cfg *config.Config, a Auth, r *http.Request) error {
	if sess, ok := a.FromContext(r.Context()); ok && sess.UserID != "" {
		return nil
	}
	if !cfg.Capabilities.Turnstile {
		return nil
	}
	token := r.Header.Get("X-Captcha-Token")
	remoteIP := httpserver.ClientIP(r, cfg.TrustProxy)
	return httpserver.VerifyTurnstile(r.Context(), cfg.TurnstileSecretKey, token, remoteIP)
}

// decodeJSON decodes r's JSON body into dst, writing the standard "invalid" envelope and
// returning false on any decode failure (including a missing body).
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		httpserver.Err(w, http.StatusBadRequest, "invalid", "request body is required", nil)
		return false
	}
	defer func() { _ = r.Body.Close() }()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		httpserver.Err(w, http.StatusBadRequest, "invalid", "malformed JSON body", nil)
		return false
	}
	return true
}

// writeServiceError maps every sentinel this package's Service methods can return to the standard
// HTTP error envelope. *ValidationError -> 422 "invalid" (carrying Fields); ErrCapacityFull -> 409
// "capacity_full" (checked before the more general ErrConflict, though the two sentinels never
// overlap); ErrConflict -> 409 "conflict"; ErrNotFound -> 404 "not_found" (this is where
// requireOrgPoll's wrong-org -> ErrNotFound mapping, and RequireManageable's own NOT_FOUND half,
// surface as a real 404 — see this file's package doc comment, item (c)); ErrForbidden -> 403
// "forbidden". Anything else is logged and reported as a generic 500.
func writeServiceError(w http.ResponseWriter, err error) {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		httpserver.Err(w, http.StatusUnprocessableEntity, "invalid", "validation failed", verr.Fields)
	case errors.Is(err, ErrCapacityFull):
		httpserver.Err(w, http.StatusConflict, "capacity_full", "this slot is full", nil)
	case errors.Is(err, ErrConflict):
		httpserver.Err(w, http.StatusConflict, "conflict", "the poll's current state does not allow this", nil)
	case errors.Is(err, ErrNotFound):
		httpserver.Err(w, http.StatusNotFound, "not_found", "not found", nil)
	case errors.Is(err, ErrForbidden):
		httpserver.Err(w, http.StatusForbidden, "forbidden", "forbidden", nil)
	default:
		slog.Default().Error("polls: unhandled service error", "error", err)
		httpserver.Err(w, http.StatusInternalServerError, "internal", "internal error", nil)
	}
}

func respondOK(w http.ResponseWriter) {
	httpserver.JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- request/response DTOs -------------------------------------------------------------------

// optionRequest is one entry of a create/update request's "options" array. Capacity is
// json.RawMessage (rather than *int) specifically so toInput can tell "the key was absent" (nil)
// apart from "the key was present with a JSON null" ([]byte("null")) apart from "the key was
// present with a number" — the 3-state OptionInput.CapacitySet/Capacity needs (see its doc
// comment in schemas.go). A plain *int can't make that distinction: encoding/json sets a pointer
// field to nil for BOTH an absent key and an explicit JSON null.
type optionRequest struct {
	ID       string          `json:"id"`
	Kind     string          `json:"kind"`
	Date     string          `json:"date"`
	StartAt  string          `json:"startAt"`
	EndAt    *string         `json:"endAt"`
	Label    string          `json:"label"`
	Capacity json.RawMessage `json:"capacity"`
}

func (o optionRequest) toInput(index int) (OptionInput, error) {
	in := OptionInput{ID: o.ID, Kind: OptionKind(o.Kind), Date: o.Date, StartAt: o.StartAt, EndAt: o.EndAt, Label: o.Label}
	if o.Capacity != nil {
		in.CapacitySet = true
		if string(o.Capacity) != "null" {
			var n int
			if err := json.Unmarshal(o.Capacity, &n); err != nil {
				return OptionInput{}, newValidationError(
					"options."+strconv.Itoa(index)+".capacity", "capacity must be a number",
				)
			}
			in.Capacity = &n
		}
	}
	return in, nil
}

func toOptionInputs(reqs []optionRequest) ([]OptionInput, error) {
	out := make([]OptionInput, len(reqs))
	for i, r := range reqs {
		in, err := r.toInput(i)
		if err != nil {
			return nil, err
		}
		out[i] = in
	}
	return out, nil
}

type createPollRequest struct {
	Type                    string          `json:"type"`
	Title                   string          `json:"title"`
	Description             *string         `json:"description"`
	Location                *string         `json:"location"`
	Timezone                string          `json:"timezone"`
	DeadlineAt              *string         `json:"deadlineAt"`
	Options                 []optionRequest `json:"options"`
	SignupMaxClaims         *int            `json:"signupMaxClaims"`
	RequireParticipantEmail *bool           `json:"requireParticipantEmail"`
	AllowComments           *bool           `json:"allowComments"`
	AllowIfNeedBe           *bool           `json:"allowIfNeedBe"`
}

func (req createPollRequest) toInput() (CreatePollInput, error) {
	options, err := toOptionInputs(req.Options)
	if err != nil {
		return CreatePollInput{}, err
	}
	return CreatePollInput{
		Type:            PollType(req.Type),
		Title:           req.Title,
		Description:     req.Description,
		Location:        req.Location,
		Timezone:        req.Timezone,
		DeadlineAt:      req.DeadlineAt,
		Options:         options,
		SignupMaxClaims: req.SignupMaxClaims,
		PollSettingsInput: PollSettingsInput{
			RequireParticipantEmail: req.RequireParticipantEmail,
			AllowComments:           req.AllowComments,
			AllowIfNeedBe:           req.AllowIfNeedBe,
		},
	}, nil
}

type updatePollRequest struct {
	Title                   *string         `json:"title"`
	Description             *string         `json:"description"`
	Location                *string         `json:"location"`
	Timezone                *string         `json:"timezone"`
	DeadlineAt              json.RawMessage `json:"deadlineAt"`
	Options                 []optionRequest `json:"options"`
	SignupMaxClaims         *int            `json:"signupMaxClaims"`
	RequireParticipantEmail *bool           `json:"requireParticipantEmail"`
	AllowComments           *bool           `json:"allowComments"`
	AllowIfNeedBe           *bool           `json:"allowIfNeedBe"`
}

func (req updatePollRequest) toInput() (UpdatePollInput, error) {
	var options []OptionInput
	if req.Options != nil {
		var err error
		options, err = toOptionInputs(req.Options)
		if err != nil {
			return UpdatePollInput{}, err
		}
	}

	in := UpdatePollInput{
		Title:           req.Title,
		Description:     req.Description,
		Location:        req.Location,
		Timezone:        req.Timezone,
		Options:         options,
		SignupMaxClaims: req.SignupMaxClaims,
		PollSettingsInput: PollSettingsInput{
			RequireParticipantEmail: req.RequireParticipantEmail,
			AllowComments:           req.AllowComments,
			AllowIfNeedBe:           req.AllowIfNeedBe,
		},
	}
	if req.DeadlineAt != nil {
		in.DeadlineAtSet = true
		if string(req.DeadlineAt) != "null" {
			var s string
			if err := json.Unmarshal(req.DeadlineAt, &s); err != nil {
				return UpdatePollInput{}, newValidationError("deadlineAt", "deadlineAt must be a string or null")
			}
			in.DeadlineAt = &s
		}
	}
	return in, nil
}

type setStatusRequest struct {
	Status string `json:"status"`
}

type finalizeRequest struct {
	OptionID string `json:"optionId"`
}

type addParticipantRequest struct {
	Name    string            `json:"name"`
	Email   *string           `json:"email"`
	Answers map[string]string `json:"answers"`
	Locale  *string           `json:"locale"`
}

type updateParticipantRequest struct {
	Name    *string           `json:"name"`
	Answers map[string]string `json:"answers"`
}

type addCommentRequest struct {
	AuthorName string `json:"authorName"`
	Body       string `json:"body"`
}

type claimRequest struct {
	OptionID      string  `json:"optionId"`
	ParticipantID string  `json:"participantId"`
	Name          string  `json:"name"`
	Email         *string `json:"email"`
	Locale        *string `json:"locale"`
}

type notificationPrefsRequest struct {
	PollID   string           `json:"pollId"`
	Channels NotificationGrid `json:"channels"`
}

type followingRequest struct {
	Following bool `json:"following"`
}

type commentResponse struct {
	ID            string  `json:"id"`
	AuthorName    string  `json:"authorName"`
	Body          string  `json:"body"`
	CreatedAt     string  `json:"createdAt"`
	UserID        *string `json:"userId"`
	ParticipantID *string `json:"participantId"`
}

func toCommentResponse(c *Comment) commentResponse {
	return commentResponse{
		ID: c.ID, AuthorName: c.AuthorName, Body: c.Body, CreatedAt: c.CreatedAt,
		UserID: c.UserID, ParticipantID: c.ParticipantID,
	}
}

// ---- handlers: polls --------------------------------------------------------------------------

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var req createPollRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toInput()
	if err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := s.Create(r.Context(), sess.ActiveOrgID, sess.UserID, in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, view)
}

func (s *Service) handleGetView(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")
		viewer := viewerFromRequest(a, r)
		view, err := s.GetView(r.Context(), pollID, viewer)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		if view == nil {
			httpserver.Err(w, http.StatusNotFound, "not_found", "not found", nil)
			return
		}
		httpserver.JSON(w, http.StatusOK, view)
	}
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	if err := s.RequireManageable(r.Context(), pollID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	var req updatePollRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	in, err := req.toInput()
	if err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := s.Update(r.Context(), pollID, sess.ActiveOrgID, in)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, view)
}

func (s *Service) handleSetStatus(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	if err := s.RequireManageable(r.Context(), pollID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	var req setStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.SetStatus(r.Context(), pollID, sess.ActiveOrgID, req.Status); err != nil {
		writeServiceError(w, err)
		return
	}
	s.respondWithFreshView(w, r, pollID, sess.UserID)
}

func (s *Service) handleFinalize(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	if err := s.RequireManageable(r.Context(), pollID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	var req finalizeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.Finalize(r.Context(), pollID, sess.ActiveOrgID, req.OptionID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	s.respondWithFreshView(w, r, pollID, sess.UserID)
}

// respondWithFreshView re-reads pollID's view (as viewerUserID) and writes it — used by
// SetStatus/Finalize, whose own Service methods return only an error, so the handler's own
// natural "here is the resource you just changed" response comes from a follow-up GetView.
func (s *Service) respondWithFreshView(w http.ResponseWriter, r *http.Request, pollID, viewerUserID string) {
	view, err := s.GetView(r.Context(), pollID, Viewer{UserID: viewerUserID})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, view)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	if err := s.RequireManageable(r.Context(), pollID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	if err := s.Delete(r.Context(), pollID, sess.ActiveOrgID); err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"id": pollID, "deleted": true})
}

func (s *Service) handleDuplicate(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	if err := s.RequireManageable(r.Context(), pollID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	view, err := s.Duplicate(r.Context(), pollID, sess.ActiveOrgID, sess.UserID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, view)
}

func (s *Service) handleListMine(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	summaries, err := s.ListMine(r.Context(), sess.ActiveOrgID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, summaries)
}

// ---- handlers: participants/comments/claims ---------------------------------------------------

// requireNotSignupPollHTTP ports requireNotSignupPoll (participants.functions.ts): AddParticipant/
// UpdateParticipant write plain (non-claim) votes, which — for a sign-up sheet — must only ever
// happen via Claim/Unclaim (capacity is only enforced there). This wrapper-level check belonged to
// the HTTP layer per participants.go's own doc comment ("out of Task 3's port scope"); Task 7 is
// that layer.
func (s *Service) requireNotSignupPollHTTP(ctx context.Context, pollID string) error {
	poll, err := s.q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if poll.Type == string(PollTypeSignup) {
		return newValidationError("type", "signup polls only accept votes via claim")
	}
	return nil
}

func (s *Service) handleAddParticipant(a Auth, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")
		ctx := r.Context()

		if err := s.requireNotSignupPollHTTP(ctx, pollID); err != nil {
			writeServiceError(w, err)
			return
		}
		if err := requireCaptchaIfAnon(cfg, a, r); err != nil {
			httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}

		var req addParticipantRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		viewer := viewerFromRequest(a, r)
		result, err := s.AddParticipant(ctx, pollID, ParticipantInput{
			Name: req.Name, Email: req.Email, Answers: req.Answers, Locale: req.Locale,
		}, viewer)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		// Task 7 (d): response.created, ported from addParticipant's own emitPollEvent call site
		// (participants.functions.ts).
		s.enqueueDigestBestEffort(ctx, pollID, DigestItem{
			Event: EventResponseCreated, Name: req.Name, ActorUserID: viewer.UserID,
		})

		resp := map[string]any{"participantId": result.ParticipantID}
		if result.IsGuest {
			resp["guestToken"] = a.MintGuestToken(result.ParticipantID)
		}
		httpserver.JSON(w, http.StatusCreated, resp)
	}
}

func (s *Service) handleUpdateParticipant(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID, participantID := r.PathValue("id"), r.PathValue("pid")
		ctx := r.Context()

		if err := s.requireNotSignupPollHTTP(ctx, pollID); err != nil {
			writeServiceError(w, err)
			return
		}

		var req updateParticipantRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		viewer := viewerFromRequest(a, r)
		in := ParticipantInput{Answers: req.Answers}
		if req.Name != nil {
			in.NameSet = true
			in.Name = *req.Name
		}
		if err := s.UpdateParticipant(ctx, pollID, participantID, in, viewer); err != nil {
			writeServiceError(w, err)
			return
		}

		// Task 7 (d): response.updated, actor name resolved from the participant's now-current
		// name (participants.functions.ts resolves `data.name ?? existing?.name ?? ''` — after a
		// successful write those are the same value either way).
		name := s.bestEffortParticipantName(ctx, participantID)
		s.enqueueDigestBestEffort(ctx, pollID, DigestItem{
			Event: EventResponseUpdated, Name: name, ActorUserID: viewer.UserID,
		})

		respondOK(w)
	}
}

func (s *Service) handleRemoveParticipant(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID, participantID := r.PathValue("id"), r.PathValue("pid")
		ctx := r.Context()

		// Captured before the removal — the row (and its name) is gone afterward.
		name := s.bestEffortParticipantName(ctx, participantID)

		viewer := viewerFromRequest(a, r)
		if err := s.RemoveParticipant(ctx, pollID, participantID, viewer); err != nil {
			writeServiceError(w, err)
			return
		}

		// Task 7 (d): response.withdrawn, ported from removeParticipant's emitPollEvent call site.
		s.enqueueDigestBestEffort(ctx, pollID, DigestItem{
			Event: EventResponseWithdrawn, Name: name, ActorUserID: viewer.UserID,
		})

		respondOK(w)
	}
}

func (s *Service) handleAddComment(a Auth, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")
		ctx := r.Context()

		if err := requireCaptchaIfAnon(cfg, a, r); err != nil {
			httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}

		var req addCommentRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		viewer := viewerFromRequest(a, r)
		comment, err := s.AddComment(ctx, pollID, CommentInput(req), viewer)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		// Task 7 (d): comment.created, ported from addComment's own emitPollEvent call site.
		s.enqueueDigestBestEffort(ctx, pollID, DigestItem{
			Event: EventCommentCreated, Name: req.AuthorName, ActorUserID: viewer.UserID,
		})

		httpserver.JSON(w, http.StatusCreated, toCommentResponse(comment))
	}
}

func (s *Service) handleDeleteComment(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID, commentID := r.PathValue("id"), r.PathValue("cid")
		viewer := viewerFromRequest(a, r)
		if err := s.DeleteComment(r.Context(), pollID, commentID, viewer); err != nil {
			writeServiceError(w, err)
			return
		}
		respondOK(w)
	}
}

func (s *Service) handleClaim(a Auth, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")
		ctx := r.Context()

		if err := requireCaptchaIfAnon(cfg, a, r); err != nil {
			httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}

		var req claimRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		optionID := req.OptionID
		if optionID == "" {
			optionID = r.URL.Query().Get("optionId")
		}
		viewer := viewerFromRequest(a, r)
		result, err := s.Claim(ctx, pollID, optionID, ClaimInput{
			ParticipantID: req.ParticipantID, Name: req.Name, Email: req.Email, Locale: req.Locale,
		}, viewer)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		// Task 7 (d): response.created + the signup.full transition check, ported from claimSlot's
		// own emitPollEvent call and PollRoom's #emitIfSheetFilled — both only on an actual change
		// (result.Changed), matching claimSlot's own "a re-claim of a slot already held is a no-op"
		// comment.
		if result.Changed {
			name := s.bestEffortParticipantName(ctx, result.ParticipantID)
			s.enqueueDigestBestEffort(ctx, pollID, DigestItem{
				Event: EventResponseCreated, Name: name, ActorUserID: viewer.UserID,
			})
			if full, ferr := s.SignupFull(ctx, pollID); ferr == nil && full {
				s.enqueueDigestBestEffort(ctx, pollID, DigestItem{Event: EventSignupFull})
			} else if ferr != nil {
				slog.Default().Error("polls: signup.full check failed", "pollId", pollID, "error", ferr)
			}
		}

		resp := map[string]any{
			"participantId":    result.ParticipantID,
			"claimedOptionIds": result.ClaimedOptionIDs,
		}
		if result.Created && result.IsGuest {
			resp["guestToken"] = a.MintGuestToken(result.ParticipantID)
		}
		httpserver.JSON(w, http.StatusOK, resp)
	}
}

func (s *Service) handleUnclaim(a Auth) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID, optionID := r.PathValue("id"), r.PathValue("oid")
		ctx := r.Context()
		viewer := viewerFromRequest(a, r)
		target := unclaimTargetParticipantID(r)

		// Best-effort actor name for the digest item below, resolved BEFORE the delete (the vote
		// row disappears once Unclaim/UnclaimFor succeeds).
		name := s.resolveUnclaimActorName(ctx, pollID, target, viewer)

		var err error
		if target != "" {
			err = s.UnclaimFor(ctx, pollID, optionID, target, viewer)
		} else {
			err = s.Unclaim(ctx, pollID, optionID, viewer)
		}
		if err != nil {
			writeServiceError(w, err)
			return
		}

		// Task 7 (d): response.withdrawn, ported from unclaimSlot's own emitPollEvent call site.
		s.enqueueDigestBestEffort(ctx, pollID, DigestItem{
			Event: EventResponseWithdrawn, Name: name, ActorUserID: viewer.UserID,
		})

		respondOK(w)
	}
}

// unclaimTargetParticipantID resolves the manager force-unclaim target (Task 7 requirement (b)):
// the "participantId" query parameter if present, else a JSON body's "participantId" field when
// the request actually carries a body. "" means the self-service path (Unclaim, not UnclaimFor).
func unclaimTargetParticipantID(r *http.Request) string {
	if v := r.URL.Query().Get("participantId"); v != "" {
		return v
	}
	if r.Body == nil || r.ContentLength == 0 {
		return ""
	}
	var body struct {
		ParticipantID string `json:"participantId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return ""
	}
	return body.ParticipantID
}

// bestEffortParticipantName looks up a participant's current display name, for a digest item's
// cosmetic "Name" field only (never used for authorization) — "" on any lookup failure.
func (s *Service) bestEffortParticipantName(ctx context.Context, participantID string) string {
	if participantID == "" {
		return ""
	}
	p, err := s.q.GetParticipant(ctx, participantID)
	if err != nil {
		return ""
	}
	return p.Name
}

// resolveUnclaimActorName is bestEffortParticipantName's twin for Unclaim/UnclaimFor, which (per
// their own doc comments) resolve the acting participant themselves rather than taking one as an
// explicit argument — this mirrors just enough of that same resolution to look their name up for
// the response.withdrawn digest item, swallowing every error (name stays "").
func (s *Service) resolveUnclaimActorName(ctx context.Context, pollID, targetParticipantID string, viewer Viewer) string {
	if targetParticipantID != "" {
		return s.bestEffortParticipantName(ctx, targetParticipantID)
	}
	if viewer.GuestParticipantID != "" {
		return s.bestEffortParticipantName(ctx, viewer.GuestParticipantID)
	}
	if viewer.UserID == "" {
		return ""
	}
	uid, err := strconv.ParseInt(viewer.UserID, 10, 64)
	if err != nil {
		return ""
	}
	p, err := s.q.GetParticipantByPollAndUser(ctx, queries.GetParticipantByPollAndUserParams{
		PollID: pollID, UserID: sql.NullInt64{Int64: uid, Valid: true},
	})
	if err != nil {
		return ""
	}
	return p.Name
}

// enqueueDigestBestEffort calls EnqueueDigestItem and logs (never fails the request on) any
// error — mirrors emitPollEvent's own try/catch-and-log contract (emit.ts).
func (s *Service) enqueueDigestBestEffort(ctx context.Context, pollID string, item DigestItem) {
	if err := s.EnqueueDigestItem(ctx, pollID, item); err != nil {
		slog.Default().Error("polls: enqueue digest item failed",
			"pollId", pollID, "event", item.Event, "error", err)
	}
}

// ---- handlers: calendar/roster/notifications --------------------------------------------------

func (s *Service) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	pollID := r.PathValue("id")
	pollURL := icsPollURL(r, pollID)
	filename, ics, err := BuildPollICS(r.Context(), s.q, pollID, pollURL)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	if ics == nil {
		httpserver.Err(w, http.StatusNotFound, "not_found", "no calendar event for this poll", nil)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ics)
}

// icsPollURL builds the absolute poll link BuildPollICS's VEVENT URL property points at, the same
// shape internal/polls/timers.go's sendFinalizedMail already builds from *mailer.Mailer.AppURL()
// (`${APP_URL}/p/{id}`) — this handler has no *mailer.Mailer, so it derives the same origin
// straight from the incoming request instead.
func icsPollURL(r *http.Request, pollID string) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
	}
	return scheme + "://" + r.Host + "/p/" + pollID
}

func (s *Service) handleRosterCSV(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	if err := s.RequireManageable(r.Context(), pollID, sess.ActiveOrgID, sess.UserID); err != nil {
		writeServiceError(w, err)
		return
	}
	csv, err := s.BuildRosterCSV(r.Context(), pollID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="roster.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(csv))
}

func (s *Service) handleUpdateNotificationPrefs(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	var req notificationPrefsRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.UpdateNotificationPrefs(r.Context(), req.PollID, sess.ActiveOrgID, sess.UserID, req.Channels); err != nil {
		writeServiceError(w, err)
		return
	}
	respondOK(w)
}

func (s *Service) handleSetFollowing(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	var req followingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.SetFollowing(r.Context(), pollID, sess.ActiveOrgID, sess.UserID, req.Following); err != nil {
		writeServiceError(w, err)
		return
	}
	respondOK(w)
}

// ---- handler: config ---------------------------------------------------------------------------

// publicConfigResponse ports getPublicConfig (config.functions.ts), extended per the brief with
// oidcEnabled/oidcName (capability flags this Go config carries that the TS source's snapshot
// above didn't yet). TurnstileSiteKey/OIDCName are omitted entirely when their capability is off —
// a client has no use for a site key with no secret behind it, or an SSO button name when SSO
// itself is unconfigured.
type publicConfigResponse struct {
	TurnstileSiteKey *string `json:"turnstileSiteKey,omitempty"`
	GoogleEnabled    bool    `json:"googleEnabled"`
	OIDCEnabled      bool    `json:"oidcEnabled"`
	OIDCName         string  `json:"oidcName,omitempty"`
}

func handleConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := publicConfigResponse{
			GoogleEnabled: cfg.Capabilities.Google,
			OIDCEnabled:   cfg.Capabilities.OIDC,
		}
		if cfg.Capabilities.Turnstile {
			key := cfg.TurnstileSiteKey
			resp.TurnstileSiteKey = &key
		}
		if cfg.Capabilities.OIDC {
			resp.OIDCName = cfg.OIDCName
		}
		httpserver.JSON(w, http.StatusOK, resp)
	}
}

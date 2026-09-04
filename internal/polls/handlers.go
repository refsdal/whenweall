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

// Auth is an alias for httpserver.Auth (promoted there in Task 8's helper-sharing refactor, once
// a second HTTP-surfaced domain package needed the same seam this one had already built): the
// narrow seam this package needs from auth.Service — RequireSession/FromContext/VerifyGuestToken/
// MintGuestToken — kept as an interface (rather than importing *auth.Service directly into every
// signature below) so tests can substitute a fake session/guest-token source instead of driving a
// real signup/signin flow through Limen for every one of this file's tests. auth.Service satisfies
// this with no adapter needed (FromContext is a plain delegation method — see
// internal/auth/session.go). Kept as a local alias (rather than rewriting every `Auth` in this
// file's signatures to `httpserver.Auth`) purely to keep this diff's noise down; the two names are
// identical from the compiler's point of view.
type Auth = httpserver.Auth

// Register mounts this package's whole HTTP surface on mux. Handler rules throughout: thin
// (decode -> Validate -> service -> respond); guest identity via X-Guest-Token header or ?token=;
// captcha (Cloudflare Turnstile, cfg-gated) on every public mutating endpoint's anonymous callers
// only — a signed-in caller never needs it, mirroring participants.functions.ts's own
// `if (!userId) await requireTurnstile(...)` branch; a light per-IP rate limit on the same public
// mutating endpoints, keyed like internal/httpserver's own authRateLimit.
func (s *Service) Register(mux *http.ServeMux, a Auth, cfg *config.Config) {
	voteLimit := httpserver.PublicRateLimit(s.db, "polls", "vote", 30, time.Minute, cfg.TrustProxy)
	commentLimit := httpserver.PublicRateLimit(s.db, "polls", "comment", 20, time.Minute, cfg.TrustProxy)

	mux.Handle("POST /api/v1/polls", httpserver.WithOrgSession(a, s.handleCreate))
	mux.HandleFunc("GET /api/v1/polls/{id}", s.handleGetView(a))
	mux.Handle("PATCH /api/v1/polls/{id}", httpserver.WithOrgSession(a, s.handleUpdate))
	mux.Handle("POST /api/v1/polls/{id}/status", httpserver.WithOrgSession(a, s.handleSetStatus))
	mux.Handle("POST /api/v1/polls/{id}/finalize", httpserver.WithOrgSession(a, s.handleFinalize))
	mux.Handle("DELETE /api/v1/polls/{id}", httpserver.WithOrgSession(a, s.handleDelete))
	mux.Handle("POST /api/v1/polls/{id}/duplicate", httpserver.WithOrgSession(a, s.handleDuplicate))
	mux.Handle("GET /api/v1/polls", httpserver.WithOrgSession(a, s.handleListMine))

	mux.Handle("POST /api/v1/polls/{id}/participants", voteLimit(http.HandlerFunc(s.handleAddParticipant(a, cfg))))
	mux.Handle("PATCH /api/v1/polls/{id}/participants/{pid}", voteLimit(s.handleUpdateParticipant(a)))
	mux.Handle("DELETE /api/v1/polls/{id}/participants/{pid}", voteLimit(s.handleRemoveParticipant(a)))

	mux.Handle("POST /api/v1/polls/{id}/comments", commentLimit(http.HandlerFunc(s.handleAddComment(a, cfg))))
	mux.HandleFunc("DELETE /api/v1/polls/{id}/comments/{cid}", s.handleDeleteComment(a))

	mux.Handle("POST /api/v1/polls/{id}/claims", voteLimit(http.HandlerFunc(s.handleClaim(a, cfg))))
	mux.Handle("DELETE /api/v1/polls/{id}/claims/{oid}", voteLimit(s.handleUnclaim(a)))

	mux.HandleFunc("GET /api/v1/polls/{id}/calendar.ics", s.handleCalendarICS(cfg))
	mux.Handle("GET /api/v1/polls/{id}/roster.csv", httpserver.WithOrgSession(a, s.handleRosterCSV))

	mux.Handle("POST /api/v1/polls/{id}/notification-prefs", httpserver.WithOrgSession(a, s.handleUpdateNotificationPrefs))
	mux.Handle("POST /api/v1/polls/{id}/following", httpserver.WithOrgSession(a, s.handleSetFollowing))

	mux.HandleFunc("GET /api/v1/config", handleConfig(cfg))
}

// viewerFromRequest resolves a Viewer for a public(token)|auth endpoint: the caller's own userID
// if signed in AND verified (never required), plus any verified guest participant id
// (httpserver.GuestParticipantID — the domain-agnostic token extraction/verification; Viewer
// itself stays here, since it's this package's own domain type).
//
// An unverified session is treated exactly like no session at all: this plan's binding decision
// is "unverified accounts cannot use the app", and signing in while unverified is allowed (so the
// account can complete verification), so a public route like this one is reachable with an
// unverified session attached. Without this check, that session's own UserID would be attributed
// to a vote/claim/comment as if it belonged to a real, usable account — see
// httpserver.RequireCaptchaIfAnon's identical EmailVerified check for the captcha half of the
// same finding.
func viewerFromRequest(a Auth, r *http.Request) Viewer {
	v := Viewer{GuestParticipantID: httpserver.GuestParticipantID(a, r)}
	if sess, ok := a.FromContext(r.Context()); ok && sess.EmailVerified {
		v.UserID = sess.UserID
	}
	return v
}

// writeServiceError maps every sentinel this package's Service methods can return to the standard
// HTTP error envelope, via httpserver.WriteDomainError's shared "map or log-and-500" plumbing
// (Task 8's helper-sharing refactor — the envelope-writing core is domain-agnostic and now lives
// in internal/httpserver; this is the thin per-package wrapper the promotion left behind, mapping
// THIS package's own sentinels). mapServiceError below is the actual mapping: *ValidationError ->
// 422 "invalid" (carrying Fields); ErrCapacityFull -> 409 "capacity_full"; each of the six
// ErrConflict-wrapping sentinels (errors.go) -> 409 with its own snake_case envelope code
// (poll_closed, poll_finalized, limit_reached, claim_limit_reached, capacity_below_claims,
// email_required) — checked before the plain ErrConflict case, since every one of them also
// satisfies errors.Is(err, ErrConflict); a bare ErrConflict (none of the six) -> 409 "conflict";
// ErrNotFound -> 404 "not_found" (this is where requireOrgPoll's wrong-org -> ErrNotFound mapping,
// and RequireManageable's own NOT_FOUND half, surface as a real 404 — see this file's package doc
// comment, item (c)); ErrForbidden -> 403 "forbidden". Anything else falls through to
// WriteDomainError's own log-and-500.
//
// The envelope codes below are this Go service's own vocabulary, not the TS frontend's — the TS
// AppError codes are SCREAMING_CASE (src/lib/errors.ts's ERROR_CODES) and the frontend's error
// switches (src/lib/use-claims.ts, src/components/poll/use-answer-draft.ts,
// src/routes/p/$id/edit.tsx) match on those directly; translating this envelope's snake_case codes
// into that shape is plan 8's job, not this handler's.
func writeServiceError(w http.ResponseWriter, err error) {
	httpserver.WriteDomainError(w, err, mapServiceError)
}

func mapServiceError(err error) (status int, code, message string, fields map[string]string, ok bool) {
	var verr *ValidationError
	switch {
	case errors.As(err, &verr):
		return http.StatusUnprocessableEntity, "invalid", "validation failed", verr.Fields, true
	case errors.Is(err, ErrCapacityFull):
		return http.StatusConflict, "capacity_full", "this slot is full", nil, true
	case errors.Is(err, ErrPollClosed):
		return http.StatusConflict, "poll_closed", "this poll is closed", nil, true
	case errors.Is(err, ErrPollFinalized):
		return http.StatusConflict, "poll_finalized", "this poll has been finalized", nil, true
	case errors.Is(err, ErrLimitReached):
		return http.StatusConflict, "limit_reached", "this poll has reached its participant limit", nil, true
	case errors.Is(err, ErrClaimLimitReached):
		return http.StatusConflict, "claim_limit_reached", "you have reached this poll's claim limit", nil, true
	case errors.Is(err, ErrCapacityBelowClaims):
		return http.StatusConflict, "capacity_below_claims", "capacity cannot be set below the current number of claims", nil, true
	case errors.Is(err, ErrEmailRequired):
		return http.StatusConflict, "email_required", "an email address is required for this poll", nil, true
	case errors.Is(err, ErrConflict):
		return http.StatusConflict, "conflict", "the poll's current state does not allow this", nil, true
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
	if !httpserver.DecodeJSON(w, r, &req) {
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
	if !httpserver.DecodeJSON(w, r, &req) {
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
	if !httpserver.DecodeJSON(w, r, &req) {
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
	if !httpserver.DecodeJSON(w, r, &req) {
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
		if err := httpserver.RequireCaptchaIfAnon(cfg, a, r); err != nil {
			httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}

		var req addParticipantRequest
		if !httpserver.DecodeJSON(w, r, &req) {
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
		if !httpserver.DecodeJSON(w, r, &req) {
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

		if err := httpserver.RequireCaptchaIfAnon(cfg, a, r); err != nil {
			httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}

		var req addCommentRequest
		if !httpserver.DecodeJSON(w, r, &req) {
			return
		}
		viewer := viewerFromRequest(a, r)
		// resolveAuthorName (participants.functions.ts): a signed-in author's display name always
		// comes from their own account, never the client-supplied value — otherwise anyone
		// signed in could impersonate another name in their own comments. Guests (no session)
		// keep the name they typed.
		authorName := req.AuthorName
		if viewer.UserID != "" {
			if uid, uerr := strconv.ParseInt(viewer.UserID, 10, 64); uerr == nil {
				if u, gerr := s.q.GetUser(ctx, uid); gerr == nil {
					authorName = displayName(u)
				}
			}
		}
		comment, err := s.AddComment(ctx, pollID, CommentInput{AuthorName: authorName, Body: req.Body}, viewer)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		// Task 7 (d): comment.created, ported from addComment's own emitPollEvent call site.
		s.enqueueDigestBestEffort(ctx, pollID, DigestItem{
			Event: EventCommentCreated, Name: authorName, ActorUserID: viewer.UserID,
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

		if err := httpserver.RequireCaptchaIfAnon(cfg, a, r); err != nil {
			httpserver.Err(w, http.StatusForbidden, "captcha_failed", "captcha verification failed", nil)
			return
		}

		var req claimRequest
		if !httpserver.DecodeJSON(w, r, &req) {
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

		// Footgun fix: a caller who passes ?participantId=<their own participant> is NOT asking
		// to force-unclaim someone else's slot, even though target != "" would otherwise route
		// them through UnclaimFor (manage-required) below — that would wrongly 403 an ordinary
		// participant unclaiming their own slot who simply happened to pass their own id
		// explicitly (e.g. a client that always sends it, self or not). Self-resolves to the same
		// participant Unclaim's own self-service path would resolve to anyway, so this is purely a
		// routing correction, never a widening of who UnclaimFor accepts as a target.
		if target != "" && target == s.selfParticipantID(ctx, pollID, viewer) {
			target = ""
		}

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

// selfParticipantID resolves the viewer's own participant id for pollID — the same identity
// resolution Unclaim's self-service path uses (claims.go's unclaim): viewer.GuestParticipantID if
// set, else the signed-in viewer's own participant row on this poll, else "" (no resolvable
// identity, or a lookup failure). Used by handleUnclaim to detect the ?participantId-matches-self
// footgun (routing that case to Unclaim instead of UnclaimFor) and by resolveUnclaimActorName
// below to look that same participant's name up for the digest item — never used for
// authorization by itself; Unclaim/UnclaimFor still do their own.
func (s *Service) selfParticipantID(ctx context.Context, pollID string, viewer Viewer) string {
	if viewer.GuestParticipantID != "" {
		return viewer.GuestParticipantID
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
	return p.ID
}

// resolveUnclaimActorName is bestEffortParticipantName's twin for Unclaim/UnclaimFor, which (per
// their own doc comments) resolve the acting participant themselves rather than taking one as an
// explicit argument — looks up whichever participant (the explicit target, or the viewer's own
// via selfParticipantID) actually acted, for the response.withdrawn digest item, swallowing every
// error (name stays "").
func (s *Service) resolveUnclaimActorName(ctx context.Context, pollID, targetParticipantID string, viewer Viewer) string {
	if targetParticipantID != "" {
		return s.bestEffortParticipantName(ctx, targetParticipantID)
	}
	return s.bestEffortParticipantName(ctx, s.selfParticipantID(ctx, pollID, viewer))
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

func (s *Service) handleCalendarICS(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pollID := r.PathValue("id")
		pollURL := icsPollURL(cfg, pollID)
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
}

// icsPollURL builds the absolute poll link BuildPollICS's VEVENT URL property points at — the
// same shape internal/polls/timers.go's sendFinalizedMail already builds from
// *mailer.Mailer.AppURL() (`${APP_URL}/p/{id}`), and from the SAME source (cfg.AppURL), not the
// incoming request's Host/X-Forwarded-Proto headers: those are caller-controlled (a request can
// set an arbitrary Host header, or forge X-Forwarded-Proto, unless every hop in front of this
// process is trusted to strip/overwrite them) and, even trusted, depend on the deploy's proxy
// setup matching this handler's assumptions exactly — cfg.AppURL is this service's own
// already-validated canonical origin (internal/config's own doc comment: "absolute http(s) URL,
// no trailing slash"), the same one every other absolute link in this codebase is built from.
func icsPollURL(cfg *config.Config, pollID string) string {
	return cfg.AppURL + "/p/" + pollID
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
	pollID := r.PathValue("id")
	var req notificationPrefsRequest
	if !httpserver.DecodeJSON(w, r, &req) {
		return
	}
	if err := s.UpdateNotificationPrefs(r.Context(), pollID, sess.ActiveOrgID, sess.UserID, req.Channels); err != nil {
		writeServiceError(w, err)
		return
	}
	respondOK(w)
}

func (s *Service) handleSetFollowing(w http.ResponseWriter, r *http.Request, sess *auth.Session) {
	pollID := r.PathValue("id")
	var req followingRequest
	if !httpserver.DecodeJSON(w, r, &req) {
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

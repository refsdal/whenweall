package polls

// Ports src/server/polls/participants.ts (participant/vote/comment mutations) and the
// participant-authorization half of claim-auth.ts's requireParticipantAuth /
// comment-auth.ts's resolveVerifiedParticipantId — see claims.go for the sign-up-sheet claim
// half (applyClaim/removeClaim).
//
// Authorization in the TS source is computed by the caller (participants.functions.ts's
// requireIsOwner/canEditParticipant) and handed in as an already-resolved `auth` struct
// ({userId, editToken|isOwner}). This port's Viewer carries only raw identity (UserID,
// GuestParticipantID — the latter already verified by the auth seam, Task 7, per the task
// brief), so every method below resolves "can this viewer manage the poll" (org owner/admin, or
// its creator — canManageContent/canManagePoll in org-roles.ts/claim-auth.ts) itself, via
// canManagePoll below.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/polls/queries"
	"github.com/refsdal/whenweall/internal/rooms"
)

// ParticipantInput carries the participant-submission fields shared by AddParticipant and
// UpdateParticipant, ported from participants.ts's separate addParticipant/updateParticipant
// input shapes ({name, email, answers, locale} vs {name?, answers}).
//
// NameSet distinguishes "omitted" (UpdateParticipant leaves the existing name alone — ported from
// updateParticipantSchema's optional name) from "explicitly provided". AddParticipant always
// reads Name directly (ignoring NameSet) — like participants.ts's own addParticipant, this port
// does not itself require Name to be non-empty; that validation lives at the schema layer
// (addParticipantSchema), out of this task's port scope. Email/Locale are read only by
// AddParticipant — updateParticipant never accepts them in the TS source either.
type ParticipantInput struct {
	NameSet bool
	Name    string
	Email   *string
	Answers map[string]string // optionID -> "yes" | "ifneedbe" | "no"
	Locale  *string
}

// ParticipantResult is AddParticipant's return value. IsGuest tells the caller (Task 7's HTTP
// handler) whether to mint a guest edit token for ParticipantID via auth.MintGuestToken — ported
// intent of addParticipant's own {participantId, editToken} return, but minting itself happens at
// the auth seam (Task 7), not here; see the task brief's "Guest tokens are MINTED by the auth
// seam" note.
type ParticipantResult struct {
	ParticipantID string
	IsGuest       bool
}

// CommentInput carries addComment's fields. AuthorName is used as given — resolveAuthorName's
// "session name overrides client-supplied name for a signed-in author" policy lives in
// participants.functions.ts, out of this task's port scope (comment-auth.ts, which *is* in
// scope, only covers resolveVerifiedParticipantId — see AddComment below). ParticipantID is not a
// field here: like AddParticipant/Claim, the link to an existing participant is resolved from
// Viewer.GuestParticipantID (already verified by the auth seam), not taken from client input.
type CommentInput struct {
	AuthorName string
	Body       string
}

// Comment is AddComment's return value. TS's addComment returns only {id}; this port also fills
// in the other columns the insert already has on hand (no extra query), mirroring CommentView's
// shape so a handler can render it without a round trip back through GetView.
type Comment struct {
	ID            string
	AuthorName    string
	Body          string
	CreatedAt     string
	UserID        *string
	ParticipantID *string
}

// requireOpenPollTx fetches pollID (queries.GetPoll already excludes soft-deleted rows) and
// requires its status to be "open" — ports requireOpenPoll (participants.ts), returning
// ErrPollClosed (TS: POLL_CLOSED) otherwise; see errors.go for the full sentinel/code table.
func requireOpenPollTx(ctx context.Context, q *queries.Queries, pollID string) (queries.Poll, error) {
	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return queries.Poll{}, ErrNotFound
	}
	if err != nil {
		return queries.Poll{}, err
	}
	if poll.Status != "open" {
		return queries.Poll{}, ErrPollClosed
	}
	return poll, nil
}

// requireParticipantInPollTx fetches participantID and requires it to belong to pollID, ported
// from requireParticipantInPoll (participants.ts).
func requireParticipantInPollTx(ctx context.Context, q *queries.Queries, pollID, participantID string) (queries.Participant, error) {
	p, err := q.GetParticipant(ctx, participantID)
	if errors.Is(err, sql.ErrNoRows) {
		return queries.Participant{}, ErrNotFound
	}
	if err != nil {
		return queries.Participant{}, err
	}
	if p.PollID != pollID {
		return queries.Participant{}, ErrNotFound
	}
	return p, nil
}

// validateAnswersTx ports validateAnswers (participants.ts): every answered option must belong to
// pollID, and an "ifneedbe" answer is only allowed when the poll's allowIfNeedBe setting permits
// it.
func validateAnswersTx(ctx context.Context, q *queries.Queries, pollID string, answers map[string]string, allowIfNeedBe bool) error {
	options, err := q.ListOptionsByPoll(ctx, pollID)
	if err != nil {
		return err
	}
	validIDs := make(map[string]bool, len(options))
	for _, o := range options {
		validIDs[o.ID] = true
	}
	for optionID, answer := range answers {
		if !validIDs[optionID] {
			return newValidationError("answers", fmt.Sprintf("option %q is not on this poll", optionID))
		}
		if answer == "ifneedbe" && !allowIfNeedBe {
			return newValidationError("answers", "ifneedbe is not allowed on this poll")
		}
	}
	return nil
}

// canManagePoll ports claim-auth.ts's canManagePoll: does viewerUserID belong to organizationID
// AT ALL (any role) first — no membership is an unconditional false, checked before anything
// else, exactly like the TS source's own `if (!membership) return false` — and, only for an
// actual member, either the poll's own creator or holding an 'owner'/'admin' role in
// organizationID (canManageContent's predicate). viewerUserID == "" (no signed-in identity at
// all) is always false, matching canManagePoll's `userId === null` early return.
//
// The membership-first ordering matters: someone who created a poll and later left the org (or
// was removed) must lose the ability to manage it — being its creator is not itself membership,
// and the TS source never treats it as such. Checking createdBy before membership (as an earlier
// version of this port did) would let an ex-member's stale creator-match keep granting manage
// access forever.
func (s *Service) canManagePoll(ctx context.Context, q *queries.Queries, organizationID int64, createdBy sql.NullInt64, viewerUserID string) (bool, error) {
	if viewerUserID == "" {
		return false, nil
	}
	userIDInt, err := strconv.ParseInt(viewerUserID, 10, 64)
	if err != nil {
		return false, nil
	}
	isMember, err := q.IsOrgMember(ctx, queries.IsOrgMemberParams{
		OrganizationID: organizationID, UserID: userIDInt,
	})
	if err != nil {
		return false, err
	}
	if !isMember {
		return false, nil
	}
	if createdBy.Valid && createdBy.Int64 == userIDInt {
		return true, nil
	}
	return q.MemberHasManagingRole(ctx, queries.MemberHasManagingRoleParams{
		OrganizationID: organizationID,
		UserID:         userIDInt,
	})
}

// participantAuthorized ports canEditParticipant (participants.ts) / the participant-scoped half
// of requireParticipantAuth (claim-auth.ts): true when canManage (the viewer can manage the whole
// poll), or the viewer's own signed-in UserID matches the participant's linked user, or the
// viewer holds that participant's already-verified guest token (Viewer.GuestParticipantID — see
// the package-level doc comment on why this port trusts it without re-verifying a token here).
func participantAuthorized(participant queries.Participant, viewer Viewer, canManage bool) bool {
	if canManage {
		return true
	}
	if viewer.GuestParticipantID != "" && viewer.GuestParticipantID == participant.ID {
		return true
	}
	if viewer.UserID != "" && participant.UserID.Valid {
		if uid, err := strconv.ParseInt(viewer.UserID, 10, 64); err == nil && uid == participant.UserID.Int64 {
			return true
		}
	}
	return false
}

// AddParticipant ports addParticipant (participants.ts): creates a participant row plus its
// votes. Whether the poll is a sign-up sheet (which — per requireNotSignupPoll in
// participants.functions.ts — must never take plain votes this way, only via Claim) is a check
// participants.functions.ts makes before calling into the service; that wrapper function is
// outside this task's port scope (participants.ts/claims.ts/claim-auth.ts/comment-auth.ts only),
// so it is not reproduced here — see the task report.
func (s *Service) AddParticipant(ctx context.Context, pollID string, in ParticipantInput, viewer Viewer) (*ParticipantResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := requireOpenPollTx(ctx, q, pollID)
	if err != nil {
		return nil, err
	}

	trimmedEmail := ""
	if in.Email != nil {
		trimmedEmail = strings.TrimSpace(*in.Email)
	}
	if poll.RequireParticipantEmail && trimmedEmail == "" {
		return nil, ErrEmailRequired
	}

	existing, err := q.ListParticipantsByPoll(ctx, pollID)
	if err != nil {
		return nil, err
	}
	if len(existing) >= LimitParticipants {
		return nil, ErrLimitReached
	}

	if err := validateAnswersTx(ctx, q, pollID, in.Answers, poll.AllowIfNeedBe); err != nil {
		return nil, err
	}

	id := db.NewID()
	now := time.Now().UTC()
	isGuest := viewer.UserID == ""

	var userIDCol sql.NullInt64
	if !isGuest {
		uid, perr := strconv.ParseInt(viewer.UserID, 10, 64)
		if perr != nil {
			return nil, ErrForbidden
		}
		userIDCol = sql.NullInt64{Int64: uid, Valid: true}
	}
	var emailCol sql.NullString
	if trimmedEmail != "" {
		emailCol = sql.NullString{String: trimmedEmail, Valid: true}
	}
	var localeCol sql.NullString
	if in.Locale != nil {
		localeCol = sql.NullString{String: *in.Locale, Valid: true}
	}

	if err := q.InsertParticipant(ctx, queries.InsertParticipantParams{
		ID: id, PollID: pollID, Name: in.Name, Email: emailCol, UserID: userIDCol,
		Locale: localeCol, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return nil, err
	}
	for optionID, answer := range in.Answers {
		if err := q.UpsertVote(ctx, queries.UpsertVoteParams{ParticipantID: id, OptionID: optionID, Answer: answer}); err != nil {
			return nil, err
		}
	}

	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "participant"}); err != nil {
		return nil, err
	}
	// Task 3 (plan 6): addParticipant's own recordResponses(Object.values(data.answers)) call
	// (participants.functions.ts).
	if err := s.incrementResponseStats(ctx, tx, in.Answers); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ParticipantResult{ParticipantID: id, IsGuest: isGuest}, nil
}

// UpdateParticipant ports updateParticipant (participants.ts): renames (if NameSet) and fully
// replaces the participant's votes with in.Answers.
func (s *Service) UpdateParticipant(ctx context.Context, pollID, participantID string, in ParticipantInput, viewer Viewer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	participant, err := requireParticipantInPollTx(ctx, q, pollID, participantID)
	if err != nil {
		return err
	}
	poll, err := requireOpenPollTx(ctx, q, pollID)
	if err != nil {
		return err
	}

	canManage, err := s.canManagePoll(ctx, q, poll.OrganizationID, poll.CreatedBy, viewer.UserID)
	if err != nil {
		return err
	}
	if !participantAuthorized(participant, viewer, canManage) {
		return ErrForbidden
	}

	if err := validateAnswersTx(ctx, q, pollID, in.Answers, poll.AllowIfNeedBe); err != nil {
		return err
	}

	now := time.Now().UTC()
	name := participant.Name
	if in.NameSet {
		name = in.Name
	}
	if err := q.UpdateParticipantName(ctx, queries.UpdateParticipantNameParams{
		ID: participantID, Name: name, UpdatedAt: now,
	}); err != nil {
		return err
	}

	if err := q.DeleteVotesByParticipant(ctx, participantID); err != nil {
		return err
	}
	for optionID, answer := range in.Answers {
		if err := q.UpsertVote(ctx, queries.UpsertVoteParams{ParticipantID: participantID, OptionID: optionID, Answer: answer}); err != nil {
			return err
		}
	}

	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "vote"}); err != nil {
		return err
	}
	// Task 3 (plan 6): updateParticipant's own recordResponses(Object.values(data.answers)) call
	// (participants.functions.ts) — "an edit is a fresh submission", counted every time, not
	// netted against the participant's previous answers.
	if err := s.incrementResponseStats(ctx, tx, in.Answers); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveParticipant ports removeParticipant (participants.ts): a manager (org owner/admin, or the
// poll's creator) may remove any participant regardless of poll status (open, closed, or
// finalized); everyone else still needs the poll to be open.
func (s *Service) RemoveParticipant(ctx context.Context, pollID, participantID string, viewer Viewer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	participant, err := requireParticipantInPollTx(ctx, q, pollID, participantID)
	if err != nil {
		return err
	}

	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	canManage, err := s.canManagePoll(ctx, q, poll.OrganizationID, poll.CreatedBy, viewer.UserID)
	if err != nil {
		return err
	}
	if !canManage && poll.Status != "open" {
		return ErrPollClosed
	}
	if !participantAuthorized(participant, viewer, canManage) {
		return ErrForbidden
	}

	if err := q.DeleteParticipant(ctx, participantID); err != nil {
		return err
	}
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "participant"}); err != nil {
		return err
	}
	return tx.Commit()
}

// AddComment ports addComment (participants.ts) plus the participant-linking half of
// resolveVerifiedParticipantId (comment-auth.ts): a comment is tagged with a participant only
// when Viewer.GuestParticipantID (already verified by the auth seam as a real, owned participant)
// actually belongs to this poll — re-checked here since the auth seam's verification isn't itself
// poll-scoped, exactly mirroring resolveVerifiedParticipantId's own `participant.pollId !==
// pollId` guard (returning "no link" rather than an error either way).
func (s *Service) AddComment(ctx context.Context, pollID string, in CommentInput, viewer Viewer) (*Comment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !poll.AllowComments {
		return nil, ErrForbidden
	}

	var participantIDCol sql.NullString
	var participantIDPtr *string
	if viewer.GuestParticipantID != "" {
		p, perr := q.GetParticipant(ctx, viewer.GuestParticipantID)
		if perr == nil && p.PollID == pollID {
			participantIDCol = sql.NullString{String: p.ID, Valid: true}
			pid := p.ID
			participantIDPtr = &pid
		}
	}

	var userIDCol sql.NullInt64
	var userIDPtr *string
	if viewer.UserID != "" {
		uid, perr := strconv.ParseInt(viewer.UserID, 10, 64)
		if perr != nil {
			return nil, ErrForbidden
		}
		userIDCol = sql.NullInt64{Int64: uid, Valid: true}
		uidStr := viewer.UserID
		userIDPtr = &uidStr
	}

	id := db.NewID()
	now := time.Now().UTC()
	if err := q.InsertComment(ctx, queries.InsertCommentParams{
		ID: id, PollID: pollID, AuthorName: in.AuthorName, ParticipantID: participantIDCol,
		UserID: userIDCol, Body: in.Body, CreatedAt: now,
	}); err != nil {
		return nil, err
	}

	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "comment"}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &Comment{
		ID: id, AuthorName: in.AuthorName, Body: in.Body, CreatedAt: formatISO(now),
		UserID: userIDPtr, ParticipantID: participantIDPtr,
	}, nil
}

// DeleteComment ports deleteComment (participants.ts): a soft delete, allowed for the poll's
// manager (org owner/admin, or its creator) or the comment's own author.
func (s *Service) DeleteComment(ctx context.Context, pollID, commentID string, viewer Viewer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	comment, err := q.GetComment(ctx, commentID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if comment.PollID != pollID {
		return ErrNotFound
	}

	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	canManage, err := s.canManagePoll(ctx, q, poll.OrganizationID, poll.CreatedBy, viewer.UserID)
	if err != nil {
		return err
	}
	isAuthor := false
	if viewer.UserID != "" && comment.UserID.Valid {
		if uid, perr := strconv.ParseInt(viewer.UserID, 10, 64); perr == nil && uid == comment.UserID.Int64 {
			isAuthor = true
		}
	}
	if !canManage && !isAuthor {
		return ErrForbidden
	}

	now := time.Now().UTC()
	if err := q.SoftDeleteComment(ctx, queries.SoftDeleteCommentParams{
		ID: commentID, PollID: pollID, DeletedAt: sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return err
	}
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "comment"}); err != nil {
		return err
	}
	return tx.Commit()
}

package polls

// Ports src/server/polls/claims.ts (applyClaim/removeClaim/countClaims) plus the poll-scoping
// half of claim-auth.ts's requireSignupPoll — see participants.go's package-level doc comment for
// how authorization (claim-auth.ts's canManagePoll/requireParticipantAuth) is folded into these
// methods via Viewer instead of a precomputed `auth`/`org` struct.
//
// THE atomicity contract (spec §9's overbooking proof): Claim runs entirely inside one
// transaction, and takes TWO row locks, always in this fixed order (never the reverse, anywhere
// in this file — a fixed lock order rules out a cyclic wait, so this can never deadlock against
// itself):
//
//  1. The contested option row's capacity vs. its current yes-vote count: `SELECT ... FOR UPDATE`
//     (queries.GetPollOptionForUpdate) is taken before the count, and held until the winning
//     claim's vote is inserted and the transaction commits — every other concurrent claimant on
//     the *same option* blocks on that same row lock, so the read-count-then-insert sequence
//     below can never interleave across transactions. See TestClaimLastSlotExactlyOneWinner
//     (claims_test.go) for the proof.
//  2. The claiming participant's own row, for an EXISTING participant only (queries.
//     GetParticipantForUpdate): protects their signupMaxClaims cap the same way, across the
//     *different* options the same participant might claim concurrently — see
//     TestClaimSharedParticipantMaxClaimsAcrossOptions (claims_test.go) for the proof.

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/polls/queries"
	"github.com/refsdal/whenweall/internal/rooms"
)

// ClaimInput carries a sign-up-sheet claim's identity, ported from claims.ts's ClaimIdentity
// union (`{participantId}` | `{name, email, userId, locale}`).
//
// ParticipantID non-empty selects the "act on this existing participant" branch — claiming an
// additional slot for someone already on the poll: that participant's own signed-in user, a guest
// via their already-verified Viewer.GuestParticipantID, or a poll manager acting on someone
// else's participant (all three checked by participantAuthorized, participants.go).
//
// ParticipantID == "" selects the "new claimant" branch, keyed on Viewer.UserID: "" (a guest)
// always creates a fresh participant; non-empty reuses that user's existing participant row on
// this poll if one exists — exactly like claims.ts's signed-in branch — rather than creating a
// second one and handing them a fresh claim budget.
type ClaimInput struct {
	ParticipantID string
	Name          string
	Email         *string
	Locale        *string
}

// ClaimResult is Claim's return value. Created && IsGuest together tell the caller (Task 7's HTTP
// handler) whether to mint a fresh guest edit token via auth.MintGuestToken(ParticipantID) — only
// when a brand-new guest participant was created, mirroring applyClaim's own
// `editToken = identity.userId === null ? generateToken() : null` (only set inside
// prepareNewParticipant, itself only called when creating).
type ClaimResult struct {
	ParticipantID    string
	Created          bool
	IsGuest          bool
	ClaimedOptionIDs []string
	Changed          bool
}

// preparedParticipant is a brand-new participant row's fields, validated but not yet written —
// ports prepareNewParticipant's (claims.ts) return shape.
type preparedParticipant struct {
	id     string
	name   string
	email  sql.NullString
	locale sql.NullString
}

// prepareNewParticipant ports prepareNewParticipant (claims.ts): validates a fresh participant's
// name (required, trimmed) and email (required-if-poll-demands-it, trimmed), and the poll-wide
// participant cap — without writing anything yet, so a claim rejected by a later check (capacity,
// max-claims) never leaves an orphaned participant row behind.
func prepareNewParticipant(ctx context.Context, q *queries.Queries, pollID string, poll queries.Poll, name string, email, locale *string) (preparedParticipant, error) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return preparedParticipant{}, newValidationError("name", "name is required")
	}

	trimmedEmail := ""
	if email != nil {
		trimmedEmail = strings.TrimSpace(*email)
	}
	if poll.RequireParticipantEmail && trimmedEmail == "" {
		return preparedParticipant{}, ErrEmailRequired
	}

	existing, err := q.ListParticipantsByPoll(ctx, pollID)
	if err != nil {
		return preparedParticipant{}, err
	}
	if len(existing) >= LimitParticipants {
		return preparedParticipant{}, ErrLimitReached
	}

	p := preparedParticipant{id: db.NewID(), name: trimmedName}
	if trimmedEmail != "" {
		p.email = sql.NullString{String: trimmedEmail, Valid: true}
	}
	if locale != nil {
		p.locale = sql.NullString{String: *locale, Valid: true}
	}
	return p, nil
}

// requireSignupPollTx fetches pollID and requires poll.Type == "signup", ported from
// requireSignupPoll (claims.ts/claim-auth.ts). When allowClosed is false, also requires
// poll.Status == "open" — returns ErrPollClosed (TS: POLL_CLOSED) otherwise; see errors.go.
func requireSignupPollTx(ctx context.Context, q *queries.Queries, pollID string, allowClosed bool) (queries.Poll, error) {
	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return queries.Poll{}, ErrNotFound
	}
	if err != nil {
		return queries.Poll{}, err
	}
	if poll.Type != string(PollTypeSignup) {
		return queries.Poll{}, newValidationError("type", "poll must be a sign-up sheet")
	}
	if !allowClosed && poll.Status != "open" {
		return queries.Poll{}, ErrPollClosed
	}
	return poll, nil
}

// resolveClaimant ports applyClaim's identity-resolution block (claims.ts): figures out which
// participant a claim acts as, without writing anything. Returns the resolved participantID, a
// created flag ("this participant does not exist yet — insert it alongside the vote"), an isGuest
// flag ("no linked user — the caller gets a mintable guest token"), and — only when created — the
// preparedParticipant to insert.
func (s *Service) resolveClaimant(
	ctx context.Context, q *queries.Queries, pollID string, poll queries.Poll, in ClaimInput, viewer Viewer,
) (participantID string, created, isGuest bool, prepared preparedParticipant, err error) {
	if in.ParticipantID != "" {
		participant, perr := q.GetParticipant(ctx, in.ParticipantID)
		if errors.Is(perr, sql.ErrNoRows) {
			return "", false, false, preparedParticipant{}, ErrNotFound
		}
		if perr != nil {
			return "", false, false, preparedParticipant{}, perr
		}
		if participant.PollID != pollID {
			return "", false, false, preparedParticipant{}, ErrNotFound
		}
		canManage, merr := s.canManagePoll(ctx, q, poll.OrganizationID, poll.CreatedBy, viewer.UserID)
		if merr != nil {
			return "", false, false, preparedParticipant{}, merr
		}
		if !participantAuthorized(participant, viewer, canManage) {
			return "", false, false, preparedParticipant{}, ErrForbidden
		}
		return participant.ID, false, !participant.UserID.Valid, preparedParticipant{}, nil
	}

	if viewer.UserID != "" {
		// A signed-in caller always reuses their own participant row on this poll — creating a
		// fresh one per claim (as the guest branch below does) would hand them a new claim
		// budget every time. Ported from applyClaim's `identity.userId !== null` branch.
		uid, uerr := strconv.ParseInt(viewer.UserID, 10, 64)
		if uerr != nil {
			return "", false, false, preparedParticipant{}, ErrForbidden
		}
		existing, eerr := q.GetParticipantByPollAndUser(ctx, queries.GetParticipantByPollAndUserParams{
			PollID: pollID, UserID: sql.NullInt64{Int64: uid, Valid: true},
		})
		if eerr == nil {
			return existing.ID, false, false, preparedParticipant{}, nil
		}
		if !errors.Is(eerr, sql.ErrNoRows) {
			return "", false, false, preparedParticipant{}, eerr
		}
		prepared, perr := prepareNewParticipant(ctx, q, pollID, poll, in.Name, in.Email, in.Locale)
		if perr != nil {
			return "", false, false, preparedParticipant{}, perr
		}
		return prepared.id, true, false, prepared, nil
	}

	prepared, perr := prepareNewParticipant(ctx, q, pollID, poll, in.Name, in.Email, in.Locale)
	if perr != nil {
		return "", false, false, preparedParticipant{}, perr
	}
	return prepared.id, true, true, prepared, nil
}

// Claim ports applyClaim (claims.ts) — see this file's package-level doc comment for the
// atomicity contract.
func (s *Service) Claim(ctx context.Context, pollID, optionID string, in ClaimInput, viewer Viewer) (*ClaimResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := requireSignupPollTx(ctx, q, pollID, false)
	if err != nil {
		return nil, err
	}

	// Lock the option row now, before the capacity decision below — see this file's and
	// queries/polls.sql's doc comments on GetPollOptionForUpdate for why the ordering matters.
	option, err := q.GetPollOptionForUpdate(ctx, optionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if option.PollID != pollID {
		return nil, ErrNotFound
	}

	participantID, created, isGuest, prepared, err := s.resolveClaimant(ctx, q, pollID, poll, in, viewer)
	if err != nil {
		return nil, err
	}

	// Lock the participant row next — option row first, then participant row, is this
	// transaction's fixed lock order everywhere a Claim needs both, so two Claim calls can never
	// wait on each other in opposite orders (no cyclic wait, no deadlock). Skipped when created:
	// a brand-new participant has no row yet to lock (it's inserted further down, still inside
	// this same transaction), and no concurrent claimant can reference an id that doesn't exist
	// yet, so there's nothing to race on. For an EXISTING participant (explicit ParticipantID, or
	// a signed-in user reusing their own row), this is THE atomicity primitive protecting
	// signupMaxClaims: without it, the SAME participant claiming two DIFFERENT options
	// concurrently would lock two different option rows above (no conflict there), both read the
	// same pre-claim vote count below, both pass the maxClaims check, and both insert — exceeding
	// the cap.
	if !created {
		if _, err := q.GetParticipantForUpdate(ctx, participantID); err != nil {
			return nil, err
		}
	}

	existingVotes, err := q.ListVotesByParticipant(ctx, participantID)
	if err != nil {
		return nil, err
	}
	claimedOptionIDs := make([]string, len(existingVotes))
	alreadyClaimed := false
	for i, v := range existingVotes {
		claimedOptionIDs[i] = v.OptionID
		if v.OptionID == optionID {
			alreadyClaimed = true
		}
	}

	if !alreadyClaimed && len(claimedOptionIDs) >= int(poll.SignupMaxClaims) {
		return nil, ErrClaimLimitReached
	}
	if !alreadyClaimed && option.Capacity.Valid {
		count, cerr := q.CountYesVotesForOption(ctx, optionID)
		if cerr != nil {
			return nil, cerr
		}
		if count >= int64(option.Capacity.Int32) {
			return nil, ErrCapacityFull
		}
	}

	if alreadyClaimed {
		// A re-claim of a slot already held is a no-op (Changed: false) — nothing changed, so
		// this must NOT queue a digest entry or resend the confirmation mail for it. Ported from
		// claimSlot's own `if (result.changed) { ... }` gate (participants.functions.ts): only the
		// changed branch below calls sendClaimConfirmation/emitPollEvent at all — an earlier
		// version of this comment claimed the opposite (mail sent "unconditionally, even for a
		// no-op re-claim"), which was a misreading of the TS source.
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &ClaimResult{
			ParticipantID: participantID, Created: created, IsGuest: isGuest,
			ClaimedOptionIDs: claimedOptionIDs, Changed: false,
		}, nil
	}

	now := time.Now().UTC()
	if created {
		var userIDCol sql.NullInt64
		if !isGuest {
			uid, _ := strconv.ParseInt(viewer.UserID, 10, 64)
			userIDCol = sql.NullInt64{Int64: uid, Valid: true}
		}
		if err := q.InsertParticipant(ctx, queries.InsertParticipantParams{
			ID: participantID, PollID: pollID, Name: prepared.name, Email: prepared.email,
			UserID: userIDCol, Locale: prepared.locale, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return nil, err
		}
	}
	if err := q.UpsertVote(ctx, queries.UpsertVoteParams{ParticipantID: participantID, OptionID: optionID, Answer: "yes"}); err != nil {
		return nil, err
	}
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "vote"}); err != nil {
		return nil, err
	}
	// Task 3 (plan 6): claimSlot's own recordResponses(['yes']) call (participants.functions.ts)
	// — "a sign-up claim is stored as a `yes` vote... so it counts as one" (that source's own
	// comment), reached only on this function's Changed==true path (a re-claim of an
	// already-held slot returns earlier above, before ever reaching this point).
	if err := s.incrementStat(ctx, tx, rooms.StatsResponsesYes); err != nil {
		return nil, err
	}
	// Ports claimSlot's call into sendClaimConfirmation (participants.functions.ts) — the mail:poll
	// handler re-reads the participant's current claims at send time and no-ops if there's nothing
	// to send (no email on file, no claims left).
	if err := enqueueMailPoll(ctx, tx, mailPollPayload{PollID: pollID, Event: "claim_confirmation", ParticipantID: participantID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &ClaimResult{
		ParticipantID: participantID, Created: created, IsGuest: isGuest,
		ClaimedOptionIDs: append(claimedOptionIDs, optionID), Changed: true,
	}, nil
}

// Unclaim ports removeClaim (claims.ts)'s self-service path: its signature (ctx, pollID, optionID,
// viewer) carries no participant identifier distinct from Viewer, unlike Claim's ClaimInput. The
// target participant is therefore always resolved from Viewer itself:
//
//   - Viewer.GuestParticipantID, if set: that participant — already verified by the auth seam
//     (Task 7) as this caller's own, exactly like removeClaim's TS callers always pass a
//     participantId they've separately verified via requireParticipantAuth. Ported faithfully:
//     removeClaim itself never re-validates its participantId argument either (no NOT_FOUND for
//     a bogus one — see its own "is a no-op when the claim does not exist" test), so this port
//     doesn't re-look-up or re-scope it to pollID.
//   - Otherwise Viewer.UserID, if set: that user's own participant row on this poll (ErrNotFound
//     if they have none).
//   - Otherwise: ErrForbidden (no identity to act on at all).
//
// See UnclaimFor for the manager force-unclaim path (removeClaim's "allowClosed" owner path,
// acting on an arbitrary *other* participant's claim) that this exact signature has no room for.
func (s *Service) Unclaim(ctx context.Context, pollID, optionID string, viewer Viewer) error {
	return s.unclaim(ctx, pollID, optionID, "", viewer)
}

// UnclaimFor is Unclaim's manager force-unclaim twin, added in Task 7 per an accumulated review
// requirement: ports the other half of removeClaim (claims.ts)/unclaimSlot (participants.functions.ts)
// that Unclaim's brief-pinned signature has no room for — a poll manager (org owner/admin, or the
// poll's own creator) freeing an arbitrary OTHER participant's claimed slot, not just their own.
// targetParticipantID must be non-empty (use Unclaim for the self-service path); the caller must
// be able to manage the poll (ErrForbidden otherwise — this is not a way for an ordinary
// participant to unclaim someone else's slot), and targetParticipantID must actually belong to
// pollID (ErrNotFound otherwise), mirroring requireParticipantAuth's own participant-scoping check
// (claim-auth.ts) that the self-service path deliberately skips (see Unclaim's doc comment).
func (s *Service) UnclaimFor(ctx context.Context, pollID, optionID, targetParticipantID string, viewer Viewer) error {
	if targetParticipantID == "" {
		return ErrForbidden
	}
	return s.unclaim(ctx, pollID, optionID, targetParticipantID, viewer)
}

// unclaim is Unclaim/UnclaimFor's shared body. targetParticipantID == "" selects Unclaim's
// resolve-from-viewer behavior; non-empty selects UnclaimFor's manager-forced behavior — see both
// methods' doc comments.
func (s *Service) unclaim(ctx context.Context, pollID, optionID, targetParticipantID string, viewer Viewer) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if poll.Type != string(PollTypeSignup) {
		return newValidationError("type", "poll must be a sign-up sheet")
	}

	canManage, err := s.canManagePoll(ctx, q, poll.OrganizationID, poll.CreatedBy, viewer.UserID)
	if err != nil {
		return err
	}

	var participantID string
	switch {
	case targetParticipantID != "":
		if !canManage {
			return ErrForbidden
		}
		target, terr := q.GetParticipant(ctx, targetParticipantID)
		if errors.Is(terr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if terr != nil {
			return terr
		}
		if target.PollID != pollID {
			return ErrNotFound
		}
		participantID = target.ID
	case viewer.GuestParticipantID != "":
		participantID = viewer.GuestParticipantID
	case viewer.UserID != "":
		uid, uerr := strconv.ParseInt(viewer.UserID, 10, 64)
		if uerr != nil {
			return ErrForbidden
		}
		existing, eerr := q.GetParticipantByPollAndUser(ctx, queries.GetParticipantByPollAndUserParams{
			PollID: pollID, UserID: sql.NullInt64{Int64: uid, Valid: true},
		})
		if errors.Is(eerr, sql.ErrNoRows) {
			return ErrNotFound
		}
		if eerr != nil {
			return eerr
		}
		participantID = existing.ID
	default:
		return ErrForbidden
	}

	if !canManage && poll.Status != "open" {
		return ErrPollClosed
	}

	if err := q.DeleteVote(ctx, queries.DeleteVoteParams{ParticipantID: participantID, OptionID: optionID}); err != nil {
		return err
	}
	// PollRoom#unclaim broadcasts poll.changed/'vote' unconditionally in the TS source (see the
	// comment in participants.functions.ts's unclaimSlot) — this port does the same, regardless
	// of whether the delete above actually matched a row.
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "vote"}); err != nil {
		return err
	}
	// unclaimSlot resends the confirmation mail unconditionally after every successful unclaim
	// (participants.functions.ts's own comment: "resend the confirmation so the participant's
	// email reflects their remaining claims") — unlike claimSlot, there's no `changed` gate here
	// at all in the TS source. sendClaimConfirmationMail (timers.go) already no-ops when the
	// participant now has zero remaining claims (or no email on file), so this is safe to enqueue
	// even for the last claim on the poll.
	if err := enqueueMailPoll(ctx, tx, mailPollPayload{PollID: pollID, Event: "claim_confirmation", ParticipantID: participantID}); err != nil {
		return err
	}
	return tx.Commit()
}

// SignupFull ports the capacity half of PollRoom's #emitIfSheetFilled (PollRoom.ts): true iff
// pollID's sign-up sheet has every option's capacity claim count at or above its capacity. Always
// false for a sheet with no options, or with any unlimited-capacity option (options.some(capacity
// === null) in the TS source) — an unlimited option can never be "full". Added in Task 7 (an
// accumulated review requirement) for the HTTP handler layer to call after a claim that actually
// changed something, to decide whether to raise a signup.full digest item.
//
// NOT a deviation, despite first appearances: #emitIfSheetFilled also storage-flags the
// transition (SIGNUP_FULL_KEY) so a later claim/unclaim cycle on an already-full sheet doesn't
// re-announce it every time someone re-takes the last slot — but PollRoom#unclaim unconditionally
// clears that same flag on every unclaim (see its own comment: "freeing a slot re-arms the
// announcement, so filling the sheet again is news again"). Once a sheet is full, every option is
// already at capacity, so no further distinct claim can succeed (Changed: true) until some slot
// is freed by an unclaim — and that unclaim is exactly when TS's flag gets cleared. So a
// Changed:true claim can only ever re-observe "full" here after an intervening unclaim, which is
// precisely the case TS's own flag re-arms for. This port has no equivalent storage slot to flag
// (no durable object) and doesn't need one: recomputing "full" fresh at each Changed claim yields
// the identical cadence — exactly one signup.full per fill, never a duplicate for redundant calls
// on an unchanged-full sheet, since no such call is reachable.
func (s *Service) SignupFull(ctx context.Context, pollID string) (bool, error) {
	options, err := s.q.ListOptionsByPoll(ctx, pollID)
	if err != nil {
		return false, err
	}
	if len(options) == 0 {
		return false, nil
	}
	for _, o := range options {
		if !o.Capacity.Valid {
			return false, nil
		}
	}

	votes, err := s.q.ListVotesByPoll(ctx, pollID)
	if err != nil {
		return false, err
	}
	counts := map[string]int{}
	for _, v := range votes {
		if v.Answer == "yes" {
			counts[v.OptionID]++
		}
	}
	for _, o := range options {
		if counts[o.ID] < int(o.Capacity.Int32) {
			return false, nil
		}
	}
	return true, nil
}

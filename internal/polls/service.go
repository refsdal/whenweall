// Package polls is a behavioral port of src/server/polls/{service,schemas,viewmodel}.ts: the poll
// domain (scheduling polls, options polls, sign-up sheets) — everything except participants,
// votes, comments and claims (Task 3) and notifications/timers (Task 4).
package polls

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/polls/queries"
	"github.com/refsdal/whenweall/internal/rooms"
)

// Viewer identifies who is asking for a poll's view: an authenticated user (UserID) or an
// anonymous participant proving their identity via a guest edit token (GuestParticipantID; see
// internal/auth.VerifyGuestToken). GetView reads only UserID today — GuestParticipantID exists on
// this struct now because Task 3's participant/claim methods share it.
type Viewer struct {
	UserID             string
	GuestParticipantID string
}

// Service is the poll domain service; every exported method below is a behavioral port of one
// function from src/server/polls/service.ts.
type Service struct {
	db *sql.DB
	q  *queries.Queries

	// stats is Task 3's (plan 6) landing-page counters wiring — nil until SetStats is called
	// (main.go's serve(), after both are constructed). A nil stats is a deliberate no-op, not an
	// error: tests that build a bare Service via NewService and never call SetStats get every
	// other behavior unchanged, with counting simply turned off. See recordStats's own doc comment
	// for the call convention every mutating method below uses it with.
	stats *rooms.StatsService
}

// NewService builds a Service bound to sqlDB. Read-only methods use the Service's own Queries;
// every mutating method opens its own transaction and builds a tx-scoped Queries so its domain
// write and its rooms.Emit land atomically (rooms.Emit must run inside the same tx as the write it
// announces — see internal/rooms's package doc).
func NewService(sqlDB *sql.DB) *Service {
	return &Service{db: sqlDB, q: queries.New(sqlDB)}
}

// SetStats wires this Service's landing-page counters — see the stats field's own doc comment.
// Kept as a post-construction setter (mirroring bookings.Service.SetGoogleSync's identical shape)
// rather than a NewService parameter, so every existing NewService call site — most of them in
// tests that have no use for stats counting at all — stays unchanged.
func (s *Service) SetStats(stats *rooms.StatsService) {
	s.stats = stats
}

// recordStats is every stats-counting call site's post-commit hook (Create/Duplicate/Finalize/
// Claim/AddParticipant/UpdateParticipant — internal/rooms.StatsService's own doc comment on
// Record explains why post-commit): a no-op when SetStats was never called (see the stats field's
// doc comment), otherwise StatsService.Record. Every call site calls this explicitly, AFTER its
// own tx.Commit() has already succeeded — never via defer, which would still fire (and record
// counters for a write that never actually landed) on a rolled-back tx.
func (s *Service) recordStats(ctx context.Context, deltas map[string]int64) {
	if s.stats == nil {
		return
	}
	s.stats.Record(ctx, deltas)
}

// tallyAnswerStats is AddParticipant's and UpdateParticipant's shared pre-commit tally: one delta
// per answer in answers, ported from stats-client.ts's recordResponses(Object.values(data.
// answers)) — every answer counts, including on an edit (participants.functions.ts's
// updateParticipant comment: "an edit is a fresh submission — see the spec's §1 on why the totals
// do not net out"), so both callers use this unconditionally, never gated on whether any vote
// actually changed. An answer value outside "yes"/"ifneedbe"/"no" (StatsFieldForAnswer's ok ==
// false) is silently skipped rather than erroring the whole tally — validateAnswersTx has already
// rejected any such value by the time either caller reaches here, so this is defense in depth, not
// a reachable path. Building this map is pure (no DB access), so it happens BEFORE the domain
// write's own tx.Commit() even though recordStats itself only ever runs after it.
func tallyAnswerStats(answers map[string]string) map[string]int64 {
	deltas := make(map[string]int64, len(answers))
	for _, answer := range answers {
		field, ok := rooms.StatsFieldForAnswer(answer)
		if !ok {
			continue
		}
		deltas[field]++
	}
	return deltas
}

const pollFinalizedStatus = "finalized"

// requireOrgPoll fetches pollID (queries.GetPoll already excludes soft-deleted rows) and checks
// it belongs to orgID — the org-scoping half of requireManagedPoll (service.ts).
//
// Wrong-org poll -> ErrNotFound, matching the TS source's own leak-avoidance intent: a poll id's
// existence must never be revealed outside its own org. (Task 7 reverted an earlier, documented
// deviation here that mapped this case to ErrForbidden instead — see the task 7 report for why:
// an accumulated review requirement from Tasks 2-4's own reviews.)
//
// Deviation from the TS source that DOES still hold, required by the brief's exact method
// signatures: every managing call here (Update/SetStatus/Finalize/Delete/Duplicate) carries an
// orgID but no userID or role, so the creator-or-admin/owner check requireManagedPoll also
// enforces (canManageContent: "a same-org member who didn't create it gets FORBIDDEN") cannot be
// reproduced by requireOrgPoll itself — there is no identity to check it against here. Task 7
// retrofits that check at the HTTP handler layer instead, via RequireManageable below, which has
// the caller's Session identity to check it against.
func requireOrgPoll(ctx context.Context, q *queries.Queries, pollID, orgID string) (queries.Poll, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return queries.Poll{}, ErrNotFound
	}
	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return queries.Poll{}, ErrNotFound
	}
	if err != nil {
		return queries.Poll{}, err
	}
	if poll.OrganizationID != orgIDInt {
		return queries.Poll{}, ErrNotFound
	}
	return poll, nil
}

// RequireManageable ports the identity-aware half of requireManagedPoll (service.ts) that
// requireOrgPoll's own signature can't reach: NOT_FOUND for a missing or wrong-org poll (see
// requireOrgPoll's doc comment), then FORBIDDEN unless userID is the poll's own creator or an
// owner/admin of orgID (canManagePoll — the same predicate participants.go/claims.go already use
// for participant/comment/claim authorization). Task 7's HTTP handler layer calls this before
// Update/SetStatus/Finalize/Delete/Duplicate — the five T2 "managing" methods whose own brief-
// pinned signatures carry no identity to check this against themselves — so the retrofit lives
// here rather than widening each of their signatures.
func (s *Service) RequireManageable(ctx context.Context, pollID, orgID, userID string) error {
	poll, err := requireOrgPoll(ctx, s.q, pollID, orgID)
	if err != nil {
		return err
	}
	canManage, err := s.canManagePoll(ctx, s.q, poll.OrganizationID, poll.CreatedBy, userID)
	if err != nil {
		return err
	}
	if !canManage {
		return ErrForbidden
	}
	return nil
}

// Create ports createPoll. Returns the freshly created poll's view; IsOwner is always true on it
// in practice (the caller is, by definition, the poll's own creator, and creating a poll in orgID
// already requires being a member of it — buildView's IsOwner also checks membership, see its
// own doc comment).
func (s *Service) Create(ctx context.Context, orgID, userID string, in CreatePollInput) (*PollView, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrForbidden
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return nil, ErrForbidden
	}

	var deadlineAt sql.NullTime
	if in.DeadlineAt != nil {
		t, perr := parseISODateTime(*in.DeadlineAt)
		if perr != nil {
			return nil, newValidationError("deadlineAt", "deadlineAt must be an ISO 8601 UTC datetime")
		}
		deadlineAt = sql.NullTime{Time: t, Valid: true}
	}

	allowIfNeedBe := true
	switch {
	case in.Type == PollTypeSignup:
		allowIfNeedBe = false
	case in.AllowIfNeedBe != nil:
		allowIfNeedBe = *in.AllowIfNeedBe
	}
	requireParticipantEmail := false
	if in.RequireParticipantEmail != nil {
		requireParticipantEmail = *in.RequireParticipantEmail
	}
	allowComments := true
	if in.AllowComments != nil {
		allowComments = *in.AllowComments
	}
	signupMaxClaims := int32(1)
	if in.SignupMaxClaims != nil {
		signupMaxClaims = int32(*in.SignupMaxClaims)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	pollID := db.NewID()
	now := time.Now().UTC()

	poll := queries.Poll{
		ID:                      pollID,
		OrganizationID:          orgIDInt,
		CreatedBy:               sql.NullInt64{Int64: userIDInt, Valid: true},
		Type:                    string(in.Type),
		Title:                   strings.TrimSpace(in.Title),
		Description:             optionalTrimmedString(in.Description),
		Location:                optionalTrimmedString(in.Location),
		Timezone:                in.Timezone,
		Status:                  "open",
		DeadlineAt:              deadlineAt,
		RequireParticipantEmail: requireParticipantEmail,
		AllowComments:           allowComments,
		AllowIfNeedBe:           allowIfNeedBe,
		SignupMaxClaims:         signupMaxClaims,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	if err := q.InsertPoll(ctx, queries.InsertPollParams{
		ID:                      poll.ID,
		OrganizationID:          poll.OrganizationID,
		CreatedBy:               poll.CreatedBy,
		Type:                    poll.Type,
		Title:                   poll.Title,
		Description:             poll.Description,
		Location:                poll.Location,
		Timezone:                poll.Timezone,
		Status:                  poll.Status,
		DeadlineAt:              poll.DeadlineAt,
		RequireParticipantEmail: poll.RequireParticipantEmail,
		AllowComments:           poll.AllowComments,
		AllowIfNeedBe:           poll.AllowIfNeedBe,
		SignupMaxClaims:         poll.SignupMaxClaims,
		CreatedAt:               poll.CreatedAt,
		UpdatedAt:               poll.UpdatedAt,
	}); err != nil {
		return nil, err
	}

	fallback := sql.NullInt32{Int32: 1, Valid: true}
	for i, opt := range in.Options {
		cols, err := pollOptionColumns(opt, in.Type, fallback)
		if err != nil {
			return nil, err
		}
		if err := q.InsertPollOption(ctx, queries.InsertPollOptionParams{
			ID:       db.NewID(),
			PollID:   pollID,
			Position: int32(i),
			Kind:     cols.Kind,
			StartAt:  cols.StartAt,
			EndAt:    cols.EndAt,
			Label:    cols.Label,
			Capacity: cols.Capacity,
		}); err != nil {
			return nil, err
		}
	}

	// Port of createPoll's own syncDeadline call (polls.functions.ts): arm the deadline job now,
	// in-tx, when the poll was created with one.
	if deadlineAt.Valid {
		t := deadlineAt.Time
		if err := armDeadline(ctx, tx, pollID, &t); err != nil {
			return nil, err
		}
	}

	view, err := s.buildView(ctx, q, poll, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Task 3 (plan 6): createPoll's own recordPollCreated call (polls.functions.ts) — post-commit,
	// best-effort (see recordStats/StatsService.Record's own doc comments): a landing-page counter
	// must never hold this poll's own write transaction open, nor fail poll creation if it errors.
	s.recordStats(ctx, map[string]int64{rooms.StatsPollsCreated: 1})

	// ensureCreatorSubscription (subscriptions.ts) — deliberately outside the transaction/after
	// commit: the creator's subscription is a notification convenience, not part of the poll's
	// integrity, so a failure here must never roll back the poll itself (see the TS source's own
	// comment at this call site).
	s.ensureCreatorSubscriptionBestEffort(ctx, pollID, poll.CreatedBy)

	return view, nil
}

// GetView ports getPollView. Returns (nil, nil) — not an error — when the poll doesn't exist or
// is soft-deleted, matching the TS source returning null.
func (s *Service) GetView(ctx context.Context, pollID string, viewer Viewer) (*PollView, error) {
	poll, err := s.q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // mirrors getPollView returning null for missing/deleted, not an error
	}
	if err != nil {
		return nil, err
	}
	return s.buildView(ctx, s.q, poll, viewer.UserID)
}

// Update ports updatePoll. Returns the freshly updated view. IsOwner and Notifications on the
// returned view are always false/nil: unlike GetView/Create/Duplicate, the brief's exact
// signature for Update carries no viewer/userID, so buildView has no identity to compute either
// against — see the task report.
func (s *Service) Update(ctx context.Context, pollID, orgID string, in UpdatePollInput) (*PollView, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := requireOrgPoll(ctx, q, pollID, orgID)
	if err != nil {
		return nil, err
	}
	if poll.Status == pollFinalizedStatus && in.Options != nil {
		return nil, ErrPollFinalized
	}

	merged := poll
	if in.Title != nil {
		merged.Title = strings.TrimSpace(*in.Title)
	}
	if in.Description != nil {
		merged.Description = optionalTrimmedString(in.Description)
	}
	if in.Location != nil {
		merged.Location = optionalTrimmedString(in.Location)
	}
	if in.Timezone != nil {
		merged.Timezone = *in.Timezone
	}
	if in.DeadlineAtSet {
		if in.DeadlineAt == nil {
			merged.DeadlineAt = sql.NullTime{}
		} else {
			t, perr := parseISODateTime(*in.DeadlineAt)
			if perr != nil {
				return nil, newValidationError("deadlineAt", "deadlineAt must be an ISO 8601 UTC datetime")
			}
			merged.DeadlineAt = sql.NullTime{Time: t, Valid: true}
		}
	}
	if in.RequireParticipantEmail != nil {
		merged.RequireParticipantEmail = *in.RequireParticipantEmail
	}
	if in.AllowComments != nil {
		merged.AllowComments = *in.AllowComments
	}
	if in.AllowIfNeedBe != nil {
		merged.AllowIfNeedBe = *in.AllowIfNeedBe
	}
	if in.SignupMaxClaims != nil {
		merged.SignupMaxClaims = int32(*in.SignupMaxClaims)
	}
	merged.UpdatedAt = time.Now().UTC()

	if err := q.UpdatePollScalars(ctx, queries.UpdatePollScalarsParams{
		ID:                      pollID,
		Title:                   merged.Title,
		Description:             merged.Description,
		Location:                merged.Location,
		Timezone:                merged.Timezone,
		DeadlineAt:              merged.DeadlineAt,
		RequireParticipantEmail: merged.RequireParticipantEmail,
		AllowComments:           merged.AllowComments,
		AllowIfNeedBe:           merged.AllowIfNeedBe,
		SignupMaxClaims:         merged.SignupMaxClaims,
		UpdatedAt:               merged.UpdatedAt,
	}); err != nil {
		return nil, err
	}

	if in.Options != nil {
		if err := replaceOptions(ctx, q, pollID, PollType(poll.Type), in.Options); err != nil {
			return nil, err
		}
	}

	// Port of updatePoll's own conditional syncDeadline call (polls.functions.ts): the DO's alarm
	// (here, the "poll.deadline" job) is only re-synced when the caller actually provided a
	// deadlineAt value — otherwise an update that doesn't touch the deadline would wrongly clear
	// a still-active one. DeadlineAtSet is exactly that "the field was provided" signal.
	if in.DeadlineAtSet {
		var deadlineAtPtr *time.Time
		if merged.DeadlineAt.Valid {
			deadlineAtPtr = &merged.DeadlineAt.Time
		}
		if err := armDeadline(ctx, tx, pollID, deadlineAtPtr); err != nil {
			return nil, err
		}
	}

	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "poll"}); err != nil {
		return nil, err
	}

	view, err := s.buildView(ctx, q, merged, "")
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return view, nil
}

// replaceOptions ports the options-array half of updatePoll: options carrying an id that still
// exists are updated in place (position/kind/fields), unrecognized/missing ids are inserted as new
// options, and existing options not named in options are deleted (poll_options' ON DELETE CASCADE
// on votes.option_id removes their votes as a side effect — matching the TS test "removing an
// option deletes its votes"). For a signup poll, lowering a retained option's capacity below its
// current claim count is rejected (ErrCapacityBelowClaims — TS: CAPACITY_BELOW_CLAIMS) before
// any row is touched.
func replaceOptions(ctx context.Context, q *queries.Queries, pollID string, pollType PollType, options []OptionInput) error {
	existing, err := q.ListOptionsByPoll(ctx, pollID)
	if err != nil {
		return err
	}
	existingByID := make(map[string]queries.PollOption, len(existing))
	for _, o := range existing {
		existingByID[o.ID] = o
	}

	if pollType == PollTypeSignup {
		votes, err := q.ListVotesByPoll(ctx, pollID)
		if err != nil {
			return err
		}
		counts := map[string]int{}
		for _, v := range votes {
			if v.Answer == "yes" {
				counts[v.OptionID]++
			}
		}
		for _, opt := range options {
			ex, ok := existingByID[opt.ID]
			if opt.ID == "" || !ok {
				continue
			}
			newCapacity := ex.Capacity
			if opt.CapacitySet {
				if opt.Capacity != nil {
					newCapacity = sql.NullInt32{Int32: int32(*opt.Capacity), Valid: true}
				} else {
					newCapacity = sql.NullInt32{}
				}
			}
			if newCapacity.Valid && int(newCapacity.Int32) < counts[opt.ID] {
				return ErrCapacityBelowClaims
			}
		}
	}

	keepIDs := make(map[string]bool, len(options))
	for i, opt := range options {
		ex, hasExisting := existingByID[opt.ID]
		if opt.ID == "" {
			hasExisting = false
		}
		id := opt.ID
		if !hasExisting {
			id = db.NewID()
		}
		keepIDs[id] = true

		fallback := sql.NullInt32{Int32: 1, Valid: true}
		if hasExisting {
			fallback = ex.Capacity
		}
		cols, err := pollOptionColumns(opt, pollType, fallback)
		if err != nil {
			return err
		}

		if hasExisting {
			if err := q.UpdatePollOption(ctx, queries.UpdatePollOptionParams{
				ID: id, Position: int32(i), Kind: cols.Kind, StartAt: cols.StartAt,
				EndAt: cols.EndAt, Label: cols.Label, Capacity: cols.Capacity,
			}); err != nil {
				return err
			}
		} else {
			if err := q.InsertPollOption(ctx, queries.InsertPollOptionParams{
				ID: id, PollID: pollID, Position: int32(i), Kind: cols.Kind, StartAt: cols.StartAt,
				EndAt: cols.EndAt, Label: cols.Label, Capacity: cols.Capacity,
			}); err != nil {
				return err
			}
		}
	}

	for _, ex := range existing {
		if !keepIDs[ex.ID] {
			if err := q.DeletePollOption(ctx, ex.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// SetStatus ports setPollStatus (open | closed).
func (s *Service) SetStatus(ctx context.Context, pollID, orgID, status string) error {
	if status != "open" && status != "closed" {
		return newValidationError("status", "status must be one of open, closed")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := requireOrgPoll(ctx, q, pollID, orgID)
	if err != nil {
		return err
	}
	if poll.Status == pollFinalizedStatus {
		return ErrPollFinalized
	}

	if err := q.SetPollStatus(ctx, queries.SetPollStatusParams{
		ID: pollID, Status: status, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return err
	}
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "poll"}); err != nil {
		return err
	}
	return tx.Commit()
}

// Finalize ports finalizePoll's domain mutation, its unconditional participant/owner "the time is
// set" mail (Task 4), AND — actorUserID, added in Task 7 per an accumulated review requirement —
// the separate subscriber notification polls.functions.ts's finalizePoll ROUTE fires afterward via
// emitPollEvent(pollId, 'poll.finalized', {actorUserId}): every org member subscribed to this poll
// with the poll.finalized email channel on, EXCLUDING the acting user (resolveRecipients already
// drops actorUserID) and excluding anyone already enqueued the direct-mail way above (so a
// subscribed owner/participant never gets two "finalized" emails for the same event).
// FinalizeWithCount is Finalize's implementation, additionally reporting how many distinct
// recipients a "finalized" mail:poll job was enqueued for — the `{ sent }` count finalizePoll
// (polls.functions.ts) returned and the SPA's success toast prints. Finalize below keeps the
// error-only signature every other caller uses.
func (s *Service) FinalizeWithCount(ctx context.Context, pollID, orgID, optionID, actorUserID string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := requireOrgPoll(ctx, q, pollID, orgID)
	if err != nil {
		return 0, err
	}
	if poll.Type == string(PollTypeSignup) {
		return 0, newValidationError("type", "signup polls cannot be finalized")
	}
	if poll.Status == pollFinalizedStatus {
		// Plain ErrConflict, deliberately not ErrPollFinalized: finalizePoll's own "already
		// finalized" guard (service.ts) throws the plain CONFLICT code, not POLL_FINALIZED — see
		// errors.go's package doc comment for why the two near-identical English messages carry
		// different TS codes.
		return 0, fmt.Errorf("%w: poll is already finalized", ErrConflict)
	}

	options, err := q.ListOptionsByPoll(ctx, pollID)
	if err != nil {
		return 0, err
	}
	found := false
	for _, o := range options {
		if o.ID == optionID {
			found = true
			break
		}
	}
	if !found {
		return 0, ErrNotFound
	}

	if err := q.FinalizePoll(ctx, queries.FinalizePollParams{
		ID:                pollID,
		FinalizedOptionID: sql.NullString{String: optionID, Valid: true},
		Status:            pollFinalizedStatus,
		UpdatedAt:         time.Now().UTC(),
	}); err != nil {
		return 0, err
	}
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "poll"}); err != nil {
		return 0, err
	}

	// Port of finalizePoll's own syncDeadline(id, null) call (polls.functions.ts): a finalized
	// poll no longer needs its deadline job OR its 24h "closes soon" reminder (I11).
	if err := jobs.Cancel(ctx, tx, jobKindDeadline, "poll:"+pollID); err != nil {
		return 0, err
	}
	if err := jobs.Cancel(ctx, tx, jobKindReminder, "poll:"+pollID); err != nil {
		return 0, err
	}

	// Port of finalizePoll's recipient computation (service.ts) + sendFinalizedEmails: every
	// participant with an email, plus the poll's creator if one exists — deduped by email (lower-
	// cased), owner added only if not already present. One "mail:poll"/"finalized" job per unique
	// recipient, ids-only (participantId or userId — never an address). sent counts them.
	sent := 0
	seenEmail := make(map[string]bool)
	seenUserID := make(map[string]bool)
	participantRows, err := q.ListParticipantsByPoll(ctx, pollID)
	if err != nil {
		return 0, err
	}
	for _, p := range participantRows {
		if !p.Email.Valid {
			continue
		}
		key := strings.ToLower(p.Email.String)
		if seenEmail[key] {
			continue
		}
		seenEmail[key] = true
		if err := enqueueMailPoll(ctx, tx, mailPollPayload{PollID: pollID, Event: "finalized", ParticipantID: p.ID}); err != nil {
			return 0, err
		}
		sent++
	}
	if poll.CreatedBy.Valid {
		owner, oerr := q.GetUser(ctx, poll.CreatedBy.Int64)
		switch {
		case errors.Is(oerr, sql.ErrNoRows):
			// Account gone — same graceful skip as the TS source (finalizePoll's own comment).
		case oerr != nil:
			return 0, oerr
		default:
			key := strings.ToLower(owner.Email)
			ownerIDStr := strconv.FormatInt(owner.ID, 10)
			if !seenEmail[key] {
				if err := enqueueMailPoll(ctx, tx, mailPollPayload{
					PollID: pollID, Event: "finalized", UserID: ownerIDStr,
				}); err != nil {
					return 0, err
				}
				sent++
			}
			seenUserID[ownerIDStr] = true
		}
	}

	// The separate subscriber notification (emitPollEvent's own poll.finalized call in
	// polls.functions.ts's finalizePoll route) — every org member subscribed to poll.finalized's
	// email channel, minus the actor (resolveRecipients' own actorUserID parameter) and minus
	// anyone the direct-mail loop above already enqueued for.
	recipients, err := s.resolveRecipients(ctx, q, poll.OrganizationID, pollID, EventPollFinalized, actorUserID)
	if err != nil {
		return 0, err
	}
	for _, r := range recipients {
		if seenUserID[r.UserID] {
			continue
		}
		seenUserID[r.UserID] = true
		if err := enqueueMailPoll(ctx, tx, mailPollPayload{PollID: pollID, Event: "finalized", UserID: r.UserID}); err != nil {
			return 0, err
		}
		sent++
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	// finalizePoll's own recordPollFinalized call (polls.functions.ts) — reached only on a genuine
	// not-decided -> decided transition. Post-commit, best-effort — see recordStats.
	s.recordStats(ctx, map[string]int64{rooms.StatsPollsFinalized: 1})

	return sent, nil
}

// Finalize ports finalizePoll — see FinalizeWithCount for the body; this is the error-only
// signature the brief pinned and every non-HTTP caller uses.
func (s *Service) Finalize(ctx context.Context, pollID, orgID, optionID, actorUserID string) error {
	_, err := s.FinalizeWithCount(ctx, pollID, orgID, optionID, actorUserID)
	return err
}

// Delete ports deletePoll: a soft delete (deleted_at set). Also emits poll.changed/entity:poll —
// the brief's own list of emitting mutations names only Update/SetStatus/Finalize, but
// polls.functions.ts's deletePoll route calls notifyChanged(pollId, 'poll') too (so connected
// viewers see the poll disappear), so this port includes it for TS behavioral parity; flagged in
// the task report as an addition beyond the brief's literal list.
func (s *Service) Delete(ctx context.Context, pollID, orgID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	if _, err := requireOrgPoll(ctx, q, pollID, orgID); err != nil {
		return err
	}

	now := time.Now().UTC()
	if err := q.SoftDeletePoll(ctx, queries.SoftDeletePollParams{
		ID: pollID, DeletedAt: sql.NullTime{Time: now, Valid: true},
	}); err != nil {
		return err
	}

	// deletePoll's deleteScopeSubscriptions (service.ts:436-448): subscriptions are keyed by a
	// polymorphic scope, so no FK cascades them — drop them here, in the same transaction.
	if err := q.DeleteSubscriptionsByScope(ctx, queries.DeleteSubscriptionsByScopeParams{
		ScopeType: "poll", ScopeID: pollID,
	}); err != nil {
		return err
	}

	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "poll"}); err != nil {
		return err
	}

	// Port of deletePoll's own syncDeadline(id, null) call (polls.functions.ts): a deleted poll
	// no longer needs its deadline job OR its 24h "closes soon" reminder (I11).
	if err := jobs.Cancel(ctx, tx, jobKindDeadline, "poll:"+pollID); err != nil {
		return err
	}
	if err := jobs.Cancel(ctx, tx, jobKindReminder, "poll:"+pollID); err != nil {
		return err
	}

	return tx.Commit()
}

// Duplicate ports duplicatePoll: a new poll in the same org, owned by userID, with the same
// options (fresh ids) but zero participants/votes/comments and status reset to "open". No room
// event is emitted — the TS source doesn't call notifyChanged after duplicatePoll either, since
// nobody is watching the brand-new poll's room yet.
func (s *Service) Duplicate(ctx context.Context, pollID, orgID, userID string) (*PollView, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrForbidden
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return nil, ErrForbidden
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	original, err := requireOrgPoll(ctx, q, pollID, orgID)
	if err != nil {
		return nil, err
	}
	options, err := q.ListOptionsByPoll(ctx, pollID)
	if err != nil {
		return nil, err
	}

	newID := db.NewID()
	now := time.Now().UTC()

	copyPoll := queries.Poll{
		ID:                      newID,
		OrganizationID:          orgIDInt,
		CreatedBy:               sql.NullInt64{Int64: userIDInt, Valid: true},
		Type:                    original.Type,
		Title:                   original.Title + " (copy)",
		Description:             original.Description,
		Location:                original.Location,
		Timezone:                original.Timezone,
		Status:                  "open",
		RequireParticipantEmail: original.RequireParticipantEmail,
		AllowComments:           original.AllowComments,
		AllowIfNeedBe:           original.AllowIfNeedBe,
		SignupMaxClaims:         original.SignupMaxClaims,
		CreatedAt:               now,
		UpdatedAt:               now,
	}

	if err := q.InsertPoll(ctx, queries.InsertPollParams{
		ID: copyPoll.ID, OrganizationID: copyPoll.OrganizationID, CreatedBy: copyPoll.CreatedBy,
		Type: copyPoll.Type, Title: copyPoll.Title, Description: copyPoll.Description,
		Location: copyPoll.Location, Timezone: copyPoll.Timezone, Status: copyPoll.Status,
		DeadlineAt:              sql.NullTime{},
		RequireParticipantEmail: copyPoll.RequireParticipantEmail,
		AllowComments:           copyPoll.AllowComments,
		AllowIfNeedBe:           copyPoll.AllowIfNeedBe,
		SignupMaxClaims:         copyPoll.SignupMaxClaims,
		CreatedAt:               copyPoll.CreatedAt,
		UpdatedAt:               copyPoll.UpdatedAt,
	}); err != nil {
		return nil, err
	}

	for _, o := range options {
		if err := q.InsertPollOption(ctx, queries.InsertPollOptionParams{
			ID: db.NewID(), PollID: newID, Position: o.Position, Kind: o.Kind,
			StartAt: o.StartAt, EndAt: o.EndAt, Label: o.Label, Capacity: o.Capacity,
		}); err != nil {
			return nil, err
		}
	}

	view, err := s.buildView(ctx, q, copyPoll, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Task 3 (plan 6): duplicatePoll's own recordPollCreated call (polls.functions.ts) — "a
	// duplicate is a new poll from the counter's point of view" (that source's own comment).
	// Post-commit, best-effort — see recordStats/StatsService.Record's own doc comments.
	s.recordStats(ctx, map[string]int64{rooms.StatsPollsCreated: 1})

	// ensureCreatorSubscription + carrying the duplicator's own notification override on the
	// original across to the copy (duplicatePoll's own post-batch calls, service.ts) —
	// deliberately outside the transaction/after commit, same reasoning as Create's.
	s.ensureCreatorSubscriptionBestEffort(ctx, newID, copyPoll.CreatedBy)
	s.copyNotificationOverrideBestEffort(ctx, pollID, newID, userIDInt)

	return view, nil
}

// ListMine ports listMyPolls.
func (s *Service) ListMine(ctx context.Context, orgID string) ([]PollSummary, error) {
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		return nil, ErrForbidden
	}

	rows, err := s.q.ListPollsByOrg(ctx, orgIDInt)
	if err != nil {
		return nil, err
	}

	out := make([]PollSummary, 0, len(rows))
	for _, p := range rows {
		participants, err := s.q.ListParticipantsByPoll(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		votes, err := s.q.ListVotesByPoll(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		claimCount := 0
		for _, v := range votes {
			if v.Answer == "yes" {
				claimCount++
			}
		}
		out = append(out, PollSummary{
			ID:               p.ID,
			Title:            p.Title,
			Type:             p.Type,
			Status:           p.Status,
			DeadlineAt:       nullTimeToISO(p.DeadlineAt),
			ParticipantCount: len(participants),
			ClaimCount:       claimCount,
			CreatedAt:        formatISO(p.CreatedAt),
			UpdatedAt:        formatISO(p.UpdatedAt),
		})
	}
	return out, nil
}

// CloseExpired ports closeExpiredPoll — the job handler body a scheduled "poll.deadline" job
// (Task 4) calls. Returns (false, nil) for a missing/deleted poll, a non-open poll, one with no
// deadline, or one whose deadline hasn't passed yet; (true, nil) after closing it.
//
// The TS source has a second regression test here for a real bug in that implementation: D1
// stored deadlineAt as text, and comparing two ISO timestamp *strings* lexicographically
// disagrees with comparing the *instants* they denote once one of them lacks the other's
// fractional-second digits (e.g. "…10:00:00Z" sorts after "…10:00:00.004Z" as strings, even
// though the first instant is earlier) — Date.parse was used specifically to avoid that. Postgres
// stores deadline_at as timestamptz and this port compares real time.Time instants throughout, so
// that bug class cannot occur here; the ported test case is kept anyway, as a regression-proof of
// the same real-world input shape (a whole-second, no-fraction deadline).
func (s *Service) CloseExpired(ctx context.Context, pollID string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if poll.Status != "open" || !poll.DeadlineAt.Valid || poll.DeadlineAt.Time.After(time.Now()) {
		return false, nil
	}

	if err := q.SetPollStatus(ctx, queries.SetPollStatusParams{
		ID: pollID, Status: "closed", UpdatedAt: time.Now().UTC(),
	}); err != nil {
		return false, err
	}
	if err := rooms.Emit(ctx, tx, "poll:"+pollID, "poll.changed", map[string]any{"entity": "poll"}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// optionalTrimmedString converts a *string field (nil = omitted) to sql.NullString, trimming
// whitespace the same way zod's z.string().trim() would on the field's parsed value. A provided-
// but-empty-after-trim string is still stored (Valid: true, String: "") — only an omitted (nil)
// field is stored as SQL NULL.
func optionalTrimmedString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: strings.TrimSpace(*s), Valid: true}
}

// optionColumns is the set of poll_options columns derived from one OptionInput.
type optionColumns struct {
	Kind     string
	StartAt  sql.NullTime
	EndAt    sql.NullTime
	Label    sql.NullString
	Capacity sql.NullInt32
}

// pollOptionColumns ports optionRowFields (service.ts). fallbackCapacity is used only when the
// option's own capacity is omitted (OptionInput.CapacitySet == false) — see OptionInput's doc
// comment for why omitted and explicit-null are handled differently.
func pollOptionColumns(opt OptionInput, pollType PollType, fallbackCapacity sql.NullInt32) (optionColumns, error) {
	var capacity sql.NullInt32
	if pollType == PollTypeSignup {
		switch {
		case opt.CapacitySet && opt.Capacity != nil:
			capacity = sql.NullInt32{Int32: int32(*opt.Capacity), Valid: true}
		case opt.CapacitySet:
			// explicit null -> unlimited (capacity left as the zero value, i.e. SQL NULL)
		default:
			capacity = fallbackCapacity
		}
	}

	switch opt.Kind {
	case OptionKindDate:
		t, err := parseDateOnly(opt.Date)
		if err != nil {
			return optionColumns{}, fmt.Errorf("polls: invalid date option %q: %w", opt.Date, err)
		}
		return optionColumns{Kind: string(OptionKindDate), StartAt: sql.NullTime{Time: t, Valid: true}, Capacity: capacity}, nil
	case OptionKindDatetime:
		start, err := parseISODateTime(opt.StartAt)
		if err != nil {
			return optionColumns{}, fmt.Errorf("polls: invalid datetime option startAt %q: %w", opt.StartAt, err)
		}
		cols := optionColumns{Kind: string(OptionKindDatetime), StartAt: sql.NullTime{Time: start, Valid: true}, Capacity: capacity}
		if opt.EndAt != nil && *opt.EndAt != "" {
			end, err := parseISODateTime(*opt.EndAt)
			if err != nil {
				return optionColumns{}, fmt.Errorf("polls: invalid datetime option endAt %q: %w", *opt.EndAt, err)
			}
			cols.EndAt = sql.NullTime{Time: end, Valid: true}
		}
		return cols, nil
	default: // text
		return optionColumns{Kind: string(OptionKindText), Label: sql.NullString{String: opt.Label, Valid: true}, Capacity: capacity}, nil
	}
}

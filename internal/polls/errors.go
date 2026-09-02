package polls

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sentinel errors every managing/mutating call can return. writeServiceError (handlers.go) maps
// these to the HTTP envelope; every one of them is deliberately reachable without ever falling
// through to a generic 500 for a client-caused condition.
//
// ErrConflict used to be a single collapsed sentinel standing in for several distinct TS
// AppError codes (POLL_CLOSED, POLL_FINALIZED, CONFLICT, LIMIT_REACHED, CLAIM_LIMIT_REACHED,
// CAPACITY_BELOW_CLAIMS, EMAIL_REQUIRED), distinguishable only by each call site's error message
// — which meant the HTTP layer could only ever report the generic "conflict" envelope code, even
// though the TS frontend (src/lib/use-claims.ts, src/components/poll/use-answer-draft.ts,
// src/routes/p/$id/edit.tsx) switches on the specific SCREAMING_CASE code to pick its error
// copy. The six sentinels below fix that: each wraps ErrConflict (errors.Is(x, ErrConflict) is
// still true for every one of them, so any old code checking only the collapsed sentinel keeps
// working) but is itself also a distinguishable target for errors.Is, so writeServiceError can
// map each one to its own snake_case envelope code. The TS SCREAMING_CASE -> our snake_case
// mapping (plan 8 owns turning our envelope back into the frontend's expected shape):
//
//	TS AppError code          Go sentinel               envelope code
//	POLL_CLOSED                ErrPollClosed             poll_closed
//	POLL_FINALIZED             ErrPollFinalized          poll_finalized
//	LIMIT_REACHED               ErrLimitReached           limit_reached
//	CLAIM_LIMIT_REACHED         ErrClaimLimitReached      claim_limit_reached
//	CAPACITY_BELOW_CLAIMS       ErrCapacityBelowClaims    capacity_below_claims
//	EMAIL_REQUIRED              ErrEmailRequired          email_required
//	CONFLICT (plain)            ErrConflict               conflict
//	SLOT_FULL                   ErrCapacityFull           capacity_full
//
// A bare ErrConflict return (not one of the six sentinels below) still maps to the generic
// "conflict" envelope code — this is the correct port of finalizePoll's plain
// `throw new AppError('CONFLICT')` for "already finalized" (service.ts), which is NOT the same
// TS code as POLL_FINALIZED despite the similar English wording; see Finalize's own call site.
var (
	// ErrForbidden is returned when the caller is authenticated but not allowed to act on the
	// poll — ports the "wrong org" half of requireManagedPoll (src/server/polls/service.ts).
	// Unlike the TS original (which maps a wrong-org poll to NOT_FOUND, so a poll id's existence
	// is never leaked outside its own org) the Go port's managing calls only ever receive an
	// orgID, no userID/role — see the doc comment on requireOrgPoll in service.go for the full
	// reasoning — so "this poll belongs to a different org" is reported as ErrForbidden here,
	// not ErrNotFound. This is an intentional, documented deviation from the TS behavior.
	ErrForbidden = errors.New("polls: forbidden")

	// ErrNotFound is returned when a poll (or an option/participant/comment within it) doesn't
	// exist or is soft-deleted.
	ErrNotFound = errors.New("polls: not found")

	// ErrConflict is returned when the poll's current state precludes the requested action and
	// no more specific sentinel below applies (TS: the plain CONFLICT code — see this file's
	// package-level doc comment for the full code table and the more specific sentinels that
	// wrap this one).
	ErrConflict = errors.New("polls: conflict")

	// ErrCapacityFull is returned by Claim when a sign-up slot's capacity is already met. This is
	// the sentinel for TS's SLOT_FULL code; it does not wrap ErrConflict (SLOT_FULL was never
	// part of the CONFLICT family in the TS source either — see writeServiceError, which checks
	// this sentinel before the ErrConflict family).
	ErrCapacityFull = errors.New("polls: capacity full")

	// ErrPollClosed wraps ErrConflict: a participant/comment/claim mutation was attempted on a
	// poll that is not open. TS: POLL_CLOSED.
	ErrPollClosed = fmt.Errorf("%w: poll is not open", ErrConflict)

	// ErrPollFinalized wraps ErrConflict: the poll has already been finalized, which blocks
	// editing its options (Update) or changing its open/closed status (SetStatus). TS:
	// POLL_FINALIZED. NOT the same TS code as Finalize's own "already finalized" guard, which is
	// a plain CONFLICT — see this file's package-level doc comment.
	ErrPollFinalized = fmt.Errorf("%w: poll is finalized", ErrConflict)

	// ErrLimitReached wraps ErrConflict: the poll's participant cap (LimitParticipants) has
	// already been reached. TS: LIMIT_REACHED.
	ErrLimitReached = fmt.Errorf("%w: participant limit reached", ErrConflict)

	// ErrClaimLimitReached wraps ErrConflict: the claiming participant has already reached the
	// poll's signupMaxClaims cap. TS: CLAIM_LIMIT_REACHED.
	ErrClaimLimitReached = fmt.Errorf("%w: claim limit reached", ErrConflict)

	// ErrCapacityBelowClaims wraps ErrConflict: an option update tried to lower a sign-up slot's
	// capacity below its current claim count. TS: CAPACITY_BELOW_CLAIMS.
	ErrCapacityBelowClaims = fmt.Errorf("%w: capacity below current claims", ErrConflict)

	// ErrEmailRequired wraps ErrConflict: the poll requires a participant email and none was
	// given. TS: EMAIL_REQUIRED — an AppError code in the TS source, exactly like its siblings
	// above, not a zod validation issue; ported here as its own sentinel (not *ValidationError)
	// so the envelope reports the distinct "email_required" code the frontend switches on,
	// rather than being folded into the generic "invalid"/Fields shape.
	ErrEmailRequired = fmt.Errorf("%w: email is required", ErrConflict)
)

// ValidationError reports one or more field-level validation failures, ported from zod's issue
// list in src/server/polls/schemas.ts. Fields maps a field path (dot-joined, array indices
// inline — e.g. "options.0.endAt", "signupMaxClaims") to a human-readable message.
//
// ErrValidation is the zero-value sentinel of this type: errors.Is(err, ErrValidation) is true
// for any *ValidationError regardless of its Fields (see Is below); errors.As(err, &verr) — or a
// type assertion — recovers the field detail.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "polls: validation failed"
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, e.Fields[k]))
	}
	return "polls: validation failed: " + strings.Join(parts, "; ")
}

// Is implements errors.Is support: any *ValidationError matches the ErrValidation sentinel (and
// any other *ValidationError), regardless of Fields content — the sentinel is a type marker, not
// a specific instance.
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)
	return ok
}

// ErrValidation is the base sentinel of *ValidationError — see the type's doc comment.
var ErrValidation = &ValidationError{}

// newValidationError builds a *ValidationError from a field/message pair, the common case of a
// single-issue validation failure.
func newValidationError(field, message string) *ValidationError {
	return &ValidationError{Fields: map[string]string{field: message}}
}

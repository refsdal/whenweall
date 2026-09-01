package polls

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sentinel errors every managing/mutating call can return. Handlers (Task 7) map these to the
// HTTP envelope; every one of them is deliberately reachable without ever falling through to a
// generic 500 for a client-caused condition.
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

	// ErrConflict is returned when the poll's current state precludes the requested action.
	// This collapses three distinct AppError codes the TS service used
	// (POLL_FINALIZED, CONFLICT, CAPACITY_BELOW_CLAIMS) into one sentinel — see the doc comment
	// at each call site in service.go for which TS code a given ErrConflict return corresponds
	// to; Task 7's handler layer can still distinguish them by message if it ever needs to.
	ErrConflict = errors.New("polls: conflict")

	// ErrCapacityFull is returned by Claim (Task 3) when a sign-up slot's capacity is already
	// met. Declared here now per the brief so Task 3 doesn't need to touch this file.
	ErrCapacityFull = errors.New("polls: capacity full")
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

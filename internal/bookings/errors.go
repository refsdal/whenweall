package bookings

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sentinel errors every managing/mutating call can return. Declared with specific conflict codes
// from the start (mirroring internal/polls/errors.go's own rationale): each TS AppError code this
// package's source (src/server/bookings/{pages,bookings}.ts) throws gets its own Go sentinel, so a
// future HTTP layer can map each one to its own envelope code rather than collapsing everything
// behind one generic "conflict".
//
//	TS AppError code     Go sentinel             Used by
//	SLUG_TAKEN            ErrSlugTaken            CreatePage/UpdatePage (this task)
//	HANDLE_TAKEN          ErrHandleTaken          SetOrgSlug (this task) — an addition beyond the
//	                                              brief's named sentinel list: SetOrgSlug's own
//	                                              conflict is a distinct TS code from a page's own
//	                                              slug conflict (organization.slug vs
//	                                              booking_pages.slug), so it gets its own sentinel
//	                                              rather than being folded into ErrSlugTaken.
//	SLOT_UNAVAILABLE      ErrSlotTaken            Task 3 (bookings.ts's createBooking)
//	PAGE_PAUSED           ErrPagePaused           Task 3
//	BOOKING_PAST          ErrBookingPast          Task 3
//	(no direct TS code — a   ErrGoogleNotConnected  Task 5 (google.go), google-sync.ts
//	 write attempted with
//	 no Google connection)
//
// ErrSlotTaken/ErrPagePaused/ErrBookingPast/ErrGoogleNotConnected are declared now, ahead of the
// tasks actually returning them (3 for the first three, 5 for the last), per this task's brief:
// conflict sentinels are named up front rather than added piecemeal as each task needs one.
var (
	// ErrForbidden is returned when the caller is authenticated but not allowed to act on a page's
	// contents (role/creator checks). A page belonging to a DIFFERENT org maps to ErrNotFound
	// instead, matching the TS source's own leak-avoidance rule (requireManagedPage, pages.ts) — a
	// page id's existence is never revealed outside its own org.
	ErrForbidden = errors.New("bookings: forbidden")

	// ErrNotFound is returned when a booking page doesn't exist, is soft-deleted, or belongs to a
	// different org than the one asking, and also for a string org/user id that fails to parse as
	// the bigint Limen expects (see this package's service.go doc comments on the id-boundary
	// convention).
	ErrNotFound = errors.New("bookings: not found")

	// ErrConflict is the generic conflict sentinel every specific one below wraps.
	ErrConflict = errors.New("bookings: conflict")

	// ErrSlugTaken wraps ErrConflict: CreatePage/UpdatePage collided with another live page's
	// (organization_id, slug) — the partial unique index booking_pages_org_slug_uidx (WHERE
	// deleted_at IS NULL), which a soft-deleted page's slug never blocks. TS: SLUG_TAKEN.
	ErrSlugTaken = fmt.Errorf("%w: slug is taken", ErrConflict)

	// ErrHandleTaken wraps ErrConflict: SetOrgSlug collided with another organization's slug
	// (organizations.slug is globally unique, unlike a booking page's per-org slug). TS:
	// HANDLE_TAKEN.
	ErrHandleTaken = fmt.Errorf("%w: handle is taken", ErrConflict)

	// ErrSlotTaken wraps ErrConflict: Task 3's createBooking found the requested slot already
	// booked. TS: SLOT_UNAVAILABLE.
	ErrSlotTaken = fmt.Errorf("%w: slot is unavailable", ErrConflict)

	// ErrPagePaused wraps ErrConflict: Task 3's createBooking was attempted against a page whose
	// status is "paused". TS: PAGE_PAUSED.
	ErrPagePaused = fmt.Errorf("%w: page is paused", ErrConflict)

	// ErrBookingPast wraps ErrConflict: Task 3's createBooking/reschedule targeted a slot that has
	// already passed. TS: BOOKING_PAST.
	ErrBookingPast = fmt.Errorf("%w: booking start is in the past", ErrConflict)

	// ErrGoogleNotConnected wraps ErrConflict: Task 5's Google Calendar sync path (google.go) was
	// invoked for a page/member with no usable Google connection.
	ErrGoogleNotConnected = fmt.Errorf("%w: google calendar is not connected", ErrConflict)

	// ErrInvalidToken is returned by Cancel/Reschedule/ManagedBooking when a manage token was
	// supplied (byOrganiser: false) and the booking it names DOES exist, but the token itself
	// doesn't match that booking's stored hash. Distinct from ErrNotFound (no such booking at all)
	// per Task 6's accumulated requirement (d): the earlier port (this file's own history, and
	// bookings.go's Cancel/Reschedule/ManagedBooking doc comments before this task) deliberately
	// collapsed a wrong token into ErrNotFound, a simplification of getBookingForManage's own
	// separate INVALID_TOKEN code (bookings.ts) — Task 6's HTTP layer needs to map "no such
	// booking" to 404 and "wrong token" to 403 differently, so this sentinel now exists to tell
	// the two apart again. TS: INVALID_TOKEN.
	ErrInvalidToken = errors.New("bookings: invalid manage token")
)

// ValidationError reports one or more field-level validation failures, ported from zod's issue
// list in src/server/bookings/schemas.ts. Fields maps a field path (dot-joined, array/object keys
// inline — e.g. "availability.1.0.end", "slotDurationMin") to a human-readable message.
//
// ErrValidation is the zero-value sentinel of this type: errors.Is(err, ErrValidation) is true for
// any *ValidationError regardless of its Fields (see Is below); errors.As(err, &verr) — or a type
// assertion — recovers the field detail.
type ValidationError struct {
	Fields map[string]string
}

func (e *ValidationError) Error() string {
	if len(e.Fields) == 0 {
		return "bookings: validation failed"
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
	return "bookings: validation failed: " + strings.Join(parts, "; ")
}

// Is implements errors.Is support: any *ValidationError matches the ErrValidation sentinel (and any
// other *ValidationError), regardless of Fields content — the sentinel is a type marker, not a
// specific instance.
func (e *ValidationError) Is(target error) bool {
	_, ok := target.(*ValidationError)
	return ok
}

// ErrValidation is the base sentinel of *ValidationError — see the type's doc comment.
var ErrValidation = &ValidationError{}

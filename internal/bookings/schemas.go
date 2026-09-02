package bookings

import (
	"fmt"
	"net/mail"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Limits ported from src/server/bookings/schemas.ts's LIMITS.
const (
	LimitHandleMin      = 3
	LimitHandleMax      = 30
	LimitSlugMin        = 3
	LimitSlugMax        = 30
	LimitTitle          = 200
	LimitDescription    = 2000
	LimitLocation       = 500
	LimitRangesPerDay   = 20
	LimitOverrideDays   = 366
	LimitSlotDurMin     = 15
	LimitSlotDurMax     = 480
	LimitBufferMin      = 0
	LimitBufferMax      = 120
	LimitMinNoticeMin   = 0
	LimitMinNoticeMax   = 10080
	LimitMaxDaysAheadLo = 1
	LimitMaxDaysAheadHi = 365
	// LimitNote/LimitName/LimitEmail port bookSlotSchema's own LIMITS.note/name and z.email().max(254)
	// (schemas.ts) — BookInput.Validate's own field caps, below.
	LimitNote  = 1000
	LimitName  = 80
	LimitEmail = 254
)

var (
	// handleSlugRegexp ports HANDLE_SLUG_RE, shared by both a booking page's own slug and an
	// organization's public handle (schemas.ts's handleSchema/slugSchema both use it).
	handleSlugRegexp = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`)
	// hhmmRegexp ports HHMM_RE: "HH:mm" on a 15-minute grid.
	hhmmRegexp = regexp.MustCompile(`^([01]\d|2[0-3]):(00|15|30|45)$`)
	// weekdayKeyRegexp ports WEEKDAY_KEY_RE: a single digit '0'..'6'.
	weekdayKeyRegexp = regexp.MustCompile(`^[0-6]$`)
	// dateKeyRegexp ports DATE_KEY_RE: a bare "YYYY-MM-DD" calendar date.
	dateKeyRegexp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// TimeRange ports schemas.ts's TimeRange: one "HH:mm"-"HH:mm" window within a day.
type TimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// Availability ports Availability: weekday key ('0'..'6', Sunday=0 as in JS's Date#getDay) to its
// list of time ranges for that day.
type Availability map[string][]TimeRange

// DateOverrides ports DateOverrides: a specific calendar date ("YYYY-MM-DD") to its own list of
// time ranges, overriding Availability's weekday default for that date. An empty slice means "no
// availability that day" (a full-day block), distinct from the key being absent entirely.
type DateOverrides map[string][]TimeRange

// PageInput ports schemas.ts's pageSchema — the merged shape createBookingPageSchema/
// updateBookingPageSchema share once the create-only "no status field" / update-only "status is
// optional" difference is set aside. Validate() enforces the same field-level rules; the caller-
// facing distinction between create and update is instead carried by the two Service methods:
// CreatePage always writes status "active" regardless of what Status holds here, UpdatePage uses
// Status (defaulting to "active" when empty). This is a deliberate simplification of the brief's
// shared PageInput type: unlike updateBookingPageSchema's zod .partial(), UpdatePage takes a FULL
// replacement value for every field (there is no PATCH-style "omitted means unchanged" here) — the
// brief's exact signature (`UpdatePage(ctx, pageID, orgID string, in PageInput)`) reuses the same
// non-partial PageInput type CreatePage does, so a caller wanting to change one field must supply
// the page's current values for the rest (typically by calling GetOwnedPage first).
type PageInput struct {
	Slug            string
	Title           string
	Description     *string
	Location        *string
	Timezone        string
	SlotDurationMin int
	BufferBeforeMin int
	BufferAfterMin  int
	MinNoticeMin    int
	MaxDaysAhead    int
	Availability    Availability
	DateOverrides   DateOverrides // nil = omitted
	GoogleSync      bool
	Reminders       bool
	// Status is only meaningful to UpdatePage ("" defaults to "active"); CreatePage ignores it and
	// always writes "active", matching createBookingPageSchema having no status field at all.
	Status string
}

// Validate ports pageSchema's shape + refinements (slug/title/description/location length and
// format, timezone validity, numeric ranges, availability/dateOverrides well-formedness, and — for
// Status — updateBookingPageSchema's `z.enum(['active', 'paused'])`).
func (in PageInput) Validate() error {
	fields := map[string]string{}

	if !handleSlugRegexp.MatchString(in.Slug) || len(in.Slug) < LimitSlugMin || len(in.Slug) > LimitSlugMax {
		fields["slug"] = "slug must be lowercase letters, digits and hyphens, 3-30 characters"
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		fields["title"] = "title is required"
	} else if len(title) > LimitTitle {
		fields["title"] = fmt.Sprintf("title must be at most %d characters", LimitTitle)
	}

	if in.Description != nil && len(strings.TrimSpace(*in.Description)) > LimitDescription {
		fields["description"] = fmt.Sprintf("description must be at most %d characters", LimitDescription)
	}
	if in.Location != nil && len(strings.TrimSpace(*in.Location)) > LimitLocation {
		fields["location"] = fmt.Sprintf("location must be at most %d characters", LimitLocation)
	}

	if msg := validateTimezone(in.Timezone); msg != "" {
		fields["timezone"] = msg
	}

	if in.SlotDurationMin < LimitSlotDurMin || in.SlotDurationMin > LimitSlotDurMax {
		fields["slotDurationMin"] = fmt.Sprintf("slotDurationMin must be between %d and %d", LimitSlotDurMin, LimitSlotDurMax)
	}
	if in.BufferBeforeMin < LimitBufferMin || in.BufferBeforeMin > LimitBufferMax {
		fields["bufferBeforeMin"] = fmt.Sprintf("bufferBeforeMin must be between %d and %d", LimitBufferMin, LimitBufferMax)
	}
	if in.BufferAfterMin < LimitBufferMin || in.BufferAfterMin > LimitBufferMax {
		fields["bufferAfterMin"] = fmt.Sprintf("bufferAfterMin must be between %d and %d", LimitBufferMin, LimitBufferMax)
	}
	if in.MinNoticeMin < LimitMinNoticeMin || in.MinNoticeMin > LimitMinNoticeMax {
		fields["minNoticeMin"] = fmt.Sprintf("minNoticeMin must be between %d and %d", LimitMinNoticeMin, LimitMinNoticeMax)
	}
	if in.MaxDaysAhead < LimitMaxDaysAheadLo || in.MaxDaysAhead > LimitMaxDaysAheadHi {
		fields["maxDaysAhead"] = fmt.Sprintf("maxDaysAhead must be between %d and %d", LimitMaxDaysAheadLo, LimitMaxDaysAheadHi)
	}

	validateDayRangesMap("availability", weekdayKeyRegexp, "Weekday keys must be '0'..'6'", in.Availability, 0, fields)
	if in.DateOverrides != nil {
		validateDayRangesMap("dateOverrides", dateKeyRegexp, "Override keys must be 'YYYY-MM-DD'", in.DateOverrides, LimitOverrideDays, fields)
	}

	if in.Status != "" && in.Status != "active" && in.Status != "paused" {
		fields["status"] = "status must be one of active, paused"
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

// validateDayRangesMap ports availabilitySchema/dateOverridesSchema: every key must match
// keyPattern, and (when maxKeys > 0) the map may have at most maxKeys entries. Each key's own
// range list is checked by validateDayRanges.
func validateDayRangesMap(field string, keyPattern *regexp.Regexp, keyMsg string, m map[string][]TimeRange, maxKeys int, fields map[string]string) {
	if maxKeys > 0 && len(m) > maxKeys {
		fields[field] = fmt.Sprintf("at most %d entries allowed", maxKeys)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !keyPattern.MatchString(k) {
			fields[field] = keyMsg
			continue
		}
		validateDayRanges(fmt.Sprintf("%s.%s", field, k), m[k], fields)
	}
}

// validateDayRanges ports dayRangesSchema: at most LimitRangesPerDay ranges, each individually
// well-formed (HH:mm on a 15-minute grid, start before end), sorted by start and non-overlapping.
func validateDayRanges(prefix string, ranges []TimeRange, fields map[string]string) {
	if len(ranges) > LimitRangesPerDay {
		fields[prefix] = fmt.Sprintf("at most %d ranges per day allowed", LimitRangesPerDay)
	}
	for i, r := range ranges {
		key := fmt.Sprintf("%s.%d", prefix, i)
		if !hhmmRegexp.MatchString(r.Start) || !hhmmRegexp.MatchString(r.End) {
			fields[key] = "must be HH:mm on a 15-minute grid"
			continue
		}
		if toMinutes(r.Start) >= toMinutes(r.End) {
			fields[key+".end"] = "start must be before end"
		}
	}
	for i := 1; i < len(ranges); i++ {
		prev, cur := ranges[i-1], ranges[i]
		key := fmt.Sprintf("%s.%d", prefix, i)
		if toMinutes(cur.Start) < toMinutes(prev.Start) {
			fields[key] = "ranges must be sorted"
			continue
		}
		if toMinutes(cur.Start) < toMinutes(prev.End) {
			fields[key] = "ranges must not overlap"
		}
	}
}

// toMinutes converts an "HH:mm" string (already regexp-validated by the caller) to minutes since
// midnight.
func toMinutes(hhmm string) int {
	var h, m int
	_, _ = fmt.Sscanf(hhmm, "%2d:%2d", &h, &m)
	return h*60 + m
}

// validateTimezone ports timezoneSchema (schemas.ts): 1-64 characters, and a real IANA zone
// time.LoadLocation recognizes. Returns "" when tz is valid, else the message to report under the
// caller's own field key — shared by PageInput.Validate (its own "timezone" field) and
// BookInput.Validate below (same rule, same field name).
func validateTimezone(tz string) string {
	if len(tz) < 1 || len(tz) > 64 {
		return "timezone must be 1-64 characters"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "invalid IANA timezone"
	}
	return ""
}

// validateEmail ports bookSlotSchema's `z.email().max(254)` (schemas.ts): a syntactically valid
// address (mail.ParseAddress, Go's closest stdlib equivalent to zod's own email check) of at most
// LimitEmail characters. mail.ParseAddress alone would also accept a display-name form
// ("Bob <bob@example.com>") that zod's z.email() rejects — the round-trip comparison against
// addr.Address below (an address with no display name always parses back to exactly the input
// string) closes that gap.
func validateEmail(email string) bool {
	if email == "" || len(email) > LimitEmail {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

// BookInput ports bookSlotSchema (schemas.ts) — see BookInput's own doc comment (bookings.go) for
// the type's fields; StartAt/Busy carry no validation of their own here (StartAt's only rule,
// "not in the past", is BOOKING_PAST, a Book-time conflict rather than a shape check — see
// bookings.go's own ErrBookingPast; Busy is this port's own internal parameter, never user input).
func (in BookInput) Validate() error {
	fields := map[string]string{}

	name := strings.TrimSpace(in.Name)
	if name == "" {
		fields["name"] = "name is required"
	} else if len(name) > LimitName {
		fields["name"] = fmt.Sprintf("name must be at most %d characters", LimitName)
	}

	if !validateEmail(in.Email) {
		fields["email"] = "must be a valid email address"
	}

	if in.Note != nil && len(strings.TrimSpace(*in.Note)) > LimitNote {
		fields["note"] = fmt.Sprintf("note must be at most %d characters", LimitNote)
	}

	if msg := validateTimezone(in.Timezone); msg != "" {
		fields["timezone"] = msg
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

// validateHandle ports handleSchema — the org-slug ("handle") counterpart of a page's own slug,
// used by SetOrgSlug. Same length bounds and character rules as a page slug, but reported under
// the "handle" field key (Task 6's accumulated requirement (b): the org-handle HTTP endpoint's
// request body names this field "handle", not "slug" — pageSchema's own "slug" field is a
// different resource entirely, so the two never look like they share a validator instance by
// accident).
func validateHandle(handle string) error {
	if !handleSlugRegexp.MatchString(handle) || len(handle) < LimitHandleMin || len(handle) > LimitHandleMax {
		return &ValidationError{Fields: map[string]string{
			"handle": "handle must be lowercase letters, digits and hyphens, 3-30 characters",
		}}
	}
	return nil
}

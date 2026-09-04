package polls

import (
	"database/sql"
	"strconv"
	"time"
)

// isoLayout formats a time.Time the same way JS's Date.prototype.toISOString() does: UTC,
// millisecond precision, literal "Z" suffix. Every timestamp this package hands back to a caller
// (createdAt, updatedAt, deadlineAt, a datetime option's startAt/endAt, ...) goes through this so
// round-tripping a value the frontend sent never drifts in precision or offset.
const isoLayout = "2006-01-02T15:04:05.000Z"

// dateOnlyLayout formats a "date" kind option's startAt as a bare calendar date (no time-of-day),
// matching optionRowFields' storage of option.date as-is in the TS source. The column itself is
// timestamptz (migrations/00003_polls.sql's re-cut), so the instant is always formatted back out
// in UTC to avoid any date shifting a non-UTC session/server timezone could otherwise introduce.
const dateOnlyLayout = "2006-01-02"

// formatISO renders t as an ISO 8601 UTC instant with millisecond precision.
func formatISO(t time.Time) string {
	return t.UTC().Format(isoLayout)
}

// formatDateOnly renders t as a bare "YYYY-MM-DD" calendar date, in UTC.
func formatDateOnly(t time.Time) string {
	return t.UTC().Format(dateOnlyLayout)
}

// parseISODateTime parses s as an ISO 8601 UTC instant (the shape isoDateTimeRegexp already
// validated it against). time.RFC3339Nano, not isoLayout, is used for parsing: isoLayout fixes
// exactly 3 fractional digits, which formatISO always produces on output, but a caller-supplied
// value may have no fractional seconds at all (isoDateTimeRegexp's own "(\.\d+)?" makes them
// optional) or a different count — RFC3339Nano's ".999999999" pattern accepts a variable number of
// fractional digits, including none.
func parseISODateTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// parseDateOnly parses s ("YYYY-MM-DD") as a UTC midnight instant, the timestamptz value stored
// for a "date" kind option.
func parseDateOnly(s string) (time.Time, error) {
	return time.ParseInLocation(dateOnlyLayout, s, time.UTC)
}

// nullTimeToISO converts a nullable DB timestamp to *string, nil when not valid.
func nullTimeToISO(t sql.NullTime) *string {
	if !t.Valid {
		return nil
	}
	s := formatISO(t.Time)
	return &s
}

// nullStringPtr converts sql.NullString to *string, nil when not valid.
func nullStringPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// nullInt32Ptr converts sql.NullInt32 to *int32, nil when not valid.
func nullInt32Ptr(n sql.NullInt32) *int32 {
	if !n.Valid {
		return nil
	}
	v := n.Int32
	return &v
}

// nullInt64ToStringPtr converts sql.NullInt64 to a stringified *string (the seam's convention:
// every id crossing the service boundary is a string, matching internal/auth's
// fmt.Sprint(user.ID)).
func nullInt64ToStringPtr(n sql.NullInt64) *string {
	if !n.Valid {
		return nil
	}
	v := strconv.FormatInt(n.Int64, 10)
	return &v
}

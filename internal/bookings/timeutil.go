package bookings

import (
	"database/sql"
	"strings"
	"time"
)

// isoLayout formats a time.Time the same way JS's Date.prototype.toISOString() does: UTC,
// millisecond precision, literal "Z" suffix — matching every createdAt/updatedAt this package
// hands back (src/server/bookings/pages.ts's own `new Date().toISOString()` calls).
const isoLayout = "2006-01-02T15:04:05.000Z"

// formatISO renders t as an ISO 8601 UTC instant with millisecond precision.
func formatISO(t time.Time) string {
	return t.UTC().Format(isoLayout)
}

// nullStringPtr converts sql.NullString to *string, nil when not valid.
func nullStringPtr(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	v := s.String
	return &v
}

// optionalTrimmedString converts a *string field (nil = omitted) to sql.NullString, trimming
// whitespace the same way zod's z.string().trim() would on the field's parsed value. A provided-
// but-empty-after-trim string is still stored (Valid: true, String: "") — only a nil field is
// stored as SQL NULL.
func optionalTrimmedString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: strings.TrimSpace(*s), Valid: true}
}

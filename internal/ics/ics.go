// Package ics is the shared iCalendar (RFC 5545) core internal/polls/ics.go and
// internal/bookings/ics.go each used to carry their own separate copy of: TEXT-value escaping,
// 75-octet line folding, the UTC/date basic date-time formats, and the CRLF-joined
// VCALENDAR...VEVENT...VCALENDAR wrapper. Both callers ported the same handful of RFC 5545 rules
// from the same TS source (src/lib/ics.ts's escapeText/foldLine/buildIcs) independently — one
// small package now backs both, so a rule only ever needs fixing in one place.
//
// What stays OUT of this package, deliberately: building a single event's own BEGIN:VEVENT..
// END:VEVENT line list is still each caller's own job (polls' buildVeventLines maps a finalized
// poll option — which can be a bare all-day date OR a timed datetime, per icsStart's own union —
// booking's BuildBookingICS maps a booking, which is always timed, never all-day). Neither
// caller's own event shape belongs in a package meant to serve both.
package ics

import (
	"strings"
	"time"
)

// LineLimit is RFC 5545's 75-octet content-line cap (CRLF excluded) that FoldLine enforces.
const LineLimit = 75

// textEscaper escapes the four characters iCalendar TEXT values must escape, backslash first (so
// later escapes' own backslashes aren't re-escaped), then semicolon, comma, and newline — ports
// ics.ts's escapeText exactly. strings.Replacer applies all pairs in a single left-to-right pass
// over the *original* string, which — since none of these four patterns can appear inside
// another's replacement — is equivalent to the TS chain of four sequential .replace() calls.
var textEscaper = strings.NewReplacer(
	`\`, `\\`,
	`;`, `\;`,
	`,`, `\,`,
	"\n", `\n`,
)

// EscapeText escapes s for use as an iCalendar TEXT value (SUMMARY, DESCRIPTION, LOCATION, a UID,
// ...).
func EscapeText(s string) string {
	return textEscaper.Replace(s)
}

// FoldLine ports ics.ts's foldLine: a content line longer than LineLimit octets is split into
// that first line plus continuation lines, each joined by "\r\n " (a CRLF followed by a single
// leading space, which iCalendar readers know to strip and rejoin) — never splitting a multi-byte
// UTF-8 rune across two lines. The first continuation's own octet budget is one less than every
// other line's, since the leading space that will precede it on rejoining still counts against
// the 75-octet cap for that physical line.
func FoldLine(line string) string {
	if len(line) <= LineLimit {
		return line
	}

	var chunks []string
	var current strings.Builder
	currentBytes := 0

	for _, r := range line {
		char := string(r)
		charBytes := len(char)
		limit := LineLimit
		if len(chunks) > 0 {
			limit = LineLimit - 1
		}
		if currentBytes+charBytes > limit {
			chunks = append(chunks, current.String())
			current.Reset()
			currentBytes = 0
		}
		current.WriteString(char)
		currentBytes += charBytes
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}

	return strings.Join(chunks, "\r\n ")
}

// FormatUTCBasic renders t in UTC as iCalendar's basic date-time form, e.g. "20260901T163000Z" —
// for a timed DTSTART/DTEND/DTSTAMP.
func FormatUTCBasic(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// FormatDateBasic renders t's (UTC) calendar date in iCalendar's basic date form, e.g. "20260901"
// — for an all-day event's own DTSTART;VALUE=DATE.
func FormatDateBasic(t time.Time) string {
	return t.UTC().Format("20060102")
}

// NextDayBasic renders the day after t's (UTC) calendar date, in the same basic date form — an
// all-day event's DTEND is exclusive per RFC 5545, so a one-day event's own DTEND is the
// following date.
func NextDayBasic(t time.Time) string {
	return t.UTC().AddDate(0, 0, 1).Format("20060102")
}

// BuildCalendar wraps veventLines (one caller-built event's own, unfolded BEGIN:VEVENT..
// END:VEVENT lines — buildVeventLines in polls/ics.go, or BuildBookingICS in bookings/ics.go) in
// one VCALENDAR, folding every line (header, VEVENT, and footer alike) to LineLimit and joining
// with CRLF, with a trailing CRLF — ports ics.ts's buildIcs.
func BuildCalendar(veventLines []string) []byte {
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//whenweall//EN",
		"CALSCALE:GREGORIAN",
		"METHOD:PUBLISH",
	}
	lines = append(lines, veventLines...)
	lines = append(lines, "END:VCALENDAR")

	folded := make([]string, len(lines))
	for i, l := range lines {
		folded[i] = FoldLine(l)
	}
	return []byte(strings.Join(folded, "\r\n") + "\r\n")
}

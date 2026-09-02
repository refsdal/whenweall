// Package bookings (this file, ics.go) builds one .ics VEVENT for a booking's confirmed slot —
// ports src/server/bookings/emails.ts's bookingIcs, itself a thin wrapper around src/lib/ics.ts's
// buildIcs for a plain timed event: a booking always has both a start and an end (unlike a poll
// option, which can be a bare date with no time), so there is no all-day branch to carry over here.
//
// internal/polls/ics.go already has this exact VCALENDAR/VEVENT/fold/escape machinery
// (buildIcsCalendar, foldLine, escapeText, ...), but keeps it unexported — nothing in that package
// is shared out today. Rather than exporting polls' internals or introducing a new shared
// internal/mailer/icsutil package for one small caller, this file duplicates the handful of RFC
// 5545 lines it needs (escaping, 75-octet line folding, the UTC basic date-time format). If either
// copy's rules ever need to change, internal/polls/ics.go is the sibling to update alongside it.
package bookings

import (
	"strings"
	"time"
)

// icsLineLimit is RFC 5545's 75-octet line-length cap (CRLF excluded) that icsFoldLine enforces.
const icsLineLimit = 75

// icsEscaper escapes the four characters iCalendar TEXT values must escape, backslash first so
// later escapes' own backslashes aren't re-escaped — mirrors internal/polls/ics.go's icsEscaper
// (itself a port of ics.ts's escapeText).
var icsEscaper = strings.NewReplacer(
	`\`, `\\`,
	`;`, `\;`,
	`,`, `\,`,
	"\n", `\n`,
)

func icsEscapeText(s string) string {
	return icsEscaper.Replace(s)
}

// icsFormatUTCBasic renders t in UTC as iCalendar's basic date-time form, e.g. "20260901T163000Z".
func icsFormatUTCBasic(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// icsFoldLine ports ics.ts's foldLine — see internal/polls/ics.go's copy for the full rationale.
func icsFoldLine(line string) string {
	if len(line) <= icsLineLimit {
		return line
	}

	var chunks []string
	var current strings.Builder
	currentBytes := 0

	for _, r := range line {
		char := string(r)
		charBytes := len(char)
		limit := icsLineLimit
		if len(chunks) > 0 {
			limit = icsLineLimit - 1
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

// BuildBookingICS builds the .ics file for one booking: a single VEVENT running [startAt, endAt),
// UID "<bookingID>@whenweall" (matching bookingIcs's own uid scheme, emails.ts), with its URL
// property set to bookingURL (the caller's absolute "{AppURL}/booking/{id}" link).
func BuildBookingICS(bookingID, title, description, location string, startAt, endAt time.Time, bookingURL string) []byte {
	lines := []string{
		"BEGIN:VEVENT",
		"UID:" + icsEscapeText(bookingID+"@whenweall"),
		"DTSTAMP:" + icsFormatUTCBasic(time.Now()),
		"DTSTART:" + icsFormatUTCBasic(startAt),
		"DTEND:" + icsFormatUTCBasic(endAt),
		"SUMMARY:" + icsEscapeText(title),
	}
	if description != "" {
		lines = append(lines, "DESCRIPTION:"+icsEscapeText(description))
	}
	if location != "" {
		lines = append(lines, "LOCATION:"+icsEscapeText(location))
	}
	lines = append(lines, "URL:"+icsEscapeText(bookingURL), "END:VEVENT")

	cal := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//whenweall//EN",
		"CALSCALE:GREGORIAN",
		"METHOD:PUBLISH",
	}
	cal = append(cal, lines...)
	cal = append(cal, "END:VCALENDAR")

	folded := make([]string, len(cal))
	for i, l := range cal {
		folded[i] = icsFoldLine(l)
	}
	return []byte(strings.Join(folded, "\r\n") + "\r\n")
}

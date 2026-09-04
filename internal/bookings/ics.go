// Package bookings (this file, ics.go) builds one .ics VEVENT for a booking's confirmed slot —
// ports src/server/bookings/emails.ts's bookingIcs, itself a thin wrapper around src/lib/ics.ts's
// buildIcs for a plain timed event: a booking always has both a start and an end (unlike a poll
// option, which can be a bare date with no time), so there is no all-day branch to carry over here.
//
// The RFC 5545 mechanics themselves (escaping, line folding, the UTC basic date-time format, and
// the VCALENDAR wrapper) live in internal/ics — triage 3's own dedup: this file and
// internal/polls/ics.go each used to carry an independent copy of the exact same rules, ported
// from the same TS source (src/lib/ics.ts). This file's own job is now just building this one
// timed event's own BEGIN:VEVENT..END:VEVENT lines and handing them to that shared core.
package bookings

import (
	"time"

	"github.com/refsdal/whenweall/internal/ics"
)

// BuildBookingICS builds the .ics file for one booking: a single VEVENT running [startAt, endAt),
// UID "<bookingID>@whenweall" (matching bookingIcs's own uid scheme, emails.ts), with its URL
// property set to bookingURL (the caller's absolute "{AppURL}/booking/{id}" link).
func BuildBookingICS(bookingID, title, description, location string, startAt, endAt time.Time, bookingURL string) []byte {
	lines := []string{
		"BEGIN:VEVENT",
		"UID:" + ics.EscapeText(bookingID+"@whenweall"),
		"DTSTAMP:" + ics.FormatUTCBasic(time.Now()),
		"DTSTART:" + ics.FormatUTCBasic(startAt),
		"DTEND:" + ics.FormatUTCBasic(endAt),
		"SUMMARY:" + ics.EscapeText(title),
	}
	if description != "" {
		lines = append(lines, "DESCRIPTION:"+ics.EscapeText(description))
	}
	if location != "" {
		lines = append(lines, "LOCATION:"+ics.EscapeText(location))
	}
	lines = append(lines, "URL:"+ics.EscapeText(bookingURL), "END:VEVENT")

	return ics.BuildCalendar(lines)
}

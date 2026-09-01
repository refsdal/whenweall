package polls

// Ports src/server/polls/ics.ts (mapping a poll's finalized option to a calendar event) fused with
// src/lib/ics.ts's buildIcs (the actual VCALENDAR/VEVENT string builder) — Go has no shared
// frontend/worker "lib" package to split the two across, so both live in this one file.
//
// Deviation from the brief's literal signature: BuildPollICS takes an extra pollURL parameter
// (the caller's already-computed absolute URL, e.g. `${APP_URL}/p/{id}`) that the brief's pinned
// (ctx, q, pollID) didn't include. TS's buildOptionIcs/buildPollIcs build that same absolute URL
// from env.APP_URL (a Workers binding) that BuildPollICS itself has no access to — the caller
// already has it (see timers.go's sendFinalizedMail, which already computed pollURL for its own
// mail body), so it's threaded straight through rather than reconstructed.

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/polls/queries"
)

// icsLineLimit is RFC 5545's 75-octet line-length cap (CRLF excluded) that foldLine enforces.
const icsLineLimit = 75

// icsStart mirrors ics.ts's IcsStart union: either an all-day date, or a timed start with an
// optional explicit end.
type icsStart struct {
	isDate      bool
	date        time.Time // valid when isDate; a UTC midnight instant (see parseDateOnly)
	dateTime    time.Time // valid when !isDate
	endDateTime time.Time
	hasEnd      bool
}

// icsStartFromOption ports icsStartFromOption (ics.ts): maps a poll option row to an icsStart, or
// (icsStart{}, false) for a plain-text option (no calendar meaning) or a date/datetime option
// missing its start.
func icsStartFromOption(o queries.PollOption) (icsStart, bool) {
	switch OptionKind(o.Kind) {
	case OptionKindText:
		return icsStart{}, false
	case OptionKindDate:
		if !o.StartAt.Valid {
			return icsStart{}, false
		}
		return icsStart{isDate: true, date: o.StartAt.Time}, true
	default: // datetime
		if !o.StartAt.Valid {
			return icsStart{}, false
		}
		s := icsStart{dateTime: o.StartAt.Time}
		if o.EndAt.Valid {
			s.hasEnd = true
			s.endDateTime = o.EndAt.Time
		}
		return s, true
	}
}

// icsEvent mirrors ics.ts's IcsEvent.
type icsEvent struct {
	uid         string
	title       string
	description string
	location    string
	url         string
	start       icsStart
}

// formatUTCBasic renders t in UTC as iCalendar's basic date-time form, e.g. "20260901T163000Z".
func formatUTCBasic(t time.Time) string {
	return t.UTC().Format("20060102T150405Z")
}

// formatDateBasic renders an all-day date's start as iCalendar's basic date form, e.g. "20260901".
func formatDateBasic(t time.Time) string {
	return t.UTC().Format("20060102")
}

// nextDayBasic renders the day after t's (UTC) date in basic date form — DTEND for an all-day
// event is exclusive per RFC 5545, so a one-day event's DTEND is the following date.
func nextDayBasic(t time.Time) string {
	return t.UTC().AddDate(0, 0, 1).Format("20060102")
}

// icsEscaper escapes the four characters iCalendar TEXT values must escape, in the same order as
// ics.ts's escapeText: backslash first (so later escapes' own backslashes aren't re-escaped),
// then semicolon, comma, and newline. strings.Replacer applies all pairs in a single left-to-right
// pass over the *original* string, which — since none of these four patterns can appear inside
// another's replacement — is equivalent to the TS chain of four sequential .replace() calls.
var icsEscaper = strings.NewReplacer(
	`\`, `\\`,
	`;`, `\;`,
	`,`, `\,`,
	"\n", `\n`,
)

func escapeText(s string) string {
	return icsEscaper.Replace(s)
}

// foldLine ports ics.ts's foldLine: RFC 5545 caps a content line at 75 octets (CRLF excluded);
// a longer line is split into that line plus continuation lines, each joined by "\r\n " (a CRLF
// followed by a single leading space, which iCalendar readers know to strip and rejoin). The
// first continuation's octet budget is one less than the first line's, since the leading space
// that will precede it on rejoining still counts against the 75-octet cap for that physical line.
func foldLine(line string) string {
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

// buildVeventLines ports buildVevent (ics.ts): the BEGIN:VEVENT..END:VEVENT lines for one event,
// unfolded (foldLine is applied once, package-wide, over every line in buildIcsCalendar).
func buildVeventLines(e icsEvent, now time.Time) []string {
	var dtstart, dtend string
	if e.start.isDate {
		dtstart = "DTSTART;VALUE=DATE:" + formatDateBasic(e.start.date)
		dtend = "DTEND;VALUE=DATE:" + nextDayBasic(e.start.date)
	} else {
		end := e.start.endDateTime
		if !e.start.hasEnd {
			end = e.start.dateTime.Add(time.Hour)
		}
		dtstart = "DTSTART:" + formatUTCBasic(e.start.dateTime)
		dtend = "DTEND:" + formatUTCBasic(end)
	}

	lines := []string{
		"BEGIN:VEVENT",
		"UID:" + escapeText(e.uid),
		"DTSTAMP:" + formatUTCBasic(now),
		dtstart,
		dtend,
		"SUMMARY:" + escapeText(e.title),
	}
	if e.description != "" {
		lines = append(lines, "DESCRIPTION:"+escapeText(e.description))
	}
	if e.location != "" {
		lines = append(lines, "LOCATION:"+escapeText(e.location))
	}
	lines = append(lines, "URL:"+escapeText(e.url), "END:VEVENT")
	return lines
}

// buildIcsCalendar ports buildIcs (ics.ts): one VCALENDAR wrapping a single VEVENT, CRLF line
// endings throughout (each line folded to RFC 5545's 75-octet cap), with a trailing CRLF.
func buildIcsCalendar(e icsEvent, now time.Time) string {
	lines := []string{
		"BEGIN:VCALENDAR",
		"VERSION:2.0",
		"PRODID:-//whenweall//EN",
		"CALSCALE:GREGORIAN",
		"METHOD:PUBLISH",
	}
	lines = append(lines, buildVeventLines(e, now)...)
	lines = append(lines, "END:VCALENDAR")

	folded := make([]string, len(lines))
	for i, l := range lines {
		folded[i] = foldLine(l)
	}
	return strings.Join(folded, "\r\n") + "\r\n"
}

// BuildPollICS builds the .ics file for pollID's finalized option, with the VEVENT's URL property
// set to pollURL (the caller's absolute poll link, e.g. "https://whenweall.example/p/{id}" —
// ports TS's `${env.APP_URL}/p/${poll.id}`). Returns ("", nil, nil) — not an error — when the
// poll is missing/soft-deleted, isn't finalized, its finalized option is gone, or the finalized
// option is a plain-text option with no calendar meaning; ports buildPollIcs's own "return null"
// cases (ics.ts) plus the filename the caller attaches it under.
func BuildPollICS(ctx context.Context, q *queries.Queries, pollID, pollURL string) (filename string, ics []byte, err error) {
	poll, err := q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if poll.Status != pollFinalizedStatus || !poll.FinalizedOptionID.Valid {
		return "", nil, nil
	}

	options, err := q.ListOptionsByPoll(ctx, poll.ID)
	if err != nil {
		return "", nil, err
	}
	var option *queries.PollOption
	for i := range options {
		if options[i].ID == poll.FinalizedOptionID.String {
			option = &options[i]
			break
		}
	}
	if option == nil {
		return "", nil, nil
	}

	start, ok := icsStartFromOption(*option)
	if !ok {
		return "", nil, nil
	}

	event := icsEvent{
		uid:   poll.ID + "@whenweall",
		title: poll.Title,
		url:   pollURL,
		start: start,
	}
	if poll.Description.Valid {
		event.description = poll.Description.String
	}
	if poll.Location.Valid {
		event.location = poll.Location.String
	}

	body := buildIcsCalendar(event, time.Now())
	return "calendar.ics", []byte(body), nil
}

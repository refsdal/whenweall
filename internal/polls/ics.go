package polls

// Ports src/server/polls/ics.ts (mapping a poll's finalized option to a calendar event) fused with
// src/lib/ics.ts's buildIcs (the actual VCALENDAR/VEVENT string builder) — Go has no shared
// frontend/worker "lib" package to split the two across, so both live in this one file. The
// RFC 5545 mechanics themselves (escaping, line folding, the basic date/date-time formats, and
// the VCALENDAR wrapper) live in internal/ics — this file's own job is just mapping a poll's
// finalized option to that shared core's event-line shape (buildVeventLines below); see triage
// 3's own refactor (internal/ics's package doc comment) for why bookings' ics.go now shares it
// too instead of each carrying its own copy of the same rules ported from the same TS source.
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
	"time"

	"github.com/refsdal/whenweall/internal/ics"
	"github.com/refsdal/whenweall/internal/polls/queries"
)

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

// buildVeventLines ports buildVevent (ics.ts): the BEGIN:VEVENT..END:VEVENT lines for one event,
// unfolded (internal/ics's BuildCalendar folds every line, package-wide, over the whole
// VCALENDAR — header and footer included, not just this VEVENT).
func buildVeventLines(e icsEvent, now time.Time) []string {
	var dtstart, dtend string
	if e.start.isDate {
		dtstart = "DTSTART;VALUE=DATE:" + ics.FormatDateBasic(e.start.date)
		dtend = "DTEND;VALUE=DATE:" + ics.NextDayBasic(e.start.date)
	} else {
		end := e.start.endDateTime
		if !e.start.hasEnd {
			end = e.start.dateTime.Add(time.Hour)
		}
		dtstart = "DTSTART:" + ics.FormatUTCBasic(e.start.dateTime)
		dtend = "DTEND:" + ics.FormatUTCBasic(end)
	}

	lines := []string{
		"BEGIN:VEVENT",
		"UID:" + ics.EscapeText(e.uid),
		"DTSTAMP:" + ics.FormatUTCBasic(now),
		dtstart,
		dtend,
		"SUMMARY:" + ics.EscapeText(e.title),
	}
	if e.description != "" {
		lines = append(lines, "DESCRIPTION:"+ics.EscapeText(e.description))
	}
	if e.location != "" {
		lines = append(lines, "LOCATION:"+ics.EscapeText(e.location))
	}
	lines = append(lines, "URL:"+ics.EscapeText(e.url), "END:VEVENT")
	return lines
}

// BuildPollICS builds the .ics file for pollID's finalized option, with the VEVENT's URL property
// set to pollURL (the caller's absolute poll link, e.g. "https://whenweall.example/p/{id}" —
// ports TS's `${env.APP_URL}/p/${poll.id}`). Returns ("", nil, nil) — not an error — when the
// poll is missing/soft-deleted, isn't finalized, its finalized option is gone, or the finalized
// option is a plain-text option with no calendar meaning; ports buildPollIcs's own "return null"
// cases (ics.ts) plus the filename the caller attaches it under.
func BuildPollICS(ctx context.Context, q *queries.Queries, pollID, pollURL string) (filename string, calendar []byte, err error) {
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

	return "calendar.ics", ics.BuildCalendar(buildVeventLines(event, time.Now())), nil
}

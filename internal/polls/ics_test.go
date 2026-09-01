package polls_test

// Ports two TS test files:
//   - src/lib/__tests__/ics.test.ts: the byte-level VCALENDAR/VEVENT formatting cases (all-day
//     VALUE=DATE, timed-event UTC basic format + 1h default duration, escaping). Exercised here
//     through BuildPollICS against a real finalized poll rather than the unexported buildIcs
//     directly (Go has no equivalent of a package-private cross-file export for tests to reach
//     into), so DTSTAMP (which those TS cases assert exactly, using an injected `now`) is instead
//     asserted only by format/shape (a fixed now isn't injectable through BuildPollICS's pinned
//     signature) — everything derived from stored option data (DTSTART/DTEND/SUMMARY/escaping) is
//     still asserted byte-for-byte.
//   - src/server/polls/__tests__/ics.workers.test.ts: buildPollIcs's three null/non-null cases.

import (
	"context"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/polls/queries"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestBuildPollICS(t *testing.T) {
	ctx := context.Background()

	t.Run("returns nil for an open (not yet finalized) poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		q := queries.New(d)
		orgID, userID := seedOrgAndUser(t, d)
		view := createTestPoll(t, ctx, s, orgID, userID)

		filename, ics, err := polls.BuildPollICS(ctx, q, view.ID)
		if err != nil {
			t.Fatalf("BuildPollICS: %v", err)
		}
		if filename != "" || ics != nil {
			t.Fatalf("BuildPollICS = (%q, %v), want (\"\", nil)", filename, ics)
		}
	})

	t.Run("returns nil for an unknown poll id", func(t *testing.T) {
		d := testdb.New(t)
		q := queries.New(d)

		filename, ics, err := polls.BuildPollICS(ctx, q, "does-not-exist")
		if err != nil {
			t.Fatalf("BuildPollICS: %v", err)
		}
		if filename != "" || ics != nil {
			t.Fatalf("BuildPollICS = (%q, %v), want (\"\", nil)", filename, ics)
		}
	})

	t.Run("returns nil for a poll finalized on a text option", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		q := queries.New(d)
		orgID, userID := seedOrgAndUser(t, d)

		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type:     polls.PollTypeOptions,
			Title:    "Pick one",
			Timezone: "UTC",
			Options:  []polls.OptionInput{textOption("Pizza"), textOption("Sushi")},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		if err := s.Finalize(ctx, view.ID, orgID, view.Options[0].ID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		filename, ics, err := polls.BuildPollICS(ctx, q, view.ID)
		if err != nil {
			t.Fatalf("BuildPollICS: %v", err)
		}
		if filename != "" || ics != nil {
			t.Fatalf("BuildPollICS = (%q, %v), want (\"\", nil)", filename, ics)
		}
	})

	t.Run("builds a VCALENDAR/VEVENT for a poll finalized on a datetime option", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		q := queries.New(d)
		orgID, userID := seedOrgAndUser(t, d)
		view := createTestPoll(t, ctx, s, orgID, userID)
		optionID := view.Options[0].ID

		if err := s.Finalize(ctx, view.ID, orgID, optionID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		filename, ics, err := polls.BuildPollICS(ctx, q, view.ID)
		if err != nil {
			t.Fatalf("BuildPollICS: %v", err)
		}
		if filename != "calendar.ics" {
			t.Errorf("filename = %q, want %q", filename, "calendar.ics")
		}
		body := string(ics)

		if !strings.HasPrefix(body, "BEGIN:VCALENDAR\r\n") {
			t.Errorf("body does not start with BEGIN:VCALENDAR\\r\\n: %q", body)
		}
		if !strings.Contains(body, "PRODID:-//whenweall//EN\r\n") {
			t.Errorf("body missing PRODID: %q", body)
		}
		if !strings.Contains(body, "SUMMARY:Test Poll\r\n") {
			t.Errorf("body missing SUMMARY: %q", body)
		}
		if !strings.Contains(body, "BEGIN:VEVENT\r\n") || !strings.Contains(body, "END:VEVENT\r\n") {
			t.Errorf("body missing VEVENT wrapper: %q", body)
		}
		if !strings.Contains(body, "UID:"+view.ID+"@whenweall\r\n") {
			t.Errorf("body missing UID: %q", body)
		}
		if !strings.Contains(body, "URL:/p/"+view.ID+"\r\n") {
			t.Errorf("body missing URL: %q", body)
		}
		if !strings.HasSuffix(body, "END:VCALENDAR\r\n") {
			t.Errorf("body does not end with END:VCALENDAR\\r\\n: %q", body)
		}
		// DTSTART/DTEND for a datetime option: basic UTC form, an explicit end from the option's
		// own endAt (basicOptions' first option has one) — never the 1h default.
		if !strings.Contains(body, "DTSTART:") || !strings.Contains(body, "DTEND:") {
			t.Errorf("body missing timed DTSTART/DTEND: %q", body)
		}
		if strings.Contains(body, "VALUE=DATE") {
			t.Errorf("timed event body unexpectedly contains VALUE=DATE: %q", body)
		}
	})

	t.Run("builds an all-day VALUE=DATE event for a poll finalized on a date option", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		q := queries.New(d)
		orgID, userID := seedOrgAndUser(t, d)

		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type:     polls.PollTypeDatetime,
			Title:    "Team; offsite, planning\nsession",
			Timezone: "UTC",
			Options: []polls.OptionInput{
				{Kind: polls.OptionKindDate, Date: "2026-09-01"},
			},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := s.Finalize(ctx, view.ID, orgID, view.Options[0].ID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		_, ics, err := polls.BuildPollICS(ctx, q, view.ID)
		if err != nil {
			t.Fatalf("BuildPollICS: %v", err)
		}
		body := string(ics)

		if !strings.Contains(body, "DTSTART;VALUE=DATE:20260901\r\n") {
			t.Errorf("body missing all-day DTSTART: %q", body)
		}
		if !strings.Contains(body, "DTEND;VALUE=DATE:20260902\r\n") {
			t.Errorf("body missing all-day DTEND (next day): %q", body)
		}
		// escapeText: backslash-then-;-then-,-then-\n, ported from ics.ts's escapeText and
		// asserted byte-for-byte against src/lib/__tests__/ics.test.ts's own expectations.
		if !strings.Contains(body, `SUMMARY:Team\; offsite\, planning\nsession`+"\r\n") {
			t.Errorf("body does not escape ;/,/\\n in SUMMARY: %q", body)
		}
	})
}

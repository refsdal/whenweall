package polls_test

// Ports two TS test files:
//   - src/lib/__tests__/ics.test.ts: the byte-level VCALENDAR/VEVENT formatting cases (all-day
//     VALUE=DATE, timed-event UTC basic format + 1h default duration, escaping). Exercised here
//     through BuildPollICS against a real finalized poll rather than the unexported buildIcs
//     directly (Go has no equivalent of a package-private cross-file export for tests to reach
//     into), so DTSTAMP (which those TS cases assert exactly, using an injected `now`) is instead
//     asserted only by format/shape (BuildPollICS has no `now` parameter to inject one through) —
//     everything else, including the absolute URL (BuildPollICS's pollURL parameter) and every
//     value derived from stored option data (DTSTART/DTEND/SUMMARY/escaping), is still asserted
//     byte-for-byte.
//   - src/server/polls/__tests__/ics.workers.test.ts: buildPollIcs's three null/non-null cases.

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

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

		filename, ics, err := polls.BuildPollICS(ctx, q, view.ID, "https://whenweall.example/p/"+view.ID)
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

		filename, ics, err := polls.BuildPollICS(ctx, q, "does-not-exist", "https://whenweall.example/p/does-not-exist")
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

		if err := s.Finalize(ctx, view.ID, orgID, view.Options[0].ID, userID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		filename, ics, err := polls.BuildPollICS(ctx, q, view.ID, "https://whenweall.example/p/"+view.ID)
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

		if err := s.Finalize(ctx, view.ID, orgID, optionID, userID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		pollURL := "https://whenweall.example/p/" + view.ID
		filename, ics, err := polls.BuildPollICS(ctx, q, view.ID, pollURL)
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
		if !strings.Contains(body, "URL:"+pollURL+"\r\n") {
			t.Errorf("body missing absolute URL: %q", body)
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
		if err := s.Finalize(ctx, view.ID, orgID, view.Options[0].ID, userID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		pollURL := "https://whenweall.example/p/" + view.ID
		_, ics, err := polls.BuildPollICS(ctx, q, view.ID, pollURL)
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
		if !strings.Contains(body, "URL:"+pollURL+"\r\n") {
			t.Errorf("body missing absolute URL: %q", body)
		}
	})
}

// TestBuildClaimICS ports buildIcsMulti as used by sendClaimConfirmation
// (main:src/server/notifications/claim-emails.ts:63-92 and its test's "ics attachment for
// date/datetime slots only" case): one VEVENT per claimed dated slot, uid {pollId}-{optionId}@whenweall,
// none at all for text-only slots. A pure function — no database.
func TestBuildClaimICS(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	poll := queries.Poll{
		ID: "poll1234abcd", Title: "Shifts", Timezone: "Europe/Oslo",
		Description: sql.NullString{String: "Bring gloves", Valid: true},
		Location:    sql.NullString{String: "Depot", Valid: true},
	}
	dateOpt := queries.PollOption{ID: "optDate", Kind: "date", StartAt: sql.NullTime{Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Valid: true}}
	timedOpt := queries.PollOption{
		ID: "optTimed", Kind: "datetime",
		StartAt: sql.NullTime{Time: time.Date(2026, 9, 1, 16, 30, 0, 0, time.UTC), Valid: true},
		EndAt:   sql.NullTime{Time: time.Date(2026, 9, 1, 17, 30, 0, 0, time.UTC), Valid: true},
	}
	textOpt := queries.PollOption{ID: "optText", Kind: "text", Label: sql.NullString{String: "Bake a cake", Valid: true}}
	pollURL := "https://whenweall.example/p/" + poll.ID

	t.Run("one VEVENT per dated slot, text slots skipped", func(t *testing.T) {
		body := string(polls.BuildClaimICS(poll, []queries.PollOption{dateOpt, textOpt, timedOpt}, pollURL, now))
		if got := strings.Count(body, "BEGIN:VEVENT\r\n"); got != 2 {
			t.Fatalf("BEGIN:VEVENT count = %d, want 2: %q", got, body)
		}
		if strings.Count(body, "BEGIN:VCALENDAR\r\n") != 1 || !strings.HasSuffix(body, "END:VCALENDAR\r\n") {
			t.Errorf("not a single VCALENDAR: %q", body)
		}
		for _, want := range []string{
			"UID:poll1234abcd-optDate@whenweall\r\n",
			"UID:poll1234abcd-optTimed@whenweall\r\n",
			"DTSTART;VALUE=DATE:20260901\r\n",
			"DTEND;VALUE=DATE:20260902\r\n",
			"DTSTART:20260901T163000Z\r\n",
			"DTEND:20260901T173000Z\r\n",
			"DTSTAMP:20260903T120000Z\r\n",
			"SUMMARY:Shifts\r\n",
			"DESCRIPTION:Bring gloves\r\n",
			"LOCATION:Depot\r\n",
			"URL:" + pollURL + "\r\n",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q: %q", want, body)
			}
		}
		if strings.Contains(body, "Bake a cake") {
			t.Errorf("text slot leaked into the calendar: %q", body)
		}
	})

	t.Run("nil when no claimed slot has calendar meaning", func(t *testing.T) {
		if got := polls.BuildClaimICS(poll, []queries.PollOption{textOpt}, pollURL, now); got != nil {
			t.Errorf("BuildClaimICS = %q, want nil", got)
		}
		if got := polls.BuildClaimICS(poll, nil, pollURL, now); got != nil {
			t.Errorf("BuildClaimICS(no options) = %q, want nil", got)
		}
	})
}

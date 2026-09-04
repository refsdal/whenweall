package ics_test

// Direct unit coverage for internal/ics's own exported surface — triage 3's shared core, factored
// out of internal/polls/ics.go and internal/bookings/ics.go (each of which keeps its own golden
// test exercising this package indirectly, through BuildPollICS/BuildBookingICS; those stay green
// unchanged, proving the refactor didn't change either caller's own output byte-for-byte). This
// file exercises the shared functions in isolation instead of only ever through one caller.

import (
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/ics"
)

func TestEscapeText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{`back\slash`, `back\\slash`},
		{"a;b", `a\;b`},
		{"a,b", `a\,b`},
		{"a\nb", `a\nb`},
		// Backslash first: a literal backslash introduced by escaping ';'/','/'\n' must never
		// itself be re-escaped.
		{"a;b\nc", `a\;b\nc`},
	}
	for _, c := range cases {
		if got := ics.EscapeText(c.in); got != c.want {
			t.Errorf("EscapeText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFoldLine(t *testing.T) {
	t.Run("a line at or under the limit is unchanged", func(t *testing.T) {
		line := "SUMMARY:" + strings.Repeat("a", ics.LineLimit-len("SUMMARY:"))
		if len(line) != ics.LineLimit {
			t.Fatalf("test setup: len(line) = %d, want %d", len(line), ics.LineLimit)
		}
		if got := ics.FoldLine(line); got != line {
			t.Errorf("FoldLine(exactly-at-limit) = %q, want unchanged", got)
		}
	})

	t.Run("a longer line is folded into continuation lines joined by CRLF+space", func(t *testing.T) {
		line := "SUMMARY:" + strings.Repeat("a", 100)
		folded := ics.FoldLine(line)
		parts := strings.Split(folded, "\r\n ")
		if len(parts) < 2 {
			t.Fatalf("FoldLine did not split a 108-byte line: %q", folded)
		}
		for i, p := range parts {
			if len(p) > ics.LineLimit {
				t.Errorf("part %d has length %d, want <= %d: %q", i, len(p), ics.LineLimit, p)
			}
		}
		// Rejoining (stripping "\r\n " exactly as an iCalendar reader would) reproduces the
		// original line.
		if rejoined := strings.Join(parts, ""); rejoined != line {
			t.Errorf("rejoined = %q, want %q", rejoined, line)
		}
	})

	t.Run("never splits a multi-byte rune across two folded lines", func(t *testing.T) {
		// "é" is 2 bytes in UTF-8 — pad the line so a naive byte-count fold would land mid-rune.
		line := "SUMMARY:" + strings.Repeat("a", ics.LineLimit-9) + "éé"
		folded := ics.FoldLine(line)
		for _, part := range strings.Split(folded, "\r\n ") {
			if !isValidUTF8Suffix(part) {
				t.Errorf("part is not valid UTF-8 on its own: %q", part)
			}
		}
	})
}

func isValidUTF8Suffix(s string) bool {
	return strings.ToValidUTF8(s, "�") == s
}

func TestFormatFunctions(t *testing.T) {
	tm := time.Date(2026, 9, 1, 16, 30, 0, 0, time.UTC)

	if got, want := ics.FormatUTCBasic(tm), "20260901T163000Z"; got != want {
		t.Errorf("FormatUTCBasic = %q, want %q", got, want)
	}
	if got, want := ics.FormatDateBasic(tm), "20260901"; got != want {
		t.Errorf("FormatDateBasic = %q, want %q", got, want)
	}
	if got, want := ics.NextDayBasic(tm), "20260902"; got != want {
		t.Errorf("NextDayBasic = %q, want %q", got, want)
	}

	t.Run("FormatUTCBasic converts a non-UTC time to UTC first", func(t *testing.T) {
		loc, err := time.LoadLocation("Europe/Oslo")
		if err != nil {
			t.Fatalf("LoadLocation: %v", err)
		}
		// 2026-09-01 18:30 CEST (+2) == 2026-09-01 16:30 UTC.
		oslo := time.Date(2026, 9, 1, 18, 30, 0, 0, loc)
		if got, want := ics.FormatUTCBasic(oslo), "20260901T163000Z"; got != want {
			t.Errorf("FormatUTCBasic(Oslo) = %q, want %q", got, want)
		}
	})
}

func TestBuildCalendar(t *testing.T) {
	vevent := []string{
		"BEGIN:VEVENT",
		"UID:test-uid@whenweall",
		"SUMMARY:Test",
		"END:VEVENT",
	}
	cal := string(ics.BuildCalendar(vevent))

	if !strings.HasPrefix(cal, "BEGIN:VCALENDAR\r\n") {
		t.Errorf("does not start with BEGIN:VCALENDAR\\r\\n: %q", cal)
	}
	if !strings.HasSuffix(cal, "END:VCALENDAR\r\n") {
		t.Errorf("does not end with END:VCALENDAR\\r\\n: %q", cal)
	}
	for _, want := range []string{
		"VERSION:2.0\r\n", "PRODID:-//whenweall//EN\r\n", "CALSCALE:GREGORIAN\r\n", "METHOD:PUBLISH\r\n",
		"BEGIN:VEVENT\r\n", "UID:test-uid@whenweall\r\n", "SUMMARY:Test\r\n", "END:VEVENT\r\n",
	} {
		if !strings.Contains(cal, want) {
			t.Errorf("missing %q in: %q", want, cal)
		}
	}

	t.Run("folds an overlong line anywhere in the calendar, not just inside the VEVENT", func(t *testing.T) {
		longLine := "SUMMARY:" + strings.Repeat("x", 100)
		cal := string(ics.BuildCalendar([]string{"BEGIN:VEVENT", longLine, "END:VEVENT"}))
		if strings.Contains(cal, longLine) {
			t.Errorf("overlong line was not folded: %q", cal)
		}
		if !strings.Contains(cal, "\r\n ") {
			t.Errorf("no fold continuation found: %q", cal)
		}
	})
}

package mailer

import (
	"testing"
	"time"
)

// Pins the old frontend expectations (main:src/lib/__tests__/time.test.ts): en "Tue 1 Sep" /
// "18:30" / "– 19:30", nb weekday starting with "tir", 24-hour clock for BOTH locales.
func TestFormatDateTimePerLocale(t *testing.T) {
	oslo := time.FixedZone("CEST", 2*60*60) // no tzdata dependency in the test
	tue := time.Date(2026, time.September, 1, 16, 30, 0, 0, time.UTC) // 18:30 in Oslo
	mon := time.Date(2026, time.August, 31, 7, 5, 0, 0, time.UTC)     // 09:05 in Oslo

	cases := []struct {
		name   string
		locale string
		t      time.Time
		want   string
	}{
		{"en tuesday", "en", tue, "Tue 1 Sep, 18:30"},
		{"nb tuesday", "nb", tue, "tir. 1. sep., 18:30"},
		{"en monday", "en", mon, "Mon 31 Aug, 09:05"},
		{"nb monday", "nb", mon, "man. 31. aug., 09:05"},
		{"unknown locale falls back to en", "de", tue, "Tue 1 Sep, 18:30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDateTime(tc.locale, tc.t, oslo); got != tc.want {
				t.Errorf("FormatDateTime(%q) = %q, want %q", tc.locale, got, tc.want)
			}
		})
	}
}

func TestFormatDatePerLocale(t *testing.T) {
	oslo := time.FixedZone("CEST", 2*60*60)
	tue := time.Date(2026, time.September, 1, 16, 30, 0, 0, time.UTC)
	if got := FormatDate("en", tue, oslo); got != "Tue 1 Sep" {
		t.Errorf("FormatDate(en) = %q, want %q", got, "Tue 1 Sep")
	}
	if got := FormatDate("nb", tue, oslo); got != "tir. 1. sep." {
		t.Errorf("FormatDate(nb) = %q, want %q", got, "tir. 1. sep.")
	}
	// The zone matters: 23:30 UTC is already the next day in Oslo.
	late := time.Date(2026, time.September, 1, 23, 30, 0, 0, time.UTC)
	if got := FormatDate("en", late, oslo); got != "Wed 2 Sep" {
		t.Errorf("FormatDate(en, late) = %q, want %q", got, "Wed 2 Sep")
	}
}

func TestFormatTimeRange(t *testing.T) {
	oslo := time.FixedZone("CEST", 2*60*60)
	start := time.Date(2026, time.September, 1, 16, 30, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	for _, locale := range SupportedLocales {
		if got := FormatTimeRange(locale, start, end, oslo); got != "18:30–19:30" {
			t.Errorf("FormatTimeRange(%q) = %q, want %q", locale, got, "18:30–19:30")
		}
	}
	// nil location means UTC.
	if got := FormatTimeRange("en", start, end, nil); got != "16:30–17:30" {
		t.Errorf("FormatTimeRange(nil loc) = %q, want %q", got, "16:30–17:30")
	}
}

func TestIsSupportedLocale(t *testing.T) {
	for _, l := range SupportedLocales {
		if !IsSupportedLocale(l) {
			t.Errorf("IsSupportedLocale(%q) = false", l)
		}
	}
	for _, l := range []string{"", "de", "EN", "nb-NO"} {
		if IsSupportedLocale(l) {
			t.Errorf("IsSupportedLocale(%q) = true, want false", l)
		}
	}
}

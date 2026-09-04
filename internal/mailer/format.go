package mailer

import (
	"fmt"
	"time"
)

// SupportedLocales is the authoritative list of locales the application renders mail and UI in.
// internal/auth validates a user's stored locale against this; the web app's paraglide config
// (web/project.inlang/settings.json) must list the same set.
var SupportedLocales = []string{"en", "nb"}

// IsSupportedLocale reports whether locale is exactly one of SupportedLocales (case-sensitive,
// no region tags — the app stores bare "en"/"nb").
func IsSupportedLocale(locale string) bool {
	for _, l := range SupportedLocales {
		if l == locale {
			return true
		}
	}
	return false
}

// Norwegian short names, matching what Intl.DateTimeFormat("nb-NO", {weekday:"short"}) and
// {month:"short"} produced in the old frontend (main:src/lib/time.ts). Index by time.Weekday
// (Sunday = 0) and time.Month-1.
var nbWeekdays = [...]string{"søn.", "man.", "tir.", "ons.", "tor.", "fre.", "lør."}
var nbMonths = [...]string{"jan.", "feb.", "mar.", "apr.", "mai", "jun.", "jul.", "aug.", "sep.", "okt.", "nov.", "des."}

// inLocation converts t to loc, treating a nil loc as UTC.
func inLocation(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		return t.UTC()
	}
	return t.In(loc)
}

// FormatDate renders the calendar day of t (in loc) the way the old frontend's formatOptionLabel
// did: en "Tue 1 Sep", nb "tir. 1. sep.". Unknown locales render as en.
func FormatDate(locale string, t time.Time, loc *time.Location) string {
	lt := inLocation(t, loc)
	if locale == "nb" {
		return fmt.Sprintf("%s %d. %s", nbWeekdays[lt.Weekday()], lt.Day(), nbMonths[lt.Month()-1])
	}
	return lt.Format("Mon 2 Jan")
}

// FormatDateTime is FormatDate plus a 24-hour clock: en "Tue 1 Sep, 18:30", nb
// "tir. 1. sep., 18:30". Both locales use the 24-hour clock (the old hhmm helper always used
// en-GB hourCycle h23 regardless of locale).
func FormatDateTime(locale string, t time.Time, loc *time.Location) string {
	lt := inLocation(t, loc)
	return FormatDate(locale, lt, lt.Location()) + ", " + lt.Format("15:04")
}

// FormatTimeRange renders "18:30–19:30" (en dash) in loc; identical for every locale.
func FormatTimeRange(_ string, start, end time.Time, loc *time.Location) string {
	return inLocation(start, loc).Format("15:04") + "–" + inLocation(end, loc).Format("15:04")
}

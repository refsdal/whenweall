// Ports src/lib/availability.ts (generateSlots/isSlotAvailable) — the pure slot-generation core
// every booking availability check runs through. No DB access here at all: PageRules/Interval are
// plain values, so this file is exhaustively table-tested (availability_test.go) independent of
// Postgres.
package bookings

import (
	"fmt"
	"sort"
	"time"
)

// Interval is a UTC [Start, End) span — a busy booking interval, or a candidate slot's own
// padded window while checking it for buffer collisions.
type Interval struct {
	Start time.Time
	End   time.Time
}

// PageRules mirrors availability.ts's PageRules: the scheduling rules Slots needs, independent of
// how they were loaded (a queries.BookingPage row, or a table test's literal). Availability keys
// are weekday strings '0'..'6' (Sunday=0, matching Go's time.Sunday==0); DateOverrides keys are
// 'YYYY-MM-DD' and fully replace that date's weekly ranges — nil means "no overrides at all", an
// entry mapping to an empty slice means "this date is fully blocked".
type PageRules struct {
	Timezone        string
	SlotDurationMin int
	BufferBeforeMin int
	BufferAfterMin  int
	MinNoticeMin    int
	MaxDaysAhead    int
	Availability    Availability
	DateOverrides   DateOverrides
}

// Policy: slots are laid on a UTC grid anchored at each local range's start, not on a per-slot
// local-time grid. Only a range's start/end local wall-clock times are converted to UTC (once
// each); every slot within the range is then `rangeStartUTC + k*duration .. +duration` in pure
// UTC arithmetic. Local wall-clock spacing between consecutive slots may therefore shift by an
// hour on a DST transition date — that's intentional: it keeps every emitted slot's duration
// exactly slotDurationMin and its ordering strictly increasing, which a naive per-slot local-time
// conversion cannot guarantee around a nonexistent (spring-forward) or ambiguous (fall-back)
// local hour. See availability_test.go's DST cases.

// localDateStr formats t (any instant) as a "YYYY-MM-DD" calendar date in loc.
func localDateStr(t time.Time, loc *time.Location) string {
	return t.In(loc).Format("2006-01-02")
}

// dateOnlyUTC parses a "YYYY-MM-DD" string as a UTC midnight instant — used only for calendar
// arithmetic (day counting, weekday lookup), never for wall-clock conversion.
func dateOnlyUTC(dateStr string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func addDaysToDateStr(dateStr string, days int) string {
	t, ok := dateOnlyUTC(dateStr)
	if !ok {
		return dateStr
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

func daysBetween(from, to string) int {
	a, aok := dateOnlyUTC(from)
	b, bok := dateOnlyUTC(to)
	if !aok || !bok {
		return -1
	}
	return int(b.Sub(a).Hours() / 24)
}

// weekdayKey is the '0'..'6' (Sunday=0) key availability.ts's weekdayKey computes off the UTC
// midnight of dateStr.
func weekdayKey(dateStr string) string {
	t, ok := dateOnlyUTC(dateStr)
	if !ok {
		return ""
	}
	return string(rune('0' + int(t.Weekday())))
}

func overlaps(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

// localToUTC converts a "HH:mm" wall-clock time on dateStr, in loc, to its UTC instant — ported
// from lib/time.ts's localToUtcIso (there backed by @date-fns/tz's TZDate). time.Date's own
// normalization matches TZDate's for both DST cases Slots must handle: a wall-clock time inside a
// spring-forward gap is pushed forward by the gap's width, and one inside a fall-back's doubled
// hour resolves to its first (pre-transition) occurrence.
func localToUTC(dateStr, hhmm string, loc *time.Location) (time.Time, bool) {
	d, ok := dateOnlyUTC(dateStr)
	if !ok {
		return time.Time{}, false
	}
	var hh, mm int
	if n, err := fmt.Sscanf(hhmm, "%2d:%2d", &hh, &mm); err != nil || n != 2 {
		return time.Time{}, false
	}
	return time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, loc), true
}

// Slots returns bookable UTC start times between from/to (any two instants — only their local
// calendar date in rules.Timezone is used, inclusive of both endpoints) — the weekly availability
// windows intersected with date overrides, minus booked intervals (padded by the page's buffers),
// minus a minNoticeMin/maxDaysAhead window around now. Pure and deterministic: sorted, deduped.
//
// Ported from generateSlots (availability.ts); see this file's policy comment above for the
// UTC-grid-anchored-per-range rationale, and the "buffer applies once, on the candidate" comment
// on bookedIntervalsForPage (bookings.go) for why booked is never itself padded here.
func Slots(rules PageRules, booked []Interval, now, from, to time.Time) []time.Time {
	loc, err := time.LoadLocation(rules.Timezone)
	if err != nil {
		return nil
	}

	fromDateStr := localDateStr(from, loc)
	toDateStr := localDateStr(to, loc)
	spanDays := daysBetween(fromDateStr, toDateStr)
	if spanDays < 0 {
		return nil
	}

	earliestStart := now.Add(time.Duration(rules.MinNoticeMin) * time.Minute)
	latestEnd := now.Add(time.Duration(rules.MaxDaysAhead) * 24 * time.Hour)
	slotDuration := time.Duration(rules.SlotDurationMin) * time.Minute

	// horizonDateStr is a defensive cap on the outer day loop below, independent of how large a
	// window [from, to] a caller passes in (handlePublicAvailability, handlers.go, rejects an
	// overlong one before it ever reaches here — I3 — but Slots is exported and callable
	// directly, so it doesn't rely on that alone): no slot dated beyond rules.MaxDaysAhead+1 days
	// from now can ever pass the per-slot `end.After(latestEnd)` check below, so once dateStr
	// walks past that point every remaining day in [from, to] is guaranteed dead weight — a
	// months- or years-long window would otherwise still cost one loop iteration (and a map
	// lookup) per day for no slot it could ever emit. The +1 (rather than exactly MaxDaysAhead)
	// gives the last legitimate range's own late-evening slots room to still fall on-or-before
	// latestEnd even after their day's own local-to-UTC conversion.
	horizonDateStr := localDateStr(now.Add(time.Duration(rules.MaxDaysAhead+1)*24*time.Hour), loc)

	type key struct {
		start, end int64
	}
	seen := make(map[key]struct{})
	var results []time.Time

	for i := 0; i <= spanDays; i++ {
		dateStr := addDaysToDateStr(fromDateStr, i)
		if dateStr > horizonDateStr {
			break
		}
		ranges, ok := rules.DateOverrides[dateStr]
		if !ok {
			ranges = rules.Availability[weekdayKey(dateStr)]
		}

		for _, r := range ranges {
			rangeStart, ok1 := localToUTC(dateStr, r.Start, loc)
			rangeEnd, ok2 := localToUTC(dateStr, r.End, loc)
			// A range whose start/end both fall inside a spring-forward gap (or otherwise map to
			// a non-increasing UTC order) can't be laid out on a grid; skip it rather than emit
			// nonsense.
			if !ok1 || !ok2 || !rangeStart.Before(rangeEnd) {
				continue
			}

			for start := rangeStart; !start.Add(slotDuration).After(rangeEnd); start = start.Add(slotDuration) {
				end := start.Add(slotDuration)

				if start.Before(earliestStart) {
					continue
				}
				if end.After(latestEnd) {
					continue
				}

				bufStart := start.Add(-time.Duration(rules.BufferBeforeMin) * time.Minute)
				bufEnd := end.Add(time.Duration(rules.BufferAfterMin) * time.Minute)
				conflict := false
				for _, b := range booked {
					if overlaps(bufStart, bufEnd, b.Start, b.End) {
						conflict = true
						break
					}
				}
				if conflict {
					continue
				}

				k := key{start.UnixMilli(), end.UnixMilli()}
				if _, dup := seen[k]; dup {
					continue
				}
				seen[k] = struct{}{}
				results = append(results, start)
			}
		}
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Before(results[j]) })
	return results
}

// IsSlotAvailable is a cheap, consistent single-slot check ported from isSlotAvailable
// (availability.ts): generates slots over a one-day window around start and looks for an exact
// start match.
func IsSlotAvailable(rules PageRules, start time.Time, now time.Time, booked []Interval) bool {
	from := start.Add(-24 * time.Hour)
	to := start.Add(24 * time.Hour)
	for _, s := range Slots(rules, booked, now, from, to) {
		if s.Equal(start) {
			return true
		}
	}
	return false
}

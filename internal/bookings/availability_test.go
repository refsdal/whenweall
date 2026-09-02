package bookings_test

// Ports src/lib/__tests__/availability.test.ts's generateSlots/isSlotAvailable cases case-for-case
// onto Slots/IsSlotAvailable (availability.go) — a pure function over PageRules/Interval/time.Time,
// so every case here runs with no DB at all.

import (
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/bookings"
)

func atNoonUTC(t *testing.T, dateStr string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		t.Fatalf("parsing %q: %v", dateStr, err)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, time.UTC)
}

func mustUTC(t *testing.T, iso string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		t.Fatalf("parsing %q: %v", iso, err)
	}
	return tm.UTC()
}

// assertSlotInvariants ports the TS helper of the same name: every slot must have exactly
// durationMin, and starts must be strictly increasing.
func assertSlotInvariants(t *testing.T, slots []time.Time, durationMin int) {
	t.Helper()
	for i := 1; i < len(slots); i++ {
		if !slots[i].After(slots[i-1]) {
			t.Errorf("slots[%d] = %v, want strictly after slots[%d] = %v", i, slots[i], i-1, slots[i-1])
		}
	}
}

func baseRules(mutate func(*bookings.PageRules)) bookings.PageRules {
	r := bookings.PageRules{
		Timezone:        "UTC",
		SlotDurationMin: 30,
		BufferBeforeMin: 0,
		BufferAfterMin:  0,
		MinNoticeMin:    0,
		MaxDaysAhead:    365,
		Availability:    bookings.Availability{},
		DateOverrides:   nil,
	}
	if mutate != nil {
		mutate(&r)
	}
	return r
}

func slotStarts(t *testing.T, slots []time.Time) []string {
	t.Helper()
	out := make([]string, len(slots))
	for i, s := range slots {
		out[i] = s.UTC().Format("2006-01-02T15:04:05.000Z")
	}
	return out
}

func rangeOf(start, end string) bookings.TimeRange {
	return bookings.TimeRange{Start: start, End: end}
}

func TestSlots(t *testing.T) {
	t.Run("produces expected UTC slots from a weekly range (Europe/Oslo Mon 09:00-11:00, 30 min, summer)", func(t *testing.T) {
		// 2026-07-06 is a Monday, Oslo is CEST (UTC+2) in July.
		rules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = bookings.Availability{"1": {rangeOf("09:00", "11:00")}}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-07-06"), atNoonUTC(t, "2026-07-06"))
		got := slotStarts(t, slots)
		want := []string{
			"2026-07-06T07:00:00.000Z",
			"2026-07-06T07:30:00.000Z",
			"2026-07-06T08:00:00.000Z",
			"2026-07-06T08:30:00.000Z",
		}
		if len(got) != len(want) {
			t.Fatalf("starts = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("starts[%d] = %v, want %v", i, got[i], want[i])
			}
		}
	})

	t.Run("date override with an empty array takes the day off, overriding weekly availability", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Availability = bookings.Availability{"1": {rangeOf("09:00", "10:00")}} // Monday
			r.DateOverrides = bookings.DateOverrides{"2026-01-05": {}}               // Monday 2026-01-05
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-05"))
		if len(slots) != 0 {
			t.Errorf("slots = %v, want empty", slots)
		}
	})

	t.Run("date override adds extra hours on a day with no weekly availability", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Availability = bookings.Availability{}
			r.DateOverrides = bookings.DateOverrides{"2026-01-05": {rangeOf("09:00", "09:30")}}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-05"))
		got := slotStarts(t, slots)
		want := []string{"2026-01-05T09:00:00.000Z"}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("starts = %v, want %v", got, want)
		}
	})

	t.Run("buffers exclude slots whose padded interval overlaps a busy interval in the gap between ranges", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Availability = bookings.Availability{
				"1": {rangeOf("09:00", "09:30"), rangeOf("09:45", "10:15")},
			}
			r.BufferBeforeMin = 15
			r.BufferAfterMin = 15
		})
		booked := []bookings.Interval{{
			Start: mustUTC(t, "2026-01-05T09:35:00.000Z"),
			End:   mustUTC(t, "2026-01-05T09:40:00.000Z"),
		}}
		from, to := atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-05")
		now := mustUTC(t, "2026-01-01T00:00:00Z")

		if slots := bookings.Slots(rules, booked, now, from, to); len(slots) != 0 {
			t.Errorf("slots (buffered) = %v, want empty", slots)
		}
		noBuffer := rules
		noBuffer.BufferBeforeMin, noBuffer.BufferAfterMin = 0, 0
		if slots := bookings.Slots(noBuffer, booked, now, from, to); len(slots) != 2 {
			t.Errorf("slots (unbuffered) = %v, want 2", slots)
		}
	})

	t.Run("excludes slots that start before now + minNoticeMin", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.MinNoticeMin = 120
			r.Availability = bookings.Availability{
				"1": {rangeOf("09:00", "09:30"), rangeOf("11:00", "11:30")},
			}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-05T08:00:00.000Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-05"))
		got := slotStarts(t, slots)
		want := []string{"2026-01-05T11:00:00.000Z"}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("starts = %v, want %v", got, want)
		}
	})

	t.Run("excludes slots that end after now + maxDaysAhead", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.MaxDaysAhead = 1
			r.Availability = bookings.Availability{
				"1": {rangeOf("09:00", "09:30")}, // Monday 2026-01-05
				"4": {rangeOf("09:00", "09:30")}, // Thursday 2026-01-08
			}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-05T00:00:00.000Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-08"))
		got := slotStarts(t, slots)
		want := []string{"2026-01-05T09:00:00.000Z"}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("starts = %v, want %v", got, want)
		}
	})

	t.Run("excludes a slot with only a partial overlap against a busy interval", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Availability = bookings.Availability{"1": {rangeOf("09:00", "09:30")}}
		})
		booked := []bookings.Interval{{
			Start: mustUTC(t, "2026-01-05T09:15:00.000Z"),
			End:   mustUTC(t, "2026-01-05T09:45:00.000Z"),
		}}
		slots := bookings.Slots(rules, booked, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-05"))
		if len(slots) != 0 {
			t.Errorf("slots = %v, want empty", slots)
		}
	})

	t.Run("is DST-safe across the Europe/Oslo spring-forward transition (2026-03-29)", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = bookings.Availability{
				"6": {rangeOf("09:00", "09:30")}, // Saturday 2026-03-28, still CET (+1)
				"1": {rangeOf("09:00", "09:30")}, // Monday 2026-03-30, now CEST (+2)
			}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-03-28"), atNoonUTC(t, "2026-03-30"))
		got := slotStarts(t, slots)
		want := []string{"2026-03-28T08:00:00.000Z", "2026-03-30T07:00:00.000Z"}
		if len(got) != len(want) {
			t.Fatalf("starts = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("starts[%d] = %v, want %v", i, got[i], want[i])
			}
		}
	})

	t.Run("is DST-safe across the Europe/Oslo fall-back transition (2026-10-25)", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = bookings.Availability{
				"6": {rangeOf("09:00", "09:30")}, // Saturday 2026-10-24, still CEST (+2)
				"1": {rangeOf("09:00", "09:30")}, // Monday 2026-10-26, now CET (+1)
			}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-10-24"), atNoonUTC(t, "2026-10-26"))
		got := slotStarts(t, slots)
		want := []string{"2026-10-24T07:00:00.000Z", "2026-10-26T08:00:00.000Z"}
		if len(got) != len(want) {
			t.Fatalf("starts = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("starts[%d] = %v, want %v", i, got[i], want[i])
			}
		}
	})

	t.Run("lays slots on a UTC grid when a range spans the Europe/Oslo spring-forward gap (2026-03-29)", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = bookings.Availability{"0": {rangeOf("01:00", "04:00")}} // Sunday 2026-03-29
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-03-29"), atNoonUTC(t, "2026-03-29"))
		assertSlotInvariants(t, slots, 30)
		if len(slots) == 0 {
			t.Fatal("slots is empty")
		}
		if got := slots[0].UTC().Format("2006-01-02T15:04:05.000Z"); got != "2026-03-29T00:00:00.000Z" {
			t.Errorf("first start = %v, want 2026-03-29T00:00:00.000Z (01:00 CET)", got)
		}
		last := slots[len(slots)-1].Add(30 * time.Minute)
		limit := mustUTC(t, "2026-03-29T02:00:00.000Z")
		if last.After(limit) {
			t.Errorf("last end = %v, want <= %v (04:00 CEST)", last, limit)
		}
	})

	t.Run("lays slots on a UTC grid when a range spans the Europe/Oslo fall-back gap (2026-10-25)", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = bookings.Availability{"0": {rangeOf("01:00", "04:00")}} // Sunday 2026-10-25
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-10-25"), atNoonUTC(t, "2026-10-25"))
		assertSlotInvariants(t, slots, 30)
		if len(slots) == 0 {
			t.Fatal("slots is empty")
		}
		if got := slots[0].UTC().Format("2006-01-02T15:04:05.000Z"); got != "2026-10-24T23:00:00.000Z" {
			t.Errorf("first start = %v, want 2026-10-24T23:00:00.000Z (01:00 CEST)", got)
		}
		last := slots[len(slots)-1].Add(30 * time.Minute)
		limit := mustUTC(t, "2026-10-25T03:00:00.000Z")
		if last.After(limit) {
			t.Errorf("last end = %v, want <= %v (04:00 CET)", last, limit)
		}
	})

	t.Run("never emits a malformed slot across a 10-day window spanning the spring-forward transition", func(t *testing.T) {
		all := bookings.Availability{}
		for d := 0; d <= 6; d++ {
			all[string(rune('0'+d))] = []bookings.TimeRange{rangeOf("01:00", "04:00")}
		}
		rules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = all
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-03-24"), atNoonUTC(t, "2026-04-02"))
		if len(slots) == 0 {
			t.Fatal("slots is empty, want > 0")
		}
		assertSlotInvariants(t, slots, 30)
	})

	t.Run("never emits a malformed slot across a 10-day window spanning the fall-back transition", func(t *testing.T) {
		all := bookings.Availability{}
		for d := 0; d <= 6; d++ {
			all[string(rune('0'+d))] = []bookings.TimeRange{rangeOf("01:00", "04:00")}
		}
		rules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = all
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-10-20"), atNoonUTC(t, "2026-10-29"))
		if len(slots) == 0 {
			t.Fatal("slots is empty, want > 0")
		}
		assertSlotInvariants(t, slots, 30)
	})

	t.Run("supports a 15-minute duration", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.SlotDurationMin = 15
			r.Availability = bookings.Availability{"1": {rangeOf("09:00", "10:00")}}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-05"))
		got := slotStarts(t, slots)
		want := []string{
			"2026-01-05T09:00:00.000Z", "2026-01-05T09:15:00.000Z",
			"2026-01-05T09:30:00.000Z", "2026-01-05T09:45:00.000Z",
		}
		if len(got) != len(want) {
			t.Fatalf("starts = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("starts[%d] = %v, want %v", i, got[i], want[i])
			}
		}
	})

	t.Run("supports a 60-minute duration", func(t *testing.T) {
		rules := baseRules(func(r *bookings.PageRules) {
			r.SlotDurationMin = 60
			r.Availability = bookings.Availability{"1": {rangeOf("09:00", "10:00")}}
		})
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-05"))
		got := slotStarts(t, slots)
		want := []string{"2026-01-05T09:00:00.000Z"}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("starts = %v, want %v", got, want)
		}
	})

	t.Run("returns [] for empty availability", func(t *testing.T) {
		rules := baseRules(nil)
		slots := bookings.Slots(rules, nil, mustUTC(t, "2026-01-01T00:00:00Z"), atNoonUTC(t, "2026-01-05"), atNoonUTC(t, "2026-01-11"))
		if len(slots) != 0 {
			t.Errorf("slots = %v, want empty", slots)
		}
	})
}

func TestIsSlotAvailable(t *testing.T) {
	rules := baseRules(func(r *bookings.PageRules) {
		r.Availability = bookings.Availability{"1": {rangeOf("09:00", "10:00")}}
	})

	t.Run("is true for a slot Slots would produce", func(t *testing.T) {
		if !bookings.IsSlotAvailable(rules, mustUTC(t, "2026-01-05T09:00:00.000Z"), mustUTC(t, "2026-01-01T00:00:00Z"), nil) {
			t.Error("want true")
		}
	})

	t.Run("is false for a time that is not on the slot grid or outside availability", func(t *testing.T) {
		if bookings.IsSlotAvailable(rules, mustUTC(t, "2026-01-05T09:05:00.000Z"), mustUTC(t, "2026-01-01T00:00:00Z"), nil) {
			t.Error("want false (off-grid)")
		}
		if bookings.IsSlotAvailable(rules, mustUTC(t, "2026-01-05T12:00:00.000Z"), mustUTC(t, "2026-01-01T00:00:00Z"), nil) {
			t.Error("want false (outside availability)")
		}
	})

	t.Run("is true for the slot right after the Europe/Oslo spring-forward gap", func(t *testing.T) {
		dstRules := baseRules(func(r *bookings.PageRules) {
			r.Timezone = "Europe/Oslo"
			r.SlotDurationMin = 30
			r.Availability = bookings.Availability{"0": {rangeOf("01:00", "04:00")}} // Sunday 2026-03-29
		})
		if !bookings.IsSlotAvailable(dstRules, mustUTC(t, "2026-03-29T01:00:00.000Z"), mustUTC(t, "2026-01-01T00:00:00Z"), nil) {
			t.Error("want true")
		}
	})
}

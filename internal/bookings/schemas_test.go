package bookings_test

// Ports the PageInput-relevant cases from
// src/server/bookings/__tests__/schemas.test.ts's handleSchema/slugSchema, timeRangeSchema,
// availabilitySchema, dateOverridesSchema, and createBookingPageSchema describe blocks
// case-for-case. publicAvailabilityQuerySchema/bookSlotSchema/manageBookingSchema/
// rescheduleSchema/pageIdSchema belong to Task 3 (src/server/bookings/bookings.ts's own input
// types), not this task's PageInput.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/bookings"
)

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var verr *bookings.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v (%T), want *bookings.ValidationError", err, err)
	}
	return verr.Fields
}

func rangesOf(count int) []bookings.TimeRange {
	out := make([]bookings.TimeRange, count)
	for i := range out {
		out[i] = bookings.TimeRange{Start: fmt.Sprintf("%02d:00", i), End: fmt.Sprintf("%02d:15", i)}
	}
	return out
}

func TestPageInputHandleAndSlug(t *testing.T) {
	// PageInput.Slug shares handleSchema/slugSchema's HANDLE_SLUG_RE + 3..30 length rule; exercised
	// here through baseInput's Slug field and validateHandle (SetOrgSlug's own validator) side by
	// side, since both are the same underlying rule in the TS source.
	valid := []string{"anders", "a-b-c", "a1b", "intro-call"}
	for _, v := range valid {
		in := baseInput(func(in *bookings.PageInput) { in.Slug = v })
		if err := in.Validate(); err != nil {
			t.Errorf("Slug %q: Validate() = %v, want nil", v, err)
		}
	}

	invalid := []string{"ab", strings.Repeat("a", 31), "Anders", "-abc", "abc-", "ab_c", "ab c", "IntroCall"}
	for _, v := range invalid {
		in := baseInput(func(in *bookings.PageInput) { in.Slug = v })
		err := in.Validate()
		fields := fieldsOf(t, err)
		if _, ok := fields["slug"]; !ok {
			t.Errorf("Slug %q: Fields = %+v, want a slug entry", v, fields)
		}
	}
}

// TestValidateHandle exercises SetOrgSlug's own handle validation, which runs (and rejects a
// malformed handle) before any DB access — see SetOrgSlug's own doc comment in pages.go. Only the
// invalid cases are asserted here without a database; the valid path is covered behaviorally by
// TestSetOrgSlug in pages_test.go, which needs a real org row to update.
func TestValidateHandle(t *testing.T) {
	s := bookings.NewService(testConfig(t), nil)
	for _, v := range []string{"ab", strings.Repeat("a", 31), "Anders", "-abc", "abc-", "ab_c", "ab c"} {
		err := s.SetOrgSlug(context.Background(), "1", v)
		var verr *bookings.ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("handle %q: err = %v, want *ValidationError", v, err)
		}
	}
}

func TestPageInputAvailability(t *testing.T) {
	t.Run("accepts weekday keys 0..6 with sorted, non-overlapping ranges, empty allowed", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) {
			in.Availability = bookings.Availability{
				"0": {},
				"1": {{Start: "09:00", End: "12:00"}, {Start: "13:00", End: "17:00"}},
			}
		})
		if err := in.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("rejects keys outside 0..6", func(t *testing.T) {
		for _, key := range []string{"7", "monday", "-1"} {
			in := baseInput(func(in *bookings.PageInput) {
				in.Availability = bookings.Availability{key: {}}
			})
			fields := fieldsOf(t, in.Validate())
			if _, ok := fields["availability"]; !ok {
				t.Errorf("key %q: Fields = %+v, want an availability entry", key, fields)
			}
		}
	})

	t.Run("rejects overlapping ranges for a weekday", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) {
			in.Availability = bookings.Availability{
				"1": {{Start: "09:00", End: "12:00"}, {Start: "11:00", End: "13:00"}},
			}
		})
		if err := in.Validate(); err == nil {
			t.Error("Validate() = nil, want an overlap error")
		}
	})

	t.Run("rejects unsorted ranges for a weekday", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) {
			in.Availability = bookings.Availability{
				"1": {{Start: "13:00", End: "17:00"}, {Start: "09:00", End: "12:00"}},
			}
		})
		if err := in.Validate(); err == nil {
			t.Error("Validate() = nil, want a sort error")
		}
	})

	t.Run("rejects more than 20 ranges in a day", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) {
			in.Availability = bookings.Availability{"1": rangesOf(21)}
		})
		if err := in.Validate(); err == nil {
			t.Error("Validate() = nil, want a too-many-ranges error")
		}
	})

	t.Run("rejects malformed or unaligned HH:mm, and end <= start", func(t *testing.T) {
		cases := [][2]string{
			{"09:05", "09:30"}, // unaligned minute
			{"09:00", "09:31"}, // unaligned minute
			{"10:00", "09:00"}, // end before start
			{"23:45", "23:45"}, // end == start
			{"24:00", "25:00"}, // malformed hour
			{"9:00", "10:00"},  // malformed (single-digit hour)
		}
		for _, c := range cases {
			in := baseInput(func(in *bookings.PageInput) {
				in.Availability = bookings.Availability{"1": {{Start: c[0], End: c[1]}}}
			})
			if err := in.Validate(); err == nil {
				t.Errorf("range %v: Validate() = nil, want an error", c)
			}
		}
	})
}

func TestPageInputDateOverrides(t *testing.T) {
	t.Run("accepts YYYY-MM-DD keys, empty slice meaning day off", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) {
			in.DateOverrides = bookings.DateOverrides{
				"2026-12-24": {},
				"2026-12-31": {{Start: "09:00", End: "10:00"}},
			}
		})
		if err := in.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("rejects malformed date keys", func(t *testing.T) {
		for _, key := range []string{"2026-1-1", "not-a-date"} {
			in := baseInput(func(in *bookings.PageInput) {
				in.DateOverrides = bookings.DateOverrides{key: {}}
			})
			fields := fieldsOf(t, in.Validate())
			if _, ok := fields["dateOverrides"]; !ok {
				t.Errorf("key %q: Fields = %+v, want a dateOverrides entry", key, fields)
			}
		}
	})

	t.Run("rejects more than 366 entries", func(t *testing.T) {
		overrides := make(bookings.DateOverrides, 367)
		start := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 367; i++ {
			overrides[start.AddDate(0, 0, i).Format("2006-01-02")] = []bookings.TimeRange{}
		}
		in := baseInput(func(in *bookings.PageInput) { in.DateOverrides = overrides })
		fields := fieldsOf(t, in.Validate())
		if _, ok := fields["dateOverrides"]; !ok {
			t.Errorf("Fields = %+v, want a dateOverrides entry", fields)
		}
	})
}

func TestPageInputValidate(t *testing.T) {
	t.Run("accepts a valid page", func(t *testing.T) {
		if err := baseInput(nil).Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("rejects an invalid timezone", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) { in.Timezone = "Not/AZone" })
		fields := fieldsOf(t, in.Validate())
		if _, ok := fields["timezone"]; !ok {
			t.Errorf("Fields = %+v, want a timezone entry", fields)
		}
	})

	t.Run("rejects out-of-range duration and buffers", func(t *testing.T) {
		cases := []func(*bookings.PageInput){
			func(in *bookings.PageInput) { in.SlotDurationMin = 10 },
			func(in *bookings.PageInput) { in.SlotDurationMin = 481 },
			func(in *bookings.PageInput) { in.BufferBeforeMin = 121 },
		}
		for _, mutate := range cases {
			in := baseInput(mutate)
			if err := in.Validate(); err == nil {
				t.Error("Validate() = nil, want a range error")
			}
		}
	})

	t.Run("rejects an invalid status", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) { in.Status = "archived" })
		fields := fieldsOf(t, in.Validate())
		if _, ok := fields["status"]; !ok {
			t.Errorf("Fields = %+v, want a status entry", fields)
		}
	})

	t.Run("empty status passes Validate (CreatePage ignores status; UpdatePage requires it itself)", func(t *testing.T) {
		if err := baseInput(func(in *bookings.PageInput) { in.Status = "" }).Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("rejects an omitted availability (nil map) — never stored as JSON null", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) { in.Availability = nil })
		fields := fieldsOf(t, in.Validate())
		if fields["availability"] != "availability is required" {
			t.Errorf("Fields = %+v, want availability: availability is required", fields)
		}
	})

	t.Run("accepts an empty availability object (a page with no open days is valid, just unbookable)", func(t *testing.T) {
		in := baseInput(func(in *bookings.PageInput) { in.Availability = bookings.Availability{} })
		if err := in.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})
}

// baseBookInput mirrors baseInput's role, for BookInput.Validate's own rule set (bookSlotSchema,
// schemas.ts): a valid visitor-facing booking request with every checked field populated.
func baseBookInput(mutate func(*bookings.BookInput)) bookings.BookInput {
	note := "See you then"
	in := bookings.BookInput{
		StartAt:  time.Now().Add(24 * time.Hour),
		Name:     "Ada Lovelace",
		Email:    "ada@example.com",
		Note:     &note,
		Timezone: "Europe/Oslo",
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

func TestBookInputValidate(t *testing.T) {
	t.Run("accepts a valid booking", func(t *testing.T) {
		if err := baseBookInput(nil).Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("name: required, trimmed, at most 80 characters", func(t *testing.T) {
		cases := []func(*bookings.BookInput){
			func(in *bookings.BookInput) { in.Name = "" },
			func(in *bookings.BookInput) { in.Name = "   " },
			func(in *bookings.BookInput) { in.Name = strings.Repeat("a", 81) },
		}
		for _, mutate := range cases {
			in := baseBookInput(mutate)
			fields := fieldsOf(t, in.Validate())
			if _, ok := fields["name"]; !ok {
				t.Errorf("Fields = %+v, want a name entry", fields)
			}
		}
		// Exactly the limit, and leading/trailing space trimmed off before counting, both pass.
		in := baseBookInput(func(in *bookings.BookInput) { in.Name = "  " + strings.Repeat("a", 80) + "  " })
		if err := in.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("email: required and must be a valid address, at most 254 characters", func(t *testing.T) {
		cases := []string{"", "not-an-email", "bob@", "Bob <bob@example.com>", strings.Repeat("a", 250) + "@a.co"}
		for _, email := range cases {
			in := baseBookInput(func(in *bookings.BookInput) { in.Email = email })
			fields := fieldsOf(t, in.Validate())
			if _, ok := fields["email"]; !ok {
				t.Errorf("email %q: Fields = %+v, want an email entry", email, fields)
			}
		}
		valid := baseBookInput(func(in *bookings.BookInput) { in.Email = "ada@example.com" })
		if err := valid.Validate(); err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
	})

	t.Run("note: optional, trimmed, at most 1000 characters", func(t *testing.T) {
		in := baseBookInput(func(in *bookings.BookInput) { in.Note = nil })
		if err := in.Validate(); err != nil {
			t.Errorf("nil note: Validate() = %v, want nil", err)
		}

		tooLong := strings.Repeat("a", 1001)
		in = baseBookInput(func(in *bookings.BookInput) { in.Note = &tooLong })
		fields := fieldsOf(t, in.Validate())
		if _, ok := fields["note"]; !ok {
			t.Errorf("Fields = %+v, want a note entry", fields)
		}
	})

	t.Run("timezone: 1-64 characters and a real IANA zone", func(t *testing.T) {
		cases := []string{"", "Not/AZone", strings.Repeat("a", 65)}
		for _, tz := range cases {
			in := baseBookInput(func(in *bookings.BookInput) { in.Timezone = tz })
			fields := fieldsOf(t, in.Validate())
			if _, ok := fields["timezone"]; !ok {
				t.Errorf("timezone %q: Fields = %+v, want a timezone entry", tz, fields)
			}
		}
	})
}

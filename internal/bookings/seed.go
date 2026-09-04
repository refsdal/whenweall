package bookings

import (
	"context"
	"crypto/rand"
)

// seedHandleAlphabet is lowercase-alnum only, matching handleSlugRegexp's own charset — unlike
// internal/db.NewID's alphabet (upper/lowercase, digits, "_", "-"), which can produce a leading
// "_"/"-" or an uppercase letter neither the org handle nor CreateSampleBookingPage's own "test-"
// prefix would survive validation with.
const seedHandleAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomHandleSuffix(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing means the host is broken
	}
	for i := range b {
		b[i] = seedHandleAlphabet[int(b[i])%len(seedHandleAlphabet)]
	}
	return string(b)
}

// CreateSampleBookingPage creates the fixed booking page internal/httpserver's test-seed route
// (Task 5) hands back as a spec's `pageId`/`handle`/`slug` — weekday (Mon-Fri) 09:00-17:00,
// Europe/Oslo, 30-minute slots, slug "intro-call", mirroring the old TS seed route's
// `sampleBookingPage` (src/routes/api/test/seed.ts's `withBookingPage` branch). Moved here
// (rather than built inline in internal/httpserver/testroutes.go) for the same reason
// internal/polls.CreateSamplePoll is: bookings already imports internal/httpserver
// (handlers.go), so the reverse edge would be a compile-time cycle.
//
// orgID's own public handle is set here too (SetOrgSlug) — a fresh signup's org has none yet, and
// the public booking route needs one to resolve `/book/{handle}/{slug}` at all.
func (s *Service) CreateSampleBookingPage(ctx context.Context, orgID, userID string) (pageID, handle, slug string, err error) {
	handle = "test-" + randomHandleSuffix(8)
	if err := s.SetOrgSlug(ctx, orgID, handle); err != nil {
		return "", "", "", err
	}

	weekday := []TimeRange{{Start: "09:00", End: "17:00"}}
	view, err := s.CreatePage(ctx, orgID, userID, PageInput{
		Slug:            "intro-call",
		Title:           "Intro call",
		Timezone:        "Europe/Oslo",
		SlotDurationMin: 30,
		BufferBeforeMin: 0,
		BufferAfterMin:  0,
		MinNoticeMin:    0,
		MaxDaysAhead:    60,
		Availability: Availability{
			"1": weekday, "2": weekday, "3": weekday, "4": weekday, "5": weekday,
		},
		GoogleSync: false,
		Reminders:  true,
	})
	if err != nil {
		return "", "", "", err
	}
	return view.ID, handle, view.Slug, nil
}

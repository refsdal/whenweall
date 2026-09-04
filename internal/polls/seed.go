package polls

import (
	"context"
	"time"
)

// CreateSamplePoll creates the fixed, always-the-same "two options a couple of days out"
// datetime poll internal/httpserver's test-seed route (Task 5) hands back as a spec's
// `pollId` — a straight port of the old TS test route's `samplePollOptions`/seed body
// (src/routes/api/test/seed.ts's `withPoll` branch), moved here (rather than built inline in
// internal/httpserver/testroutes.go) because that package cannot import this one — polls already
// imports internal/httpserver (handlers.go), so the reverse edge would be a compile-time cycle,
// the same reasoning internal/rooms/endpoints.go's own doc comment gives for its narrow
// PollService/BookingService seams.
func (s *Service) CreateSamplePoll(ctx context.Context, orgID, userID string) (string, error) {
	description := "Created by the test seed route."
	allowComments := true
	allowIfNeedBe := true

	base := time.Now().UTC().Truncate(time.Hour).Add(16*time.Hour + 30*time.Minute)
	options := make([]OptionInput, 0, 2)
	for _, offsetDays := range []int{2, 3} {
		start := base.Add(time.Duration(offsetDays) * 24 * time.Hour)
		end := start.Add(90 * time.Minute)
		endAt := end.Format("2006-01-02T15:04:05Z")
		options = append(options, OptionInput{
			Kind:    OptionKindDatetime,
			StartAt: start.Format("2006-01-02T15:04:05Z"),
			EndAt:   &endAt,
		})
	}

	view, err := s.Create(ctx, orgID, userID, CreatePollInput{
		Type:        PollTypeDatetime,
		Title:       "Seeded test poll",
		Description: &description,
		Timezone:    "Europe/Oslo",
		Options:     options,
		PollSettingsInput: PollSettingsInput{
			AllowComments: &allowComments,
			AllowIfNeedBe: &allowIfNeedBe,
		},
	})
	if err != nil {
		return "", err
	}
	return view.ID, nil
}

// CreateSampleSignup creates the fixed sign-up sheet the old TS seed route's `withSignup` branch
// did — two text slots, one capped at 1 (enough for an e2e test to fill it and see it go "full"),
// one unlimited.
func (s *Service) CreateSampleSignup(ctx context.Context, orgID, userID string) (string, error) {
	description := "Created by the test seed route."
	capacityOne := 1

	view, err := s.Create(ctx, orgID, userID, CreatePollInput{
		Type:        PollTypeSignup,
		Title:       "Seeded sign-up sheet",
		Description: &description,
		Timezone:    "Europe/Oslo",
		Options: []OptionInput{
			{Kind: OptionKindText, Label: "Slot 1", CapacitySet: true, Capacity: &capacityOne},
			{Kind: OptionKindText, Label: "Slot 2", CapacitySet: true, Capacity: nil},
		},
	})
	if err != nil {
		return "", err
	}
	return view.ID, nil
}

package polls_test

// Ports src/server/polls/__tests__/schemas.test.ts's createPollSchema/updatePollSchema cases
// case-for-case (the addParticipant/addComment/claim/unclaim schema cases belong to Task 3, which
// owns those input types). Where the TS test asserted a specific zod issue path, the Go test
// asserts the equivalent key is present in the returned *polls.ValidationError.Fields — see
// refinePollOptions/validateOptionShape in schemas.go for the "options.<i>[.field]" convention.

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/polls"
)

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func datetimeOption(startAt string, endAt ...string) polls.OptionInput {
	opt := polls.OptionInput{Kind: polls.OptionKindDatetime, StartAt: startAt}
	if len(endAt) > 0 {
		opt.EndAt = &endAt[0]
	}
	return opt
}

func textOption(label string) polls.OptionInput {
	return polls.OptionInput{Kind: polls.OptionKindText, Label: label}
}

func dateOption(date string) polls.OptionInput {
	return polls.OptionInput{Kind: polls.OptionKindDate, Date: date}
}

func withCapacity(opt polls.OptionInput, capacity *int) polls.OptionInput {
	opt.CapacitySet = true
	opt.Capacity = capacity
	return opt
}

func baseDatetimePoll(options ...[]polls.OptionInput) polls.CreatePollInput {
	opts := []polls.OptionInput{
		datetimeOption("2026-09-01T10:00:00.000Z", "2026-09-01T11:00:00.000Z"),
		datetimeOption("2026-09-02T10:00:00.000Z"),
	}
	if len(options) > 0 {
		opts = options[0]
	}
	return polls.CreatePollInput{
		Type:     polls.PollTypeDatetime,
		Title:    "Team sync",
		Timezone: "Europe/Oslo",
		Options:  opts,
	}
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var verr *polls.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("error is not a *polls.ValidationError: %v (%T)", err, err)
	}
	return verr.Fields
}

func TestCreatePollInputValidate(t *testing.T) {
	manyOptions := make([]polls.OptionInput, 101)
	for i := range manyOptions {
		manyOptions[i] = datetimeOption(fmt.Sprintf("2026-01-%02dT00:00:00.000Z", 1+i%27))
	}

	tests := []struct {
		name      string
		input     polls.CreatePollInput
		wantValid bool
		wantField string // asserted present in Fields when wantValid is false
	}{
		{
			name:      "accepts a valid datetime poll",
			input:     baseDatetimePoll(),
			wantValid: true,
		},
		{
			name: "rejects an options poll containing a date option",
			input: polls.CreatePollInput{
				Type:     polls.PollTypeOptions,
				Title:    "Pick a date",
				Timezone: "Europe/Oslo",
				Options:  []polls.OptionInput{dateOption("2026-09-01")},
			},
			wantValid: false,
			wantField: "options.0",
		},
		{
			name: "rejects a datetime poll containing a text option",
			input: baseDatetimePoll([]polls.OptionInput{
				datetimeOption("2026-09-01T10:00:00.000Z"),
				textOption("Pizza"),
			}),
			wantValid: false,
			wantField: "options.1",
		},
		{
			name: "rejects a title longer than 200 characters",
			input: func() polls.CreatePollInput {
				in := baseDatetimePoll()
				in.Title = strings.Repeat("x", 201)
				return in
			}(),
			wantValid: false,
			wantField: "title",
		},
		{
			name:      "rejects more than 100 options",
			input:     baseDatetimePoll(manyOptions),
			wantValid: false,
			wantField: "options",
		},
		{
			name: "rejects a datetime option whose endAt is not after startAt",
			input: baseDatetimePoll([]polls.OptionInput{
				datetimeOption("2026-09-01T10:00:00.000Z", "2026-09-01T10:00:00.000Z"),
			}),
			wantValid: false,
			wantField: "options.0.endAt",
		},
		{
			name: "rejects duplicate date options",
			input: polls.CreatePollInput{
				Type:     polls.PollTypeDatetime,
				Title:    "Dupes",
				Timezone: "Europe/Oslo",
				Options:  []polls.OptionInput{dateOption("2026-09-01"), dateOption("2026-09-01")},
			},
			wantValid: false,
			wantField: "options.1",
		},
		{
			name: "rejects duplicate datetime options (same startAt|endAt)",
			input: baseDatetimePoll([]polls.OptionInput{
				datetimeOption("2026-09-01T10:00:00.000Z", "2026-09-01T11:00:00.000Z"),
				datetimeOption("2026-09-01T10:00:00.000Z", "2026-09-01T11:00:00.000Z"),
			}),
			wantValid: false,
			wantField: "options.1",
		},
		{
			name: "rejects duplicate text option labels after trim/lowercase",
			input: polls.CreatePollInput{
				Type:     polls.PollTypeOptions,
				Title:    "Dupes",
				Timezone: "Europe/Oslo",
				Options:  []polls.OptionInput{textOption("Pizza"), textOption("  pizza  ")},
			},
			wantValid: false,
			wantField: "options.1",
		},
		{
			name: "signup: rejects a signup poll with mixed option kinds",
			input: polls.CreatePollInput{
				Type:     polls.PollTypeSignup,
				Title:    "Bring a dish",
				Timezone: "Europe/Oslo",
				Options:  []polls.OptionInput{textOption("Salad"), dateOption("2026-09-01")},
			},
			wantValid: false,
			wantField: "options.1",
		},
		{
			name: "signup: rejects capacity on a datetime poll option",
			input: baseDatetimePoll([]polls.OptionInput{
				withCapacity(datetimeOption("2026-09-01T10:00:00.000Z"), intPtr(2)),
			}),
			wantValid: false,
			wantField: "options.0.capacity",
		},
		{
			name: "signup: rejects signupMaxClaims on a datetime poll",
			input: func() polls.CreatePollInput {
				in := baseDatetimePoll()
				in.SignupMaxClaims = intPtr(2)
				return in
			}(),
			wantValid: false,
			wantField: "signupMaxClaims",
		},
		{
			name: "signup: accepts a signup poll with capacities [1, null] (unlimited)",
			input: polls.CreatePollInput{
				Type:     polls.PollTypeSignup,
				Title:    "Bring a dish",
				Timezone: "Europe/Oslo",
				Options: []polls.OptionInput{
					withCapacity(textOption("Salad"), intPtr(1)),
					withCapacity(textOption("Drinks"), nil),
				},
			},
			wantValid: true,
		},
		{
			name: "signup: rejects signupMaxClaims of 0",
			input: polls.CreatePollInput{
				Type:            polls.PollTypeSignup,
				Title:           "Bring a dish",
				Timezone:        "Europe/Oslo",
				Options:         []polls.OptionInput{textOption("Salad")},
				SignupMaxClaims: intPtr(0),
			},
			wantValid: false,
			wantField: "signupMaxClaims",
		},
		{
			name: "signup: rejects signupMaxClaims of 101",
			input: polls.CreatePollInput{
				Type:            polls.PollTypeSignup,
				Title:           "Bring a dish",
				Timezone:        "Europe/Oslo",
				Options:         []polls.OptionInput{textOption("Salad")},
				SignupMaxClaims: intPtr(101),
			},
			wantValid: false,
			wantField: "signupMaxClaims",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if tt.wantValid {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want a validation error")
			}
			if !errors.Is(err, polls.ErrValidation) {
				t.Errorf("errors.Is(err, ErrValidation) = false, want true (err: %v)", err)
			}
			fields := fieldsOf(t, err)
			if _, ok := fields[tt.wantField]; !ok {
				t.Errorf("Fields = %v, want key %q present", fields, tt.wantField)
			}
		})
	}
}

func TestUpdatePollInputValidate(t *testing.T) {
	tests := []struct {
		name      string
		input     polls.UpdatePollInput
		wantValid bool
		wantField string
	}{
		{
			name:      "accepts a partial update with just one field",
			input:     polls.UpdatePollInput{Title: strPtr("New title")},
			wantValid: true,
		},
		{
			name:      "accepts an empty update (no fields touched)",
			input:     polls.UpdatePollInput{},
			wantValid: true,
		},
		{
			name: "still enforces endAt > startAt on options provided in an update",
			input: polls.UpdatePollInput{
				Options: []polls.OptionInput{
					datetimeOption("2026-09-01T10:00:00.000Z", "2026-09-01T09:00:00.000Z"),
				},
			},
			wantValid: false,
			wantField: "options.0.endAt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if tt.wantValid {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want a validation error")
			}
			fields := fieldsOf(t, err)
			if _, ok := fields[tt.wantField]; !ok {
				t.Errorf("Fields = %v, want key %q present", fields, tt.wantField)
			}
		})
	}
}

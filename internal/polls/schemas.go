package polls

import (
	"fmt"
	"regexp"
	"strings"
)

// Limits ported from src/server/polls/schemas.ts's LIMITS (only the ones Task 2's poll-level
// input needs — participant/comment limits belong to Task 3).
const (
	LimitTitle       = 200
	LimitDescription = 2000
	LimitLocation    = 200
	LimitOptions     = 100
	LimitOptionLabel = 100
)

// PollType mirrors src/server/db/schema's PollType ('datetime' | 'options' | 'signup').
type PollType string

const (
	PollTypeDatetime PollType = "datetime"
	PollTypeOptions  PollType = "options"
	PollTypeSignup   PollType = "signup"
)

// OptionKind mirrors OptionKind from the TS schema ('date' | 'datetime' | 'text').
type OptionKind string

const (
	OptionKindDate     OptionKind = "date"
	OptionKindDatetime OptionKind = "datetime"
	OptionKindText     OptionKind = "text"
)

var (
	dateOnlyRegexp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	// isoDateTimeRegexp ports zod's z.iso.datetime() default shape: UTC only ("Z" suffix, no
	// numeric offset), optional fractional seconds.
	isoDateTimeRegexp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`)
)

// OptionInput ports optionInputSchema's discriminated union (schemas.ts). Only the fields that
// apply to Kind are read by the service; the others are zero-valued.
//
// Capacity is a 3-state field ("omitted" / explicit null / a number), because optionRowFields
// (service.ts) treats "omitted" and "explicit null" differently for a signup poll: omitted keeps
// whatever capacity a retained option already had (or defaults to 1 for a brand-new option),
// while explicit null clears it to "unlimited". CapacitySet distinguishes the two: false means
// omitted; true with Capacity == nil means explicit null; true with Capacity != nil is a number.
type OptionInput struct {
	// ID identifies an existing poll_options row to update in place (Update only); empty means
	// "this is a new option" (Create always leaves this empty).
	ID          string
	Kind        OptionKind
	Date        string  // kind == date: "YYYY-MM-DD"
	StartAt     string  // kind == datetime: ISO 8601 UTC datetime
	EndAt       *string // kind == datetime, optional: ISO 8601 UTC datetime
	Label       string  // kind == text
	CapacitySet bool
	Capacity    *int
}

// PollSettingsInput mirrors pollSettingsSchema.partial() — every field optional, applied to both
// CreatePollInput and UpdatePollInput.
type PollSettingsInput struct {
	RequireParticipantEmail *bool
	AllowComments           *bool
	AllowIfNeedBe           *bool
}

// CreatePollInput ports createPollSchema.
type CreatePollInput struct {
	Type            PollType
	Title           string
	Description     *string
	Location        *string
	Timezone        string
	DeadlineAt      *string // ISO 8601 UTC datetime; nil = no deadline
	Options         []OptionInput
	SignupMaxClaims *int
	PollSettingsInput
}

// Validate ports createPollSchema's shape + refinePollOptions + the signupMaxClaims-only-for-
// signup superRefine check.
func (in CreatePollInput) Validate() error {
	fields := map[string]string{}

	switch in.Type {
	case PollTypeDatetime, PollTypeOptions, PollTypeSignup:
	default:
		fields["type"] = "type must be one of datetime, options, signup"
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		fields["title"] = "title is required"
	} else if len(title) > LimitTitle {
		fields["title"] = fmt.Sprintf("title must be at most %d characters", LimitTitle)
	}

	if in.Description != nil && len(strings.TrimSpace(*in.Description)) > LimitDescription {
		fields["description"] = fmt.Sprintf("description must be at most %d characters", LimitDescription)
	}
	if in.Location != nil && len(strings.TrimSpace(*in.Location)) > LimitLocation {
		fields["location"] = fmt.Sprintf("location must be at most %d characters", LimitLocation)
	}

	if len(in.Timezone) < 1 || len(in.Timezone) > 64 {
		fields["timezone"] = "timezone must be 1-64 characters"
	}

	if in.DeadlineAt != nil && !isoDateTimeRegexp.MatchString(*in.DeadlineAt) {
		fields["deadlineAt"] = "deadlineAt must be an ISO 8601 UTC datetime"
	}

	validateOptionsField(in.Options, fields)
	refinePollOptions(string(in.Type), in.Options, fields)

	if in.SignupMaxClaims != nil {
		if *in.SignupMaxClaims < 1 || *in.SignupMaxClaims > 100 {
			fields["signupMaxClaims"] = "signupMaxClaims must be between 1 and 100"
		}
		if in.Type != PollTypeSignup {
			fields["signupMaxClaims"] = "signupMaxClaims is only allowed for sign-up sheets"
		}
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

// UpdatePollInput ports updatePollSchema (createPollBase.omit({type:true}).partial(), plus the
// pollId field the brief's Update signature carries separately rather than on this struct). A nil
// pointer field means "omitted" (leave unchanged) for every field except DeadlineAt, which needs a
// third state — see DeadlineAtSet's doc comment.
type UpdatePollInput struct {
	Title       *string
	Description *string
	Location    *string
	Timezone    *string

	// DeadlineAtSet distinguishes "omitted" (false: leave the deadline unchanged) from "provided"
	// (true): when true, DeadlineAt == nil means "clear the deadline", non-nil is the new value.
	// Ports deadlineAt's z.iso.datetime().nullable().optional() 3-state.
	DeadlineAtSet bool
	DeadlineAt    *string

	Options         []OptionInput // nil = omitted (options unchanged); non-nil = full replacement
	SignupMaxClaims *int
	PollSettingsInput
}

// Validate ports updatePollSchema's own constraints: refinePollOptions runs with an undefined
// type (so it only enforces endAt>startAt and duplicate detection — the kind/capacity-vs-type
// checks are gated on a known type in the TS source and never fire here), and every other field
// simply validates when present since "type" itself can't be changed by an update.
func (in UpdatePollInput) Validate() error {
	fields := map[string]string{}

	if in.Title != nil {
		title := strings.TrimSpace(*in.Title)
		if title == "" {
			fields["title"] = "title is required"
		} else if len(title) > LimitTitle {
			fields["title"] = fmt.Sprintf("title must be at most %d characters", LimitTitle)
		}
	}
	if in.Description != nil && len(strings.TrimSpace(*in.Description)) > LimitDescription {
		fields["description"] = fmt.Sprintf("description must be at most %d characters", LimitDescription)
	}
	if in.Location != nil && len(strings.TrimSpace(*in.Location)) > LimitLocation {
		fields["location"] = fmt.Sprintf("location must be at most %d characters", LimitLocation)
	}
	if in.Timezone != nil && (len(*in.Timezone) < 1 || len(*in.Timezone) > 64) {
		fields["timezone"] = "timezone must be 1-64 characters"
	}
	if in.DeadlineAtSet && in.DeadlineAt != nil && !isoDateTimeRegexp.MatchString(*in.DeadlineAt) {
		fields["deadlineAt"] = "deadlineAt must be an ISO 8601 UTC datetime"
	}
	if in.SignupMaxClaims != nil && (*in.SignupMaxClaims < 1 || *in.SignupMaxClaims > 100) {
		fields["signupMaxClaims"] = "signupMaxClaims must be between 1 and 100"
	}

	if in.Options != nil {
		validateOptionsField(in.Options, fields)
		refinePollOptions("", in.Options, fields)
	}

	if len(fields) > 0 {
		return &ValidationError{Fields: fields}
	}
	return nil
}

// validateOptionsField checks the options array's own count bound and each option's per-kind
// shape (the discriminated-union member schemas in optionInputSchema).
func validateOptionsField(options []OptionInput, fields map[string]string) {
	if len(options) == 0 {
		fields["options"] = "at least one option is required"
		return
	}
	if len(options) > LimitOptions {
		fields["options"] = fmt.Sprintf("at most %d options allowed", LimitOptions)
	}
	for i, opt := range options {
		validateOptionShape(i, opt, fields)
	}
}

func validateOptionShape(i int, opt OptionInput, fields map[string]string) {
	prefix := fmt.Sprintf("options.%d", i)

	switch opt.Kind {
	case OptionKindDate:
		if !dateOnlyRegexp.MatchString(opt.Date) {
			fields[prefix+".date"] = "date must be formatted YYYY-MM-DD"
		}
	case OptionKindDatetime:
		if !isoDateTimeRegexp.MatchString(opt.StartAt) {
			fields[prefix+".startAt"] = "startAt must be an ISO 8601 UTC datetime"
		}
		if opt.EndAt != nil && *opt.EndAt != "" && !isoDateTimeRegexp.MatchString(*opt.EndAt) {
			fields[prefix+".endAt"] = "endAt must be an ISO 8601 UTC datetime"
		}
	case OptionKindText:
		label := strings.TrimSpace(opt.Label)
		if label == "" {
			fields[prefix+".label"] = "label is required"
		} else if len(label) > LimitOptionLabel {
			fields[prefix+".label"] = fmt.Sprintf("label must be at most %d characters", LimitOptionLabel)
		}
	default:
		fields[prefix+".kind"] = "kind must be one of date, datetime, text"
	}

	if opt.CapacitySet && opt.Capacity != nil && (*opt.Capacity < 1 || *opt.Capacity > 10000) {
		fields[prefix+".capacity"] = "capacity must be between 1 and 10000"
	}
}

// refinePollOptions ports refinePollOptions from schemas.ts. pollType == "" mirrors TS's `type ===
// undefined` (updatePollSchema's call site): the kind-must-match-poll-type, signup-mixed-kind, and
// capacity-only-on-signup checks all short-circuit, leaving only the endAt>startAt and
// duplicate-option checks active.
func refinePollOptions(pollType string, options []OptionInput, fields map[string]string) {
	seenKeys := map[string]bool{}
	var signupKind OptionKind
	signupKindSet := false

	for i, opt := range options {
		prefix := fmt.Sprintf("options.%d", i)

		if pollType == string(PollTypeDatetime) && opt.Kind != OptionKindDate && opt.Kind != OptionKindDatetime {
			fields[prefix] = "options for a datetime poll must be dates or date/times"
		}
		if pollType == string(PollTypeOptions) && opt.Kind != OptionKindText {
			fields[prefix] = "options for an options poll must be text"
		}
		if pollType == string(PollTypeSignup) {
			if !signupKindSet {
				signupKind = opt.Kind
				signupKindSet = true
			} else if opt.Kind != signupKind {
				fields[prefix] = "sign-up sheet options must all be the same kind"
			}
		}
		if pollType != "" && pollType != string(PollTypeSignup) && opt.CapacitySet && opt.Capacity != nil {
			fields[prefix+".capacity"] = "capacity is only allowed on sign-up sheet options"
		}

		if opt.Kind == OptionKindDatetime && opt.EndAt != nil && *opt.EndAt != "" {
			end, endErr := parseISODateTime(*opt.EndAt)
			start, startErr := parseISODateTime(opt.StartAt)
			if endErr == nil && startErr == nil && !end.After(start) {
				fields[prefix+".endAt"] = "endAt must be after startAt"
			}
		}

		var key string
		switch opt.Kind {
		case OptionKindDate:
			key = "date:" + opt.Date
		case OptionKindDatetime:
			endPart := ""
			if opt.EndAt != nil {
				endPart = *opt.EndAt
			}
			key = "datetime:" + opt.StartAt + "|" + endPart
		default:
			key = "text:" + strings.ToLower(strings.TrimSpace(opt.Label))
		}
		if seenKeys[key] {
			fields[prefix] = "duplicate option"
		} else {
			seenKeys[key] = true
		}
	}
}

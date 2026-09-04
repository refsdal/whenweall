package polls

// The types below mirror src/server/polls/viewmodel.ts field-for-field; JSON tags are camelCase
// to match what the frontend (plan 8) already expects from the TS API.

// PollOptionView mirrors viewmodel.ts's PollOptionView.
type PollOptionView struct {
	ID       string  `json:"id"`
	Position int32   `json:"position"`
	Kind     string  `json:"kind"`
	StartAt  *string `json:"startAt"`
	EndAt    *string `json:"endAt"`
	Label    *string `json:"label"`
	Capacity *int32  `json:"capacity"`
}

// ParticipantView mirrors viewmodel.ts's ParticipantView. Votes maps optionID -> answer
// ("yes"|"ifneedbe"|"no").
type ParticipantView struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	UserID    *string           `json:"userId"`
	HasEmail  bool              `json:"hasEmail"`
	Votes     map[string]string `json:"votes"`
	CreatedAt string            `json:"createdAt"`
}

// CommentView mirrors viewmodel.ts's CommentView.
type CommentView struct {
	ID            string  `json:"id"`
	AuthorName    string  `json:"authorName"`
	Body          string  `json:"body"`
	CreatedAt     string  `json:"createdAt"`
	UserID        *string `json:"userId"`
	ParticipantID *string `json:"participantId"`
}

// PollSettingsView mirrors PollView['settings'].
type PollSettingsView struct {
	RequireParticipantEmail bool  `json:"requireParticipantEmail"`
	AllowComments           bool  `json:"allowComments"`
	AllowIfNeedBe           bool  `json:"allowIfNeedBe"`
	SignupMaxClaims         int32 `json:"signupMaxClaims"`
}

// NotificationsView mirrors PollView['notifications'] — populated by buildView (view_builder.go)
// for any org member (nil for a non-member/anonymous viewer), from notification_subscriptions
// (Channels/Following) and notification_prefs (Defaults); see buildView's own doc comment.
type NotificationsView struct {
	Channels  map[string]any `json:"channels"`
	Defaults  map[string]any `json:"defaults"`
	Following bool           `json:"following"`
}

// PollOwnerView mirrors PollView['owner'] — deliberately just a name, no id: this view is public
// (any participant/viewer sees it) and nothing in the client reads the org id.
type PollOwnerView struct {
	Name string `json:"name"`
}

// OptionScore mirrors src/lib/scoring.ts's OptionScore.
type OptionScore struct {
	Yes      int `json:"yes"`
	IfNeedBe int `json:"ifneedbe"`
	No       int `json:"no"`
	Score    int `json:"score"`
}

// ClaimView mirrors one entry of PollView['claims'].
type ClaimView struct {
	Count    int    `json:"count"`
	Capacity *int32 `json:"capacity"`
	Full     bool   `json:"full"`
}

// PollView mirrors viewmodel.ts's PollView.
type PollView struct {
	ID                string                 `json:"id"`
	Type              string                 `json:"type"`
	Title             string                 `json:"title"`
	Description       *string                `json:"description"`
	Location          *string                `json:"location"`
	Timezone          string                 `json:"timezone"`
	Status            string                 `json:"status"`
	DeadlineAt        *string                `json:"deadlineAt"`
	FinalizedOptionID *string                `json:"finalizedOptionId"`
	CreatedAt         string                 `json:"createdAt"`
	Settings          PollSettingsView       `json:"settings"`
	Notifications     *NotificationsView     `json:"notifications"`
	Owner             PollOwnerView          `json:"owner"`
	IsOwner           bool                   `json:"isOwner"`
	Options           []PollOptionView       `json:"options"`
	Participants      []ParticipantView      `json:"participants"`
	Comments          []CommentView          `json:"comments"`
	Scores            map[string]OptionScore `json:"scores"`
	BestOptionID      *string                `json:"bestOptionId"`
	Claims            map[string]ClaimView   `json:"claims"`
}

// PollSummary mirrors viewmodel.ts's PollSummary.
type PollSummary struct {
	ID               string  `json:"id"`
	Title            string  `json:"title"`
	Type             string  `json:"type"`
	Status           string  `json:"status"`
	DeadlineAt       *string `json:"deadlineAt"`
	ParticipantCount int     `json:"participantCount"`
	ClaimCount       int     `json:"claimCount"`
	CreatedAt        string  `json:"createdAt"`
	UpdatedAt        string  `json:"updatedAt"`
}

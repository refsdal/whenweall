package bookings

// The types below mirror src/server/bookings/viewmodel.ts field-for-field; JSON tags are camelCase
// to match what the frontend (plan 8) already expects from the TS API.

// PageSummary mirrors viewmodel.ts's PageSummary.
type PageSummary struct {
	ID            string `json:"id"`
	Slug          string `json:"slug"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	UpcomingCount int    `json:"upcomingCount"`
	CreatedAt     string `json:"createdAt"`
	UpdatedAt     string `json:"updatedAt"`
}

// PageView mirrors viewmodel.ts's PageView — full page detail as seen by its owner.
type PageView struct {
	ID              string        `json:"id"`
	Slug            string        `json:"slug"`
	Title           string        `json:"title"`
	Description     *string       `json:"description"`
	Location        *string       `json:"location"`
	Timezone        string        `json:"timezone"`
	SlotDurationMin int           `json:"slotDurationMin"`
	BufferBeforeMin int           `json:"bufferBeforeMin"`
	BufferAfterMin  int           `json:"bufferAfterMin"`
	MinNoticeMin    int           `json:"minNoticeMin"`
	MaxDaysAhead    int           `json:"maxDaysAhead"`
	Availability    Availability  `json:"availability"`
	DateOverrides   DateOverrides `json:"dateOverrides"`
	GoogleSync      bool          `json:"googleSync"`
	Reminders       bool          `json:"reminders"`
	Status          string        `json:"status"`
	CreatedAt       string        `json:"createdAt"`
	UpdatedAt       string        `json:"updatedAt"`
}

// PublicPageOwnerView mirrors PublicPageView['owner'] — deliberately just a name, no id: this view
// is public and nothing in the client reads the org id.
type PublicPageOwnerView struct {
	Name string `json:"name"`
}

// PublicPageView mirrors viewmodel.ts's PublicPageView — what a visitor sees at
// `/book/<handle>/<slug>`, trimmed to exactly the fields the public client renders. No owner id, no
// email, and — unlike PageView — no availability/dateOverrides/buffers/minNotice: slot generation
// runs server-side against the raw page row (Task 3), so those scheduling rules never reach the
// browser.
type PublicPageView struct {
	ID              string              `json:"id"`
	Handle          string              `json:"handle"`
	Slug            string              `json:"slug"`
	Title           string              `json:"title"`
	Description     *string             `json:"description"`
	Location        *string             `json:"location"`
	Timezone        string              `json:"timezone"`
	SlotDurationMin int                 `json:"slotDurationMin"`
	MaxDaysAhead    int                 `json:"maxDaysAhead"`
	Status          string              `json:"status"`
	Owner           PublicPageOwnerView `json:"owner"`
}

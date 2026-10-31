// Package dataexport builds the self-service copy of a user's personal data (#50): GDPR Art. 15
// (access) and Art. 20 (portability), served as one JSON document by GET /api/v1/me/export
// (internal/httpserver/account.go).
//
// What goes in, and what deliberately does not:
//
//   - In full: the account itself, its sign-in methods and sessions (timestamps only), its
//     organizations and roles, invitations addressed to it, notification settings, an e-mail
//     unsubscribe if any, and staff actions taken on the account (action, reason, time — not which
//     staff member; docs/admin-console.md tells staff the reason is shown to the user).
//   - In full: every poll, sign-up sheet and booking page the user created — titles, options,
//     settings, availability, timestamps — including ones deleted or suspended but not yet purged,
//     since we still hold them.
//   - As counts only: other people's responses, comments and bookings on that content. They are
//     third parties' personal data (Art. 20(4)); the organiser can already see them in the app, so
//     nothing is withheld, but a portable file full of other people's names and addresses would be
//     a data-protection incident the moment an account was compromised.
//   - In full: the user's own participation anywhere — their answers, comments and bookings —
//     matched by user id AND by their e-mail address, because a guest answer or a booking made
//     before signing up carries only the address. That match is why the route requires a VERIFIED
//     session: an unproven address must never unlock someone else's guest data. Anyone can type
//     an address into a guest form, so this can pull in answers a stranger left under the user's
//     address — data the stranger chose to attach to it, nothing of anyone else's. Rows tied to
//     another account are never claimed by address (ownParticipants).
//   - Never: password hashes, OAuth tokens, session tokens, invitation tokens, guest edit-token
//     hashes or booking manage-token hashes.
package dataexport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Format identifies the document's shape, so a consumer can tell versions apart if it changes.
const Format = "whenweall-export/1"

// ErrNoSuchUser is returned by Build when userID names no account.
var ErrNoSuchUser = errors.New("dataexport: no such user")

type Export struct {
	Format              string           `json:"format"`
	ExportedAt          time.Time        `json:"exportedAt"`
	Account             Account          `json:"account"`
	SignInMethods       []SignInMethod   `json:"signInMethods"`
	Sessions            []Session        `json:"sessions"`
	Organizations       []Organization   `json:"organizations"`
	InvitationsReceived []Invitation     `json:"invitationsReceived"`
	Notifications       Notifications    `json:"notifications"`
	PollsCreated        []CreatedPoll    `json:"pollsCreated"`
	BookingPagesCreated []CreatedPage    `json:"bookingPagesCreated"`
	PollResponses       []PollResponse   `json:"pollResponses"`
	Comments            []Comment        `json:"comments"`
	BookingsMade        []VisitorBooking `json:"bookingsMade"`
	StaffActions        []StaffAction    `json:"staffActions"`
}

type Account struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	FirstName       *string    `json:"firstName"`
	LastName        *string    `json:"lastName"`
	Locale          *string    `json:"locale"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type SignInMethod struct {
	Provider  string    `json:"provider"`
	CreatedAt time.Time `json:"createdAt"`
}

type Session struct {
	CreatedAt  time.Time `json:"createdAt"`
	LastAccess time.Time `json:"lastAccess"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type Organization struct {
	Name     string    `json:"name"`
	Slug     string    `json:"slug"`
	Roles    []string  `json:"roles"`
	JoinedAt time.Time `json:"joinedAt"`
}

type Invitation struct {
	Organization string     `json:"organization"`
	Roles        *string    `json:"roles"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"createdAt"`
	ExpiresAt    *time.Time `json:"expiresAt"`
}

type Notifications struct {
	Preferences   json.RawMessage `json:"preferences"`
	Subscriptions []Subscription  `json:"subscriptions"`
	// UnsubscribedAt is set when the user's address is on the notification-mail suppression list.
	UnsubscribedAt *time.Time `json:"unsubscribedAt"`
}

type Subscription struct {
	ScopeType string          `json:"scopeType"`
	ScopeID   string          `json:"scopeId"`
	Source    string          `json:"source"`
	Channels  json.RawMessage `json:"channels"`
	CreatedAt time.Time       `json:"createdAt"`
}

type CreatedPoll struct {
	ID                      string          `json:"id"`
	Type                    string          `json:"type"`
	Title                   string          `json:"title"`
	Description             *string         `json:"description"`
	Location                *string         `json:"location"`
	Timezone                string          `json:"timezone"`
	Status                  string          `json:"status"`
	DeadlineAt              *time.Time      `json:"deadlineAt"`
	RequireParticipantEmail bool            `json:"requireParticipantEmail"`
	AllowComments           bool            `json:"allowComments"`
	AllowIfNeedBe           bool            `json:"allowIfNeedBe"`
	SignupMaxClaims         int             `json:"signupMaxClaims"`
	CreatedAt               time.Time       `json:"createdAt"`
	UpdatedAt               time.Time       `json:"updatedAt"`
	DeletedAt               *time.Time      `json:"deletedAt"`
	SuspendedAt             *time.Time      `json:"suspendedAt"`
	ParticipantCount        int             `json:"participantCount"`
	CommentCount            int             `json:"commentCount"`
	Options                 []CreatedOption `json:"options"`
}

// CreatedOption carries answer counts, never who gave them.
type CreatedOption struct {
	Label         *string    `json:"label"`
	StartAt       *time.Time `json:"startAt"`
	EndAt         *time.Time `json:"endAt"`
	Capacity      *int       `json:"capacity"`
	YesCount      int        `json:"yesCount"`
	IfNeedBeCount int        `json:"ifNeedBeCount"`
	NoCount       int        `json:"noCount"`
}

type CreatedPage struct {
	ID              string          `json:"id"`
	Slug            string          `json:"slug"`
	Title           string          `json:"title"`
	Description     *string         `json:"description"`
	Location        *string         `json:"location"`
	Timezone        string          `json:"timezone"`
	SlotDurationMin int             `json:"slotDurationMin"`
	BufferBeforeMin int             `json:"bufferBeforeMin"`
	BufferAfterMin  int             `json:"bufferAfterMin"`
	MinNoticeMin    int             `json:"minNoticeMin"`
	MaxDaysAhead    int             `json:"maxDaysAhead"`
	Availability    json.RawMessage `json:"availability"`
	DateOverrides   json.RawMessage `json:"dateOverrides"`
	Reminders       bool            `json:"reminders"`
	Status          string          `json:"status"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	DeletedAt       *time.Time      `json:"deletedAt"`
	SuspendedAt     *time.Time      `json:"suspendedAt"`
	BookingCount    int             `json:"bookingCount"`
	CancelledCount  int             `json:"cancelledCount"`
}

type PollResponse struct {
	PollID    string    `json:"pollId"`
	PollTitle string    `json:"pollTitle"`
	Name      string    `json:"name"`
	Email     *string   `json:"email"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Answers   []Answer  `json:"answers"`
}

type Answer struct {
	Label   *string    `json:"label"`
	StartAt *time.Time `json:"startAt"`
	EndAt   *time.Time `json:"endAt"`
	Answer  string     `json:"answer"`
}

type Comment struct {
	PollID     string     `json:"pollId"`
	PollTitle  string     `json:"pollTitle"`
	AuthorName string     `json:"authorName"`
	Body       string     `json:"body"`
	CreatedAt  time.Time  `json:"createdAt"`
	DeletedAt  *time.Time `json:"deletedAt"`
}

type VisitorBooking struct {
	PageTitle       string    `json:"pageTitle"`
	StartAt         time.Time `json:"startAt"`
	EndAt           time.Time `json:"endAt"`
	VisitorName     string    `json:"visitorName"`
	VisitorEmail    string    `json:"visitorEmail"`
	VisitorNote     *string   `json:"visitorNote"`
	VisitorLocale   *string   `json:"visitorLocale"`
	VisitorTimezone string    `json:"visitorTimezone"`
	Status          string    `json:"status"`
	CancelledBy     *string   `json:"cancelledBy"`
	CreatedAt       time.Time `json:"createdAt"`
}

type StaffAction struct {
	Action    string    `json:"action"`
	Reason    *string   `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
}

// Build assembles userID's export. It runs as a series of reads in one REPEATABLE READ, read-only
// transaction so the document is a consistent snapshot even while the user keeps using the app.
func Build(ctx context.Context, sqlDB *sql.DB, userID string) (*Export, error) {
	tx, err := sqlDB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	e := &Export{Format: Format, ExportedAt: time.Now().UTC()}
	if err := readAccount(ctx, tx, userID, &e.Account); err != nil {
		return nil, err
	}
	// Guest answers and bookings carry the address as typed; compare normalised on both sides.
	email := strings.ToLower(strings.TrimSpace(e.Account.Email))

	steps := []func() error{
		func() (err error) { e.SignInMethods, err = readSignInMethods(ctx, tx, userID); return },
		func() (err error) { e.Sessions, err = readSessions(ctx, tx, userID); return },
		func() (err error) { e.Organizations, err = readOrganizations(ctx, tx, userID); return },
		func() (err error) { e.InvitationsReceived, err = readInvitations(ctx, tx, email); return },
		func() error { return readNotifications(ctx, tx, userID, email, &e.Notifications) },
		func() (err error) { e.PollsCreated, err = readCreatedPolls(ctx, tx, userID); return },
		func() (err error) { e.BookingPagesCreated, err = readCreatedPages(ctx, tx, userID); return },
		func() (err error) { e.PollResponses, err = readPollResponses(ctx, tx, userID, email); return },
		func() (err error) { e.Comments, err = readComments(ctx, tx, userID, email); return },
		func() (err error) { e.BookingsMade, err = readBookingsMade(ctx, tx, email); return },
		func() (err error) { e.StaffActions, err = readStaffActions(ctx, tx, userID); return },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func readAccount(ctx context.Context, tx *sql.Tx, userID string, a *Account) error {
	var first, last, locale sql.NullString
	var verified sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT u.id::text, u.email, u.first_name, u.last_name, p.locale, u.email_verified_at, u.created_at
		FROM users u LEFT JOIN user_preferences p ON p.user_id = u.id
		WHERE u.id = $1`, userID,
	).Scan(&a.ID, &a.Email, &first, &last, &locale, &verified, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoSuchUser
	}
	if err != nil {
		return err
	}
	a.FirstName, a.LastName, a.Locale, a.EmailVerifiedAt = str(first), str(last), str(locale), tm(verified)
	return nil
}

func readSignInMethods(ctx context.Context, tx *sql.Tx, userID string) ([]SignInMethod, error) {
	// A password lives on users itself (Limen keeps only OAuth links in accounts), so it is listed
	// as the "password" method here — that one exists, never the hash.
	return collect(ctx, tx, `
		SELECT 'password', created_at FROM users WHERE id = $1 AND password IS NOT NULL
		UNION ALL
		SELECT provider, created_at FROM accounts WHERE user_id = $1
		ORDER BY 2`,
		[]any{userID}, func(rows *sql.Rows) (SignInMethod, error) {
			var m SignInMethod
			return m, rows.Scan(&m.Provider, &m.CreatedAt)
		})
}

func readSessions(ctx context.Context, tx *sql.Tx, userID string) ([]Session, error) {
	return collect(ctx, tx, `
		SELECT created_at, last_access, expires_at FROM sessions WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows *sql.Rows) (Session, error) {
			var s Session
			return s, rows.Scan(&s.CreatedAt, &s.LastAccess, &s.ExpiresAt)
		})
}

func readOrganizations(ctx context.Context, tx *sql.Tx, userID string) ([]Organization, error) {
	return collect(ctx, tx, `
		SELECT o.name, o.slug, coalesce(string_agg(r.role, ',' ORDER BY r.role), ''), m.created_at
		FROM organization_members m
		JOIN organizations o ON o.id = m.organization_id
		LEFT JOIN organization_member_roles r ON r.member_id = m.id
		WHERE m.user_id = $1
		GROUP BY o.id, m.id
		ORDER BY m.created_at`,
		[]any{userID}, func(rows *sql.Rows) (Organization, error) {
			var o Organization
			var roles string
			err := rows.Scan(&o.Name, &o.Slug, &roles, &o.JoinedAt)
			o.Roles = []string{}
			if roles != "" {
				o.Roles = strings.Split(roles, ",")
			}
			return o, err
		})
}

func readInvitations(ctx context.Context, tx *sql.Tx, email string) ([]Invitation, error) {
	return collect(ctx, tx, `
		SELECT o.name, i.roles, i.status, i.created_at, i.expires_at
		FROM organization_invitations i JOIN organizations o ON o.id = i.organization_id
		WHERE lower(trim(i.email)) = $1
		ORDER BY i.created_at`,
		[]any{email}, func(rows *sql.Rows) (Invitation, error) {
			var inv Invitation
			var roles sql.NullString
			var expires sql.NullTime
			err := rows.Scan(&inv.Organization, &roles, &inv.Status, &inv.CreatedAt, &expires)
			inv.Roles, inv.ExpiresAt = str(roles), tm(expires)
			return inv, err
		})
}

func readNotifications(ctx context.Context, tx *sql.Tx, userID, email string, n *Notifications) error {
	var prefs []byte
	err := tx.QueryRowContext(ctx, `SELECT channels FROM notification_prefs WHERE user_id = $1`, userID).Scan(&prefs)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	n.Preferences = rawJSON(prefs)

	n.Subscriptions, err = collect(ctx, tx, `
		SELECT scope_type, scope_id, source, channels, created_at
		FROM notification_subscriptions WHERE user_id = $1 ORDER BY created_at`,
		[]any{userID}, func(rows *sql.Rows) (Subscription, error) {
			var s Subscription
			var channels []byte
			err := rows.Scan(&s.ScopeType, &s.ScopeID, &s.Source, &channels, &s.CreatedAt)
			s.Channels = rawJSON(channels)
			return s, err
		})
	if err != nil {
		return err
	}

	var at time.Time
	err = tx.QueryRowContext(ctx, `SELECT created_at FROM mail_unsubscribes WHERE email = $1`, email).Scan(&at)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	}
	n.UnsubscribedAt = &at
	return nil
}

func readCreatedPolls(ctx context.Context, tx *sql.Tx, userID string) ([]CreatedPoll, error) {
	polls, err := collect(ctx, tx, `
		SELECT p.id, p.type, p.title, p.description, p.location, p.timezone, p.status, p.deadline_at,
			p.require_participant_email, p.allow_comments, p.allow_if_need_be, p.signup_max_claims,
			p.created_at, p.updated_at, p.deleted_at, p.suspended_at,
			(SELECT count(*) FROM participants pa WHERE pa.poll_id = p.id),
			(SELECT count(*) FROM comments c WHERE c.poll_id = p.id AND c.deleted_at IS NULL)
		FROM polls p WHERE p.created_by = $1
		ORDER BY p.created_at`,
		[]any{userID}, func(rows *sql.Rows) (CreatedPoll, error) {
			var p CreatedPoll
			var desc, loc sql.NullString
			var deadline, deleted, suspended sql.NullTime
			err := rows.Scan(&p.ID, &p.Type, &p.Title, &desc, &loc, &p.Timezone, &p.Status, &deadline,
				&p.RequireParticipantEmail, &p.AllowComments, &p.AllowIfNeedBe, &p.SignupMaxClaims,
				&p.CreatedAt, &p.UpdatedAt, &deleted, &suspended, &p.ParticipantCount, &p.CommentCount)
			p.Description, p.Location = str(desc), str(loc)
			p.DeadlineAt, p.DeletedAt, p.SuspendedAt = tm(deadline), tm(deleted), tm(suspended)
			p.Options = []CreatedOption{}
			return p, err
		})
	if err != nil || len(polls) == 0 {
		return polls, err
	}

	byPoll := make(map[string]*CreatedPoll, len(polls))
	for i := range polls {
		byPoll[polls[i].ID] = &polls[i]
	}
	type optionRow struct {
		pollID string
		opt    CreatedOption
	}
	options, err := collect(ctx, tx, `
		SELECT o.poll_id, o.label, o.start_at, o.end_at, o.capacity,
			count(*) FILTER (WHERE v.answer = 'yes'),
			count(*) FILTER (WHERE v.answer = 'ifneedbe'),
			count(*) FILTER (WHERE v.answer = 'no')
		FROM poll_options o
		JOIN polls p ON p.id = o.poll_id
		LEFT JOIN votes v ON v.option_id = o.id
		WHERE p.created_by = $1
		GROUP BY o.id
		ORDER BY o.poll_id, o.position`,
		[]any{userID}, func(rows *sql.Rows) (optionRow, error) {
			var r optionRow
			var label sql.NullString
			var start, end sql.NullTime
			var capacity sql.NullInt64
			err := rows.Scan(&r.pollID, &label, &start, &end, &capacity,
				&r.opt.YesCount, &r.opt.IfNeedBeCount, &r.opt.NoCount)
			r.opt.Label, r.opt.StartAt, r.opt.EndAt = str(label), tm(start), tm(end)
			if capacity.Valid {
				c := int(capacity.Int64)
				r.opt.Capacity = &c
			}
			return r, err
		})
	if err != nil {
		return nil, err
	}
	for _, r := range options {
		if p := byPoll[r.pollID]; p != nil {
			p.Options = append(p.Options, r.opt)
		}
	}
	return polls, nil
}

func readCreatedPages(ctx context.Context, tx *sql.Tx, userID string) ([]CreatedPage, error) {
	return collect(ctx, tx, `
		SELECT bp.id, bp.slug, bp.title, bp.description, bp.location, bp.timezone, bp.slot_duration_min,
			bp.buffer_before_min, bp.buffer_after_min, bp.min_notice_min, bp.max_days_ahead,
			bp.availability, bp.date_overrides, bp.reminders, bp.status,
			bp.created_at, bp.updated_at, bp.deleted_at, bp.suspended_at,
			(SELECT count(*) FROM bookings b WHERE b.page_id = bp.id),
			(SELECT count(*) FROM bookings b WHERE b.page_id = bp.id AND b.status = 'cancelled')
		FROM booking_pages bp WHERE bp.created_by = $1
		ORDER BY bp.created_at`,
		[]any{userID}, func(rows *sql.Rows) (CreatedPage, error) {
			var p CreatedPage
			var desc, loc sql.NullString
			var availability, overrides []byte
			var deleted, suspended sql.NullTime
			err := rows.Scan(&p.ID, &p.Slug, &p.Title, &desc, &loc, &p.Timezone, &p.SlotDurationMin,
				&p.BufferBeforeMin, &p.BufferAfterMin, &p.MinNoticeMin, &p.MaxDaysAhead,
				&availability, &overrides, &p.Reminders, &p.Status,
				&p.CreatedAt, &p.UpdatedAt, &deleted, &suspended, &p.BookingCount, &p.CancelledCount)
			p.Description, p.Location = str(desc), str(loc)
			p.Availability, p.DateOverrides = rawJSON(availability), rawJSON(overrides)
			p.DeletedAt, p.SuspendedAt = tm(deleted), tm(suspended)
			return p, err
		})
}

// ownParticipants is the set of participant rows that are the user's own: signed-in answers
// (user_id) and GUEST answers left with their verified address. The e-mail match only claims rows
// that belong to no account — the address on a participant row is free text, so another signed-in
// user who typed this user's address made a row that is that other account's data, not this one's.
// (A row left behind by a since-deleted account has user_id NULLed and does match, which is no
// different from a guest typing the address.) Spliced into the queries that need it; $1 is the
// user id and $2 the normalised e-mail in each.
const ownParticipants = `SELECT id FROM participants
	WHERE user_id = $1 OR (user_id IS NULL AND lower(trim(email)) = $2)`

func readPollResponses(ctx context.Context, tx *sql.Tx, userID, email string) ([]PollResponse, error) {
	responses, err := collect(ctx, tx, `
		SELECT pa.id, p.id, p.title, pa.name, pa.email, pa.created_at, pa.updated_at
		FROM participants pa JOIN polls p ON p.id = pa.poll_id
		WHERE pa.id IN (`+ownParticipants+`)
		ORDER BY pa.created_at`,
		[]any{userID, email}, func(rows *sql.Rows) (struct {
			id string
			r  PollResponse
		}, error) {
			var row struct {
				id string
				r  PollResponse
			}
			var mail sql.NullString
			err := rows.Scan(&row.id, &row.r.PollID, &row.r.PollTitle, &row.r.Name, &mail,
				&row.r.CreatedAt, &row.r.UpdatedAt)
			row.r.Email = str(mail)
			row.r.Answers = []Answer{}
			return row, err
		})
	if err != nil {
		return nil, err
	}

	byID := make(map[string]int, len(responses))
	out := make([]PollResponse, len(responses))
	for i, row := range responses {
		byID[row.id] = i
		out[i] = row.r
	}
	if len(out) == 0 {
		return out, nil
	}

	type answerRow struct {
		participantID string
		a             Answer
	}
	answers, err := collect(ctx, tx, `
		SELECT v.participant_id, o.label, o.start_at, o.end_at, v.answer
		FROM votes v JOIN poll_options o ON o.id = v.option_id
		WHERE v.participant_id IN (`+ownParticipants+`)
		ORDER BY v.participant_id, o.position`,
		[]any{userID, email}, func(rows *sql.Rows) (answerRow, error) {
			var r answerRow
			var label sql.NullString
			var start, end sql.NullTime
			err := rows.Scan(&r.participantID, &label, &start, &end, &r.a.Answer)
			r.a.Label, r.a.StartAt, r.a.EndAt = str(label), tm(start), tm(end)
			return r, err
		})
	if err != nil {
		return nil, err
	}
	for _, r := range answers {
		if i, ok := byID[r.participantID]; ok {
			out[i].Answers = append(out[i].Answers, r.a)
		}
	}
	return out, nil
}

func readComments(ctx context.Context, tx *sql.Tx, userID, email string) ([]Comment, error) {
	return collect(ctx, tx, `
		SELECT p.id, p.title, c.author_name, c.body, c.created_at, c.deleted_at
		FROM comments c JOIN polls p ON p.id = c.poll_id
		WHERE c.user_id = $1 OR (c.user_id IS NULL AND c.participant_id IN (`+ownParticipants+`))
		ORDER BY c.created_at`,
		[]any{userID, email}, func(rows *sql.Rows) (Comment, error) {
			var c Comment
			var deleted sql.NullTime
			err := rows.Scan(&c.PollID, &c.PollTitle, &c.AuthorName, &c.Body, &c.CreatedAt, &deleted)
			c.DeletedAt = tm(deleted)
			return c, err
		})
}

func readBookingsMade(ctx context.Context, tx *sql.Tx, email string) ([]VisitorBooking, error) {
	return collect(ctx, tx, `
		SELECT bp.title, b.start_at, b.end_at, b.visitor_name, b.visitor_email, b.visitor_note,
			b.visitor_locale, b.visitor_timezone, b.status, b.cancelled_by, b.created_at
		FROM bookings b JOIN booking_pages bp ON bp.id = b.page_id
		WHERE lower(trim(b.visitor_email)) = $1
		ORDER BY b.start_at`,
		[]any{email}, func(rows *sql.Rows) (VisitorBooking, error) {
			var b VisitorBooking
			var note, locale, cancelledBy sql.NullString
			err := rows.Scan(&b.PageTitle, &b.StartAt, &b.EndAt, &b.VisitorName, &b.VisitorEmail, &note,
				&locale, &b.VisitorTimezone, &b.Status, &cancelledBy, &b.CreatedAt)
			b.VisitorNote, b.VisitorLocale, b.CancelledBy = str(note), str(locale), str(cancelledBy)
			return b, err
		})
}

// readStaffActions lists what staff did to this account (internal/admin's audit rows targeting
// it), without saying which staff member — that is the staff member's personal data, not theirs.
func readStaffActions(ctx context.Context, tx *sql.Tx, userID string) ([]StaffAction, error) {
	return collect(ctx, tx, `
		SELECT action, reason, created_at FROM admin_audit_log
		WHERE target_type = 'user' AND target_id = $1
		ORDER BY created_at`,
		[]any{userID}, func(rows *sql.Rows) (StaffAction, error) {
			var a StaffAction
			var reason sql.NullString
			err := rows.Scan(&a.Action, &reason, &a.CreatedAt)
			a.Reason = str(reason)
			return a, err
		})
}

// collect runs query and maps every row through scan. It always returns a non-nil slice, so an
// empty section encodes as [] rather than null.
func collect[T any](ctx context.Context, tx *sql.Tx, query string, args []any, scan func(*sql.Rows) (T, error)) ([]T, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func str(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func tm(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}

// rawJSON passes a jsonb column through untouched, as null when the column is NULL.
func rawJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("null")
	}
	return json.RawMessage(b)
}

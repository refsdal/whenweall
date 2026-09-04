// Package bookings (this file, emails.go) is a behavioral port of src/server/bookings/emails.ts
// (the kind -> template map, the ids-only queue contract) fused with src/do/BookingRoom.ts's
// reminder-alarm arming — the mail half of the booking lifecycle, on top of Book/Cancel/Reschedule
// (bookings.go, Task 3).
//
// Architecture, and how it differs from the TS source:
//
//   - TS sends confirmed/cancelled/rescheduled mail INLINE on the request that triggered it
//     (bookings.functions.ts), with a separate best-effort MailJob queue (mailer/queue.ts) used
//     only to retry a failed send. This port instead routes every lifecycle send through one
//     job kind, "mail:booking" — Book/Cancel/Reschedule (bookings.go) enqueue it in the SAME
//     transaction as their domain write (mirroring internal/polls/timers.go's enqueueMailPoll),
//     and the worker's own attempt/backoff/dead-letter machinery (internal/jobs) stands in for
//     mailer/queue.ts's bespoke retry queue. A request never blocks on SMTP either way.
//
//   - Payload is ids-only (kind + bookingId + recipient), same rationale as mailer/queue.ts's
//     MailJob: never an address, and the handler re-reads the booking fresh at send time so a
//     booking cancelled between scheduling and sending is a no-op rather than a stale send (see
//     composeBookingMail's per-kind skip checks below).
//
//     ONE JOB PER RECIPIENT (visitor / organiser), like internal/polls/timers.go's one mail:poll
//     row per recipient: a job that sent both halves in sequence re-sent the visitor's already-
//     delivered mail on every retry of a failing organiser send (up to mailBookingMaxAttempts
//     times). Each row now composes exactly one mailer.Message (composeBookingMail) and the worker
//     retries only that one.
//
//     "rescheduled" also carries previousStartAt (a plain timestamp, never personal data): TS's
//     own sendBookingEmails needs it to render "moved from {previousWhen} to {when}", and it isn't
//     recoverable once the booking row has been updated.
//
//   - The organiser recipient is simplified to page.MemberUserID's account alone, falling back to
//     the org's own name (never its own mail) when unset. TS instead resolves the *booking page's*
//     notification subscribers via the notification subsystem (recipients.ts) for confirmed/
//     cancelled/rescheduled, keeping memberUserId-only just for the reminder. That subsystem has
//     no booking_page scope in this Go port yet (internal/polls/notifications.go's own scope is
//     "poll" only) — porting it is out of this task's file list, so every kind here uses the same
//     single-recipient rule the reminder already used in TS.
//
//   - A soft-deleted page is treated as "nothing to send" for every kind, not just the reminder.
//     TS's own page lookup in sendBookingEmails has no deletedAt filter (only BookingRoom's
//     reminder path checks it explicitly) — meaning TS would still send confirmed/cancelled/
//     rescheduled mail for a page deleted in the interim. This port's GetBookingPage query
//     already excludes soft-deleted rows package-wide, so re-adding an unfiltered lookup just for
//     this narrow race is not worth a second query shape; treating it uniformly as a no-op is the
//     safer simplification.
//
//   - Both mails are rendered in their recipient's own locale, as emails.ts did: the visitor's from
//     bookings.visitor_locale, the organiser's from their account preference (SetLocaleResolver →
//     auth.Service.LocaleFor, user_preferences.locale); the "When" line goes through internal/
//     mailer's FormatDate/FormatTimeRange/FormatDateTime (the Go stand-in for formatOptionLabel's
//     Intl.DateTimeFormat), so a Norwegian organiser reads "tir. 1. sep., 09:00–09:30".
package bookings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/bookings/queries"
	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/mailer"
)

const (
	jobKindMailBooking     = "mail:booking"
	jobKindBookingReminder = "booking.reminder"

	// mailBookingMaxAttempts mirrors mailer's own mailMaxAttempts / polls' mailPollMaxAttempts: an
	// SMTP hiccup is worth retrying several times, a message still failing after that is a bad
	// address or a broken relay.
	mailBookingMaxAttempts = 10

	// reminderLead mirrors REMINDER_LEAD_MS (BookingRoom.ts): how far ahead of a booking's start
	// the reminder fires.
	reminderLead = 24 * time.Hour
)

// Mail recipients — one "mail:booking" row per value (see this file's package doc comment).
const (
	mailRecipientVisitor   = "visitor"
	mailRecipientOrganiser = "organiser"
)

// mailBookingPayload is the "mail:booking" job's payload — see this file's package doc comment for
// why Recipient and previousStartAt sit alongside the ids.
type mailBookingPayload struct {
	Kind            string     `json:"kind"`
	BookingID       string     `json:"bookingId"`
	Recipient       string     `json:"recipient"`
	PreviousStartAt *time.Time `json:"previousStartAt,omitempty"`
}

// enqueueMailBookingTo schedules ONE "mail:booking" job for one recipient. Not room-scoped
// (RoomKey nil): every queued mail is independent, matching internal/polls/timers.go's
// enqueueMailPoll — two reschedules in quick succession must leave two rows per recipient, never
// an upsert collapsing them into one stale send.
func enqueueMailBookingTo(ctx context.Context, tx db.DBTX, kind, bookingID, recipient string, previousStartAt *time.Time) error {
	return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:  jobKindMailBooking,
		RunAt: time.Now(),
		Payload: mailBookingPayload{
			Kind: kind, BookingID: bookingID, Recipient: recipient, PreviousStartAt: previousStartAt,
		},
		MaxAttempts: mailBookingMaxAttempts,
	})
}

// enqueueMailBooking schedules kind's mail for BOTH parties — one row for the visitor, one for the
// organiser — the shape every lifecycle kind (confirmed/cancelled/rescheduled/reminder) wants.
// Organiser-only kinds (sync_failed) call enqueueMailBookingTo directly.
func enqueueMailBooking(ctx context.Context, tx db.DBTX, kind, bookingID string, previousStartAt *time.Time) error {
	for _, recipient := range []string{mailRecipientVisitor, mailRecipientOrganiser} {
		if err := enqueueMailBookingTo(ctx, tx, kind, bookingID, recipient, previousStartAt); err != nil {
			return err
		}
	}
	return nil
}

// reminderRoomKey is "booking.reminder"'s room_key for bookingID — the id-swap upsert target that
// makes re-arming (Reschedule) and cancelling (Cancel) a single job per booking, never a pile-up.
func reminderRoomKey(bookingID string) string {
	return "booking:" + bookingID
}

// armBookingReminder upserts "booking.reminder" for bookingID at startAt-reminderLead. Ports
// BookingRoom.scheduleReminder verbatim, including its lack of a "still in the future" guard
// (unlike internal/polls/timers.go's armDeadline, which skips arming a reminder already in the
// past): a booking made with under 24h's notice gets a reminder job whose run_at is already due,
// which the worker picks up on its very next poll — the same effect as BookingRoom's own
// setAlarm(earliest) firing immediately for an alarm time already passed.
func armBookingReminder(ctx context.Context, tx db.DBTX, bookingID string, startAt time.Time) error {
	roomKey := reminderRoomKey(bookingID)
	return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:    jobKindBookingReminder,
		RoomKey: &roomKey,
		RunAt:   startAt.Add(-reminderLead),
		Payload: map[string]any{"bookingId": bookingID},
	})
}

// cancelBookingReminder cancels bookingID's pending reminder job, if any. Ports
// BookingRoom.cancelReminder. Cancelling a job that isn't there is not an error (jobs.Cancel's own
// contract).
func cancelBookingReminder(ctx context.Context, tx db.DBTX, bookingID string) error {
	return jobs.Cancel(ctx, tx, jobKindBookingReminder, reminderRoomKey(bookingID))
}

// RegisterJobs wires this package's two job kinds into w. m is the real mailer used only by
// "mail:booking" — "booking.reminder" never touches SMTP directly, it only ever schedules a
// further "mail:booking" job (mirroring internal/polls/timers.go's own poll.reminder ->
// mail:poll indirection).
func (s *Service) RegisterJobs(w *jobs.Worker, m *mailer.Mailer) {
	w.Register(jobKindBookingReminder, func(ctx context.Context, job jobs.Job) error {
		var p struct {
			BookingID string `json:"bookingId"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("bookings: decode booking.reminder payload: %w", err)
		}
		return s.handleBookingReminderJob(ctx, p.BookingID)
	})

	w.Register(jobKindMailBooking, func(ctx context.Context, job jobs.Job) error {
		return s.handleMailBookingJob(ctx, m, job)
	})

	// "google:sync" (Task 5, google.go) needs no mailer of its own — a hard failure enqueues a
	// "mail:booking"/"sync_failed" job instead, handled by the registration just above.
	w.Register(jobKindGoogleSync, func(ctx context.Context, job jobs.Job) error {
		return s.handleGoogleSyncJob(ctx, job)
	})
}

// handleBookingReminderJob is "booking.reminder"'s body: re-check the booking is still confirmed
// and the page still wants reminders (both may have changed since this was armed — a cancelled
// booking or a reminders-off page must not fire one), then enqueue the actual "mail:booking"
// "reminder" send. Ports BookingRoom.alarm's due-reminder guard (booking.status !== 'confirmed',
// page.reminders, page.deletedAt — the last folded into GetBookingPage's own deleted_at filter).
func (s *Service) handleBookingReminderJob(ctx context.Context, bookingID string) error {
	booking, err := s.q.GetBooking(ctx, bookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if booking.Status != "confirmed" {
		return nil
	}

	page, err := s.q.GetBookingPage(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !page.Reminders {
		return nil
	}

	return enqueueMailBooking(ctx, s.db, "reminder", bookingID, nil)
}

// handleMailBookingJob is "mail:booking"'s body: compose the ONE message this row stands for
// (composeBookingMail — nil means the world has moved on and there is nothing to send), then
// deliver it. A Send failure is returned so the worker retries this row alone.
func (s *Service) handleMailBookingJob(ctx context.Context, m *mailer.Mailer, job jobs.Job) error {
	var payload mailBookingPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("bookings: decode mail:booking payload: %w", err)
	}
	msg, err := s.composeBookingMail(ctx, m.AppURL(), payload)
	if err != nil {
		return err
	}
	if msg == nil {
		return nil
	}
	return m.Send(ctx, *msg)
}

// composeBookingMail re-reads the booking/page/org fresh (any one missing — including a
// soft-deleted page — is a silent no-op, the world has moved on since this was scheduled), applies
// the per-kind skip rules (queue.ts's own rationale: a booking cancelled between scheduling and
// sending must not get its confirmation anyway), and builds the single mailer.Message for
// payload.Recipient. (nil, nil) means nothing to send. An unknown recipient or kind is an error —
// never a guess that could send twice.
func (s *Service) composeBookingMail(ctx context.Context, appURL string, payload mailBookingPayload) (*mailer.Message, error) {
	if payload.Recipient != mailRecipientVisitor && payload.Recipient != mailRecipientOrganiser {
		return nil, fmt.Errorf("bookings: unknown mail:booking recipient %q", payload.Recipient)
	}

	booking, err := s.q.GetBooking(ctx, payload.BookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to send: the booking is gone
	}
	if err != nil {
		return nil, err
	}
	page, err := s.q.GetBookingPage(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to send: the page is gone or soft-deleted
	}
	if err != nil {
		return nil, err
	}
	org, err := s.q.GetOrganization(ctx, page.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to send: the org is gone
	}
	if err != nil {
		return nil, err
	}

	switch payload.Kind {
	case "confirmed":
		if booking.Status == "cancelled" {
			return nil, nil //nolint:nilnil // cancelled since scheduling
		}
		return s.composeBookingConfirmed(ctx, appURL, payload.Recipient, booking, page, org)
	case "cancelled":
		return s.composeBookingCancelled(ctx, appURL, payload.Recipient, booking, page, org)
	case "rescheduled":
		if booking.Status == "cancelled" {
			return nil, nil //nolint:nilnil // cancelled since scheduling
		}
		return s.composeBookingRescheduled(ctx, appURL, payload.Recipient, booking, page, org, payload.PreviousStartAt)
	case "reminder":
		// Re-checked here too (handleBookingReminderJob already checked once when it enqueued
		// this): the booking could have been cancelled, or the page's reminders toggled off, in
		// the time between that enqueue and this job actually running.
		if booking.Status != "confirmed" || !page.Reminders {
			return nil, nil //nolint:nilnil // no longer wanted
		}
		return s.composeBookingReminder(ctx, appURL, payload.Recipient, booking, page, org)
	case "sync_failed":
		// Ports sendGoogleSyncFailedNotice's contract (emails.ts): organiser-only, unconditional on
		// booking.Status — the booking itself is unaffected by a sync failure (google.go), only
		// the organiser's calendar may be out of sync.
		if payload.Recipient != mailRecipientOrganiser {
			return nil, nil //nolint:nilnil // there is no visitor half of this notice
		}
		return s.composeGoogleSyncFailed(ctx, page)
	default:
		return nil, fmt.Errorf("bookings: unknown mail:booking kind %q", payload.Kind)
	}
}

// resolveOrganiser looks up page.MemberUserID's account (the organiser recipient — see this file's
// package doc comment on why this port doesn't resolve a full subscriber list). Returns the
// display name to use in visitor-facing copy ("your booking with {organiser}") — the assigned
// member's name, or the org's own name when there's no member assigned or their account is gone —
// alongside the user row itself (nil when there's no organiser to mail).
func (s *Service) resolveOrganiser(ctx context.Context, page queries.BookingPage, org queries.Organization) (name string, owner *queries.User, err error) {
	if !page.MemberUserID.Valid {
		return org.Name, nil, nil
	}
	u, err := s.q.GetUser(ctx, page.MemberUserID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return org.Name, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	return displayName(u), &u, nil
}

// displayName builds a recipient's display name from a Go `users` row, which (unlike Drizzle's
// `user.name`) has no single name column — only nullable FirstName/LastName. Mirrors
// internal/polls/notifications.go's own displayName exactly.
func displayName(u queries.User) string {
	first := ""
	if u.FirstName.Valid {
		first = strings.TrimSpace(u.FirstName.String)
	}
	last := ""
	if u.LastName.Valid {
		last = strings.TrimSpace(u.LastName.String)
	}
	name := strings.TrimSpace(strings.TrimSpace(first + " " + last))
	if name != "" {
		return name
	}
	if i := strings.IndexByte(u.Email, '@'); i > 0 {
		return u.Email[:i]
	}
	return u.Email
}

// orDefaultLocale returns l if set, else "en" — bookings (unlike users) do carry their own
// visitor_locale column.
func orDefaultLocale(l sql.NullString) string {
	if l.Valid && l.String != "" {
		return l.String
	}
	return "en"
}

// pageLocationText/pageDescriptionText read a booking page's optional Location/Description as a
// plain string, "" when unset.
func pageLocationText(page queries.BookingPage) string {
	if page.Location.Valid {
		return page.Location.String
	}
	return ""
}

func pageDescriptionText(page queries.BookingPage) string {
	if page.Description.Valid {
		return page.Description.String
	}
	return ""
}

// organiserLocale resolves the organiser recipient's mail locale through the resolver wired by
// SetLocaleResolver (auth.Service.LocaleFor in production — the per-user locale persisted in
// user_preferences). "en" when no resolver is wired, there is no owner, or the resolver has no
// answer.
func (s *Service) organiserLocale(ctx context.Context, owner *queries.User) string {
	if s.localeFor == nil || owner == nil {
		return "en"
	}
	if l := s.localeFor(ctx, strconv.FormatInt(owner.ID, 10)); l != "" {
		return l
	}
	return "en"
}

// bookingWhenText renders a booking mail's "When" line in the recipient's locale and timezone —
// the port of emails.ts's bookingWhen (formatOptionLabel in the recipient's locale). end is nil for
// a bare point in time (a reschedule's *previous* start, whose end was never recorded).
//
//	en: "Tue 1 Sep, 09:00–09:30"    nb: "tir. 1. sep., 09:00–09:30"    no end: "Tue 1 Sep, 09:00"
func bookingWhenText(locale string, start time.Time, end *time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	if end == nil {
		return mailer.FormatDateTime(locale, start, loc)
	}
	return mailer.FormatDate(locale, start, loc) + ", " + mailer.FormatTimeRange(locale, start, *end, loc)
}

// bookingManageURL/bookingDashboardURL/bookingPublicPageURL mirror emails.ts's manageUrl/
// dashboardUrl/publicPageUrl. bookingManageURL is a Service method because it appends a real
// `?t=` credential: a booking's manage token is deterministically re-derivable from its id alone
// (bookings.go's manageToken), so this queued, ids-only job can rebuild a working manage link —
// and the .ics invite's URL property gets the same link (the visitor's own credential for their
// own booking, no more sensitive in the attachment than in the body right above it).
func (s *Service) bookingManageURL(appURL, bookingID string) string {
	return appURL + "/booking/" + bookingID + "?t=" + s.manageToken(bookingID)
}

func bookingDashboardURL(appURL, pageID string) string {
	return appURL + "/bookings/" + pageID
}

func bookingPublicPageURL(appURL, orgSlug, pageSlug string) string {
	return appURL + "/book/" + orgSlug + "/" + pageSlug
}

// bookingICSAttachment is the .ics invite both confirmed/rescheduled mails carry (visitor and
// organiser get the same file).
func (s *Service) bookingICSAttachment(appURL string, booking queries.Booking, page queries.BookingPage) []mailer.Attachment {
	ics := BuildBookingICS(booking.ID, page.Title, pageDescriptionText(page), pageLocationText(page),
		booking.StartAt, booking.EndAt, s.bookingManageURL(appURL, booking.ID))
	return []mailer.Attachment{{Filename: "calendar.ics", ContentType: "text/calendar", Content: ics}}
}

// composeBookingConfirmed ports sendBookingEmails' kind==='confirmed' branch for ONE recipient:
// the visitor's confirmation (with its .ics invite), or the organiser notice — nil when the page
// has no assigned member whose account still resolves. Each side gets its own locale and timezone.
func (s *Service) composeBookingConfirmed(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)
	manageURL := s.bookingManageURL(appURL, booking.ID)
	attachments := s.bookingICSAttachment(appURL, booking, page)

	if recipient == mailRecipientVisitor {
		locale := orDefaultLocale(booking.VisitorLocale)
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_confirmed",
			Data: map[string]any{
				"VisitorName":   booking.VisitorName,
				"PageTitle":     page.Title,
				"OrganiserName": organiserName,
				"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"Location":      location,
				"ManageURL":     manageURL,
				"Locale":        locale,
			},
			Attachments: attachments,
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	locale := s.organiserLocale(ctx, owner)
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_organiser_notice",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"VisitorEmail": booking.VisitorEmail,
			"VisitorNote":  nullString(booking.VisitorNote),
			"When":         bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":     location,
			"ViewURL":      bookingDashboardURL(appURL, page.ID),
			"Locale":       locale,
		},
		Attachments: attachments,
	}, nil
}

// composeBookingCancelled ports sendBookingEmails' kind==='cancelled' branch for ONE recipient:
// both sides get the "cancelled" template, worded relative to who they are (bookingCancelledBody,
// internal/mailer/helpers.go) — "you cancelled" for whichever side caused it, "the organiser/
// visitor cancelled" for the other.
func (s *Service) composeBookingCancelled(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	cancelledBy := "organiser"
	if booking.CancelledBy.Valid {
		cancelledBy = booking.CancelledBy.String
	}

	if recipient == mailRecipientVisitor {
		visitorCancelledBy := "organiser"
		if cancelledBy == "visitor" {
			visitorCancelledBy = "you"
		}
		locale := orDefaultLocale(booking.VisitorLocale)
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_cancelled",
			Data: map[string]any{
				"RecipientName": booking.VisitorName,
				"PageTitle":     page.Title,
				"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"CancelledBy":   visitorCancelledBy,
				"ViewURL":       bookingPublicPageURL(appURL, org.Slug, page.Slug),
				"Locale":        locale,
			},
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	organiserCancelledBy := "visitor"
	if cancelledBy == "organiser" {
		organiserCancelledBy = "you"
	}
	locale := s.organiserLocale(ctx, owner)
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_cancelled",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone),
			"CancelledBy":   organiserCancelledBy,
			"VisitorName":   booking.VisitorName,
			"ViewURL":       bookingDashboardURL(appURL, page.ID),
			"Locale":        locale,
		},
	}, nil
}

// composeBookingRescheduled ports sendBookingEmails' kind==='rescheduled' branch for ONE recipient.
// previousStartAt is this row's own payload field; nil falls back to reusing the current When for
// PreviousWhen too, matching emails.ts's own `opts.previousStartAt ? ... : visitorWhen` fallback.
func (s *Service) composeBookingRescheduled(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization, previousStartAt *time.Time) (*mailer.Message, error) {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)
	manageURL := s.bookingManageURL(appURL, booking.ID)
	attachments := s.bookingICSAttachment(appURL, booking, page)

	if recipient == mailRecipientVisitor {
		locale := orDefaultLocale(booking.VisitorLocale)
		when := bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone)
		previousWhen := when
		if previousStartAt != nil {
			previousWhen = bookingWhenText(locale, *previousStartAt, nil, booking.VisitorTimezone)
		}
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_rescheduled",
			Data: map[string]any{
				"VisitorName":   booking.VisitorName,
				"PageTitle":     page.Title,
				"OrganiserName": organiserName,
				"PreviousWhen":  previousWhen,
				"When":          when,
				"Location":      location,
				"ManageURL":     manageURL,
				"Locale":        locale,
			},
			Attachments: attachments,
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to notify
	}
	locale := s.organiserLocale(ctx, owner)
	when := bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone)
	previousWhen := when
	if previousStartAt != nil {
		previousWhen = bookingWhenText(locale, *previousStartAt, nil, page.Timezone)
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_rescheduled_organiser",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"PreviousWhen": previousWhen,
			"When":         when,
			"Location":     location,
			"ViewURL":      bookingDashboardURL(appURL, page.ID),
			"Locale":       locale,
		},
		Attachments: attachments,
	}, nil
}

// composeBookingReminder ports sendBookingEmails' kind==='reminder' branch for ONE recipient (no
// notification-event gating — the reminder has none, per emails.ts's own ORGANISER_EVENT comment).
// No .ics attachment, matching the TS source (a reminder isn't a new calendar entry).
func (s *Service) composeBookingReminder(ctx context.Context, appURL, recipient string, booking queries.Booking, page queries.BookingPage, org queries.Organization) (*mailer.Message, error) {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return nil, err
	}
	location := pageLocationText(page)

	if recipient == mailRecipientVisitor {
		locale := orDefaultLocale(booking.VisitorLocale)
		return &mailer.Message{
			To:       booking.VisitorEmail,
			Template: "booking_reminder",
			Data: map[string]any{
				"RecipientName": booking.VisitorName,
				"PageTitle":     page.Title,
				"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
				"Location":      location,
				"ViewURL":       s.bookingManageURL(appURL, booking.ID),
				"Locale":        locale,
			},
		}, nil
	}
	if owner == nil {
		return nil, nil //nolint:nilnil // no organiser to remind
	}
	locale := s.organiserLocale(ctx, owner)
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_reminder",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(locale, booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":      location,
			"ViewURL":       bookingDashboardURL(appURL, page.ID),
			"Locale":        locale,
		},
	}, nil
}

// composeGoogleSyncFailed ports sendGoogleSyncFailedNotice (emails.ts): a best-effort organiser
// notice that a Google Calendar sync failed (google.go's googleSyncInsert/Delete/Reschedule are the
// only enqueuers of the "sync_failed" kind). nil when the page has no assigned member (nothing to
// notify), matching the TS source's own `if (!owner) return`.
//
// Unlike every other compose* function in this file, this one sets no Locale on the returned
// mailer.Message — unreachable today only because googleSyncActive (google.go) stops "sync_failed"
// from ever being enqueued; see that function's own "reviving Google Calendar sync" checklist,
// which carries this as an item to fix before the gate reopens.
func (s *Service) composeGoogleSyncFailed(ctx context.Context, page queries.BookingPage) (*mailer.Message, error) {
	if !page.MemberUserID.Valid {
		return nil, nil //nolint:nilnil // nothing to notify
	}
	owner, err := s.q.GetUser(ctx, page.MemberUserID.Int64)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nothing to notify
	}
	if err != nil {
		return nil, err
	}
	return &mailer.Message{
		To:       owner.Email,
		Template: "booking_sync_failed",
		Data:     map[string]any{"PageTitle": page.Title},
	}, nil
}

// nullString reads a sql.NullString as a plain string, "" when unset.
func nullString(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

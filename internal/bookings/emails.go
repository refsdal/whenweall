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
//   - Payload is ids-only (kind + bookingId), same rationale as mailer/queue.ts's MailJob: never
//     an address, and the handler re-reads the booking fresh at send time so a booking cancelled
//     between scheduling and sending is a no-op rather than a stale send (see
//     handleMailBookingJob's per-kind skip checks below).
//
//     One deliberate addition beyond the brief's literal "{kind, bookingId}" shorthand:
//     "rescheduled" also carries previousStartAt (a plain timestamp, never personal data). TS's
//     own sendBookingEmails needs this to render "moved from {previousWhen} to {when}", and — per
//     its own doc comment — it "isn't otherwise recoverable once the booking row has been updated
//     to its new time." Omitting it (as a maximally-literal reading of the brief would) would make
//     the Go port's rescheduled mail show the same time twice, on every send, not just the TS
//     retry path's already-accepted degrade case.
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
//   - The email body's "When" text is plain English, timezone-aware but not locale-aware — the
//     same simplification internal/polls/timers.go's optionLabelText already made and documented,
//     since there is no Go port of formatOptionLabel/Intl.DateTimeFormat yet. The .ics attachment
//     and the template catalog (messages.go) are still genuinely localized via booking.VisitorLocale/
//     the recipient's Locale field.
//
//   - No user-locale column exists in this Go port's `users` table (unlike Drizzle's `user.locale`)
//     — mirrors notifications.go's own note — so the organiser side of every mail always renders
//     "en". The visitor side keeps its own real per-booking locale (bookings.visitor_locale).
package bookings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// mailBookingPayload is the "mail:booking" job's payload — see this file's package doc comment for
// why previousStartAt is included alongside the brief's literal "{kind, bookingId}" shorthand.
type mailBookingPayload struct {
	Kind            string     `json:"kind"`
	BookingID       string     `json:"bookingId"`
	PreviousStartAt *time.Time `json:"previousStartAt,omitempty"`
}

// enqueueMailBooking schedules one "mail:booking" job. Not room-scoped (RoomKey nil): every queued
// mail is independent, matching internal/polls/timers.go's enqueueMailPoll — two reschedules in
// quick succession must leave two rows, never an upsert collapsing them into one stale send.
func enqueueMailBooking(ctx context.Context, tx db.DBTX, kind, bookingID string, previousStartAt *time.Time) error {
	return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:        jobKindMailBooking,
		RunAt:       time.Now(),
		Payload:     mailBookingPayload{Kind: kind, BookingID: bookingID, PreviousStartAt: previousStartAt},
		MaxAttempts: mailBookingMaxAttempts,
	})
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

// handleMailBookingJob is "mail:booking"'s body: re-read the booking/page/org fresh (any one
// missing — including a soft-deleted page — is a silent no-op, the world has moved on since this
// was scheduled), then dispatch to the kind-specific sender below.
func (s *Service) handleMailBookingJob(ctx context.Context, m *mailer.Mailer, job jobs.Job) error {
	var payload mailBookingPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("bookings: decode mail:booking payload: %w", err)
	}

	booking, err := s.q.GetBooking(ctx, payload.BookingID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	page, err := s.q.GetBookingPage(ctx, booking.PageID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	org, err := s.q.GetOrganization(ctx, page.OrganizationID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	switch payload.Kind {
	case "confirmed":
		// Skip-if-cancelled-since (queue.ts's own rationale): a booking cancelled between Book's
		// commit and this job running must not have its confirmation sent anyway.
		if booking.Status == "cancelled" {
			return nil
		}
		return s.sendBookingConfirmedMail(ctx, m, booking, page, org)
	case "cancelled":
		return s.sendBookingCancelledMail(ctx, m, booking, page, org)
	case "rescheduled":
		if booking.Status == "cancelled" {
			return nil
		}
		return s.sendBookingRescheduledMail(ctx, m, booking, page, org, payload.PreviousStartAt)
	case "reminder":
		// Re-checked here too (handleBookingReminderJob already checked once when it enqueued
		// this): the booking could have been cancelled, or the page's reminders toggled off, in
		// the time between that enqueue and this job actually running.
		if booking.Status != "confirmed" || !page.Reminders {
			return nil
		}
		return s.sendBookingReminderMail(ctx, m, booking, page, org)
	default:
		return fmt.Errorf("bookings: unknown mail:booking kind %q", payload.Kind)
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

// bookingWhenText renders a plain-English "When" line for a booking mail — see this file's package
// doc comment on why this isn't locale-aware the way src/lib/time.ts's formatOptionLabel is. end is
// nil for a bare point in time (a reschedule's *previous* start, whose end was never recorded —
// mirrors emails.ts's own bookingWhen(startAt, endAt | null, ...) signature).
func bookingWhenText(start time.Time, end *time.Time, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	s := start.In(loc).Format("Monday, January 2, 2006 3:04 PM")
	if end != nil {
		s += " – " + end.In(loc).Format("3:04 PM")
	}
	return s
}

// bookingManageURL/bookingDashboardURL/bookingPublicPageURL mirror emails.ts's manageUrl/
// dashboardUrl/publicPageUrl. bookingManageURL never carries a manage token here — unlike TS's
// live (non-retry) send, this port's mail always goes through the job queue, whose payload is
// ids-only (see this file's package doc comment); the visitor's own browser restores the token
// client-side from its own copy, exactly as TS's retry path (and this port's every path) already
// relies on.
func bookingManageURL(appURL, bookingID string) string {
	return appURL + "/booking/" + bookingID
}

func bookingDashboardURL(appURL, pageID string) string {
	return appURL + "/bookings/" + pageID
}

func bookingPublicPageURL(appURL, orgSlug, pageSlug string) string {
	return appURL + "/book/" + orgSlug + "/" + pageSlug
}

// sendBookingConfirmedMail ports sendBookingEmails' kind==='confirmed' branch: the visitor's
// confirmation (with its .ics invite) always sends; the organiser notice sends only when the page
// has an assigned member whose account still resolves.
func (s *Service) sendBookingConfirmedMail(ctx context.Context, m *mailer.Mailer, booking queries.Booking, page queries.BookingPage, org queries.Organization) error {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return err
	}

	location := pageLocationText(page)
	manageURL := bookingManageURL(m.AppURL(), booking.ID)
	ics := BuildBookingICS(booking.ID, page.Title, pageDescriptionText(page), location, booking.StartAt, booking.EndAt, manageURL)
	attachments := []mailer.Attachment{{Filename: "calendar.ics", ContentType: "text/calendar", Content: ics}}

	if err := m.Send(ctx, mailer.Message{
		To:       booking.VisitorEmail,
		Template: "booking_confirmed",
		Data: map[string]any{
			"VisitorName":   booking.VisitorName,
			"PageTitle":     page.Title,
			"OrganiserName": organiserName,
			"When":          bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
			"Location":      location,
			"ManageURL":     manageURL,
			"Locale":        orDefaultLocale(booking.VisitorLocale),
		},
		Attachments: attachments,
	}); err != nil {
		return err
	}

	if owner == nil {
		return nil
	}

	return m.Send(ctx, mailer.Message{
		To:       owner.Email,
		Template: "booking_organiser_notice",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"VisitorEmail": booking.VisitorEmail,
			"VisitorNote":  nullString(booking.VisitorNote),
			"When":         bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":     location,
			"ViewURL":      bookingDashboardURL(m.AppURL(), page.ID),
			"Locale":       "en",
		},
		Attachments: attachments,
	})
}

// sendBookingCancelledMail ports sendBookingEmails' kind==='cancelled' branch: both sides get the
// "cancelled" template, each with wording relative to who they are (bookingCancelledBody,
// internal/mailer/helpers.go) — "you cancelled" for whichever side caused it, "the organiser/
// visitor cancelled" for the other.
func (s *Service) sendBookingCancelledMail(ctx context.Context, m *mailer.Mailer, booking queries.Booking, page queries.BookingPage, org queries.Organization) error {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return err
	}

	cancelledBy := "organiser"
	if booking.CancelledBy.Valid {
		cancelledBy = booking.CancelledBy.String
	}

	visitorCancelledBy := "organiser"
	if cancelledBy == "visitor" {
		visitorCancelledBy = "you"
	}
	if err := m.Send(ctx, mailer.Message{
		To:       booking.VisitorEmail,
		Template: "booking_cancelled",
		Data: map[string]any{
			"RecipientName": booking.VisitorName,
			"PageTitle":     page.Title,
			"When":          bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
			"CancelledBy":   visitorCancelledBy,
			"ViewURL":       bookingPublicPageURL(m.AppURL(), org.Slug, page.Slug),
			"Locale":        orDefaultLocale(booking.VisitorLocale),
		},
	}); err != nil {
		return err
	}

	if owner == nil {
		return nil
	}

	organiserCancelledBy := "visitor"
	if cancelledBy == "organiser" {
		organiserCancelledBy = "you"
	}
	return m.Send(ctx, mailer.Message{
		To:       owner.Email,
		Template: "booking_cancelled",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone),
			"CancelledBy":   organiserCancelledBy,
			"VisitorName":   booking.VisitorName,
			"ViewURL":       bookingDashboardURL(m.AppURL(), page.ID),
			"Locale":        "en",
		},
	})
}

// sendBookingRescheduledMail ports sendBookingEmails' kind==='rescheduled' branch. previousStartAt
// is this send's own payload field (see this file's package doc comment for why it's carried
// alongside the ids); nil falls back to reusing the current When for PreviousWhen too, matching
// emails.ts's own `opts.previousStartAt ? ... : visitorWhen` fallback.
func (s *Service) sendBookingRescheduledMail(ctx context.Context, m *mailer.Mailer, booking queries.Booking, page queries.BookingPage, org queries.Organization, previousStartAt *time.Time) error {
	organiserName, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return err
	}

	location := pageLocationText(page)
	manageURL := bookingManageURL(m.AppURL(), booking.ID)
	ics := BuildBookingICS(booking.ID, page.Title, pageDescriptionText(page), location, booking.StartAt, booking.EndAt, manageURL)
	attachments := []mailer.Attachment{{Filename: "calendar.ics", ContentType: "text/calendar", Content: ics}}

	visitorWhen := bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone)
	previousVisitorWhen := visitorWhen
	if previousStartAt != nil {
		previousVisitorWhen = bookingWhenText(*previousStartAt, nil, booking.VisitorTimezone)
	}

	if err := m.Send(ctx, mailer.Message{
		To:       booking.VisitorEmail,
		Template: "booking_rescheduled",
		Data: map[string]any{
			"VisitorName":   booking.VisitorName,
			"PageTitle":     page.Title,
			"OrganiserName": organiserName,
			"PreviousWhen":  previousVisitorWhen,
			"When":          visitorWhen,
			"Location":      location,
			"ManageURL":     manageURL,
			"Locale":        orDefaultLocale(booking.VisitorLocale),
		},
		Attachments: attachments,
	}); err != nil {
		return err
	}

	if owner == nil {
		return nil
	}

	organiserWhen := bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone)
	previousOrganiserWhen := organiserWhen
	if previousStartAt != nil {
		previousOrganiserWhen = bookingWhenText(*previousStartAt, nil, page.Timezone)
	}

	return m.Send(ctx, mailer.Message{
		To:       owner.Email,
		Template: "booking_rescheduled_organiser",
		Data: map[string]any{
			"PageTitle":    page.Title,
			"VisitorName":  booking.VisitorName,
			"PreviousWhen": previousOrganiserWhen,
			"When":         organiserWhen,
			"Location":     location,
			"ViewURL":      bookingDashboardURL(m.AppURL(), page.ID),
			"Locale":       "en",
		},
		Attachments: attachments,
	})
}

// sendBookingReminderMail ports sendBookingEmails' kind==='reminder' branch (no notification-event
// gating — the reminder has none, per emails.ts's own ORGANISER_EVENT comment — so both sides use
// the same memberUserId-or-nothing organiser rule every other kind now uses too). No .ics
// attachment, matching the TS source (a reminder isn't a new calendar entry).
func (s *Service) sendBookingReminderMail(ctx context.Context, m *mailer.Mailer, booking queries.Booking, page queries.BookingPage, org queries.Organization) error {
	_, owner, err := s.resolveOrganiser(ctx, page, org)
	if err != nil {
		return err
	}

	location := pageLocationText(page)
	manageURL := bookingManageURL(m.AppURL(), booking.ID)

	if err := m.Send(ctx, mailer.Message{
		To:       booking.VisitorEmail,
		Template: "booking_reminder",
		Data: map[string]any{
			"RecipientName": booking.VisitorName,
			"PageTitle":     page.Title,
			"When":          bookingWhenText(booking.StartAt, &booking.EndAt, booking.VisitorTimezone),
			"Location":      location,
			"ViewURL":       manageURL,
			"Locale":        orDefaultLocale(booking.VisitorLocale),
		},
	}); err != nil {
		return err
	}

	if owner == nil {
		return nil
	}

	return m.Send(ctx, mailer.Message{
		To:       owner.Email,
		Template: "booking_reminder",
		Data: map[string]any{
			"RecipientName": displayName(*owner),
			"PageTitle":     page.Title,
			"When":          bookingWhenText(booking.StartAt, &booking.EndAt, page.Timezone),
			"Location":      location,
			"ViewURL":       bookingDashboardURL(m.AppURL(), page.ID),
			"Locale":        "en",
		},
	})
}

// nullString reads a sql.NullString as a plain string, "" when unset.
func nullString(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

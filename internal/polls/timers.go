package polls

// The job-queue side of notifications.go: arming/cancelling a poll's deadline timer (ports
// PollRoom's syncDeadline — see service.go's Create/Update/Finalize/Delete for the call sites),
// the debounced digest accumulator (ports PollRoom's enqueueDigest/#rearm), and the three
// scheduled_jobs kinds this package registers:
//
//   - "poll.deadline" (room-scoped, one pending job per poll): fires once the poll's deadline
//     passes — closes the poll (Service.CloseExpired, which already emits room.changed) and, if
//     it actually closed, schedules one "mail:poll"/"closed" job per subscribed recipient.
//   - "poll.digest" (room-scoped, one pending job per poll): fires DIGEST_DELAY_MS after the
//     first item lands in a debounce window (EnqueueDigestItem) — resolves recipients per
//     distinct event, excludes each recipient's own items, and schedules one "mail:poll"/"digest"
//     job per recipient.
//   - "mail:poll" (not room-scoped — every queued mail is independent): the actual send. Payload
//     is ids-only (pollId/event/participantId/userId, plus a digest's own item list — event+name+
//     actorUserId, never an address); the handler re-reads the poll (and participant/user) fresh
//     at send time and renders+sends via a real *mailer.Mailer, so a poll deleted or re-finalized
//     between scheduling and sending is a no-op rather than a stale mail.
//
// Every handler starts by re-reading the poll and treating "missing or soft-deleted" as a
// silent no-op — mirrors emitPollEvent/sendClaimConfirmation/PollRoom#processDigest all returning
// early on `!poll || poll.deletedAt`.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/refsdal/whenweall/internal/db"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/polls/queries"
	"github.com/sqlc-dev/pqtype"
)

const (
	jobKindDeadline = "poll.deadline"
	jobKindReminder = "poll.reminder"
	jobKindDigest   = "poll.digest"
	jobKindMailPoll = "mail:poll"

	// mailPollMaxAttempts mirrors mailer's own mailMaxAttempts for the auth-mail path: an SMTP
	// hiccup is worth retrying several times, a message still failing after that is a bad
	// address or a broken relay.
	mailPollMaxAttempts = 10

	// digestDelay mirrors DIGEST_DELAY_MS (PollRoom.ts): how long a poll's digest debounces after
	// its first queued item before sending.
	digestDelay = 10 * time.Minute

	// reminderLead mirrors REMINDER_LEAD_MS (PollRoom.ts): how far ahead of a poll's deadline the
	// "closes soon" (deadline.approaching) reminder fires.
	reminderLead = 24 * time.Hour
)

// mailPollPayload is the "mail:poll" job's ids-only payload — pollId/event plus whichever
// recipient identifier applies (a participant for finalized/claim_confirmation, a user for
// closed/digest), and — for a digest send only — the batched items themselves (event+name+
// actorUserId, never an address).
type mailPollPayload struct {
	PollID        string       `json:"pollId"`
	Event         string       `json:"event"`
	ParticipantID string       `json:"participantId,omitempty"`
	UserID        string       `json:"userId,omitempty"`
	Items         []DigestItem `json:"items,omitempty"`
}

// enqueueMailPoll schedules one "mail:poll" job. Not room-scoped (RoomKey nil): every queued mail
// is independent, so N recipients means N rows, never an upsert collapsing them.
func enqueueMailPoll(ctx context.Context, tx db.DBTX, payload mailPollPayload) error {
	return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:        jobKindMailPoll,
		RunAt:       time.Now(),
		Payload:     payload,
		MaxAttempts: mailPollMaxAttempts,
	})
}

// armDeadline ports PollRoom.syncDeadline in full: schedules (upserts) "poll.deadline" for pollID
// when deadlineAt is non-nil, cancels the pending job when it's nil — AND does the same for
// "poll.reminder" (I11: PollRoom's 24h "closes soon" reminder, deadline.approaching), armed
// reminderLead ahead of the same deadline. Must run inside the same transaction as the domain
// write that changed the deadline (Create/Update), matching jobs.Schedule/Cancel's own contract.
//
// Finalize/Delete cancel both jobs directly (jobs.Cancel(..., jobKindDeadline, ...) and
// jobs.Cancel(..., jobKindReminder, ...), service.go) rather than through this function, since
// they aren't changing a deadline value — they're clearing both timers unconditionally because
// the poll itself no longer needs either one.
func armDeadline(ctx context.Context, tx db.DBTX, pollID string, deadlineAt *time.Time) error {
	roomKey := "poll:" + pollID
	if deadlineAt == nil {
		if err := jobs.Cancel(ctx, tx, jobKindDeadline, roomKey); err != nil {
			return err
		}
		return jobs.Cancel(ctx, tx, jobKindReminder, roomKey)
	}
	if err := jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:    jobKindDeadline,
		RoomKey: &roomKey,
		RunAt:   *deadlineAt,
		Payload: map[string]any{"pollId": pollID},
	}); err != nil {
		return err
	}

	// Only arm the reminder when it's still ahead of us — ports syncDeadline's own `remindAt >
	// Date.now()` check (PollRoom.ts): a poll created/updated with a deadline already inside the
	// next 24h would otherwise fire "closes soon" immediately, which reads as a bug, not a
	// reminder.
	remindAt := deadlineAt.Add(-reminderLead)
	if remindAt.After(time.Now()) {
		return jobs.Schedule(ctx, tx, jobs.ScheduleInput{
			Kind:    jobKindReminder,
			RoomKey: &roomKey,
			RunAt:   remindAt,
			Payload: map[string]any{"pollId": pollID},
		})
	}
	return jobs.Cancel(ctx, tx, jobKindReminder, roomKey)
}

// EnqueueDigestItem ports PollRoom.enqueueDigest's storage+debounce semantics, using the pending
// "poll.digest" scheduled_jobs row (keyed by room_key "poll:"+pollID) as the accumulator in place
// of a durable object's own storage: the first item for a poll arms the job DIGEST_DELAY_MS from
// now; every item after that appends to the same row's payload without resetting the timer.
//
// The read-modify-write below is NOT safe on its own: the SELECT takes no row lock, and
// jobs.Schedule's upsert (ON CONFLICT DO UPDATE SET payload = EXCLUDED.payload) blindly overwrites
// whatever's there with this call's own computed payload — two concurrent calls for the SAME poll
// under READ COMMITTED could both read the same pending payload, each append their own item to
// that same stale snapshot, and the second commit to land would silently clobber the first
// caller's item. The pg_advisory_xact_lock below closes that gap by serializing every
// EnqueueDigestItem call for a given poll: PollRoom's own equivalent needs no such lock, since a
// durable object serializes all its own storage access by construction (see #serialize call sites
// throughout PollRoom.ts) — there's no concurrent-write race to guard against there at all.
//
// event must be one of the digest-batched events (isDigestEvent) — an immediate event has no
// debounce window and must go through its own dedicated path instead (e.g. Finalize/Claim's
// direct enqueueMailPoll calls, or the "poll.deadline" handler's "closed" mail).
func (s *Service) EnqueueDigestItem(ctx context.Context, pollID string, item DigestItem) error {
	if !isDigestEvent(item.Event) {
		return fmt.Errorf("polls: %q is not a digest-batched event", item.Event)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Blocks until any other in-flight EnqueueDigestItem transaction holding this SAME poll's
	// lock commits or rolls back — see this function's own doc comment for why the
	// read-modify-write immediately below needs it. hashtext(...) folds the poll-scoped key into
	// the single bigint pg_advisory_xact_lock takes; the "_xact_" variant releases automatically
	// at this transaction's commit/rollback, never held past this function returning.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('poll.digest:' || $1))`, pollID); err != nil {
		return err
	}

	roomKey := "poll:" + pollID

	var runAt time.Time
	var raw pqtype.NullRawMessage
	scanErr := tx.QueryRowContext(ctx,
		`SELECT run_at, payload FROM scheduled_jobs WHERE kind = $1 AND room_key = $2`,
		jobKindDigest, roomKey,
	).Scan(&runAt, &raw)

	var payload digestPayload
	switch {
	case errors.Is(scanErr, sql.ErrNoRows):
		runAt = time.Now().Add(digestDelay)
	case scanErr != nil:
		return scanErr
	case raw.Valid:
		if err := json.Unmarshal(raw.RawMessage, &payload); err != nil {
			return fmt.Errorf("polls: decode poll.digest payload: %w", err)
		}
	}

	payload.PollID = pollID
	payload.Items = append(payload.Items, item)

	if err := jobs.Schedule(ctx, tx, jobs.ScheduleInput{
		Kind:    jobKindDigest,
		RoomKey: &roomKey,
		RunAt:   runAt,
		Payload: payload,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

// MailSender is the narrow seam this package's send paths need from *mailer.Mailer — the
// canonical app origin for links, and Send. An interface (rather than *mailer.Mailer in every
// signature) so a test can record the rendered Message instead of running an SMTP server.
type MailSender interface {
	AppURL() string
	Send(ctx context.Context, msg mailer.Message) error
}

var _ MailSender = (*mailer.Mailer)(nil)

// RegisterJobs wires this package's four job kinds into w. m is the real mailer (any MailSender)
// used only by mail:poll — "poll.deadline"/"poll.reminder"/"poll.digest" never touch SMTP
// directly, they only ever schedule further "mail:poll" jobs.
func (s *Service) RegisterJobs(w *jobs.Worker, m MailSender) {
	w.Register(jobKindDeadline, func(ctx context.Context, job jobs.Job) error {
		var p struct {
			PollID string `json:"pollId"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("polls: decode poll.deadline payload: %w", err)
		}
		return s.handleDeadlineJob(ctx, p.PollID)
	})

	w.Register(jobKindReminder, func(ctx context.Context, job jobs.Job) error {
		var p struct {
			PollID string `json:"pollId"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("polls: decode poll.reminder payload: %w", err)
		}
		return s.handleReminderJob(ctx, p.PollID)
	})

	w.Register(jobKindDigest, func(ctx context.Context, job jobs.Job) error {
		var p digestPayload
		if err := json.Unmarshal(job.Payload, &p); err != nil {
			return fmt.Errorf("polls: decode poll.digest payload: %w", err)
		}
		return s.handleDigestJob(ctx, p)
	})

	w.Register(jobKindMailPoll, func(ctx context.Context, job jobs.Job) error {
		return s.handleMailPollJob(ctx, m, job)
	})
}

// handleDeadlineJob is "poll.deadline"'s body: CloseExpired, then — only on an actual open->closed
// transition — resolve poll.closed's recipients and schedule one "closed" mail:poll job per
// recipient. Ports PollRoom#processDeadline's deadline half; the reminder half (deadline.
// approaching) is its own separate job kind (jobKindReminder/handleReminderJob below), armed and
// fired independently of this one — see armDeadline's own doc comment for why they're scheduled
// together but processed apart.
func (s *Service) handleDeadlineJob(ctx context.Context, pollID string) error {
	changed, err := s.CloseExpired(ctx, pollID)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	poll, err := s.q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	recipients, err := s.resolveRecipients(ctx, s.q, poll.OrganizationID, pollID, EventPollClosed, "")
	if err != nil {
		return err
	}
	for _, r := range recipients {
		if err := enqueueMailPoll(ctx, s.db, mailPollPayload{PollID: pollID, Event: "closed", UserID: r.UserID}); err != nil {
			return err
		}
	}
	return nil
}

// handleReminderJob is "poll.reminder"'s body: resolve deadline.approaching's recipients and
// schedule one "deadline.approaching" mail:poll job per recipient. Ports PollRoom#processReminder
// (minus the storage cleanup half — REMIND_AT_KEY's deletion — which this port has no equivalent
// storage slot for; jobs.Complete already removes the one-shot scheduled_jobs row this ran from,
// so there's nothing left over to clean up here). No poll.Status check: TS's own #processReminder
// doesn't gate on it either (only emitPollEvent's blanket "poll missing or soft-deleted" check
// applies, via GetPoll below) — a poll finalized or closed early, with its deadline unchanged,
// still gets its "closes soon" reminder, exactly like the TS source.
func (s *Service) handleReminderJob(ctx context.Context, pollID string) error {
	poll, err := s.q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	recipients, err := s.resolveRecipients(ctx, s.q, poll.OrganizationID, pollID, EventDeadlineApproaching, "")
	if err != nil {
		return err
	}
	for _, r := range recipients {
		if err := enqueueMailPoll(ctx, s.db, mailPollPayload{PollID: pollID, Event: string(EventDeadlineApproaching), UserID: r.UserID}); err != nil {
			return err
		}
	}
	return nil
}

// handleDigestJob is "poll.digest"'s body: resolve recipients per distinct event among the
// accumulated items (once per event, not once per item — a burst of twenty votes resolves once,
// same as PollRoom#processDigest), invert into "what does each recipient get" (dropping items
// they caused themselves), and schedule one "digest" mail:poll job per recipient.
func (s *Service) handleDigestJob(ctx context.Context, payload digestPayload) error {
	poll, err := s.q.GetPoll(ctx, payload.PollID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(payload.Items) == 0 {
		return nil
	}

	perEvent := make(map[NotificationEvent][]Recipient)
	for _, item := range payload.Items {
		if _, ok := perEvent[item.Event]; ok {
			continue
		}
		recips, err := s.resolveRecipients(ctx, s.q, poll.OrganizationID, payload.PollID, item.Event, "")
		if err != nil {
			return err
		}
		perEvent[item.Event] = recips
	}

	type bucket struct {
		items []DigestItem
	}
	byRecipient := make(map[string]*bucket)
	order := make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		for _, r := range perEvent[item.Event] {
			if r.UserID == item.ActorUserID {
				continue
			}
			b, ok := byRecipient[r.UserID]
			if !ok {
				b = &bucket{}
				byRecipient[r.UserID] = b
				order = append(order, r.UserID)
			}
			b.items = append(b.items, item)
		}
	}

	for _, uid := range order {
		if err := enqueueMailPoll(ctx, s.db, mailPollPayload{
			PollID: payload.PollID, Event: "digest", UserID: uid, Items: byRecipient[uid].items,
		}); err != nil {
			return err
		}
	}
	return nil
}

// handleMailPollJob is "mail:poll"'s body: re-read the poll fresh (a deleted/missing poll is a
// silent no-op — the world has moved on since this was scheduled), then dispatch to the
// event-specific renderer/sender below.
func (s *Service) handleMailPollJob(ctx context.Context, m MailSender, job jobs.Job) error {
	var payload mailPollPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("polls: decode mail:poll payload: %w", err)
	}

	poll, err := s.q.GetPoll(ctx, payload.PollID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	pollURL := m.AppURL() + "/p/" + poll.ID

	switch payload.Event {
	case "finalized":
		return s.sendFinalizedMail(ctx, m, poll, pollURL, payload)
	case "closed":
		return s.sendClosedMail(ctx, m, poll, pollURL, payload)
	case string(EventDeadlineApproaching):
		return s.sendReminderMail(ctx, m, poll, pollURL, payload)
	case "digest":
		return s.sendDigestMail(ctx, m, poll, pollURL, payload)
	case "claim_confirmation":
		return s.sendClaimConfirmationMail(ctx, m, poll, pollURL, payload)
	default:
		return fmt.Errorf("polls: unknown mail:poll event %q", payload.Event)
	}
}

// sendFinalizedMail ports sendFinalizedEmails' per-recipient body (finalize-emails.ts). Re-checks
// poll.Status/FinalizedOptionID fresh (a poll re-finalized to a different option — or somehow
// un-finalized — between scheduling and sending must not send a stale "the time is set" mail for
// an option that's no longer the answer).
//
// Attaches the finalized option's .ics invite (internal/polls/ics.go's BuildPollICS, given the
// same absolute pollURL this mail's own body links to) whenever it has calendar meaning — nil for
// a plain-text finalized option, matching buildOptionIcs's own null case (finalize-emails.ts).
func (s *Service) sendFinalizedMail(ctx context.Context, m MailSender, poll queries.Poll, pollURL string, payload mailPollPayload) error {
	if poll.Status != pollFinalizedStatus || !poll.FinalizedOptionID.Valid {
		return nil
	}

	options, err := s.q.ListOptionsByPoll(ctx, poll.ID)
	if err != nil {
		return err
	}
	var option *queries.PollOption
	for i := range options {
		if options[i].ID == poll.FinalizedOptionID.String {
			option = &options[i]
			break
		}
	}
	if option == nil {
		return nil
	}

	icsFilename, ics, err := BuildPollICS(ctx, s.q, poll.ID, pollURL)
	if err != nil {
		return err
	}
	var attachments []mailer.Attachment
	if ics != nil {
		attachments = []mailer.Attachment{{Filename: icsFilename, ContentType: "text/calendar", Content: ics}}
	}

	var name, email, locale string
	switch {
	case payload.ParticipantID != "":
		p, err := s.q.GetParticipant(ctx, payload.ParticipantID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if p.PollID != poll.ID || !p.Email.Valid {
			return nil
		}
		name, email, locale = p.Name, p.Email.String, orDefaultLocale(p.Locale)
	case payload.UserID != "":
		uid, perr := strconv.ParseInt(payload.UserID, 10, 64)
		if perr != nil {
			return nil
		}
		u, err := s.q.GetUser(ctx, uid)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		name, email, locale = displayName(u), u.Email, s.userLocale(ctx, payload.UserID)
	default:
		return nil
	}

	return m.Send(ctx, mailer.Message{
		To:       email,
		Template: "finalized",
		Data: map[string]any{
			"PollTitle":     poll.Title,
			"PollURL":       pollURL,
			"OptionLabel":   optionLabelText(*option, locale, poll.Timezone),
			"RecipientName": name,
			"Locale":        locale,
		},
		Attachments: attachments,
	})
}

// sendClosedMail ports the "closed" half of emitPollEvent's sendImmediate, specialised to the
// "closed" template (see internal/mailer/templates/closed.html's own doc comment: poll.closed has
// its own dedicated template, not the generic "notification" one). Re-checks poll.Status: if the
// poll moved on (e.g. finalized) since this was scheduled, the "closed without a winner" mail
// would be misleading, so this is a no-op.
func (s *Service) sendClosedMail(ctx context.Context, m MailSender, poll queries.Poll, pollURL string, payload mailPollPayload) error {
	if poll.Status != "closed" || payload.UserID == "" {
		return nil
	}
	uid, err := strconv.ParseInt(payload.UserID, 10, 64)
	if err != nil {
		return nil
	}
	u, err := s.q.GetUser(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	return m.Send(ctx, mailer.Message{
		To:       u.Email,
		Template: "closed",
		Data:     map[string]any{"PollTitle": poll.Title, "PollURL": pollURL, "Locale": s.userLocale(ctx, payload.UserID)},
	})
}

// sendReminderMail ports the deadline.approaching half of emitPollEvent's sendImmediate
// (emit.ts): unlike finalized/closed, this event has no dedicated template — it renders through
// the generic "notification" template (internal/mailer/templates/notification.html), exactly the
// shape recipients.ts's own non-digest branch builds ({event, title, url}), using the
// email_notification_deadline_subject/body catalog keys (messages.go) notifSubject/notifBody
// already resolve for this event.
func (s *Service) sendReminderMail(ctx context.Context, m MailSender, poll queries.Poll, pollURL string, payload mailPollPayload) error {
	if payload.UserID == "" {
		return nil
	}
	uid, err := strconv.ParseInt(payload.UserID, 10, 64)
	if err != nil {
		return nil
	}
	u, err := s.q.GetUser(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	return m.Send(ctx, mailer.Message{
		To:       u.Email,
		Template: "notification",
		Data: map[string]any{
			"Event":  string(EventDeadlineApproaching),
			"Title":  poll.Title,
			"URL":    pollURL,
			"Locale": s.userLocale(ctx, payload.UserID),
		},
	})
}

// sendDigestMail ports PollRoom#processDigest's per-recipient send (the render+mail half only —
// the resolve/invert half already ran in handleDigestJob).
func (s *Service) sendDigestMail(ctx context.Context, m MailSender, poll queries.Poll, pollURL string, payload mailPollPayload) error {
	if payload.UserID == "" {
		return nil
	}
	uid, err := strconv.ParseInt(payload.UserID, 10, 64)
	if err != nil {
		return nil
	}
	u, err := s.q.GetUser(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	return m.Send(ctx, mailer.Message{
		To:       u.Email,
		Template: "digest",
		Data: map[string]any{
			"PollTitle": poll.Title,
			"PollURL":   pollURL,
			"Lines":     buildDigestLines(payload.Items),
			"Locale":    s.userLocale(ctx, payload.UserID),
		},
	})
}

// sendClaimConfirmationMail ports sendClaimConfirmation (claim-emails.ts): re-derives the
// participant's current "yes" votes fresh at send time (never the claim-time snapshot), so it
// reflects whatever their claims are by the time this actually sends — and is a no-op for every
// "nothing to send" case: participant gone, no email on file, or no claims left.
//
// Attaches BuildClaimICS's multi-VEVENT calendar.ics whenever any claimed slot has calendar
// meaning (claim-emails.ts's buildIcsMulti).
func (s *Service) sendClaimConfirmationMail(ctx context.Context, m MailSender, poll queries.Poll, pollURL string, payload mailPollPayload) error {
	if payload.ParticipantID == "" {
		return nil
	}
	p, err := s.q.GetParticipant(ctx, payload.ParticipantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.PollID != poll.ID || !p.Email.Valid {
		return nil
	}
	locale := orDefaultLocale(p.Locale)

	votes, err := s.q.ListVotesByParticipant(ctx, p.ID)
	if err != nil {
		return err
	}
	claimed := make(map[string]bool, len(votes))
	for _, v := range votes {
		if v.Answer == "yes" {
			claimed[v.OptionID] = true
		}
	}
	if len(claimed) == 0 {
		return nil
	}

	options, err := s.q.ListOptionsByPoll(ctx, poll.ID)
	if err != nil {
		return err
	}
	slots := make([]string, 0, len(claimed))
	claimedOptions := make([]queries.PollOption, 0, len(claimed))
	for _, o := range options {
		if claimed[o.ID] {
			slots = append(slots, optionLabelText(o, locale, poll.Timezone))
			claimedOptions = append(claimedOptions, o)
		}
	}

	var attachments []mailer.Attachment
	if cal := BuildClaimICS(poll, claimedOptions, pollURL, time.Now()); cal != nil {
		attachments = []mailer.Attachment{{Filename: "calendar.ics", ContentType: "text/calendar", Content: cal}}
	}

	return m.Send(ctx, mailer.Message{
		To:       p.Email.String,
		Template: "claim_confirmation",
		Data: map[string]any{
			"Name":      p.Name,
			"PollTitle": poll.Title,
			"PollURL":   pollURL,
			"Slots":     slots,
			"Locale":    locale,
		},
		Attachments: attachments,
	})
}

// buildDigestLines ports PollRoom.ts's buildDigestLines: collapses a recipient's queued items
// into one summarised row per event, preserving first-seen order so the mail reads
// chronologically, deduping names (the same person editing twice is one name, not two).
func buildDigestLines(items []DigestItem) []mailer.DigestLine {
	type agg struct {
		names     map[string]bool
		nameOrder []string
		count     int
	}
	byEvent := make(map[NotificationEvent]*agg)
	order := make([]NotificationEvent, 0, len(items))
	for _, item := range items {
		a, ok := byEvent[item.Event]
		if !ok {
			a = &agg{names: make(map[string]bool)}
			byEvent[item.Event] = a
			order = append(order, item.Event)
		}
		a.count++
		if item.Name != "" && !a.names[item.Name] {
			a.names[item.Name] = true
			a.nameOrder = append(a.nameOrder, item.Name)
		}
	}
	lines := make([]mailer.DigestLine, 0, len(order))
	for _, ev := range order {
		a := byEvent[ev]
		lines = append(lines, mailer.DigestLine{Event: string(ev), Names: a.nameOrder, Count: a.count})
	}
	return lines
}

// optionLabelText renders one poll option as a plain-text label in the recipient's locale —
// formatOptionLabel (src/lib/time.ts) with primary/secondary/tertiary joined into one string,
// built on internal/mailer's locale-aware formatters: a text option is its label; a date option
// is a calendar date (FormatDate in UTC — never shifted by the poll's timezone); a datetime option
// is FormatDateTime in the poll's timezone, or "<date>, <start>–<end>" when the option has an
// end. Used by transactional mail (finalized/claim_confirmation) and the roster CSV.
func optionLabelText(o queries.PollOption, locale, timezone string) string {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}

	switch OptionKind(o.Kind) {
	case OptionKindText:
		if o.Label.Valid {
			return o.Label.String
		}
		return ""
	case OptionKindDate:
		if !o.StartAt.Valid {
			return ""
		}
		return mailer.FormatDate(locale, o.StartAt.Time, time.UTC)
	case OptionKindDatetime:
		if !o.StartAt.Valid {
			return ""
		}
		if o.EndAt.Valid {
			return mailer.FormatDate(locale, o.StartAt.Time, loc) + ", " +
				mailer.FormatTimeRange(locale, o.StartAt.Time, o.EndAt.Time, loc)
		}
		return mailer.FormatDateTime(locale, o.StartAt.Time, loc)
	default:
		return ""
	}
}

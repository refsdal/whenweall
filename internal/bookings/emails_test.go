package bookings_test

// Ports the behavioral cases from src/server/bookings/__tests__/emails.workers.test.ts's own
// intent (a lifecycle event fans out to visitor+organiser mail, ids-only, and a booking cancelled
// in between makes a queued send a no-op) as reached through this Go port's job-queue-first
// architecture (see emails.go's package doc comment): Book/Cancel/Reschedule enqueue "mail:booking"
// in the same transaction as their domain write, and BookingRoom's reminder-arming rules
// (scheduleReminder/cancelReminder/#rearmReminders) are ported as the room-scoped "booking.reminder"
// job Book/Cancel/Reschedule arm, cancel, or re-arm alongside it.
//
// This task's brief TDD list, and where each lands:
//   - "booking -> confirmation jobs enqueued for visitor+organiser, ids-only" ->
//     TestBookEnqueuesConfirmedMailJob (the enqueue footprint) and
//     TestMailBookingDeliversRealMail (the Mailpit end-to-end proof that running that one job
//     really does send both).
//   - "cancelled booking's queued mail becomes a no-op at run time" ->
//     TestMailBookingJobSkipsStaleConfirmedAfterCancellation.
//   - "reminder job re-arms on reschedule and cancels on cancel" ->
//     TestBookArmsReminderJob / TestRescheduleReArmsReminderJob /
//     TestRescheduleCancelsReminderWhenPageReminderersOff / TestCancelCancelsReminderJob.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/refsdal/whenweall/internal/bookings"
	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/mailer"
)

// jobRow is the subset of scheduled_jobs this file's assertions need.
type jobRow struct {
	RoomKey sql.NullString
	Payload []byte
}

func listJobs(t *testing.T, d *sql.DB, kind string) []jobRow {
	t.Helper()
	rows, err := d.QueryContext(context.Background(),
		`SELECT room_key, payload FROM scheduled_jobs WHERE kind = $1 ORDER BY created_at`, kind)
	if err != nil {
		t.Fatalf("query scheduled_jobs(%s): %v", kind, err)
	}
	defer func() { _ = rows.Close() }()

	var out []jobRow
	for rows.Next() {
		var r jobRow
		var payload sql.NullString
		if err := rows.Scan(&r.RoomKey, &payload); err != nil {
			t.Fatalf("scan scheduled_jobs(%s): %v", kind, err)
		}
		if payload.Valid {
			r.Payload = []byte(payload.String)
		}
		out = append(out, r)
	}
	return out
}

func countJobs(t *testing.T, d *sql.DB, kind string) int {
	t.Helper()
	return len(listJobs(t, d, kind))
}

// mailBookingPayload mirrors emails.go's own unexported payload shape — redeclared here since the
// tests live in bookings_test and need to decode what Book/Cancel/Reschedule actually wrote.
type mailBookingPayload struct {
	Kind            string     `json:"kind"`
	BookingID       string     `json:"bookingId"`
	PreviousStartAt *time.Time `json:"previousStartAt,omitempty"`
}

func decodeMailBookingJobs(t *testing.T, rows []jobRow) []mailBookingPayload {
	t.Helper()
	out := make([]mailBookingPayload, len(rows))
	for i, r := range rows {
		if err := json.Unmarshal(r.Payload, &out[i]); err != nil {
			t.Fatalf("decode mail:booking payload %s: %v", r.Payload, err)
		}
	}
	return out
}

// reminderJob returns the pending "booking.reminder" row for bookingID, if any.
func reminderJob(t *testing.T, d *sql.DB, bookingID string) (runAt time.Time, ok bool) {
	t.Helper()
	err := d.QueryRowContext(context.Background(),
		`SELECT run_at FROM scheduled_jobs WHERE kind = 'booking.reminder' AND room_key = $1`,
		"booking:"+bookingID,
	).Scan(&runAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false
	}
	if err != nil {
		t.Fatalf("query booking.reminder for %s: %v", bookingID, err)
	}
	return runAt, true
}

func testBookingMailer(appURL string) *mailer.Mailer {
	return mailer.New(&config.Config{
		SMTPHost: "127.0.0.1", SMTPPort: 1, EmailFrom: "whenweall <no-reply@whenweall.example>", AppURL: appURL,
	})
}

// ownerEmail reads the email of pageID's assigned member (CreatePage defaults it to the creator).
func ownerEmail(t *testing.T, d *sql.DB, pageID string) string {
	t.Helper()
	var email string
	if err := d.QueryRowContext(context.Background(),
		`SELECT u.email FROM users u JOIN booking_pages bp ON bp.member_user_id = u.id WHERE bp.id = $1`, pageID,
	).Scan(&email); err != nil {
		t.Fatalf("ownerEmail(%s): %v", pageID, err)
	}
	return email
}

// TestBookEnqueuesConfirmedMailJob is the brief's required Step-1 test: Book enqueues exactly one
// "mail:booking" job, ids-only (no '@' anywhere in its payload), not room-scoped (every queued mail
// is independent).
func TestBookEnqueuesConfirmedMailJob(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)

	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	rows := listJobs(t, p.db, "mail:booking")
	if len(rows) != 1 {
		t.Fatalf("mail:booking jobs = %d, want 1", len(rows))
	}
	for _, row := range rows {
		if strings.Contains(string(row.Payload), "@") {
			t.Errorf("mail:booking payload contains an address: %s", row.Payload)
		}
		if row.RoomKey.Valid {
			t.Errorf("mail:booking job has a room_key (%s), want none", row.RoomKey.String)
		}
	}

	payload := decodeMailBookingJobs(t, rows)[0]
	if payload.Kind != "confirmed" {
		t.Errorf("Kind = %q, want confirmed", payload.Kind)
	}
	if payload.BookingID != result.BookingID {
		t.Errorf("BookingID = %q, want %q", payload.BookingID, result.BookingID)
	}
}

// TestBookArmsReminderJob: Book on a reminders-enabled page arms "booking.reminder" at
// start-24h (openPageInput's own default is Reminders: true).
func TestBookArmsReminderJob(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)

	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	runAt, ok := reminderJob(t, p.db, result.BookingID)
	if !ok {
		t.Fatal("booking.reminder job not armed")
	}
	want := start.Add(-24 * time.Hour)
	if diff := runAt.Sub(want); diff < -time.Second || diff > time.Second {
		t.Errorf("run_at = %v, want ~%v", runAt, want)
	}
}

// TestBookDoesNotArmReminderWhenPageOptsOut: a page with Reminders: false never gets a
// "booking.reminder" row at all.
func TestBookDoesNotArmReminderWhenPageOptsOut(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, func(in *bookings.PageInput) { in.Reminders = false })
	start := futureUTCSlot(3, 9, 0)

	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	if _, ok := reminderJob(t, p.db, result.BookingID); ok {
		t.Error("booking.reminder job armed for a page with reminders off")
	}
}

// TestCancelCancelsReminderJob: Cancel both enqueues the "cancelled" mail job and cancels the
// booking's pending reminder — ports BookingRoom.cancel's own cancelReminder call.
func TestCancelCancelsReminderJob(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if _, ok := reminderJob(t, p.db, result.BookingID); !ok {
		t.Fatal("booking.reminder job not armed after Book")
	}

	if err := p.svc.Cancel(ctx, result.BookingID, result.ManageToken, false); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	if _, ok := reminderJob(t, p.db, result.BookingID); ok {
		t.Error("booking.reminder job still present after Cancel")
	}

	rows := listJobs(t, p.db, "mail:booking")
	payloads := decodeMailBookingJobs(t, rows)
	var sawCancelled bool
	for _, pl := range payloads {
		if pl.Kind == "cancelled" && pl.BookingID == result.BookingID {
			sawCancelled = true
		}
	}
	if !sawCancelled {
		t.Errorf("no cancelled mail:booking job found among %+v", payloads)
	}
}

// TestRescheduleReArmsReminderJob: Reschedule enqueues a "rescheduled" mail job carrying
// previousStartAt (needed to render "moved from ... to ...", and not otherwise recoverable once
// the booking row's start_at has changed) and re-arms the SAME reminder job at the new start-24h
// — never a second row (jobs.Schedule's room-scoped upsert, id-swapped).
func TestRescheduleReArmsReminderJob(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}

	newStart := futureUTCSlot(5, 14, 0)
	if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, newStart, false); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	runAt, ok := reminderJob(t, p.db, result.BookingID)
	if !ok {
		t.Fatal("booking.reminder job missing after Reschedule")
	}
	want := newStart.Add(-24 * time.Hour)
	if diff := runAt.Sub(want); diff < -time.Second || diff > time.Second {
		t.Errorf("run_at = %v, want ~%v (re-armed at the NEW start)", runAt, want)
	}
	if n := countJobs(t, p.db, "booking.reminder"); n != 1 {
		t.Errorf("booking.reminder rows = %d, want 1 (re-armed in place, not duplicated)", n)
	}

	rows := listJobs(t, p.db, "mail:booking")
	payloads := decodeMailBookingJobs(t, rows)
	var found bool
	for _, pl := range payloads {
		if pl.Kind == "rescheduled" && pl.BookingID == result.BookingID {
			found = true
			if pl.PreviousStartAt == nil {
				t.Fatal("rescheduled payload has no previousStartAt")
			}
			if !pl.PreviousStartAt.Equal(start) {
				t.Errorf("PreviousStartAt = %v, want %v", *pl.PreviousStartAt, start)
			}
		}
	}
	if !found {
		t.Errorf("no rescheduled mail:booking job found among %+v", payloads)
	}
}

// TestRescheduleCancelsReminderWhenPageReminderersOff: rescheduling a booking on a page whose
// reminders are off cancels any pending reminder outright, rather than re-arming it — ports
// BookingRoom.reschedule's if/else exactly.
func TestRescheduleCancelsReminderWhenPageReminderersOff(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if _, ok := reminderJob(t, p.db, result.BookingID); !ok {
		t.Fatal("booking.reminder job not armed after Book")
	}

	if _, err := p.svc.UpdatePage(ctx, p.pageID, p.orgID, openPageInput(func(in *bookings.PageInput) {
		in.Reminders = false
	})); err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}

	newStart := futureUTCSlot(5, 14, 0)
	if _, err := p.svc.Reschedule(ctx, result.BookingID, result.ManageToken, newStart, false); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	if _, ok := reminderJob(t, p.db, result.BookingID); ok {
		t.Error("booking.reminder job still present after Reschedule on a reminders-off page")
	}
}

// TestMailBookingJobSkipsStaleConfirmedAfterCancellation is the brief's required "cancelled
// booking's queued mail becomes a no-op at run time" case: Book leaves a "confirmed" mail:booking
// job pending; Cancel commits before that job ever runs. Running the worker completes the stale
// "confirmed" job as a silent no-op (it's simply gone afterwards, no error, no send attempted) —
// only the "cancelled" job Cancel itself enqueued is left, and it fails/retries because this test's
// mailer points at an unreachable SMTP host, proving the "confirmed" job's disappearance was a
// same-tick no-op rather than a lucky, fast, successful send.
func TestMailBookingJobSkipsStaleConfirmedAfterCancellation(t *testing.T) {
	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "bob@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	if n := countJobs(t, p.db, "mail:booking"); n != 1 {
		t.Fatalf("mail:booking jobs after Book = %d, want 1", n)
	}

	if err := p.svc.Cancel(ctx, result.BookingID, result.ManageToken, false); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if n := countJobs(t, p.db, "mail:booking"); n != 2 {
		t.Fatalf("mail:booking jobs after Cancel = %d, want 2 (stale confirmed + fresh cancelled)", n)
	}

	m := testBookingMailer("https://whenweall.example")
	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, m)

	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	rows := listJobs(t, p.db, "mail:booking")
	if len(rows) != 1 {
		t.Fatalf("mail:booking jobs remaining = %d, want 1 (only cancelled, retrying against unreachable SMTP)", len(rows))
	}
	payload := decodeMailBookingJobs(t, rows)[0]
	if payload.Kind != "cancelled" {
		t.Errorf("remaining job Kind = %q, want cancelled (confirmed should have completed as a no-op)", payload.Kind)
	}
}

// --- Mailpit end-to-end: the actual render+send path for "mail:booking" ---

func startMailpitForBookings(t *testing.T) (smtpHost string, smtpPort int, apiBaseURL string) {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForListeningPort("8025/tcp").WithStartupTimeout(60 * time.Second),
	}
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	if err != nil {
		t.Skipf("mailpit testcontainer unavailable: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	host, err := ctr.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	smtpMapped, err := ctr.MappedPort(ctx, "1025/tcp")
	if err != nil {
		t.Fatalf("smtp mapped port: %v", err)
	}
	apiMapped, err := ctr.MappedPort(ctx, "8025/tcp")
	if err != nil {
		t.Fatalf("api mapped port: %v", err)
	}
	return host, int(smtpMapped.Num()), "http://" + host + ":" + apiMapped.Port()
}

type mailpitAddress struct {
	Address string `json:"Address"`
}

type mailpitMessageSummary struct {
	ID string           `json:"ID"`
	To []mailpitAddress `json:"To"`
}

type mailpitMessagesResponse struct {
	Total    int                     `json:"total"`
	Messages []mailpitMessageSummary `json:"messages"`
}

type mailpitAttachment struct {
	PartID      string `json:"PartID"`
	FileName    string `json:"FileName"`
	ContentType string `json:"ContentType"`
}

type mailpitMessageDetail struct {
	ID          string              `json:"ID"`
	Text        string              `json:"Text"`
	Attachments []mailpitAttachment `json:"Attachments"`
}

func fetchMailpitMessages(t *testing.T, apiBaseURL string) mailpitMessagesResponse {
	t.Helper()
	resp, err := http.Get(apiBaseURL + "/api/v1/messages")
	if err != nil {
		t.Fatalf("GET /api/v1/messages: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /api/v1/messages: %v", err)
	}
	var out mailpitMessagesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal /api/v1/messages: %v", err)
	}
	return out
}

func mailpitTotal(t *testing.T, apiBaseURL string) int {
	t.Helper()
	return fetchMailpitMessages(t, apiBaseURL).Total
}

// fetchMailpitMessageTo returns the detail (including attachments) of the one message addressed
// to `to`. Fails the test if there isn't exactly one.
func fetchMailpitMessageTo(t *testing.T, apiBaseURL, to string) mailpitMessageDetail {
	t.Helper()
	list := fetchMailpitMessages(t, apiBaseURL)
	var id string
	matches := 0
	for _, msg := range list.Messages {
		for _, addr := range msg.To {
			if addr.Address == to {
				id = msg.ID
				matches++
			}
		}
	}
	if matches != 1 {
		t.Fatalf("mailpit messages to %q = %d, want 1", to, matches)
	}

	resp, err := http.Get(apiBaseURL + "/api/v1/message/" + id)
	if err != nil {
		t.Fatalf("GET /api/v1/message/%s: %v", id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /api/v1/message/%s: %v", id, err)
	}
	var out mailpitMessageDetail
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("unmarshal /api/v1/message/%s: %v", id, err)
	}
	return out
}

// fetchMailpitAttachmentContent fetches one attachment part's raw bytes via Mailpit's
// GET /api/v1/message/{ID}/part/{PartID} — the message detail response only carries the
// attachment's metadata (name/content-type), not its content.
func fetchMailpitAttachmentContent(t *testing.T, apiBaseURL, messageID, partID string) string {
	t.Helper()
	resp, err := http.Get(apiBaseURL + "/api/v1/message/" + messageID + "/part/" + partID)
	if err != nil {
		t.Fatalf("GET /api/v1/message/%s/part/%s: %v", messageID, partID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /api/v1/message/%s/part/%s: %v", messageID, partID, err)
	}
	return string(body)
}

// TestMailBookingDeliversRealMail runs the whole re-read-then-render-then-send pipeline for a
// "confirmed" booking against a live Mailpit container: both the visitor's confirmation (with its
// .ics invite) and the organiser's notice actually arrive.
func TestMailBookingDeliversRealMail(t *testing.T) {
	smtpHost, smtpPort, apiBaseURL := startMailpitForBookings(t)
	m := mailer.New(&config.Config{
		SMTPHost: smtpHost, SMTPPort: smtpPort,
		EmailFrom: "whenweall <no-reply@whenweall.example>", AppURL: "https://whenweall.example",
	})

	ctx := context.Background()
	p := setupBookablePage(t, nil)
	start := futureUTCSlot(3, 9, 0)
	result, err := p.svc.Book(ctx, p.orgSlug, p.slug, bookInput(start, "ada@example.com"))
	if err != nil {
		t.Fatalf("Book: %v", err)
	}
	owner := ownerEmail(t, p.db, p.pageID)

	w := jobs.NewWorker(p.db, "test-replica", slog.Default())
	p.svc.RegisterJobs(w, m)
	for {
		n, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n == 0 {
			break
		}
	}
	if remaining := countJobs(t, p.db, "mail:booking"); remaining != 0 {
		t.Fatalf("mail:booking jobs remaining = %d, want 0 (all processed)", remaining)
	}

	// visitor confirmation + organiser notice = 2 real sends.
	if total := mailpitTotal(t, apiBaseURL); total != 2 {
		t.Errorf("mailpit total = %d, want 2", total)
	}

	detail := fetchMailpitMessageTo(t, apiBaseURL, "ada@example.com")
	if len(detail.Attachments) != 1 {
		t.Fatalf("len(Attachments) for ada@example.com = %d, want 1", len(detail.Attachments))
	}
	att := detail.Attachments[0]
	if att.FileName != "calendar.ics" {
		t.Errorf("attachment FileName = %q, want %q", att.FileName, "calendar.ics")
	}
	if att.ContentType != "text/calendar" {
		t.Errorf("attachment ContentType = %q, want %q", att.ContentType, "text/calendar")
	}

	// I4: the .ics invite's own URL property now carries a working manage token (bookingManageURL,
	// emails.go's own doc comment on the conscious call this is) — this port's mail always goes
	// through the async "mail:booking" job (never Book's own return value directly), so this also
	// proves the job independently re-derives the SAME token from the booking id alone. Unfolded
	// first (RFC 5545 line folding, internal/ics's FoldLine): the token pushes this one property line
	// past 75 octets, so the raw wire form now legitimately splits it across a "\r\n " fold this
	// plain Contains would otherwise miss.
	wantURL := m.AppURL() + "/booking/" + result.BookingID + "?t=" + result.ManageToken
	icsBody := fetchMailpitAttachmentContent(t, apiBaseURL, detail.ID, att.PartID)
	unfoldedICS := strings.ReplaceAll(icsBody, "\r\n ", "")
	if !strings.Contains(unfoldedICS, "URL:"+wantURL) {
		t.Errorf(".ics body missing absolute URL %q: %q", wantURL, icsBody)
	}

	// The confirmation body itself (not just the .ics attachment) carries the same working link —
	// extract the token straight out of the delivered mail (rather than assuming it matches
	// result.ManageToken) and prove it's accepted by ManagedBooking.
	linkPrefix := m.AppURL() + "/booking/" + result.BookingID + "?t="
	idx := strings.Index(detail.Text, linkPrefix)
	if idx < 0 {
		t.Fatalf("confirmation body missing a %q link: %q", linkPrefix, detail.Text)
	}
	rest := detail.Text[idx+len(linkPrefix):]
	end := strings.IndexAny(rest, " \r\n")
	if end < 0 {
		end = len(rest)
	}
	deliveredToken := rest[:end]
	if deliveredToken != result.ManageToken {
		t.Errorf("token in mail body = %q, want %q", deliveredToken, result.ManageToken)
	}
	if _, err := p.svc.ManagedBooking(ctx, result.BookingID, deliveredToken, false); err != nil {
		t.Errorf("ManagedBooking with the mail's own token: %v", err)
	}

	organiserDetail := fetchMailpitMessageTo(t, apiBaseURL, owner)
	if len(organiserDetail.Attachments) != 1 {
		t.Errorf("len(Attachments) for organiser = %d, want 1 (the same .ics invite)", len(organiserDetail.Attachments))
	}
}

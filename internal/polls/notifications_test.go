package polls_test

// Ports the behavioral cases from src/server/notifications/__tests__/recipients.workers.test.ts
// (via EnqueueDigestItem + the "poll.digest" job, the one repeatable path that exercises
// resolveRecipients — CloseExpired's open->closed transition, by contrast, only ever fires once
// per poll) plus the brief's own required Step-1 TDD: finalize enqueues one "mail:poll" job per
// emailed recipient (ids-only payload, no addresses), a "poll.deadline" job closes the poll and
// schedules "closed" mail to its recipients, and prefs/following toggles change who's resolved.
//
// The one TS recipients.workers.test.ts case NOT ported: "resolves no push recipients on a free
// org and some on premium" — Go has no billing/entitlements service yet, and this port doesn't
// resolve push recipients at all (see notifications.go's package doc comment).

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/refsdal/whenweall/internal/config"
	"github.com/refsdal/whenweall/internal/jobs"
	"github.com/refsdal/whenweall/internal/mailer"
	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
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

// forceDue backdates every pending job of kind to make it immediately claimable — a test-only
// bypass for jobs (like "poll.digest") this package deliberately arms minutes in the future.
func forceDue(t *testing.T, d *sql.DB, kind string) {
	t.Helper()
	if _, err := d.ExecContext(context.Background(),
		`UPDATE scheduled_jobs SET run_at = now() - interval '1 second' WHERE kind = $1`, kind,
	); err != nil {
		t.Fatalf("forceDue(%s): %v", kind, err)
	}
}

// mailPollPayload mirrors timers.go's unexported payload shape — redeclared here since the tests
// live in polls_test and need to decode what Finalize/Claim/the job handlers actually wrote.
type mailPollPayload struct {
	PollID        string `json:"pollId"`
	Event         string `json:"event"`
	ParticipantID string `json:"participantId"`
	UserID        string `json:"userId"`
}

func decodeMailPollJobs(t *testing.T, rows []jobRow) []mailPollPayload {
	t.Helper()
	out := make([]mailPollPayload, len(rows))
	for i, r := range rows {
		if err := json.Unmarshal(r.Payload, &out[i]); err != nil {
			t.Fatalf("decode mail:poll payload %s: %v", r.Payload, err)
		}
	}
	return out
}

func filterByEvent(payloads []mailPollPayload, event string) []mailPollPayload {
	var out []mailPollPayload
	for _, p := range payloads {
		if p.Event == event {
			out = append(out, p)
		}
	}
	return out
}

func testMailer(appURL string) *mailer.Mailer {
	return mailer.New(&config.Config{
		SMTPHost: "127.0.0.1", SMTPPort: 1, EmailFrom: "whenweall <no-reply@whenweall.example>", AppURL: appURL,
	}, nil)
}

// TestFinalizeEnqueuesMailForEachEmailedParticipant is the brief's required Step-1 test: N
// emailed recipients -> N scheduled "mail:poll" rows, kind + ids-only payload, never an address.
func TestFinalizeEnqueuesMailForEachEmailedParticipant(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	slot := created.Options[0].ID

	seedParticipant(t, d, created.ID, "Ada", map[string]string{slot: "yes"}, "ada@example.com")
	seedParticipant(t, d, created.ID, "Bob", map[string]string{slot: "yes"}, "")                 // no email -> excluded
	seedParticipant(t, d, created.ID, "Cleo", map[string]string{slot: "yes"}, "ADA@example.com") // same address, different case -> deduped with Ada

	if err := s.Finalize(ctx, created.ID, orgID, slot, ownerID); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	all := listJobs(t, d, "mail:poll")
	for _, row := range all {
		if strings.Contains(string(row.Payload), "@") {
			t.Errorf("mail:poll payload contains an address: %s", row.Payload)
		}
		if row.RoomKey.Valid {
			t.Errorf("mail:poll job has a room_key (%s), want none — every queued mail is independent", row.RoomKey.String)
		}
	}

	payloads := decodeMailPollJobs(t, all)
	finalized := filterByEvent(payloads, "finalized")
	// Ada+Cleo dedupe to one recipient, plus the poll's owner (a distinct address) = 2.
	if len(finalized) != 2 {
		t.Fatalf("finalized mail:poll jobs = %d, want 2 (deduped participant + owner); payloads = %+v", len(finalized), finalized)
	}
	var sawParticipant, sawOwner bool
	for _, p := range finalized {
		if p.PollID != created.ID {
			t.Errorf("PollID = %q, want %q", p.PollID, created.ID)
		}
		if p.ParticipantID != "" {
			sawParticipant = true
		}
		if p.UserID != "" {
			sawOwner = true
		}
	}
	if !sawParticipant || !sawOwner {
		t.Errorf("finalized payloads = %+v, want one participantId-keyed and one userId-keyed job", finalized)
	}
}

// TestFinalizeCancelsPendingDeadlineJob ports finalizePoll's own syncDeadline(id, null) call.
func TestFinalizeCancelsPendingDeadlineJob(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	future := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &future}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if n := countJobs(t, d, "poll.deadline"); n != 1 {
		t.Fatalf("poll.deadline jobs before finalize = %d, want 1", n)
	}

	if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	if n := countJobs(t, d, "poll.deadline"); n != 0 {
		t.Errorf("poll.deadline jobs after finalize = %d, want 0", n)
	}
}

// TestDeadlineArming ports PollRoom.syncDeadline's re-arm semantics as reached through
// Create/Update, per polls.functions.ts's own call sites.
func TestDeadlineArming(t *testing.T) {
	ctx := context.Background()

	t.Run("Create with a deadline arms poll.deadline", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		future := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")

		view, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "Has deadline", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &future,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		deadlineJobs := listJobs(t, d, "poll.deadline")
		if len(deadlineJobs) != 1 {
			t.Fatalf("poll.deadline jobs = %d, want 1", len(deadlineJobs))
		}
		if !deadlineJobs[0].RoomKey.Valid || deadlineJobs[0].RoomKey.String != "poll:"+view.ID {
			t.Errorf("room_key = %+v, want poll:%s", deadlineJobs[0].RoomKey, view.ID)
		}
	})

	t.Run("Create without a deadline arms nothing", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		createTestPoll(t, ctx, s, orgID, ownerID)
		if n := countJobs(t, d, "poll.deadline"); n != 0 {
			t.Errorf("poll.deadline jobs = %d, want 0", n)
		}
	})

	t.Run("Update without touching deadlineAt leaves an armed job alone", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		future := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &future,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		newTitle := "Renamed"
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{Title: &newTitle}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if n := countJobs(t, d, "poll.deadline"); n != 1 {
			t.Errorf("poll.deadline jobs after untouched-deadline update = %d, want 1 (still armed)", n)
		}
	})

	t.Run("Update clearing the deadline cancels the job", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		future := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &future,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: nil}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if n := countJobs(t, d, "poll.deadline"); n != 0 {
			t.Errorf("poll.deadline jobs after clearing = %d, want 0", n)
		}
	})

	t.Run("Update re-arming to a new deadline upserts (still exactly one row)", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		first := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &first,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		second := time.Now().Add(2 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &second}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		deadlineJobs := listJobs(t, d, "poll.deadline")
		if len(deadlineJobs) != 1 {
			t.Fatalf("poll.deadline jobs = %d, want 1 (upsert, not append)", len(deadlineJobs))
		}
	})

	// I11: the 24h "closes soon" (deadline.approaching) reminder is armed/cancelled alongside
	// poll.deadline by the same armDeadline call — these cases are its own arming math, ported
	// from syncDeadline's `remindAt > Date.now()` check (PollRoom.ts).

	t.Run("Create with a deadline less than 24h away arms poll.deadline but not poll.reminder", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		soon := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")

		if _, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &soon,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if n := countJobs(t, d, "poll.deadline"); n != 1 {
			t.Errorf("poll.deadline jobs = %d, want 1", n)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 0 {
			t.Errorf("poll.reminder jobs = %d, want 0 (deadline is under 24h away — firing now would read as a bug)", n)
		}
	})

	t.Run("Create with a deadline more than 24h away arms poll.reminder 24h ahead of it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		deadline := time.Now().Add(72 * time.Hour)
		deadlineStr := deadline.UTC().Format("2006-01-02T15:04:05.000Z")

		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &deadlineStr,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		reminderJobs := listJobs(t, d, "poll.reminder")
		if len(reminderJobs) != 1 {
			t.Fatalf("poll.reminder jobs = %d, want 1", len(reminderJobs))
		}
		if !reminderJobs[0].RoomKey.Valid || reminderJobs[0].RoomKey.String != "poll:"+created.ID {
			t.Errorf("room_key = %+v, want poll:%s", reminderJobs[0].RoomKey, created.ID)
		}

		var runAt time.Time
		if err := d.QueryRowContext(ctx,
			`SELECT run_at FROM scheduled_jobs WHERE kind = 'poll.reminder' AND room_key = $1`, "poll:"+created.ID,
		).Scan(&runAt); err != nil {
			t.Fatalf("query run_at: %v", err)
		}
		wantRunAt := deadline.Add(-24 * time.Hour)
		if diff := runAt.Sub(wantRunAt); diff < -time.Second || diff > time.Second {
			t.Errorf("run_at = %v, want %v (deadline - 24h)", runAt, wantRunAt)
		}
	})

	t.Run("Update extending a deadline from under 24h to over 24h away arms the reminder", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		soon := time.Now().Add(time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &soon,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 0 {
			t.Fatalf("poll.reminder jobs before extending = %d, want 0", n)
		}

		later := time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &later}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 1 {
			t.Errorf("poll.reminder jobs after extending past 24h = %d, want 1", n)
		}
	})

	t.Run("Update re-arming a still-over-24h deadline upserts the reminder (still exactly one row)", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		first := time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &first,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		second := time.Now().Add(96 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &second}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 1 {
			t.Errorf("poll.reminder jobs = %d, want 1 (upsert, not append)", n)
		}
	})

	t.Run("Update clearing the deadline cancels the reminder too", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		future := time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &future,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 1 {
			t.Fatalf("poll.reminder jobs before clearing = %d, want 1", n)
		}

		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: nil}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 0 {
			t.Errorf("poll.reminder jobs after clearing = %d, want 0", n)
		}
	})

	t.Run("Finalize cancels a pending reminder", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		future := time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &future,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 1 {
			t.Fatalf("poll.reminder jobs before finalize = %d, want 1", n)
		}

		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 0 {
			t.Errorf("poll.reminder jobs after finalize = %d, want 0", n)
		}
	})

	t.Run("Delete cancels a pending reminder", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		future := time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options: basicOptions(), DeadlineAt: &future,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 1 {
			t.Fatalf("poll.reminder jobs before delete = %d, want 1", n)
		}

		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if n := countJobs(t, d, "poll.reminder"); n != 0 {
			t.Errorf("poll.reminder jobs after delete = %d, want 0", n)
		}
	})
}

// TestReminderJobFiresMailToRecipients is I11's firing proof: once poll.reminder is due, it
// resolves deadline.approaching's recipients (systemDefaults already default that event on) and
// schedules one ids-only "mail:poll"/"deadline.approaching" job per recipient — mirroring
// TestPollDeadlineJobClosesPollAndSchedulesClosedMail's own shape for the "closed" event.
func TestReminderJobFiresMailToRecipients(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "member")
	mateID := seedUser(t, d)
	addOrgMember(t, d, orgID, mateID, "member")

	future := time.Now().Add(72 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")
	created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
		Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
		Options: basicOptions(), DeadlineAt: &future,
	}) // owner auto-subscribed (ensureCreatorSubscription)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
		t.Fatalf("SetFollowing: %v", err)
	}

	forceDue(t, d, "poll.reminder")

	w := jobs.NewWorker(d, "test-replica", slog.Default())
	s.RegisterJobs(w, testMailer("https://whenweall.example"))
	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce (poll.reminder): %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 (the poll.reminder job)", processed)
	}

	rows := listJobs(t, d, "mail:poll")
	reminderJobs := filterByEvent(decodeMailPollJobs(t, rows), "deadline.approaching")
	gotUserIDs := map[string]bool{}
	for _, p := range reminderJobs {
		gotUserIDs[p.UserID] = true
	}
	if !gotUserIDs[ownerID] || !gotUserIDs[mateID] || len(gotUserIDs) != 2 {
		t.Errorf("deadline.approaching mail recipients = %v, want exactly {%s, %s}", gotUserIDs, ownerID, mateID)
	}
	for _, r := range rows {
		if strings.Contains(string(r.Payload), "@") {
			t.Errorf("mail:poll payload contains an address: %s", r.Payload)
		}
	}

	// The one-shot poll.reminder job itself must be gone (jobs.Complete removes it), not left
	// behind to re-fire.
	if n := countJobs(t, d, "poll.reminder"); n != 0 {
		t.Errorf("poll.reminder jobs remaining after firing = %d, want 0", n)
	}
}

// TestClaimEnqueuesClaimConfirmationMail ports claimSlot's call into sendClaimConfirmation.
func TestClaimEnqueuesClaimConfirmationMail(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
	slot := created.Options[0]

	result, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{
		Name: "Ada", Email: strPtr("ada@example.com"),
	}, polls.Viewer{})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}

	rows := listJobs(t, d, "mail:poll")
	payloads := decodeMailPollJobs(t, rows)
	claims := filterByEvent(payloads, "claim_confirmation")
	if len(claims) != 1 {
		t.Fatalf("claim_confirmation mail:poll jobs = %d, want 1", len(claims))
	}
	if claims[0].ParticipantID != result.ParticipantID {
		t.Errorf("ParticipantID = %q, want %q", claims[0].ParticipantID, result.ParticipantID)
	}
	for _, r := range rows {
		if strings.Contains(string(r.Payload), "@") {
			t.Errorf("payload contains an address: %s", r.Payload)
		}
	}

	// Re-claiming the same slot is a no-op (Changed: false) and must NOT resend the confirmation —
	// ported from claimSlot's own `if (result.changed) { ... sendClaimConfirmation ... }` gate
	// (participants.functions.ts): only the changed branch calls it at all.
	if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{ParticipantID: result.ParticipantID}, polls.Viewer{GuestParticipantID: result.ParticipantID}); err != nil {
		t.Fatalf("re-Claim: %v", err)
	}
	if n := len(filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "claim_confirmation")); n != 1 {
		t.Errorf("claim_confirmation jobs after a no-op re-claim = %d, want 1 (unchanged)", n)
	}
}

// TestUnclaimEnqueuesClaimConfirmationMail ports unclaimSlot's unconditional call into
// sendClaimConfirmation: unlike claimSlot, there is no `changed` gate on the TS side at all — every
// successful unclaim resends the confirmation so the participant's email reflects their remaining
// claims, even when that leaves them with none (sendClaimConfirmation itself no-ops in that case).
func TestUnclaimEnqueuesClaimConfirmationMail(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 2)
	slot1, slot2 := created.Options[0], created.Options[1]

	result, err := s.Claim(ctx, created.ID, slot1.ID, polls.ClaimInput{
		Name: "Ada", Email: strPtr("ada@example.com"),
	}, polls.Viewer{})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if _, err := s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{
		ParticipantID: result.ParticipantID,
	}, polls.Viewer{GuestParticipantID: result.ParticipantID}); err != nil {
		t.Fatalf("Claim (2): %v", err)
	}
	// Both claims already enqueued a claim_confirmation job of their own — start counting fresh
	// from here.
	before := len(filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "claim_confirmation"))

	if err := s.Unclaim(ctx, created.ID, slot1.ID, polls.Viewer{GuestParticipantID: result.ParticipantID}); err != nil {
		t.Fatalf("Unclaim: %v", err)
	}
	claims := filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "claim_confirmation")
	if len(claims) != before+1 {
		t.Fatalf("claim_confirmation jobs after Unclaim = %d, want %d", len(claims), before+1)
	}
	if claims[len(claims)-1].ParticipantID != result.ParticipantID {
		t.Errorf("ParticipantID = %q, want %q", claims[len(claims)-1].ParticipantID, result.ParticipantID)
	}

	// Unclaiming their last remaining slot still enqueues the job — sendClaimConfirmationMail's
	// own zero-remaining-claims no-op (timers.go) is what makes this safe, not a gate here.
	if err := s.Unclaim(ctx, created.ID, slot2.ID, polls.Viewer{GuestParticipantID: result.ParticipantID}); err != nil {
		t.Fatalf("Unclaim (last slot): %v", err)
	}
	claims = filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "claim_confirmation")
	if len(claims) != before+2 {
		t.Fatalf("claim_confirmation jobs after unclaiming the last slot = %d, want %d", len(claims), before+2)
	}
}

// TestPollDeadlineJobClosesPollAndSchedulesClosedMail ports PollRoom#processDeadline: the
// "poll.deadline" handler closes the poll via CloseExpired and, on an actual transition,
// schedules one "mail:poll"/"closed" job per subscribed recipient.
func TestPollDeadlineJobClosesPollAndSchedulesClosedMail(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "member")
	mateID := seedUser(t, d)
	addOrgMember(t, d, orgID, mateID, "member")

	created := createTestPoll(t, ctx, s, orgID, ownerID) // owner auto-subscribed (ensureCreatorSubscription)
	if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
		t.Fatalf("SetFollowing: %v", err)
	}

	past := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &past}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	w := jobs.NewWorker(d, "test-replica", slog.Default())
	s.RegisterJobs(w, testMailer("https://whenweall.example"))

	processed, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1 (the poll.deadline job)", processed)
	}

	view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
	if view.Status != "closed" {
		t.Fatalf("Status = %q, want closed", view.Status)
	}

	closedJobs := filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "closed")
	gotUserIDs := map[string]bool{}
	for _, p := range closedJobs {
		gotUserIDs[p.UserID] = true
	}
	if !gotUserIDs[ownerID] || !gotUserIDs[mateID] || len(gotUserIDs) != 2 {
		t.Errorf("closed mail recipients = %v, want exactly {%s, %s}", gotUserIDs, ownerID, mateID)
	}
}

// TestResolveRecipientsViaDigest ports recipients.workers.test.ts case-for-case, using
// EnqueueDigestItem + the "poll.digest" job as the (repeatable) window into resolveRecipients —
// CloseExpired's open->closed transition, by contrast, only ever fires once per poll.
func TestResolveRecipientsViaDigest(t *testing.T) {
	ctx := context.Background()

	// digestRecipientUserIDs enqueues one response.created digest item (actor optional) and
	// returns the set of userIDs the resulting "mail:poll"/"digest" jobs were addressed to.
	digestRecipientUserIDs := func(t *testing.T, d *sql.DB, s *polls.Service, pollID, actorUserID string) map[string]bool {
		t.Helper()
		if err := s.EnqueueDigestItem(ctx, pollID, polls.DigestItem{
			Event: polls.EventResponseCreated, Name: "Someone", ActorUserID: actorUserID,
		}); err != nil {
			t.Fatalf("EnqueueDigestItem: %v", err)
		}
		forceDue(t, d, "poll.digest")

		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, testMailer("https://whenweall.example"))
		if _, err := w.RunOnce(ctx); err != nil {
			t.Fatalf("RunOnce (poll.digest): %v", err)
		}

		got := map[string]bool{}
		for _, p := range filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "digest") {
			got[p.UserID] = true
		}
		return got
	}

	t.Run("returns the creator on system defaults", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		got := digestRecipientUserIDs(t, d, s, created.ID, "")
		if len(got) != 1 || !got[ownerID] {
			t.Errorf("recipients = %v, want {%s}", got, ownerID)
		}
	})

	t.Run("omits an event the user turned off in their defaults", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		setUserNotificationDefaults(t, d, ownerID, `{"response.created": {"email": false, "push": false}}`)

		got := digestRecipientUserIDs(t, d, s, created.ID, "")
		if len(got) != 0 {
			t.Errorf("recipients = %v, want none", got)
		}
	})

	t.Run("lets a per-poll override win over the user default", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		setUserNotificationDefaults(t, d, ownerID, `{"response.created": {"email": false, "push": false}}`)
		if err := s.UpdateNotificationPrefs(ctx, created.ID, orgID, ownerID, polls.NotificationGrid{
			polls.EventResponseCreated: {Email: true, Push: false},
		}); err != nil {
			t.Fatalf("UpdateNotificationPrefs: %v", err)
		}

		got := digestRecipientUserIDs(t, d, s, created.ID, "")
		if len(got) != 1 || !got[ownerID] {
			t.Errorf("recipients = %v, want {%s}", got, ownerID)
		}
	})

	t.Run("includes a teammate who follows the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		mateID := seedUser(t, d)
		addOrgMember(t, d, orgID, mateID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
			t.Fatalf("SetFollowing: %v", err)
		}

		got := digestRecipientUserIDs(t, d, s, created.ID, "")
		if len(got) != 2 || !got[ownerID] || !got[mateID] {
			t.Errorf("recipients = %v, want {%s, %s}", got, ownerID, mateID)
		}
	})

	t.Run("excludes a subscriber who is no longer an org member", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		_, outsiderID := seedOrgAndUser(t, d) // a member of a DIFFERENT org
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.SetFollowing(ctx, created.ID, orgID, outsiderID, true); err != nil {
			t.Fatalf("SetFollowing: %v", err)
		}

		got := digestRecipientUserIDs(t, d, s, created.ID, "")
		if len(got) != 1 || !got[ownerID] {
			t.Errorf("recipients = %v, want {%s} (outsider excluded)", got, ownerID)
		}
	})

	t.Run("suppresses the actor who caused the event", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		mateID := seedUser(t, d)
		addOrgMember(t, d, orgID, mateID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
			t.Fatalf("SetFollowing: %v", err)
		}

		got := digestRecipientUserIDs(t, d, s, created.ID, mateID)
		if len(got) != 1 || !got[ownerID] {
			t.Errorf("recipients = %v, want {%s} (actor mate suppressed)", got, ownerID)
		}
	})

	t.Run("returns nothing when nobody is subscribed", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.SetFollowing(ctx, created.ID, orgID, ownerID, false); err != nil {
			t.Fatalf("SetFollowing(unfollow owner): %v", err)
		}

		got := digestRecipientUserIDs(t, d, s, created.ID, "")
		if len(got) != 0 {
			t.Errorf("recipients = %v, want none", got)
		}
	})
}

// setUserNotificationDefaults seeds notification_prefs directly via SQL — this port has no
// service method for a user's own cross-poll defaults (that's a settings-UI concern, out of this
// task's scope; see notifications.go), exactly mirroring recipients.workers.test.ts's own direct
// `db.insert(notificationPrefs).values(...)` in its test setup.
func setUserNotificationDefaults(t *testing.T, d *sql.DB, userID, channelsJSON string) {
	t.Helper()
	uid, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		t.Fatalf("parse userID: %v", err)
	}
	if _, err := d.ExecContext(context.Background(),
		`INSERT INTO notification_prefs (user_id, channels, created_at, updated_at) VALUES ($1, $2::jsonb, now(), now())`,
		uid, channelsJSON,
	); err != nil {
		t.Fatalf("seeding notification_prefs: %v", err)
	}
}

// --- Mailpit end-to-end: the actual render+send path for "mail:poll" ---

func startMailpitForPolls(t *testing.T) (smtpHost string, smtpPort int, apiBaseURL string) {
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
		testdb.Unavailable(t, "mailpit testcontainer", err)
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
	return host, int(smtpMapped.Num()), fmt.Sprintf("http://%s:%s", host, apiMapped.Port())
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

// TestMailPollDeliversRealMail runs the "finalized" (participant path) and "claim_confirmation"
// send paths against a live Mailpit container, proving out the whole re-read-then-render-then-
// send pipeline for real — not just the scheduled_jobs footprint the other tests here assert.
func TestMailPollDeliversRealMail(t *testing.T) {
	smtpHost, smtpPort, apiBaseURL := startMailpitForPolls(t)
	m := mailer.New(&config.Config{
		SMTPHost: smtpHost, SMTPPort: smtpPort,
		EmailFrom: "whenweall <no-reply@whenweall.example>", AppURL: "https://whenweall.example",
	}, nil)

	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)

	// finalized: a datetime poll, one participant with an email, finalize it.
	scheduling := createTestPoll(t, ctx, s, orgID, ownerID)
	seedParticipant(t, d, scheduling.ID, "Ada", map[string]string{scheduling.Options[0].ID: "yes"}, "ada@example.com")
	if err := s.Finalize(ctx, scheduling.ID, orgID, scheduling.Options[0].ID, ownerID); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	// claim_confirmation: a signup poll, claim a slot with an email.
	signup := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
	if _, err := s.Claim(ctx, signup.ID, signup.Options[0].ID, polls.ClaimInput{
		Name: "Bob", Email: strPtr("bob@example.com"),
	}, polls.Viewer{}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	w := jobs.NewWorker(d, "test-replica", slog.Default())
	s.RegisterJobs(w, m)

	// Two "mail:poll" jobs pending (finalized participant + finalized owner, since owner has a
	// distinct address, plus claim_confirmation) — drain until none are left.
	for {
		n, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n == 0 {
			break
		}
	}

	if remaining := countJobs(t, d, "mail:poll"); remaining != 0 {
		t.Fatalf("mail:poll jobs remaining = %d, want 0 (all processed)", remaining)
	}

	// finalized (participant) + finalized (owner) + claim_confirmation = 3 real sends.
	if total := mailpitTotal(t, apiBaseURL); total != 3 {
		t.Errorf("mailpit total = %d, want 3", total)
	}

	// The finalized participant mail (Ada) is for a datetime poll's finalized option — it must
	// carry the .ics invite BuildPollICS produces (internal/polls/ics.go), attached by
	// sendFinalizedMail (timers.go).
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

	// The VEVENT's URL property must be the same absolute poll link (m.AppURL() + "/p/" + id)
	// sendFinalizedMail's own mail body links to, not a bare "/p/{id}" — the .ics is attached
	// via BuildPollICS(ctx, s.q, poll.ID, pollURL), threading that absolute URL straight through.
	wantURL := m.AppURL() + "/p/" + scheduling.ID
	icsBody := fetchMailpitAttachmentContent(t, apiBaseURL, detail.ID, att.PartID)
	if !strings.Contains(icsBody, "URL:"+wantURL) {
		t.Errorf(".ics body missing absolute URL %q: %q", wantURL, icsBody)
	}
}

// TestEnqueueDigestItemConcurrentRace is I4's evidence: two goroutines racing EnqueueDigestItem
// for the SAME poll must both land their item in the accumulator, not have one silently clobber
// the other. Without the pg_advisory_xact_lock in EnqueueDigestItem (timers.go), the read-modify-
// write there has no row lock of its own, and jobs.Schedule's upsert blindly overwrites the whole
// payload with whatever the calling transaction computed — so two concurrent calls that both read
// the same starting payload can each append their own item to that same stale snapshot, and
// whichever commits last wins, silently dropping the other's item.
func TestEnqueueDigestItemConcurrentRace(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	var wg sync.WaitGroup
	names := make([]string, 20)
	for i := range names {
		names[i] = fmt.Sprintf("Racer%d", i)
	}
	errs := make(chan error, len(names))
	start := make(chan struct{})
	for _, name := range names {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			errs <- s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{
				Event: polls.EventResponseCreated, Name: name,
			})
		}(name)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("EnqueueDigestItem: %v", err)
		}
	}

	rows, err := d.QueryContext(ctx,
		`SELECT payload FROM scheduled_jobs WHERE kind = $1 AND room_key = $2`,
		"poll.digest", "poll:"+created.ID)
	if err != nil {
		t.Fatalf("query scheduled_jobs: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var payloads []struct {
		PollID string             `json:"pollId"`
		Items  []polls.DigestItem `json:"items"`
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan payload: %v", err)
		}
		var p struct {
			PollID string             `json:"pollId"`
			Items  []polls.DigestItem `json:"items"`
		}
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		payloads = append(payloads, p)
	}
	if len(payloads) != 1 {
		t.Fatalf("poll.digest rows for this poll = %d, want 1 (one accumulator row)", len(payloads))
	}

	gotNames := map[string]bool{}
	for _, item := range payloads[0].Items {
		gotNames[item.Name] = true
	}
	for _, name := range names {
		if !gotNames[name] {
			t.Errorf("digest items = %+v, missing %q (lost update — the advisory lock isn't serializing concurrent accumulation)", payloads[0].Items, name)
		}
	}
	if len(payloads[0].Items) != len(names) {
		t.Errorf("len(Items) = %d, want %d: %+v", len(payloads[0].Items), len(names), payloads[0].Items)
	}
}

// recordingMailer is a polls.MailSender that records every Send instead of dialing SMTP — the
// seam send-path tests use to assert on the rendered Message (template, Data, attachments)
// directly, without Mailpit.
type recordingMailer struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (r *recordingMailer) AppURL() string { return "https://whenweall.example" }

func (r *recordingMailer) Send(_ context.Context, msg mailer.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, msg)
	return nil
}

func (r *recordingMailer) byTemplate(template string) []mailer.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []mailer.Message
	for _, m := range r.sent {
		if m.Template == template {
			out = append(out, m)
		}
	}
	return out
}

// fakeLocales is a polls.LocaleSource keyed by userID — the test stand-in for
// auth.Service.LocaleFor (Plan A), which this package never calls directly in tests.
type fakeLocales map[string]string

func (f fakeLocales) LocaleFor(_ context.Context, userID string) string {
	if l, ok := f[userID]; ok {
		return l
	}
	return "en"
}

// countingLocales is a polls.LocaleSource that records how many times LocaleFor was called for
// each userID — the seam TestFanOutDigestItemsMemoizesLocaleLookups uses to pin the fix for the
// N+1 locale lookup a whole-plan review flagged (resolveRecipients/fanOutDigestItems/
// userLocaleMemo, locale.go/notifications.go/timers.go).
type countingLocales struct {
	mu    sync.Mutex
	calls map[string]int
}

func (c *countingLocales) LocaleFor(_ context.Context, userID string) string {
	c.mu.Lock()
	c.calls[userID]++
	c.mu.Unlock()
	return "en"
}

// drainJobs runs w until a RunOnce claims nothing — every due job, and every job those jobs
// scheduled in turn (poll.digest -> mail:poll), has been processed.
func drainJobs(t *testing.T, ctx context.Context, w *jobs.Worker) {
	t.Helper()
	for i := 0; i < 50; i++ {
		n, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("drainJobs: jobs still pending after 50 rounds")
}

// TestUserRecipientMailUsesLocaleSource restores the user.locale half of the old recipients
// (main:src/server/notifications/recipients.ts:78, finalize-emails.ts:51): a user-identified
// recipient renders in the locale the LocaleSource (auth.Service.LocaleFor in production)
// reports, not a hard-coded "en".
func TestUserRecipientMailUsesLocaleSource(t *testing.T) {
	ctx := context.Background()

	t.Run("digest and finalized owner mail render in the user's locale", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		s.SetLocaleSource(fakeLocales{ownerID: "nb"})
		created := createTestPoll(t, ctx, s, orgID, ownerID) // Create subscribes the creator

		if err := s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{Event: polls.EventResponseCreated, Name: "Ada"}); err != nil {
			t.Fatalf("EnqueueDigestItem: %v", err)
		}
		forceDue(t, d, "poll.digest")
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ""); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		digests := rec.byTemplate("digest")
		if len(digests) != 1 || digests[0].Data["Locale"] != "nb" {
			t.Errorf("digest mails = %+v, want exactly one with Locale nb", digests)
		}
		finalized := rec.byTemplate("finalized")
		if len(finalized) != 1 || finalized[0].Data["Locale"] != "nb" {
			t.Errorf("finalized mails = %+v, want exactly one (the owner) with Locale nb", finalized)
		}
	})

	t.Run("falls back to en when no LocaleSource is wired", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{Event: polls.EventResponseCreated, Name: "Ada"}); err != nil {
			t.Fatalf("EnqueueDigestItem: %v", err)
		}
		forceDue(t, d, "poll.digest")

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		if digests := rec.byTemplate("digest"); len(digests) != 1 || digests[0].Data["Locale"] != "en" {
			t.Errorf("digest mails = %+v, want exactly one with Locale en", digests)
		}
	})
}

// fixtureStart/fixtureEnd is the one dated slot every locale-label test uses: Tue 1 Sep 2026,
// 18:30–19:30 Europe/Oslo (16:30–17:30 UTC).
const (
	fixtureStart = "2026-09-01T16:30:00.000Z"
	fixtureEnd   = "2026-09-01T17:30:00.000Z"
	wantLabelEN  = "Tue 1 Sep, 18:30–19:30"
	wantLabelNB  = "tir. 1. sep., 18:30–19:30"
)

// createDatedSignupPoll creates a sign-up sheet (Europe/Oslo) whose single slot is the fixture
// datetime above, with unlimited capacity and maxClaims 2.
func createDatedSignupPoll(t *testing.T, ctx context.Context, s *polls.Service, orgID, userID string) *polls.PollView {
	t.Helper()
	view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
		Type: polls.PollTypeSignup, Title: "Shifts", Timezone: "Europe/Oslo",
		Options:         []polls.OptionInput{withCapacity(datetimeOption(fixtureStart, fixtureEnd), nil)},
		SignupMaxClaims: intPtr(2),
	})
	if err != nil {
		t.Fatalf("Create (dated signup): %v", err)
	}
	return view
}

// TestOptionLabelsFollowRecipientLocale ports formatOptionLabel's per-locale output
// (main:src/lib/__tests__/time.test.ts:18-49: en "Tue 1 Sep" / "18:30" / "– 19:30", nb weekday
// "tir…") as rendered into claim-confirmation Slots and the finalized mail's OptionLabel, via
// Plan A's mailer.Format* helpers.
func TestOptionLabelsFollowRecipientLocale(t *testing.T) {
	ctx := context.Background()

	t.Run("claim confirmation slots use the participant's locale", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createDatedSignupPoll(t, ctx, s, orgID, ownerID)
		slot := created.Options[0].ID

		if _, err := s.Claim(ctx, created.ID, slot, polls.ClaimInput{Name: "Kari", Email: strPtr("kari@example.com"), Locale: strPtr("nb")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim (nb): %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, slot, polls.ClaimInput{Name: "Ada", Email: strPtr("ada@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim (en): %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		want := map[string][2]string{"kari@example.com": {"nb", wantLabelNB}, "ada@example.com": {"en", wantLabelEN}}
		mails := rec.byTemplate("claim_confirmation")
		if len(mails) != 2 {
			t.Fatalf("claim_confirmation mails = %d, want 2", len(mails))
		}
		for _, m := range mails {
			exp := want[m.To]
			slots, _ := m.Data["Slots"].([]string)
			if m.Data["Locale"] != exp[0] || len(slots) != 1 || slots[0] != exp[1] {
				t.Errorf("mail to %s: Locale=%v Slots=%v, want Locale=%s Slots=[%s]", m.To, m.Data["Locale"], slots, exp[0], exp[1])
			}
		}
	})

	t.Run("finalized mail's OptionLabel uses the recipient's locale", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		s.SetLocaleSource(fakeLocales{ownerID: "nb"})
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "Kickoff", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{datetimeOption(fixtureStart, fixtureEnd)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name: "Ada", Email: strPtr("ada@example.com"), Answers: map[string]string{created.Options[0].ID: "yes"},
		}, polls.Viewer{}); err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ""); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		var gotEN, gotNB bool
		for _, m := range rec.byTemplate("finalized") {
			switch {
			case m.To == "ada@example.com" && m.Data["Locale"] == "en" && m.Data["OptionLabel"] == wantLabelEN:
				gotEN = true
			case m.To != "ada@example.com" && m.Data["Locale"] == "nb" && m.Data["OptionLabel"] == wantLabelNB:
				gotNB = true
			default:
				t.Errorf("unexpected finalized mail: To=%s Locale=%v OptionLabel=%v", m.To, m.Data["Locale"], m.Data["OptionLabel"])
			}
		}
		if !gotEN || !gotNB {
			t.Errorf("finalized mails: en participant seen=%v, nb owner seen=%v; want both", gotEN, gotNB)
		}
	})
}

// TestClaimConfirmationAttachesMultiEventICS: the sent claim_confirmation mail carries
// calendar.ics with one VEVENT per claimed dated slot (claim-emails.ts), and no attachment at all
// for a text-only sheet.
func TestClaimConfirmationAttachesMultiEventICS(t *testing.T) {
	ctx := context.Background()

	t.Run("dated slots: one attachment, one VEVENT per claim", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeSignup, Title: "Shifts", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{
				withCapacity(datetimeOption(fixtureStart, fixtureEnd), nil),
				withCapacity(datetimeOption("2026-09-02T16:30:00.000Z"), nil),
			},
			SignupMaxClaims: intPtr(2),
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		first, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Ada", Email: strPtr("ada@example.com")}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim 1: %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, created.Options[1].ID, polls.ClaimInput{ParticipantID: first.ParticipantID}, polls.Viewer{GuestParticipantID: first.ParticipantID}); err != nil {
			t.Fatalf("Claim 2: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		mails := rec.byTemplate("claim_confirmation")
		if len(mails) == 0 {
			t.Fatal("no claim_confirmation mail sent")
		}
		last := mails[len(mails)-1] // re-reads current claims at send time: both slots by now
		if len(last.Attachments) != 1 {
			t.Fatalf("attachments = %d, want 1", len(last.Attachments))
		}
		att := last.Attachments[0]
		if att.Filename != "calendar.ics" || att.ContentType != "text/calendar" {
			t.Errorf("attachment = %q/%q, want calendar.ics/text/calendar", att.Filename, att.ContentType)
		}
		body := string(att.Content)
		if got := strings.Count(body, "BEGIN:VEVENT\r\n"); got != 2 {
			t.Errorf("BEGIN:VEVENT count = %d, want 2: %q", got, body)
		}
		for _, o := range created.Options {
			if !strings.Contains(body, "UID:"+created.ID+"-"+o.ID+"@whenweall\r\n") {
				t.Errorf("body missing UID for option %s: %q", o.ID, body)
			}
		}
	})

	t.Run("text-only slots: no attachment", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Bob", Email: strPtr("bob@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim: %v", err)
		}

		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)
		drainJobs(t, ctx, w)

		mails := rec.byTemplate("claim_confirmation")
		if len(mails) != 1 || len(mails[0].Attachments) != 0 {
			t.Errorf("mails = %+v, want exactly one with no attachments", mails)
		}
	})
}

// claimOnePollDigest claims exactly one due "poll.digest" job by hand (jobs.ClaimDue directly,
// not through a Worker), so a test can hold it "mid-run" before deciding what happens next.
func claimOnePollDigest(t *testing.T, ctx context.Context, d *sql.DB) jobs.Job {
	t.Helper()
	claimed, err := jobs.ClaimDue(ctx, d, "test-replica", 20)
	if err != nil {
		t.Fatalf("ClaimDue: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Kind != "poll.digest" {
		t.Fatalf("claimed = %+v, want exactly one poll.digest job", claimed)
	}
	return claimed[0]
}

// digestJobRow reads back a poll's pending "poll.digest" row's run_at and the names of its
// accumulated items, straight from scheduled_jobs.
func digestJobRow(t *testing.T, ctx context.Context, d *sql.DB, pollID string) (runAt time.Time, names []string) {
	t.Helper()
	var raw []byte
	err := d.QueryRowContext(ctx,
		`SELECT run_at, payload FROM scheduled_jobs WHERE kind = 'poll.digest' AND room_key = $1`, "poll:"+pollID,
	).Scan(&runAt, &raw)
	if err != nil {
		t.Fatalf("digest row: %v", err)
	}
	var p struct {
		Items []polls.DigestItem `json:"items"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode digest payload %s: %v", raw, err)
	}
	for _, it := range p.Items {
		names = append(names, it.Name)
	}
	return runAt, names
}

// TestDigestItemEnqueuedMidRunIsNotResent reproduces the race deterministically by driving the
// worker's two steps by hand: ClaimDue (the row is now "mid-run") -> EnqueueDigestItem lands
// -> the claimed job's handler runs (ProcessClaimed) -> the next poll runs. Before the fix, the
// handler fanned out its stale batch AND the merged replacement row was processed again, so the
// owner received Ada twice. PollRoom.ts's #clearDigest-after-send made this impossible by
// construction; processDigestJob is that guarantee's Postgres form.
func TestDigestItemEnqueuedMidRunIsNotResent(t *testing.T) {
	ctx := context.Background()

	enqueue := func(t *testing.T, s *polls.Service, pollID, name string) {
		t.Helper()
		if err := s.EnqueueDigestItem(ctx, pollID, polls.DigestItem{Event: polls.EventResponseCreated, Name: name}); err != nil {
			t.Fatalf("EnqueueDigestItem(%s): %v", name, err)
		}
	}
	claimOne := func(t *testing.T, d *sql.DB) jobs.Job { return claimOnePollDigest(t, ctx, d) }
	digestRow := func(t *testing.T, d *sql.DB, pollID string) (runAt time.Time, names []string) {
		return digestJobRow(t, ctx, d, pollID)
	}

	t.Run("item landing between claim and handler: one digest, each item exactly once", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)

		enqueue(t, s, created.ID, "Ada")
		forceDue(t, d, "poll.digest")
		stale := claimOne(t, d) // the worker holds [Ada] "mid-run"
		enqueue(t, s, created.ID, "Bob")
		w.ProcessClaimed(ctx, stale) // the held job's handler finally runs
		if n := len(rec.byTemplate("digest")) + countJobs(t, d, "mail:poll"); n != 0 {
			t.Fatalf("the superseded job sent/queued %d digest mails, want 0 (its batch now lives in the replacement row)", n)
		}

		forceDue(t, d, "poll.digest")
		drainJobs(t, ctx, w)

		digests := rec.byTemplate("digest")
		if len(digests) != 1 {
			t.Fatalf("digest mails = %d, want exactly 1: %+v", len(digests), digests)
		}
		lines, _ := digests[0].Data["Lines"].([]mailer.DigestLine)
		if len(lines) != 1 || lines[0].Event != "response.created" || lines[0].Count != 2 ||
			len(lines[0].Names) != 2 || lines[0].Names[0] != "Ada" || lines[0].Names[1] != "Bob" {
			t.Errorf("digest lines = %+v, want one response.created line, count 2, names [Ada Bob]", lines)
		}
		if n := countJobs(t, d, "poll.digest"); n != 0 {
			t.Errorf("poll.digest rows left = %d, want 0", n)
		}
	})

	t.Run("item landing after the handler took the batch starts a fresh window", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		rec := &recordingMailer{}
		w := jobs.NewWorker(d, "test-replica", slog.Default())
		s.RegisterJobs(w, rec)

		enqueue(t, s, created.ID, "Ada")
		forceDue(t, d, "poll.digest")
		held := claimOne(t, d)
		// Exactly what takeDigestItems does at the top of the handler (the handler's fan-out is
		// simulated as already done): the accumulator is emptied in place, id unchanged.
		if _, err := d.ExecContext(ctx,
			`UPDATE scheduled_jobs SET payload = jsonb_build_object('pollId', $1::text, 'items', '[]'::jsonb) WHERE id = $2`,
			created.ID, held.ID,
		); err != nil {
			t.Fatalf("simulate takeDigestItems: %v", err)
		}

		before := time.Now()
		enqueue(t, s, created.ID, "Bob")

		runAt, names := digestRow(t, d, created.ID)
		if len(names) != 1 || names[0] != "Bob" {
			t.Errorf("items = %v, want [Bob] only", names)
		}
		if runAt.Before(before.Add(9 * time.Minute)) {
			t.Errorf("run_at = %s, want a fresh ~10 minute window (not the taken batch's past run_at)", runAt)
		}

		// The held job finishing now must send nothing — its id is gone.
		w.ProcessClaimed(ctx, held)
		if n := len(rec.byTemplate("digest")) + countJobs(t, d, "mail:poll"); n != 0 {
			t.Errorf("superseded job produced %d digest mails, want 0", n)
		}
		if _, names := digestRow(t, d, created.ID); len(names) != 1 || names[0] != "Bob" {
			t.Errorf("Bob's fresh window was disturbed: items = %v", names)
		}
	})
}

// TestFanOutDigestItemsMemoizesLocaleLookups pins the fix for the N+1 locale lookup a whole-plan
// review flagged: resolveRecipients used to call the LocaleSource once per (event, recipient) pair
// inside fanOutDigestItems's loop over a digest batch's distinct events — every one of those calls
// made inside processDigestJob's own advisory-locked transaction, lengthening exactly the lock
// window a previous review already flagged. A recipient subscribed to two distinct
// digest-batched events queued in the same debounce window (Ada's response and Bob's comment,
// below — both the poll's creator and its one other subscriber are subscribed to both by system
// default) must now cost exactly ONE LocaleSource round trip per recipient for the whole job, not
// one per event.
//
// Asserted by claiming the "poll.digest" job by hand and running only that ONE job
// (w.ProcessClaimed, not a full drainJobs): the eventual per-recipient "mail:poll" sends make
// their own, separate (unmemoized, and rightly so — a different transaction entirely)
// LocaleSource call later, which this test must not count.
func TestFanOutDigestItemsMemoizesLocaleLookups(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "member")
	mateID := seedUser(t, d)
	addOrgMember(t, d, orgID, mateID, "member")
	created := createTestPoll(t, ctx, s, orgID, ownerID) // Create subscribes the creator
	if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
		t.Fatalf("SetFollowing: %v", err)
	}

	locales := &countingLocales{calls: map[string]int{}}
	s.SetLocaleSource(locales)

	if err := s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{Event: polls.EventResponseCreated, Name: "Ada"}); err != nil {
		t.Fatalf("EnqueueDigestItem: %v", err)
	}
	if err := s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{Event: polls.EventCommentCreated, Name: "Bob"}); err != nil {
		t.Fatalf("EnqueueDigestItem: %v", err)
	}
	forceDue(t, d, "poll.digest")
	held := claimOnePollDigest(t, ctx, d)

	w := jobs.NewWorker(d, "test-replica", slog.Default())
	s.RegisterJobs(w, &recordingMailer{})
	w.ProcessClaimed(ctx, held)

	for _, uid := range []string{ownerID, mateID} {
		if got := locales.calls[uid]; got != 1 {
			t.Errorf("LocaleFor calls for %s = %d, want exactly 1 (memoized across both digest-batched events in this job)", uid, got)
		}
	}
}

// TestDigestFanOutFailurePreservesItemsForRetry closes the review finding on the round-1 fix
// (takeDigestItems committing the emptied-accumulator row in its own transaction, separately from
// handleDigestJob's fan-out): if the fan-out failed partway through delivery — one recipient's
// enqueueMailPoll erroring after an earlier recipient's had already gone through — the retry
// would find the row already emptied and silently no-op, permanently losing every item that
// hadn't been fanned out yet. processDigestJob now does both steps in ONE transaction, so a
// mid-fan-out failure must roll back the ownership-taking UPDATE too, leaving the retry with the
// original batch intact.
//
// Reproduced deterministically via polls.SetDigestFanOutFailAfterN (a test-only fault-injection
// seam): with two recipients subscribed to the same event, set it to 1 so the SECOND recipient's
// enqueueMailPoll call fails after the FIRST has already run inside the same (as-yet uncommitted)
// transaction — proving a partial, in-flight fan-out rolls back completely, not just the failing
// call.
func TestDigestFanOutFailurePreservesItemsForRetry(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	addOrgMember(t, d, orgID, ownerID, "member")
	mateID := seedUser(t, d)
	addOrgMember(t, d, orgID, mateID, "member")
	created := createTestPoll(t, ctx, s, orgID, ownerID)
	if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
		t.Fatalf("SetFollowing: %v", err)
	}

	rec := &recordingMailer{}
	w := jobs.NewWorker(d, "test-replica", slog.Default())
	s.RegisterJobs(w, rec)

	if err := s.EnqueueDigestItem(ctx, created.ID, polls.DigestItem{Event: polls.EventResponseCreated, Name: "Ada"}); err != nil {
		t.Fatalf("EnqueueDigestItem: %v", err)
	}
	forceDue(t, d, "poll.digest")
	held := claimOnePollDigest(t, ctx, d)

	// Force the SECOND recipient's enqueue to fail: the first recipient's mail:poll INSERT has
	// already run (inside the still-open transaction) by the time this one errors, so this proves
	// a PARTIAL fan-out rolls back completely, not just the failing call.
	restore := polls.SetDigestFanOutFailAfterN(1)
	t.Cleanup(restore)

	w.ProcessClaimed(ctx, held)

	if n := countJobs(t, d, "mail:poll"); n != 0 {
		t.Fatalf("mail:poll jobs after a failed fan-out = %d, want 0 (the whole transaction must roll back, including the first recipient's already-attempted insert)", n)
	}
	if n := countJobs(t, d, "poll.digest"); n != 1 {
		t.Fatalf("poll.digest rows = %d, want 1 (the failed job must be retried, not completed)", n)
	}
	if _, names := digestJobRow(t, ctx, d, created.ID); len(names) != 1 || names[0] != "Ada" {
		t.Fatalf("digest row items after a failed fan-out = %v, want [Ada] intact (not emptied)", names)
	}

	// Retry: turn off the injected fault, force the same row due again, and run exactly one more
	// poll.digest pass (not a full drain) so the resulting mail:poll rows can be inspected before
	// they're themselves processed and deleted.
	polls.SetDigestFanOutFailAfterN(0)
	forceDue(t, d, "poll.digest")
	if _, err := w.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce (retry): %v", err)
	}

	got := map[string]bool{}
	for _, p := range filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "digest") {
		got[p.UserID] = true
	}
	if len(got) != 2 || !got[ownerID] || !got[mateID] {
		t.Fatalf("digest mail:poll recipients after retry = %v, want exactly {%s, %s} (each exactly once)", got, ownerID, mateID)
	}
	if n := countJobs(t, d, "poll.digest"); n != 0 {
		t.Errorf("poll.digest rows left after a successful retry = %d, want 0", n)
	}
}

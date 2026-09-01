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
	})
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

	if err := s.Finalize(ctx, created.ID, orgID, slot); err != nil {
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

	if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID); err != nil {
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

	// Re-claiming the same slot (a no-op vote-wise) still resends the confirmation — ported from
	// claimSlot's own unconditional call.
	if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{ParticipantID: result.ParticipantID}, polls.Viewer{GuestParticipantID: result.ParticipantID}); err != nil {
		t.Fatalf("re-Claim: %v", err)
	}
	if n := len(filterByEvent(decodeMailPollJobs(t, listJobs(t, d, "mail:poll")), "claim_confirmation")); n != 2 {
		t.Errorf("claim_confirmation jobs after re-claim = %d, want 2", n)
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
	return host, int(smtpMapped.Num()), fmt.Sprintf("http://%s:%s", host, apiMapped.Port())
}

type mailpitMessagesResponse struct {
	Total int `json:"total"`
}

func mailpitTotal(t *testing.T, apiBaseURL string) int {
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
	return out.Total
}

// TestMailPollDeliversRealMail runs the "finalized" (participant path) and "claim_confirmation"
// send paths against a live Mailpit container, proving out the whole re-read-then-render-then-
// send pipeline for real — not just the scheduled_jobs footprint the other tests here assert.
func TestMailPollDeliversRealMail(t *testing.T) {
	smtpHost, smtpPort, apiBaseURL := startMailpitForPolls(t)
	m := mailer.New(&config.Config{
		SMTPHost: smtpHost, SMTPPort: smtpPort,
		EmailFrom: "whenweall <no-reply@whenweall.example>", AppURL: "https://whenweall.example",
	})

	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)

	// finalized: a datetime poll, one participant with an email, finalize it.
	scheduling := createTestPoll(t, ctx, s, orgID, ownerID)
	seedParticipant(t, d, scheduling.ID, "Ada", map[string]string{scheduling.Options[0].ID: "yes"}, "ada@example.com")
	if err := s.Finalize(ctx, scheduling.ID, orgID, scheduling.Options[0].ID); err != nil {
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
}

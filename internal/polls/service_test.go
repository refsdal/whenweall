package polls_test

// Ports the behavioral cases from src/server/polls/__tests__/service.workers.test.ts
// case-for-case, plus the org-scoping half of org-authz.workers.test.ts's "org authorization
// matrix" test (requireManagedPoll). Two things from those TS files are deliberately not ported —
// see the doc comments at TestOrgScoping and TestFinalize for why:
//
//  1. The same-org "second member cannot manage a poll they didn't create" FORBIDDEN case: the
//     brief's exact signatures for Update/SetStatus/Finalize/Delete/Duplicate carry an orgID but
//     no userID or role, so there is no identity for this port to check that rule against (see
//     requireOrgPoll's doc comment in service.go).
//  2. finalizePoll's recipients/mail computation and org-authz.workers.test.ts's
//     requireSessionMiddleware/requireOrgMiddleware cases: session/org resolution and
//     notification mail are a different package's and a different task's concern (internal/auth;
//     Task 4), not this poll service.
//
// participants/votes are seeded directly via SQL (seedParticipant) rather than through a service
// call, since AddParticipant/Claim are Task 3's — this mirrors the TS tests' own use of
// test/helpers.ts's makeParticipant/applyClaim, which exist independently of polls/service.ts.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

var seedSeq atomic.Int64

// seedOrgAndUser inserts one organization and one user directly via SQL — a Limen-shaped row is
// just users(email, updated_at) and organizations(name, slug, updated_at), both BIGSERIAL-keyed
// (migrations/00002_auth.sql) — and returns their ids stringified, matching the seam's convention
// every poll service method expects.
func seedOrgAndUser(t *testing.T, d *sql.DB) (orgID, userID string) {
	t.Helper()
	return seedOrgAndUserNamed(t, d, "Test Org")
}

func seedOrgAndUserNamed(t *testing.T, d *sql.DB, orgName string) (orgID, userID string) {
	t.Helper()
	n := seedSeq.Add(1)
	ctx := context.Background()

	var uid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO users (email, updated_at) VALUES ($1, now()) RETURNING id`,
		fmt.Sprintf("user-%d@example.com", n),
	).Scan(&uid); err != nil {
		t.Fatalf("seeding user: %v", err)
	}

	var oid int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO organizations (name, slug, updated_at) VALUES ($1, $2, now()) RETURNING id`,
		orgName, fmt.Sprintf("test-org-%d", n),
	).Scan(&oid); err != nil {
		t.Fatalf("seeding organization: %v", err)
	}

	return fmt.Sprint(oid), fmt.Sprint(uid)
}

// seedUser inserts a standalone user with no organization membership.
func seedUser(t *testing.T, d *sql.DB) string {
	t.Helper()
	n := seedSeq.Add(1)
	var uid int64
	if err := d.QueryRowContext(context.Background(),
		`INSERT INTO users (email, updated_at) VALUES ($1, now()) RETURNING id`,
		fmt.Sprintf("user-%d@example.com", n),
	).Scan(&uid); err != nil {
		t.Fatalf("seeding user: %v", err)
	}
	return fmt.Sprint(uid)
}

// seedParticipant inserts a participant (and its votes) directly via SQL.
func seedParticipant(t *testing.T, d *sql.DB, pollID, name string, votes map[string]string, email string) string {
	t.Helper()
	ctx := context.Background()
	id := fmt.Sprintf("participant-%d", seedSeq.Add(1))
	var emailArg sql.NullString
	if email != "" {
		emailArg = sql.NullString{String: email, Valid: true}
	}
	now := time.Now().UTC()
	if _, err := d.ExecContext(ctx,
		`INSERT INTO participants (id, poll_id, name, email, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$5)`,
		id, pollID, name, emailArg, now,
	); err != nil {
		t.Fatalf("seeding participant: %v", err)
	}
	for optID, answer := range votes {
		if _, err := d.ExecContext(ctx,
			`INSERT INTO votes (participant_id, option_id, answer) VALUES ($1,$2,$3)`,
			id, optID, answer,
		); err != nil {
			t.Fatalf("seeding vote: %v", err)
		}
	}
	return id
}

func tomorrowAt(hour string) string {
	d := time.Now().Add(24 * time.Hour).UTC()
	return fmt.Sprintf("%04d-%02d-%02dT%s:00.000Z", d.Year(), int(d.Month()), d.Day(), hour)
}

func basicOptions() []polls.OptionInput {
	return []polls.OptionInput{
		datetimeOption(tomorrowAt("10:00"), tomorrowAt("11:00")),
		datetimeOption(tomorrowAt("14:00")),
	}
}

func createTestPoll(t *testing.T, ctx context.Context, s *polls.Service, orgID, userID string) *polls.PollView {
	t.Helper()
	view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
		Type: polls.PollTypeDatetime, Title: "Test Poll", Timezone: "Europe/Oslo",
		Options: basicOptions(),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return view
}

func createSignupPoll(t *testing.T, ctx context.Context, s *polls.Service, orgID, userID string, capacities []*int, maxClaims int) *polls.PollView {
	t.Helper()
	options := make([]polls.OptionInput, len(capacities))
	for i, capacity := range capacities {
		opt := textOption(fmt.Sprintf("Slot %d", i))
		opt.CapacitySet, opt.Capacity = true, capacity
		options[i] = opt
	}
	in := polls.CreatePollInput{
		Type: polls.PollTypeSignup, Title: "Signup poll", Timezone: "Europe/Oslo", Options: options,
	}
	if maxClaims > 0 {
		in.SignupMaxClaims = intPtr(maxClaims)
	}
	view, err := s.Create(ctx, orgID, userID, in)
	if err != nil {
		t.Fatalf("Create (signup): %v", err)
	}
	return view
}

func TestCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("stores options in order with kinds and positions", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, userID := seedOrgAndUser(t, d)

		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type: polls.PollTypeOptions, Title: "Lunch spot", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{textOption("Pizza"), textOption("Sushi")},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if len(view.Options) != 2 {
			t.Fatalf("len(Options) = %d, want 2", len(view.Options))
		}
		if *view.Options[0].Label != "Pizza" || view.Options[0].Position != 0 {
			t.Errorf("Options[0] = %+v, want Pizza @ 0", view.Options[0])
		}
		if *view.Options[1].Label != "Sushi" || view.Options[1].Position != 1 {
			t.Errorf("Options[1] = %+v, want Sushi @ 1", view.Options[1])
		}
	})

	t.Run("maps a date option kind to startAt as YYYY-MM-DD", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, userID := seedOrgAndUser(t, d)

		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "Offsite", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{dateOption("2026-09-15")},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if view.Options[0].Kind != "date" || *view.Options[0].StartAt != "2026-09-15" || view.Options[0].EndAt != nil {
			t.Errorf("Options[0] = %+v, want date 2026-09-15 with nil endAt", view.Options[0])
		}
	})

	t.Run("maps a datetime option kind to startAt/endAt", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, userID := seedOrgAndUser(t, d)
		start, end := tomorrowAt("10:00"), tomorrowAt("11:00")

		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "Sync", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{datetimeOption(start, end)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if *view.Options[0].StartAt != start || *view.Options[0].EndAt != end {
			t.Errorf("Options[0] = %+v, want startAt=%s endAt=%s", view.Options[0], start, end)
		}
	})

	t.Run("applies default settings, no notifications block, status open", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, userID := seedOrgAndUser(t, d)

		view := createTestPoll(t, ctx, s, orgID, userID)
		want := polls.PollSettingsView{RequireParticipantEmail: false, AllowComments: true, AllowIfNeedBe: true, SignupMaxClaims: 1}
		if view.Settings != want {
			t.Errorf("Settings = %+v, want %+v", view.Settings, want)
		}
		if view.Notifications != nil {
			t.Errorf("Notifications = %+v, want nil (Task 4 owns this field)", view.Notifications)
		}
		if view.Status != "open" {
			t.Errorf("Status = %q, want open", view.Status)
		}
	})

	t.Run("persists per-option capacity and signupMaxClaims for a signup poll, forcing allowIfNeedBe false", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, userID := seedOrgAndUser(t, d)

		two, none := intPtr(2), (*int)(nil)
		allowIfNeedBe := true
		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type: polls.PollTypeSignup, Title: "Bring a dish", Timezone: "Europe/Oslo",
			SignupMaxClaims: intPtr(3),
			Options: []polls.OptionInput{
				withCapacity(textOption("Starter"), two),
				withCapacity(textOption("Dessert"), none),
			},
			PollSettingsInput: polls.PollSettingsInput{AllowIfNeedBe: &allowIfNeedBe},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if view.Settings.AllowIfNeedBe {
			t.Errorf("AllowIfNeedBe = true, want false (forced for signup polls)")
		}
		if view.Settings.SignupMaxClaims != 3 {
			t.Errorf("SignupMaxClaims = %d, want 3", view.Settings.SignupMaxClaims)
		}
		if *view.Options[0].Capacity != 2 {
			t.Errorf("Options[0].Capacity = %v, want 2", view.Options[0].Capacity)
		}
		if view.Options[1].Capacity != nil {
			t.Errorf("Options[1].Capacity = %v, want nil", view.Options[1].Capacity)
		}
	})

	t.Run("creates a poll with 20 datetime options", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, userID := seedOrgAndUser(t, d)

		options := make([]polls.OptionInput, 20)
		starts := make([]string, 20)
		for i := range options {
			starts[i] = fmt.Sprintf("2026-06-%02dT%02d:00:00.000Z", 1+i%27, i%24)
			options[i] = datetimeOption(starts[i])
		}
		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "Big scheduling poll", Timezone: "Europe/Oslo", Options: options,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if len(view.Options) != 20 {
			t.Fatalf("len(Options) = %d, want 20", len(view.Options))
		}
		for i, o := range view.Options {
			if *o.StartAt != starts[i] {
				t.Errorf("Options[%d].StartAt = %s, want %s", i, *o.StartAt, starts[i])
			}
		}
	})

	t.Run("forces capacity to null and signupMaxClaims to 1 for a non-signup poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, userID := seedOrgAndUser(t, d)

		view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
			Type: polls.PollTypeOptions, Title: "Lunch spot", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{textOption("Pizza")},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if view.Options[0].Capacity != nil {
			t.Errorf("Capacity = %v, want nil", view.Options[0].Capacity)
		}
		if view.Settings.SignupMaxClaims != 1 {
			t.Errorf("SignupMaxClaims = %d, want 1", view.Settings.SignupMaxClaims)
		}
	})
}

func TestGetView(t *testing.T) {
	ctx := context.Background()

	t.Run("isOwner true for the creator, false for others and anonymous; owner name only", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		otherID := seedUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		asOwner, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if err != nil || asOwner == nil {
			t.Fatalf("GetView(owner) = %v, %v", asOwner, err)
		}
		if !asOwner.IsOwner {
			t.Errorf("asOwner.IsOwner = false, want true")
		}
		if asOwner.Owner.Name != "Test Org" {
			t.Errorf("Owner.Name = %q, want %q", asOwner.Owner.Name, "Test Org")
		}

		// otherID has no organization_members row at all (seedUser, not addOrgMember) — a plain
		// non-member, not even a poll-unrelated org member.
		asOther, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: otherID})
		if err != nil || asOther == nil {
			t.Fatalf("GetView(other) = %v, %v", asOther, err)
		}
		if asOther.IsOwner {
			t.Errorf("asOther.IsOwner = true, want false")
		}
		if asOther.Notifications != nil {
			t.Errorf("asOther.Notifications = %+v, want nil for a non-member", asOther.Notifications)
		}

		asAnon, err := s.GetView(ctx, created.ID, polls.Viewer{})
		if err != nil || asAnon == nil {
			t.Fatalf("GetView(anon) = %v, %v", asAnon, err)
		}
		if asAnon.IsOwner {
			t.Errorf("asAnon.IsOwner = true, want false")
		}
		if asAnon.Notifications != nil {
			t.Errorf("asAnon.Notifications = %+v, want nil for an anonymous viewer", asAnon.Notifications)
		}
	})

	t.Run("isOwner true for an org admin who did not create the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		adminID := seedUser(t, d)
		addOrgMember(t, d, orgID, adminID, "admin")

		view, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: adminID})
		if err != nil || view == nil {
			t.Fatalf("GetView(admin): %v, %v", view, err)
		}
		if !view.IsOwner {
			t.Errorf("view.IsOwner = false, want true for a non-creator admin")
		}
	})

	t.Run("isOwner false for a plain member who did not create the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		mateID := seedUser(t, d)
		addOrgMember(t, d, orgID, mateID, "member")

		view, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: mateID})
		if err != nil || view == nil {
			t.Fatalf("GetView(member): %v, %v", view, err)
		}
		if view.IsOwner {
			t.Errorf("view.IsOwner = true, want false for a non-creator, non-managing member")
		}
	})

	t.Run("notifications populated for a subscribed org member, nil for a non-member", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		mateID := seedUser(t, d)
		addOrgMember(t, d, orgID, mateID, "member")
		if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
			t.Fatalf("SetFollowing: %v", err)
		}
		if err := s.UpdateNotificationPrefs(ctx, created.ID, orgID, mateID, polls.NotificationGrid{
			polls.EventCommentCreated: {Email: false, Push: false},
		}); err != nil {
			t.Fatalf("UpdateNotificationPrefs: %v", err)
		}

		view, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: mateID})
		if err != nil || view == nil {
			t.Fatalf("GetView(member): %v, %v", view, err)
		}
		if view.Notifications == nil {
			t.Fatal("view.Notifications = nil, want populated for a subscribed member")
		}
		if !view.Notifications.Following {
			t.Errorf("Notifications.Following = false, want true")
		}
		if view.Notifications.Channels == nil {
			t.Errorf("Notifications.Channels = nil, want the comment.created override just set")
		}

		// A member who never followed/tuned anything still gets a non-nil block (following:
		// false, channels/defaults nil) — populated per getPollView's own `isMember ? {...} :
		// null`, not gated on having ever interacted with notifications.
		plainID := seedUser(t, d)
		addOrgMember(t, d, orgID, plainID, "member")
		plainView, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: plainID})
		if err != nil || plainView == nil {
			t.Fatalf("GetView(plain member): %v, %v", plainView, err)
		}
		if plainView.Notifications == nil {
			t.Fatal("plainView.Notifications = nil, want a populated-but-empty block for any member")
		}
		if plainView.Notifications.Following {
			t.Errorf("plainView.Notifications.Following = true, want false")
		}
	})

	t.Run("reports hasEmail true/false per participant without exposing the email", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1, opt2 := created.Options[0].ID, created.Options[1].ID

		withEmailID := seedParticipant(t, d, created.ID, "Alice", map[string]string{opt1: "yes"}, "alice@example.com")
		withoutEmailID := seedParticipant(t, d, created.ID, "Bob", map[string]string{opt2: "no"}, "")

		view, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if err != nil || view == nil {
			t.Fatalf("GetView: %v, %v", view, err)
		}
		var alice, bob *polls.ParticipantView
		for i := range view.Participants {
			switch view.Participants[i].ID {
			case withEmailID:
				alice = &view.Participants[i]
			case withoutEmailID:
				bob = &view.Participants[i]
			}
		}
		if alice == nil || !alice.HasEmail {
			t.Fatalf("alice.HasEmail = %v, want true", alice)
		}
		if bob == nil || bob.HasEmail {
			t.Fatalf("bob.HasEmail = %v, want false", bob)
		}
		if alice.Votes[opt1] != "yes" {
			t.Errorf("alice.Votes[opt1] = %q, want yes", alice.Votes[opt1])
		}
	})

	t.Run("returns nil, nil when the poll is missing or soft-deleted", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)

		view, err := s.GetView(ctx, "missing12345", polls.Viewer{})
		if err != nil || view != nil {
			t.Fatalf("GetView(missing) = %v, %v, want nil, nil", view, err)
		}

		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		view, err = s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if err != nil || view != nil {
			t.Fatalf("GetView(deleted) = %v, %v, want nil, nil", view, err)
		}
	})

	t.Run("exposes a claims map with full flags and empty scores for a signup poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		one := intPtr(1)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{one, nil}, 2)
		slot1, slot2 := created.Options[0].ID, created.Options[1].ID
		seedParticipant(t, d, created.ID, "Alice", map[string]string{slot1: "yes"}, "")

		view, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if err != nil || view == nil {
			t.Fatalf("GetView: %v, %v", view, err)
		}
		if view.Claims[slot1].Count != 1 || !view.Claims[slot1].Full {
			t.Errorf("Claims[slot1] = %+v, want count=1 full=true", view.Claims[slot1])
		}
		if view.Claims[slot2].Count != 0 || view.Claims[slot2].Full {
			t.Errorf("Claims[slot2] = %+v, want count=0 full=false", view.Claims[slot2])
		}
		if len(view.Scores) != 0 {
			t.Errorf("Scores = %+v, want empty for a signup poll", view.Scores)
		}
		if view.BestOptionID != nil {
			t.Errorf("BestOptionID = %v, want nil for a signup poll", view.BestOptionID)
		}
	})
}

func TestListMine(t *testing.T) {
	ctx := context.Background()

	t.Run("lists only the org's non-deleted polls, newest first, with participantCount", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		otherOrgID, otherUserID := seedOrgAndUser(t, d)

		poll1, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{Type: polls.PollTypeDatetime, Title: "First", Timezone: "Europe/Oslo", Options: basicOptions()})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
		poll2, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{Type: polls.PollTypeDatetime, Title: "Second", Timezone: "Europe/Oslo", Options: basicOptions()})
		if err != nil {
			t.Fatal(err)
		}
		poll3, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{Type: polls.PollTypeDatetime, Title: "Deleted", Timezone: "Europe/Oslo", Options: basicOptions()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Create(ctx, otherOrgID, otherUserID, polls.CreatePollInput{Type: polls.PollTypeDatetime, Title: "Not mine", Timezone: "Europe/Oslo", Options: basicOptions()}); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, poll3.ID, orgID); err != nil {
			t.Fatal(err)
		}
		seedParticipant(t, d, poll1.ID, "Alice", map[string]string{poll1.Options[0].ID: "yes"}, "")
		seedParticipant(t, d, poll1.ID, "Bob", map[string]string{poll1.Options[0].ID: "no"}, "")

		list, err := s.ListMine(ctx, orgID)
		if err != nil {
			t.Fatalf("ListMine: %v", err)
		}
		if len(list) != 2 || list[0].Title != "Second" || list[1].Title != "First" {
			t.Fatalf("list = %+v, want [Second, First]", list)
		}
		first := findSummary(list, poll1.ID)
		if first == nil || first.ParticipantCount != 2 {
			t.Errorf("first.ParticipantCount = %+v, want 2", first)
		}
		second := findSummary(list, poll2.ID)
		if second == nil || second.ParticipantCount != 0 {
			t.Errorf("second.ParticipantCount = %+v, want 0", second)
		}
	})

	t.Run("reports claimCount as the sum of yes-votes, distinct from participantCount, for a signup sheet", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 2)
		slot1, slot2 := created.Options[0].ID, created.Options[1].ID

		// Alice claims both slots (one participant, two claims); Bob claims one.
		seedParticipant(t, d, created.ID, "Alice", map[string]string{slot1: "yes", slot2: "yes"}, "")
		seedParticipant(t, d, created.ID, "Bob", map[string]string{slot1: "yes"}, "")

		list, err := s.ListMine(ctx, orgID)
		if err != nil {
			t.Fatalf("ListMine: %v", err)
		}
		summary := findSummary(list, created.ID)
		if summary == nil {
			t.Fatalf("summary for %s not found in %+v", created.ID, list)
		}
		if summary.ParticipantCount != 2 {
			t.Errorf("ParticipantCount = %d, want 2", summary.ParticipantCount)
		}
		if summary.ClaimCount != 3 {
			t.Errorf("ClaimCount = %d, want 3", summary.ClaimCount)
		}
	})
}

func findSummary(list []polls.PollSummary, id string) *polls.PollSummary {
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

func TestUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("removing an option deletes its votes; adding an option keeps positions", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1, opt2 := created.Options[0], created.Options[1]
		seedParticipant(t, d, created.ID, "Alice", map[string]string{opt1.ID: "yes", opt2.ID: "no"}, "")

		after, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{
			Options: []polls.OptionInput{
				{ID: opt1.ID, Kind: polls.OptionKindDatetime, StartAt: *opt1.StartAt},
				datetimeOption(tomorrowAt("12:00")),
			},
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if len(after.Options) != 2 {
			t.Fatalf("len(Options) = %d, want 2", len(after.Options))
		}
		if after.Options[0].ID != opt1.ID || after.Options[0].Position != 0 {
			t.Errorf("Options[0] = %+v, want id=%s pos=0", after.Options[0], opt1.ID)
		}
		if after.Options[1].ID == opt2.ID {
			t.Errorf("Options[1].ID unexpectedly still equals the removed option's id")
		}

		refreshed, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if err != nil || refreshed == nil {
			t.Fatalf("GetView: %v, %v", refreshed, err)
		}
		var alice *polls.ParticipantView
		for i := range refreshed.Participants {
			if refreshed.Participants[i].Name == "Alice" {
				alice = &refreshed.Participants[i]
			}
		}
		if alice == nil {
			t.Fatal("alice not found")
		}
		if len(alice.Votes) != 1 || alice.Votes[opt1.ID] != "yes" {
			t.Errorf("alice.Votes = %+v, want only {%s: yes}", alice.Votes, opt1.ID)
		}
	})

	t.Run("ErrPollFinalized when editing options on a finalized poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		if err := s.Finalize(ctx, created.ID, orgID, opt1.ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		_, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{
			Options: []polls.OptionInput{{ID: opt1.ID, Kind: polls.OptionKindDatetime, StartAt: *opt1.StartAt}},
		})
		if !errors.Is(err, polls.ErrPollFinalized) {
			t.Errorf("Update on finalized poll's options: err = %v, want ErrPollFinalized", err)
		}
	})

	t.Run("still allows non-option edits (title) on a finalized poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		after, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{Title: strPtr("Renamed after finalizing")})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if after.Title != "Renamed after finalizing" {
			t.Errorf("Title = %q, want %q", after.Title, "Renamed after finalizing")
		}
	})

	t.Run("changing the deadline updates it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		newDeadline := time.Now().Add(48 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z")

		after, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &newDeadline})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if after.DeadlineAt == nil || *after.DeadlineAt != newDeadline {
			t.Errorf("DeadlineAt = %v, want %s", after.DeadlineAt, newDeadline)
		}
	})

	t.Run("ErrCapacityBelowClaims when lowering a slot's capacity under its claim count", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]
		seedParticipant(t, d, created.ID, "Alice", map[string]string{slot.ID: "yes"}, "")
		seedParticipant(t, d, created.ID, "Bob", map[string]string{slot.ID: "yes"}, "")

		_, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{
			Options: []polls.OptionInput{withCapacity(polls.OptionInput{ID: slot.ID, Kind: polls.OptionKindText, Label: *slot.Label}, intPtr(1))},
		})
		if !errors.Is(err, polls.ErrCapacityBelowClaims) {
			t.Errorf("err = %v, want ErrCapacityBelowClaims", err)
		}
	})

	t.Run("allows raising a slot's capacity to at or above its claim count", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(1)}, 0)
		slot := created.Options[0]
		seedParticipant(t, d, created.ID, "Alice", map[string]string{slot.ID: "yes"}, "")

		after, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{
			Options: []polls.OptionInput{withCapacity(polls.OptionInput{ID: slot.ID, Kind: polls.OptionKindText, Label: *slot.Label}, intPtr(5))},
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if *after.Options[0].Capacity != 5 {
			t.Errorf("Capacity = %v, want 5", after.Options[0].Capacity)
		}
	})

	t.Run("keeps a retained option's existing capacity when the update omits it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(7)}, 0)
		slot := created.Options[0]

		after, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{
			Options: []polls.OptionInput{{ID: slot.ID, Kind: polls.OptionKindText, Label: "Renamed slot"}},
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if after.Options[0].Capacity == nil || *after.Options[0].Capacity != 7 {
			t.Errorf("Capacity = %v, want 7", after.Options[0].Capacity)
		}
		if *after.Options[0].Label != "Renamed slot" {
			t.Errorf("Label = %v, want Renamed slot", after.Options[0].Label)
		}
	})

	t.Run("still defaults a brand-new option to capacity 1 when the update omits it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(7)}, 0)
		slot := created.Options[0]

		after, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{
			Options: []polls.OptionInput{
				withCapacity(polls.OptionInput{ID: slot.ID, Kind: polls.OptionKindText, Label: "Slot 1"}, intPtr(7)),
				textOption("New slot"),
			},
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		var newSlot *polls.PollOptionView
		for i := range after.Options {
			if *after.Options[i].Label == "New slot" {
				newSlot = &after.Options[i]
			}
		}
		if newSlot == nil || newSlot.Capacity == nil || *newSlot.Capacity != 1 {
			t.Errorf("new slot capacity = %v, want 1", newSlot)
		}
	})
}

func TestSetStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("closes then reopens", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus(closed): %v", err)
		}
		view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		if view.Status != "closed" {
			t.Errorf("Status = %q, want closed", view.Status)
		}

		if err := s.SetStatus(ctx, created.ID, orgID, "open"); err != nil {
			t.Fatalf("SetStatus(open): %v", err)
		}
		view, _ = s.GetView(ctx, created.ID, polls.Viewer{})
		if view.Status != "open" {
			t.Errorf("Status = %q, want open", view.Status)
		}
	})

	t.Run("ErrPollFinalized for a finalized poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); !errors.Is(err, polls.ErrPollFinalized) {
			t.Errorf("err = %v, want ErrPollFinalized", err)
		}
	})
}

func TestFinalize(t *testing.T) {
	ctx := context.Background()

	t.Run("sets status and finalizedOptionId", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]

		if err := s.Finalize(ctx, created.ID, orgID, opt1.ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		view, err := s.GetView(ctx, created.ID, polls.Viewer{})
		if err != nil || view == nil {
			t.Fatalf("GetView: %v, %v", view, err)
		}
		if view.Status != "finalized" || view.FinalizedOptionID == nil || *view.FinalizedOptionID != opt1.ID {
			t.Errorf("view = %+v, want finalized/%s", view, opt1.ID)
		}
	})

	t.Run("ErrNotFound when the option does not belong to the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		other := createTestPoll(t, ctx, s, orgID, ownerID)

		if err := s.Finalize(ctx, created.ID, orgID, other.Options[0].ID, ownerID); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrConflict when finalizing an already-finalized poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		err := s.Finalize(ctx, created.ID, orgID, created.Options[1].ID, ownerID)
		if !errors.Is(err, polls.ErrConflict) {
			t.Errorf("err = %v, want ErrConflict", err)
		}
		// Deliberately the plain CONFLICT code, not POLL_FINALIZED — see errors.go / Finalize's
		// own call site comment for why these two near-identical English messages differ.
		if errors.Is(err, polls.ErrPollFinalized) {
			t.Errorf("err = %v, should NOT be ErrPollFinalized (TS: plain CONFLICT here)", err)
		}
	})

	t.Run("leaves bestOptionId unaffected by finalization", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1, opt2 := created.Options[0], created.Options[1]
		seedParticipant(t, d, created.ID, "Alice", map[string]string{opt1.ID: "yes", opt2.ID: "no"}, "")

		before, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		if before.BestOptionID == nil || *before.BestOptionID != opt1.ID {
			t.Fatalf("before.BestOptionID = %v, want %s", before.BestOptionID, opt1.ID)
		}

		if err := s.Finalize(ctx, created.ID, orgID, opt2.ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		after, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		if after.BestOptionID == nil || *after.BestOptionID != opt1.ID {
			t.Errorf("after.BestOptionID = %v, want %s (unchanged)", after.BestOptionID, opt1.ID)
		}
		if after.FinalizedOptionID == nil || *after.FinalizedOptionID != opt2.ID {
			t.Errorf("after.FinalizedOptionID = %v, want %s", after.FinalizedOptionID, opt2.ID)
		}
	})

	t.Run("ErrValidation for a signup poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)

		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
	})
}

func TestDelete(t *testing.T) {
	ctx := context.Background()

	t.Run("soft deletes: GetView returns nil and ListMine excludes it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		view, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if err != nil || view != nil {
			t.Fatalf("GetView = %v, %v, want nil, nil", view, err)
		}
		list, err := s.ListMine(ctx, orgID)
		if err != nil {
			t.Fatalf("ListMine: %v", err)
		}
		if findSummary(list, created.ID) != nil {
			t.Errorf("ListMine still includes the deleted poll")
		}
	})

	t.Run("deleting twice returns ErrNotFound", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := s.Delete(ctx, created.ID, orgID); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("second Delete err = %v, want ErrNotFound", err)
		}
	})

	t.Run("removes the poll's notification_subscriptions rows (the manual cascade for the polymorphic scope)", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID) // Create subscribes the creator
		mateID := seedUser(t, d)
		if err := s.SetFollowing(ctx, created.ID, orgID, mateID, true); err != nil {
			t.Fatalf("SetFollowing: %v", err)
		}
		// A second, undeleted poll's own subscription — proves the delete is scoped to exactly
		// created.ID and does not touch another poll's rows via a too-broad predicate.
		other := createTestPoll(t, ctx, s, orgID, ownerID)
		countSubs := func(pollID string) int {
			var n int
			if err := d.QueryRowContext(ctx,
				`SELECT count(*) FROM notification_subscriptions WHERE scope_type = 'poll' AND scope_id = $1`, pollID,
			).Scan(&n); err != nil {
				t.Fatalf("count subscriptions: %v", err)
			}
			return n
		}
		if got := countSubs(created.ID); got != 2 {
			t.Fatalf("subscriptions before delete = %d, want 2 (creator + follower)", got)
		}
		if got := countSubs(other.ID); got != 1 {
			t.Fatalf("other poll's subscriptions before delete = %d, want 1 (its own creator)", got)
		}

		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if got := countSubs(created.ID); got != 0 {
			t.Errorf("subscriptions after delete = %d, want 0", got)
		}
		if got := countSubs(other.ID); got != 1 {
			t.Errorf("other poll's subscriptions after delete = %d, want 1 (must survive; scoping must not bleed across polls)", got)
		}
	})
}

func TestDuplicate(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a new poll with the same options, zero participants, and a title suffix", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		original, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "Original", Timezone: "Europe/Oslo", Options: basicOptions(),
		})
		if err != nil {
			t.Fatal(err)
		}
		seedParticipant(t, d, original.ID, "Alice", map[string]string{original.Options[0].ID: "yes"}, "")

		dup, err := s.Duplicate(ctx, original.ID, orgID, ownerID)
		if err != nil {
			t.Fatalf("Duplicate: %v", err)
		}
		if dup.ID == original.ID {
			t.Fatal("dup.ID equals original.ID")
		}
		if dup.Title != "Original (copy)" {
			t.Errorf("Title = %q, want %q", dup.Title, "Original (copy)")
		}
		if dup.Status != "open" {
			t.Errorf("Status = %q, want open", dup.Status)
		}
		if len(dup.Participants) != 0 {
			t.Errorf("Participants = %+v, want empty", dup.Participants)
		}
		if len(dup.Options) != len(original.Options) {
			t.Fatalf("len(Options) = %d, want %d", len(dup.Options), len(original.Options))
		}
		for i := range dup.Options {
			if dup.Options[i].ID == original.Options[i].ID {
				t.Errorf("Options[%d].ID unexpectedly matches the original's", i)
			}
			if *dup.Options[i].StartAt != *original.Options[i].StartAt {
				t.Errorf("Options[%d].StartAt = %v, want %v", i, dup.Options[i].StartAt, original.Options[i].StartAt)
			}
		}
	})

	t.Run("duplicates a poll with 30 options", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		options := make([]polls.OptionInput, 30)
		for i := range options {
			options[i] = textOption(fmt.Sprintf("Option %d", i))
		}
		original, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeOptions, Title: "Big options poll", Timezone: "Europe/Oslo", Options: options,
		})
		if err != nil {
			t.Fatal(err)
		}

		dup, err := s.Duplicate(ctx, original.ID, orgID, ownerID)
		if err != nil {
			t.Fatalf("Duplicate: %v", err)
		}
		if len(dup.Options) != 30 {
			t.Errorf("len(Options) = %d, want 30", len(dup.Options))
		}
	})
}

func TestCloseExpired(t *testing.T) {
	ctx := context.Background()

	t.Run("closes an open poll past its deadline and returns true", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		past := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &past}); err != nil {
			t.Fatalf("Update: %v", err)
		}

		changed, err := s.CloseExpired(ctx, created.ID)
		if err != nil {
			t.Fatalf("CloseExpired: %v", err)
		}
		if !changed {
			t.Fatal("changed = false, want true")
		}
		view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		if view.Status != "closed" {
			t.Errorf("Status = %q, want closed", view.Status)
		}
	})

	t.Run("closes a poll whose deadline has no fractional seconds", func(t *testing.T) {
		// Regression-proof only, not a live bug in this port: the TS source compares deadlineAt
		// as strings (D1 stores it as text), where a whole-second deadline like "…10:00:00Z"
		// sorts *after* "…10:00:00.004Z" lexicographically even though the first instant is
		// earlier — Date.parse in the original avoids exactly that. This port stores deadline_at
		// as Postgres timestamptz and always compares real time.Time instants
		// (poll.DeadlineAt.Time.After(time.Now())), so the bug class this guards against cannot
		// occur here; kept anyway to document the same input shape works.
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		deadline := time.Now().UTC().Format("2006-01-02T15:04:05Z")
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &deadline}); err != nil {
			t.Fatalf("Update: %v", err)
		}

		changed, err := s.CloseExpired(ctx, created.ID)
		if err != nil {
			t.Fatalf("CloseExpired: %v", err)
		}
		if !changed {
			t.Fatal("changed = false, want true")
		}
	})

	t.Run("returns false for a future deadline", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		future := time.Now().Add(time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &future}); err != nil {
			t.Fatalf("Update: %v", err)
		}

		changed, err := s.CloseExpired(ctx, created.ID)
		if err != nil {
			t.Fatalf("CloseExpired: %v", err)
		}
		if changed {
			t.Error("changed = true, want false")
		}
	})

	t.Run("returns false for an already-finalized poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		past := time.Now().Add(-time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{DeadlineAtSet: true, DeadlineAt: &past}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		changed, err := s.CloseExpired(ctx, created.ID)
		if err != nil {
			t.Fatalf("CloseExpired: %v", err)
		}
		if changed {
			t.Error("changed = true, want false")
		}
	})
}

// TestOrgScoping ports the org-scoping half of org-authz.workers.test.ts's "org authorization
// matrix" test: requireManagedPoll's NOT_FOUND-for-missing AND NOT_FOUND-for-wrong-org behavior.
// Task 7 reverted requireOrgPoll's wrong-org mapping from this port's earlier (documented)
// ErrForbidden deviation back to ErrNotFound, matching the TS source's own leak-avoidance intent
// (a poll id's existence must never be revealed outside its own org) — see the accumulated
// review requirement in the task 7 report. The TS test's other half — a same-org member who
// didn't create the poll gets FORBIDDEN, while an admin/owner who didn't create it is still
// allowed — is retrofitted at the HTTP handler layer (Service.RequireManageable), since
// Update/SetStatus/Finalize/Delete/Duplicate's brief-mandated signatures take only an orgID,
// never a userID or role, so there is no way for this service's own methods to tell "some other
// member of this org" apart from "the creator" or "an admin". See requireOrgPoll's and
// RequireManageable's doc comments.
func TestOrgScoping(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	otherOrgID, otherUserID := seedOrgAndUser(t, d)
	created := createTestPoll(t, ctx, s, orgID, ownerID)

	if _, err := s.Update(ctx, "missing12345", orgID, polls.UpdatePollInput{}); !errors.Is(err, polls.ErrNotFound) {
		t.Errorf("Update(missing) err = %v, want ErrNotFound", err)
	}
	if _, err := s.Update(ctx, created.ID, otherOrgID, polls.UpdatePollInput{}); !errors.Is(err, polls.ErrNotFound) {
		t.Errorf("Update(wrong org) err = %v, want ErrNotFound", err)
	}
	if err := s.SetStatus(ctx, created.ID, otherOrgID, "closed"); !errors.Is(err, polls.ErrNotFound) {
		t.Errorf("SetStatus(wrong org) err = %v, want ErrNotFound", err)
	}
	if err := s.Finalize(ctx, created.ID, otherOrgID, created.Options[0].ID, ownerID); !errors.Is(err, polls.ErrNotFound) {
		t.Errorf("Finalize(wrong org) err = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, created.ID, otherOrgID); !errors.Is(err, polls.ErrNotFound) {
		t.Errorf("Delete(wrong org) err = %v, want ErrNotFound", err)
	}
	if _, err := s.Duplicate(ctx, created.ID, otherOrgID, otherUserID); !errors.Is(err, polls.ErrNotFound) {
		t.Errorf("Duplicate(wrong org) err = %v, want ErrNotFound", err)
	}

	// Same org, different (arbitrary) member: allowed, since this layer only checks org
	// membership of the poll itself — see the doc comment above.
	if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{Title: strPtr("Still allowed")}); err != nil {
		t.Errorf("Update(same org) err = %v, want nil", err)
	}
}

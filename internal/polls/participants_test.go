package polls_test

// Ports the behavioral cases from src/server/polls/__tests__/participants.workers.test.ts
// case-for-case, adapted to Viewer{UserID, GuestParticipantID} in place of TS's separate
// {userId, editToken, isOwner} auth struct — see participants.go's package doc comment. Two TS
// cases are deliberately not ported:
//
//   - "stores votes for all 100 options ... without exceeding D1 bound-parameter limits": this
//     exists solely because D1 (SQLite over Cloudflare's binding protocol) caps bound parameters
//     per statement, which chunkedInsert works around. Postgres via database/sql has no such
//     limit and UpsertVote issues one exec per vote regardless of poll size, so the scenario this
//     test guards against is structurally impossible here — same rationale as Task 2's
//     CloseExpired deviation. The "only stores votes for options answered" case below already
//     covers the behavioral part (a participant's votes are exactly their answers).
//   - name-required validation: participants.ts's own addParticipant/updateParticipant never
//     validate Name (that lives in addParticipantSchema/updateParticipantSchema, out of this
//     task's port scope — participants.ts/claims.ts/claim-auth.ts/comment-auth.ts only); no such
//     test exists in participants.workers.test.ts either.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

func boolPtr(b bool) *bool { return &b }

// findParticipant locates a participant by id in a *polls.PollView, or nil.
func findParticipant(view *polls.PollView, participantID string) *polls.ParticipantView {
	for i := range view.Participants {
		if view.Participants[i].ID == participantID {
			return &view.Participants[i]
		}
	}
	return nil
}

// findComment locates a comment by id in a *polls.PollView, or nil.
func findComment(view *polls.PollView, commentID string) *polls.CommentView {
	for i := range view.Comments {
		if view.Comments[i].ID == commentID {
			return &view.Comments[i]
		}
	}
	return nil
}

// addOrgMember inserts an organization_members row (+ its organization_member_roles row) directly
// via SQL — the Go-side equivalent of test/helpers.ts's addOrgMember. Needed here (unlike Task
// 2's tests) because canManagePoll's role check is a real DB query in this port, not a
// precomputed `org.role` the caller hands in.
func addOrgMember(t *testing.T, d *sql.DB, orgID, userID, role string) {
	t.Helper()
	ctx := context.Background()
	orgIDInt, err := strconv.ParseInt(orgID, 10, 64)
	if err != nil {
		t.Fatalf("parse orgID: %v", err)
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		t.Fatalf("parse userID: %v", err)
	}

	var memberID int64
	if err := d.QueryRowContext(ctx,
		`INSERT INTO organization_members (organization_id, user_id) VALUES ($1, $2) RETURNING id`,
		orgIDInt, userIDInt,
	).Scan(&memberID); err != nil {
		t.Fatalf("seeding organization_member: %v", err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO organization_member_roles (member_id, organization_id, role) VALUES ($1, $2, $3)`,
		memberID, orgIDInt, role,
	); err != nil {
		t.Fatalf("seeding organization_member_role: %v", err)
	}
}

func TestAddParticipant(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a participant with votes and returns IsGuest true for an anonymous caller", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1, opt2 := created.Options[0], created.Options[1]

		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name:    "Alice",
			Answers: map[string]string{opt1.ID: "yes", opt2.ID: "no"},
		}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if result.ParticipantID == "" || !result.IsGuest {
			t.Fatalf("result = %+v, want non-empty ParticipantID and IsGuest", result)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		alice := findParticipant(view, result.ParticipantID)
		if alice == nil {
			t.Fatal("participant not found in view")
		}
		if alice.Votes[opt1.ID] != "yes" || alice.Votes[opt2.ID] != "no" {
			t.Errorf("votes = %+v, want yes/no", alice.Votes)
		}
	})

	t.Run("only stores votes for options the participant answered", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1, opt2 := created.Options[0], created.Options[1]

		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name: "Alice", Answers: map[string]string{opt1.ID: "yes"},
		}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		alice := findParticipant(view, result.ParticipantID)
		if len(alice.Votes) != 1 || alice.Votes[opt1.ID] != "yes" {
			t.Errorf("votes = %+v, want only opt1=yes", alice.Votes)
		}
		if _, ok := alice.Votes[opt2.ID]; ok {
			t.Errorf("votes contains opt2, want absent")
		}
	})

	t.Run("stores the given locale", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		locale := "nb"
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name: "Alice", Answers: map[string]string{}, Locale: &locale,
		}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		var row string
		if err := d.QueryRowContext(ctx, `SELECT locale FROM participants WHERE id = $1`, result.ParticipantID).Scan(&row); err != nil {
			t.Fatalf("query locale: %v", err)
		}
		if row != "nb" {
			t.Errorf("locale = %q, want nb", row)
		}
	})

	t.Run("returns IsGuest false for a signed-in participant", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		voterID := seedUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]

		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{
			Name: "Voter", Answers: map[string]string{opt1.ID: "yes"},
		}, polls.Viewer{UserID: voterID})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if result.IsGuest {
			t.Errorf("IsGuest = true, want false")
		}

		var editTokenHash sql.NullString
		var userIDCol sql.NullInt64
		if err := d.QueryRowContext(ctx, `SELECT edit_token_hash, user_id FROM participants WHERE id = $1`, result.ParticipantID).Scan(&editTokenHash, &userIDCol); err != nil {
			t.Fatalf("query participant: %v", err)
		}
		if editTokenHash.Valid {
			t.Errorf("edit_token_hash = %v, want NULL", editTokenHash)
		}
		wantUID, _ := strconv.ParseInt(voterID, 10, 64)
		if !userIDCol.Valid || userIDCol.Int64 != wantUID {
			t.Errorf("user_id = %+v, want %d", userIDCol, wantUID)
		}
	})

	t.Run("ErrNotFound for a missing poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		if _, err := s.AddParticipant(ctx, "missing12345", polls.ParticipantInput{Name: "Alice", Answers: map[string]string{}}, polls.Viewer{}); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrNotFound for a soft-deleted poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Alice", Answers: map[string]string{}}, polls.Viewer{}); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrPollClosed when the poll is not open", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Alice", Answers: map[string]string{}}, polls.Viewer{}); !errors.Is(err, polls.ErrPollClosed) {
			t.Errorf("err = %v, want ErrPollClosed", err)
		}
	})

	t.Run("ErrEmailRequired when the poll requires an email and none (or blank) is given", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		view, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options:           basicOptions(),
			PollSettingsInput: polls.PollSettingsInput{RequireParticipantEmail: boolPtr(true)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		if _, err := s.AddParticipant(ctx, view.ID, polls.ParticipantInput{Name: "Alice", Answers: map[string]string{}}, polls.Viewer{}); !errors.Is(err, polls.ErrEmailRequired) {
			t.Errorf("err (no email) = %v, want ErrEmailRequired", err)
		}
		blank := "   "
		if _, err := s.AddParticipant(ctx, view.ID, polls.ParticipantInput{Name: "Alice", Email: &blank, Answers: map[string]string{}}, polls.Viewer{}); !errors.Is(err, polls.ErrEmailRequired) {
			t.Errorf("err (blank email) = %v, want ErrEmailRequired", err)
		}
	})

	t.Run("accepts a trimmed email when the poll requires one", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		view, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options:           basicOptions(),
			PollSettingsInput: polls.PollSettingsInput{RequireParticipantEmail: boolPtr(true)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		email := "  alice@example.com  "
		result, err := s.AddParticipant(ctx, view.ID, polls.ParticipantInput{Name: "Alice", Email: &email, Answers: map[string]string{}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if result.ParticipantID == "" {
			t.Error("ParticipantID is empty")
		}
	})

	t.Run("ErrValidation when an answer references an option not on the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Alice", Answers: map[string]string{"bogus": "yes"}}, polls.Viewer{}); !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("ErrValidation for an ifneedbe answer when the poll disallows it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		view, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options:           basicOptions(),
			PollSettingsInput: polls.PollSettingsInput{AllowIfNeedBe: boolPtr(false)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		opt1 := view.Options[0]

		if _, err := s.AddParticipant(ctx, view.ID, polls.ParticipantInput{Name: "Alice", Answers: map[string]string{opt1.ID: "ifneedbe"}}, polls.Viewer{}); !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("ErrLimitReached once the poll has 500 participants", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		for i := 0; i < polls.LimitParticipants; i++ {
			seedParticipant(t, d, created.ID, fmt.Sprintf("P%d", i), nil, "")
		}

		if _, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Overflow", Answers: map[string]string{}}, polls.Viewer{}); !errors.Is(err, polls.ErrLimitReached) {
			t.Errorf("err = %v, want ErrLimitReached", err)
		}
	})
}

func TestUpdateParticipant(t *testing.T) {
	ctx := context.Background()

	t.Run("lets a manager update the name and replace votes", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1, opt2 := created.Options[0], created.Options[1]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Alice", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		if err := s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			NameSet: true, Name: "Alice Renamed", Answers: map[string]string{opt2.ID: "no"},
		}, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("UpdateParticipant: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		alice := findParticipant(view, result.ParticipantID)
		if alice.Name != "Alice Renamed" {
			t.Errorf("name = %q, want Alice Renamed", alice.Name)
		}
		if len(alice.Votes) != 1 || alice.Votes[opt2.ID] != "no" {
			t.Errorf("votes = %+v, want only opt2=no", alice.Votes)
		}
	})

	t.Run("lets the participant update via a matching guest participant id", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		if err := s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{opt1.ID: "no"},
		}, polls.Viewer{GuestParticipantID: result.ParticipantID}); err != nil {
			t.Fatalf("UpdateParticipant: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		if findParticipant(view, result.ParticipantID).Votes[opt1.ID] != "no" {
			t.Errorf("vote not updated")
		}
	})

	t.Run("ErrForbidden for a mismatched guest participant id", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{opt1.ID: "no"},
		}, polls.Viewer{GuestParticipantID: "wrong-participant"})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("ErrForbidden when neither manager, matching userId, nor a matching guest id is given", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		otherUserID := seedUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{opt1.ID: "no"},
		}, polls.Viewer{UserID: otherUserID})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("lets a signed-in participant update via a matching userId", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		voterID := seedUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Voter", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{UserID: voterID})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		if err := s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{opt1.ID: "no"},
		}, polls.Viewer{UserID: voterID}); err != nil {
			t.Fatalf("UpdateParticipant: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		if findParticipant(view, result.ParticipantID).Votes[opt1.ID] != "no" {
			t.Errorf("vote not updated")
		}
	})

	t.Run("ErrNotFound when the participant does not belong to the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		other := createTestPoll(t, ctx, s, orgID, ownerID)
		otherOpt := other.Options[0]
		result, err := s.AddParticipant(ctx, other.ID, polls.ParticipantInput{Name: "Elsewhere", Answers: map[string]string{otherOpt.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{Answers: map[string]string{}}, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrPollClosed once the poll is no longer open, even for a manager", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		err = s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{opt1.ID: "no"},
		}, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrPollClosed) {
			t.Errorf("err = %v, want ErrPollClosed", err)
		}
	})

	t.Run("ErrValidation when an answer references an option not on the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.UpdateParticipant(ctx, created.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{"bogus": "yes"},
		}, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("ErrValidation for an ifneedbe answer when the poll disallows it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		view, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options:           basicOptions(),
			PollSettingsInput: polls.PollSettingsInput{AllowIfNeedBe: boolPtr(false)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		opt1 := view.Options[0]
		result, err := s.AddParticipant(ctx, view.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.UpdateParticipant(ctx, view.ID, result.ParticipantID, polls.ParticipantInput{
			Answers: map[string]string{opt1.ID: "ifneedbe"},
		}, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
	})
}

func TestRemoveParticipant(t *testing.T) {
	ctx := context.Background()

	t.Run("lets a manager remove any participant", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		if err := s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("RemoveParticipant: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if findParticipant(view, result.ParticipantID) != nil {
			t.Error("participant still present")
		}
	})

	t.Run("ErrForbidden for the poll's creator once they've left the org (canManagePoll is membership-first)", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		// The creator leaves (or is removed from) the org — being the poll's own creator is not
		// itself membership, so canManagePoll must stop granting manage access once the
		// organization_members row is gone, even though poll.created_by still names them.
		ownerIDInt, perr := strconv.ParseInt(ownerID, 10, 64)
		if perr != nil {
			t.Fatalf("parse ownerID: %v", perr)
		}
		orgIDInt, perr := strconv.ParseInt(orgID, 10, 64)
		if perr != nil {
			t.Fatalf("parse orgID: %v", perr)
		}
		if _, err := d.ExecContext(ctx,
			`DELETE FROM organization_members WHERE organization_id = $1 AND user_id = $2`,
			orgIDInt, ownerIDInt,
		); err != nil {
			t.Fatalf("remove membership: %v", err)
		}

		if err := s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: ownerID}); !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("lets the participant remove themselves via their guest participant id", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		if err := s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{GuestParticipantID: result.ParticipantID}); err != nil {
			t.Fatalf("RemoveParticipant: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if findParticipant(view, result.ParticipantID) != nil {
			t.Error("participant still present")
		}
	})

	t.Run("ErrForbidden for an unrelated user", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		otherUserID := seedUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: otherUserID})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("ErrNotFound when the participant does not belong to the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		other := createTestPoll(t, ctx, s, orgID, ownerID)
		otherOpt := other.Options[0]
		result, err := s.AddParticipant(ctx, other.ID, polls.ParticipantInput{Name: "Elsewhere", Answers: map[string]string{otherOpt.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("lets a manager remove a participant once the poll is closed", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		if err := s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("RemoveParticipant: %v", err)
		}
	})

	t.Run("lets a manager remove a participant once the poll is finalized", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if err := s.Finalize(ctx, created.ID, orgID, opt1.ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}

		if err := s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("RemoveParticipant: %v", err)
		}
	})

	t.Run("ErrPollClosed for a non-manager once the poll is no longer open", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		err = s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{GuestParticipantID: result.ParticipantID})
		if !errors.Is(err, polls.ErrPollClosed) {
			t.Errorf("err = %v, want ErrPollClosed", err)
		}
	})

	t.Run("ErrNotFound for a manager when the poll is soft-deleted", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}
		if err := s.Delete(ctx, created.ID, orgID); err != nil {
			t.Fatalf("Delete: %v", err)
		}

		err = s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("lets an admin who did not create the poll manage it too", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		adminID := seedUser(t, d)
		addOrgMember(t, d, orgID, adminID, "admin")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		if err := s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: adminID}); err != nil {
			t.Fatalf("RemoveParticipant: %v", err)
		}
	})

	t.Run("ErrForbidden for a same-org member who is not an admin/owner and did not create the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		memberID := seedUser(t, d)
		addOrgMember(t, d, orgID, memberID, "member")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]
		result, err := s.AddParticipant(ctx, created.ID, polls.ParticipantInput{Name: "Bob", Answers: map[string]string{opt1.ID: "yes"}}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddParticipant: %v", err)
		}

		err = s.RemoveParticipant(ctx, created.ID, result.ParticipantID, polls.Viewer{UserID: memberID})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})
}

func TestAddComment(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a comment and returns it", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		comment, err := s.AddComment(ctx, created.ID, polls.CommentInput{AuthorName: "Alice", Body: "Looks good"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}
		if comment.ID == "" {
			t.Fatal("Comment.ID is empty")
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{})
		if findComment(view, comment.ID) == nil {
			t.Error("comment not found in view")
		}
	})

	t.Run("ErrNotFound for a missing poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		if _, err := s.AddComment(ctx, "missing12345", polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{}); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrForbidden when the poll disallows comments", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		view, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeDatetime, Title: "T", Timezone: "Europe/Oslo",
			Options:           basicOptions(),
			PollSettingsInput: polls.PollSettingsInput{AllowComments: boolPtr(false)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		if _, err := s.AddComment(ctx, view.ID, polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{}); !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("allows comments on a closed or finalized poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		if _, err := s.AddComment(ctx, created.ID, polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{}); err != nil {
			t.Fatalf("AddComment (closed): %v", err)
		}

		if err := s.Finalize(ctx, created.ID, orgID, created.Options[0].ID, ownerID); err != nil {
			t.Fatalf("Finalize: %v", err)
		}
		if _, err := s.AddComment(ctx, created.ID, polls.CommentInput{AuthorName: "Bob", Body: "y"}, polls.Viewer{}); err != nil {
			t.Fatalf("AddComment (finalized): %v", err)
		}
	})
}

func TestDeleteComment(t *testing.T) {
	ctx := context.Background()

	t.Run("lets a manager delete any comment, excluding it from GetView", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		comment, err := s.AddComment(ctx, created.ID, polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}

		if err := s.DeleteComment(ctx, created.ID, comment.ID, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("DeleteComment: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if findComment(view, comment.ID) != nil {
			t.Error("comment still present")
		}
	})

	t.Run("lets the author delete their own comment via userId", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		authorID := seedUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		comment, err := s.AddComment(ctx, created.ID, polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{UserID: authorID})
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}

		if err := s.DeleteComment(ctx, created.ID, comment.ID, polls.Viewer{UserID: authorID}); err != nil {
			t.Fatalf("DeleteComment: %v", err)
		}
	})

	t.Run("ErrForbidden for an unrelated user", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		authorID := seedUser(t, d)
		otherID := seedUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		comment, err := s.AddComment(ctx, created.ID, polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{UserID: authorID})
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}

		err = s.DeleteComment(ctx, created.ID, comment.ID, polls.Viewer{UserID: otherID})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("ErrNotFound when the comment does not belong to the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		other := createTestPoll(t, ctx, s, orgID, ownerID)
		comment, err := s.AddComment(ctx, other.ID, polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}

		err = s.DeleteComment(ctx, created.ID, comment.ID, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrNotFound when deleting an already-deleted comment", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		addOrgMember(t, d, orgID, ownerID, "owner")
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		comment, err := s.AddComment(ctx, created.ID, polls.CommentInput{AuthorName: "Alice", Body: "x"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("AddComment: %v", err)
		}
		if err := s.DeleteComment(ctx, created.ID, comment.ID, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("DeleteComment: %v", err)
		}

		err = s.DeleteComment(ctx, created.ID, comment.ID, polls.Viewer{UserID: ownerID})
		if !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

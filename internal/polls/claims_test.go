package polls_test

// Ports the behavioral cases from src/server/polls/__tests__/claims.workers.test.ts and
// claim-auth.workers.test.ts case-for-case, adapted to Viewer{UserID, GuestParticipantID} — see
// claims.go's/participants.go's package doc comments for how claim-auth.ts's
// requireParticipantAuth/canManagePoll are folded into Claim/Unclaim via Viewer instead of a
// separately exported function. Unclaim's own signature deviation (no explicit target
// participant id, so the org-manager-unclaims-someone-else's-slot path isn't reachable) is
// documented on Unclaim itself (claims.go) and in the task report; the "no-op when the claim
// doesn't exist" and "allowClosed owner path" cases below are ported in the closest form that
// signature supports.

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

func TestClaim(t *testing.T) {
	ctx := context.Background()

	t.Run("creates a guest participant and reports IsGuest/Created", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]

		result, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if !result.Created || !result.IsGuest {
			t.Fatalf("result = %+v, want Created && IsGuest", result)
		}
		if len(result.ClaimedOptionIDs) != 1 || result.ClaimedOptionIDs[0] != slot.ID {
			t.Errorf("ClaimedOptionIDs = %v, want [%s]", result.ClaimedOptionIDs, slot.ID)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		alice := findParticipant(view, result.ParticipantID)
		if alice == nil || alice.Votes[slot.ID] != "yes" {
			t.Errorf("view participant votes = %+v, want yes", alice)
		}
	})

	t.Run("reports IsGuest false for a signed-in claimant", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		claimantID := seedUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]

		result, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Voter"}, polls.Viewer{UserID: claimantID})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if result.IsGuest {
			t.Errorf("IsGuest = true, want false")
		}
	})

	t.Run("claims an additional slot for an existing participant via ParticipantID", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 2)
		slot1, slot2 := created.Options[0], created.Options[1]

		first, err := s.Claim(ctx, created.ID, slot1.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim (1): %v", err)
		}
		second, err := s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{ParticipantID: first.ParticipantID}, polls.Viewer{GuestParticipantID: first.ParticipantID})
		if err != nil {
			t.Fatalf("Claim (2): %v", err)
		}

		if second.ParticipantID != first.ParticipantID {
			t.Errorf("ParticipantID = %s, want %s", second.ParticipantID, first.ParticipantID)
		}
		if second.Created {
			t.Error("Created = true, want false")
		}
		wantIDs := map[string]bool{slot1.ID: true, slot2.ID: true}
		for _, id := range second.ClaimedOptionIDs {
			delete(wantIDs, id)
		}
		if len(wantIDs) != 0 || len(second.ClaimedOptionIDs) != 2 {
			t.Errorf("ClaimedOptionIDs = %v, want both slots", second.ClaimedOptionIDs)
		}
	})

	t.Run("is idempotent: claiming an already-claimed slot is a no-op", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(1)}, 0)
		slot := created.Options[0]

		first, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim (1): %v", err)
		}
		if !first.Changed {
			t.Fatal("Changed = false on first claim, want true")
		}

		again, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{ParticipantID: first.ParticipantID}, polls.Viewer{GuestParticipantID: first.ParticipantID})
		if err != nil {
			t.Fatalf("Claim (2): %v", err)
		}
		if again.ParticipantID != first.ParticipantID || again.Changed {
			t.Errorf("again = %+v, want same participant, Changed=false", again)
		}
		if len(again.ClaimedOptionIDs) != 1 || again.ClaimedOptionIDs[0] != slot.ID {
			t.Errorf("ClaimedOptionIDs = %v, want [%s]", again.ClaimedOptionIDs, slot.ID)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if view.Claims[slot.ID].Count != 1 {
			t.Errorf("claim count = %d, want 1", view.Claims[slot.ID].Count)
		}
	})

	t.Run("reuses the same participant for a signed-in caller claiming twice without ParticipantID", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		claimantID := seedUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 2)
		slot1, slot2 := created.Options[0], created.Options[1]

		first, err := s.Claim(ctx, created.ID, slot1.ID, polls.ClaimInput{Name: "Voter"}, polls.Viewer{UserID: claimantID})
		if err != nil {
			t.Fatalf("Claim (1): %v", err)
		}
		if !first.Created {
			t.Fatal("Created = false on first claim, want true")
		}

		second, err := s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{Name: "Voter"}, polls.Viewer{UserID: claimantID})
		if err != nil {
			t.Fatalf("Claim (2): %v", err)
		}
		if second.ParticipantID != first.ParticipantID || second.Created {
			t.Errorf("second = %+v, want reuse of %s", second, first.ParticipantID)
		}
	})

	t.Run("ErrClaimLimitReached for a signed-in caller who never passes ParticipantID", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		claimantID := seedUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 1)
		slot1, slot2 := created.Options[0], created.Options[1]

		if _, err := s.Claim(ctx, created.ID, slot1.ID, polls.ClaimInput{Name: "Voter"}, polls.Viewer{UserID: claimantID}); err != nil {
			t.Fatalf("Claim (1): %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{Name: "Voter"}, polls.Viewer{UserID: claimantID}); !errors.Is(err, polls.ErrClaimLimitReached) {
			t.Errorf("err = %v, want ErrClaimLimitReached", err)
		}
	})

	t.Run("ErrClaimLimitReached, then succeeds after signupMaxClaims is raised", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 1)
		slot1, slot2 := created.Options[0], created.Options[1]

		first, err := s.Claim(ctx, created.ID, slot1.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim (1): %v", err)
		}
		_, err = s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{ParticipantID: first.ParticipantID}, polls.Viewer{GuestParticipantID: first.ParticipantID})
		if !errors.Is(err, polls.ErrClaimLimitReached) {
			t.Errorf("err = %v, want ErrClaimLimitReached", err)
		}

		two := 2
		if _, err := s.Update(ctx, created.ID, orgID, polls.UpdatePollInput{SignupMaxClaims: &two}); err != nil {
			t.Fatalf("Update: %v", err)
		}

		second, err := s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{ParticipantID: first.ParticipantID}, polls.Viewer{GuestParticipantID: first.ParticipantID})
		if err != nil {
			t.Fatalf("Claim (2, after raise): %v", err)
		}
		if len(second.ClaimedOptionIDs) != 2 {
			t.Errorf("ClaimedOptionIDs = %v, want both slots", second.ClaimedOptionIDs)
		}
	})

	t.Run("ErrCapacityFull once a capacity-1 slot has a claim", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(1)}, 5)
		slot := created.Options[0]

		if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim (1): %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Bob"}, polls.Viewer{}); !errors.Is(err, polls.ErrCapacityFull) {
			t.Errorf("err = %v, want ErrCapacityFull", err)
		}
	})

	t.Run("accepts many claims on an unlimited (nil) capacity slot", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 5)
		slot := created.Options[0]

		for _, name := range []string{"Alice", "Bob", "Carol"} {
			if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: name}, polls.Viewer{}); err != nil {
				t.Fatalf("Claim(%s): %v", name, err)
			}
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if view.Claims[slot.ID].Count != 3 {
			t.Errorf("count = %d, want 3", view.Claims[slot.ID].Count)
		}
	})

	t.Run("ErrPollClosed when the poll is not open", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{}); !errors.Is(err, polls.ErrPollClosed) {
			t.Errorf("err = %v, want ErrPollClosed", err)
		}
	})

	t.Run("ErrValidation for a non-signup (datetime) poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		opt1 := created.Options[0]

		if _, err := s.Claim(ctx, created.ID, opt1.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{}); !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("ErrNotFound for an option that is not on the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		other := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)

		if _, err := s.Claim(ctx, created.ID, other.Options[0].ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{}); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrNotFound when the given ParticipantID is not on the poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]

		if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{ParticipantID: "pa_missing"}, polls.Viewer{}); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrValidation for an empty name", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]

		if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "   "}, polls.Viewer{}); !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err = %v, want ErrValidation", err)
		}
	})

	t.Run("ErrEmailRequired when the poll requires an email and none is given", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPollWithEmail(t, ctx, s, orgID, ownerID, []*int{nil})
		slot := created.Options[0]

		if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{}); !errors.Is(err, polls.ErrEmailRequired) {
			t.Errorf("err = %v, want ErrEmailRequired", err)
		}
	})
}

// createSignupPollWithEmail is createSignupPoll plus RequireParticipantEmail:true — needed by the
// EMAIL_REQUIRED claim test, since createSignupPoll (service_test.go) doesn't expose that knob.
func createSignupPollWithEmail(t *testing.T, ctx context.Context, s *polls.Service, orgID, userID string, capacities []*int) *polls.PollView {
	t.Helper()
	options := make([]polls.OptionInput, len(capacities))
	for i, capacity := range capacities {
		opt := textOption("Slot")
		opt.CapacitySet, opt.Capacity = true, capacity
		options[i] = opt
	}
	view, err := s.Create(ctx, orgID, userID, polls.CreatePollInput{
		Type: polls.PollTypeSignup, Title: "Signup poll", Timezone: "Europe/Oslo", Options: options,
		PollSettingsInput: polls.PollSettingsInput{RequireParticipantEmail: boolPtr(true)},
	})
	if err != nil {
		t.Fatalf("Create (signup+email): %v", err)
	}
	return view
}

// TestClaimAuth ports claim-auth.workers.test.ts's requireParticipantAuth matrix, exercised
// through Claim's ParticipantID branch (participantAuthorized in participants.go).
func TestClaimAuth(t *testing.T) {
	ctx := context.Background()

	setup := func(t *testing.T) (d *sql.DB, s *polls.Service, orgID, ownerID, pollID string, guestParticipantID, signedInParticipantID, signedInClaimantID string) {
		t.Helper()
		d = testdb.New(t)
		s = polls.NewService(d)
		orgID, ownerID = seedOrgAndUser(t, d)
		signedInClaimantID = seedUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 0)
		slot1, slot2 := created.Options[0], created.Options[1]

		guestClaim, err := s.Claim(ctx, created.ID, slot1.ID, polls.ClaimInput{Name: "Guest"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim (guest): %v", err)
		}
		signedInClaim, err := s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{Name: "Signed in"}, polls.Viewer{UserID: signedInClaimantID})
		if err != nil {
			t.Fatalf("Claim (signed in): %v", err)
		}
		return d, s, orgID, ownerID, created.ID, guestClaim.ParticipantID, signedInClaim.ParticipantID, signedInClaimantID
	}

	// A second slot on the poll for the authorization matrix to attempt claiming (distinct from
	// each identity's own already-claimed slot); created lazily per subtest via a 3rd/4th option
	// would complicate setup, so instead these tests target the SAME participant's *own* already-
	// claimed slot (a harmless idempotent re-claim) purely to exercise participantAuthorized.

	t.Run("allows the poll owner (manager)", func(t *testing.T) {
		_, s, orgID, ownerID, pollID, guestParticipantID, _, _ := setup(t)
		view, _ := s.GetView(ctx, pollID, polls.Viewer{UserID: ownerID})
		slot1 := view.Options[0]
		_ = orgID

		_, err := s.Claim(ctx, pollID, slot1.ID, polls.ClaimInput{ParticipantID: guestParticipantID}, polls.Viewer{UserID: ownerID})
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("allows an admin who did not create the poll", func(t *testing.T) {
		d, s, orgID, _, pollID, guestParticipantID, _, _ := setup(t)
		adminID := seedUser(t, d)
		addOrgMember(t, d, orgID, adminID, "admin")
		view, _ := s.GetView(ctx, pollID, polls.Viewer{UserID: adminID})
		slot1 := view.Options[0]

		_, err := s.Claim(ctx, pollID, slot1.ID, polls.ClaimInput{ParticipantID: guestParticipantID}, polls.Viewer{UserID: adminID})
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("allows the participant acting as their own signed-in user", func(t *testing.T) {
		_, s, _, _, pollID, _, signedInParticipantID, signedInClaimantID := setup(t)
		view, _ := s.GetView(ctx, pollID, polls.Viewer{UserID: signedInClaimantID})
		slot2 := view.Options[1]

		_, err := s.Claim(ctx, pollID, slot2.ID, polls.ClaimInput{ParticipantID: signedInParticipantID}, polls.Viewer{UserID: signedInClaimantID})
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("allows a matching guest participant id", func(t *testing.T) {
		_, s, _, ownerID, pollID, guestParticipantID, _, _ := setup(t)
		view, _ := s.GetView(ctx, pollID, polls.Viewer{UserID: ownerID})
		slot1 := view.Options[0]

		_, err := s.Claim(ctx, pollID, slot1.ID, polls.ClaimInput{ParticipantID: guestParticipantID}, polls.Viewer{GuestParticipantID: guestParticipantID})
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
	})

	t.Run("ErrForbidden for a same-org member who did not create the poll, with no matching guest id", func(t *testing.T) {
		d, s, orgID, ownerID, pollID, guestParticipantID, _, _ := setup(t)
		memberID := seedUser(t, d)
		addOrgMember(t, d, orgID, memberID, "member")
		view, _ := s.GetView(ctx, pollID, polls.Viewer{UserID: ownerID})
		slot1 := view.Options[0]

		_, err := s.Claim(ctx, pollID, slot1.ID, polls.ClaimInput{ParticipantID: guestParticipantID}, polls.Viewer{UserID: memberID})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("ErrForbidden for an unrelated signed-in user with no matching guest id", func(t *testing.T) {
		d, s, _, ownerID, pollID, guestParticipantID, _, _ := setup(t)
		otherUserID := seedUser(t, d)
		view, _ := s.GetView(ctx, pollID, polls.Viewer{UserID: ownerID})
		slot1 := view.Options[0]

		_, err := s.Claim(ctx, pollID, slot1.ID, polls.ClaimInput{ParticipantID: guestParticipantID}, polls.Viewer{UserID: otherUserID})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("ErrForbidden for a mismatched guest participant id (wrong-token analog)", func(t *testing.T) {
		_, s, _, ownerID, pollID, guestParticipantID, _, _ := setup(t)
		view, _ := s.GetView(ctx, pollID, polls.Viewer{UserID: ownerID})
		slot1 := view.Options[0]

		_, err := s.Claim(ctx, pollID, slot1.ID, polls.ClaimInput{ParticipantID: guestParticipantID}, polls.Viewer{GuestParticipantID: "wrong-participant"})
		if !errors.Is(err, polls.ErrForbidden) {
			t.Errorf("err = %v, want ErrForbidden", err)
		}
	})

	t.Run("ErrNotFound when the given ParticipantID does not belong to the poll", func(t *testing.T) {
		_, s, otherOrgID, otherOwnerID, _, _, _, _ := setup(t)
		otherCreated := createSignupPoll(t, ctx, s, otherOrgID, otherOwnerID, []*int{nil}, 0)
		otherClaim, err := s.Claim(ctx, otherCreated.ID, otherCreated.Options[0].ID, polls.ClaimInput{Name: "Elsewhere"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim (elsewhere): %v", err)
		}

		secondPoll := createSignupPoll(t, ctx, s, otherOrgID, otherOwnerID, []*int{nil}, 0)
		_, err = s.Claim(ctx, secondPoll.ID, secondPoll.Options[0].ID, polls.ClaimInput{ParticipantID: otherClaim.ParticipantID}, polls.Viewer{UserID: otherOwnerID})
		if !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestUnclaim(t *testing.T) {
	ctx := context.Background()

	t.Run("removes a claim", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil, nil}, 2)
		slot1, slot2 := created.Options[0], created.Options[1]
		claim, err := s.Claim(ctx, created.ID, slot1.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, slot2.ID, polls.ClaimInput{ParticipantID: claim.ParticipantID}, polls.Viewer{GuestParticipantID: claim.ParticipantID}); err != nil {
			t.Fatalf("Claim (2nd slot): %v", err)
		}

		if err := s.Unclaim(ctx, created.ID, slot1.ID, polls.Viewer{GuestParticipantID: claim.ParticipantID}); err != nil {
			t.Fatalf("Unclaim: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if view.Claims[slot1.ID].Count != 0 {
			t.Errorf("slot1 count = %d, want 0", view.Claims[slot1.ID].Count)
		}
		if view.Claims[slot2.ID].Count != 1 {
			t.Errorf("slot2 count = %d, want 1", view.Claims[slot2.ID].Count)
		}
	})

	t.Run("is a no-op when the claim does not exist", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]
		claim, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}

		if err := s.Unclaim(ctx, created.ID, slot.ID, polls.Viewer{GuestParticipantID: claim.ParticipantID}); err != nil {
			t.Fatalf("Unclaim (1): %v", err)
		}
		if err := s.Unclaim(ctx, created.ID, slot.ID, polls.Viewer{GuestParticipantID: claim.ParticipantID}); err != nil {
			t.Fatalf("Unclaim (2, no-op): %v", err)
		}
	})

	t.Run("ErrPollClosed when the poll is not open", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]
		claim, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Alice"}, polls.Viewer{})
		if err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		err = s.Unclaim(ctx, created.ID, slot.ID, polls.Viewer{GuestParticipantID: claim.ParticipantID})
		if !errors.Is(err, polls.ErrPollClosed) {
			t.Errorf("err = %v, want ErrPollClosed", err)
		}
	})

	t.Run("allows removal on a closed sheet for a manager freeing their own claim", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
		slot := created.Options[0]
		// The owner is also a claimant on their own poll — the closest form of TS's "owner
		// unclaims a claim on a closed sheet" reachable through Unclaim's signature (which has no
		// room for an explicit target distinct from Viewer — see Unclaim's doc comment).
		if _, err := s.Claim(ctx, created.ID, slot.ID, polls.ClaimInput{Name: "Owner"}, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		if err := s.SetStatus(ctx, created.ID, orgID, "closed"); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}

		if err := s.Unclaim(ctx, created.ID, slot.ID, polls.Viewer{UserID: ownerID}); err != nil {
			t.Fatalf("Unclaim: %v", err)
		}

		view, _ := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
		if view.Claims[slot.ID].Count != 0 {
			t.Errorf("count = %d, want 0", view.Claims[slot.ID].Count)
		}
	})

	t.Run("ErrNotFound for a missing poll and ErrValidation for a non-signup poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)

		if err := s.Unclaim(ctx, "missing12345", "x", polls.Viewer{GuestParticipantID: "pa_x"}); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err (missing poll) = %v, want ErrNotFound", err)
		}
		if err := s.Unclaim(ctx, created.ID, "x", polls.Viewer{GuestParticipantID: "pa_x"}); !errors.Is(err, polls.ErrValidation) {
			t.Errorf("err (non-signup) = %v, want ErrValidation", err)
		}
	})
}

// TestClaimLastSlotExactlyOneWinner is spec §9's overbooking proof (task brief's exact scenario):
// 16 goroutines race to claim the last (and only) slot of a capacity-1 option. THE atomicity
// contract (SELECT ... FOR UPDATE on poll_options before the capacity count, held until the
// winning insert commits — see claims.go's package doc comment) must let exactly one of them
// through; every other goroutine must observe ErrCapacityFull, never a second successful claim.
//
// This environment has no cgo, so `go test -race` cannot run (the race detector requires cgo);
// per the task brief, this is compensated for with `-count=5` repetition instead (see the task
// report) — run the same 16-way race five times over rather than relying on -race to catch a
// hypothetical missed lock.
func TestClaimLastSlotExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	d := testdb.New(t)
	s := polls.NewService(d)
	orgID, ownerID := seedOrgAndUser(t, d)
	created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(1)}, 1)
	optionID := created.Options[0].ID

	const racers = 16
	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Claim(ctx, created.ID, optionID, polls.ClaimInput{Name: raceName(i)}, polls.Viewer{})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, polls.ErrCapacityFull) {
				t.Errorf("racer %d: err = %v, want nil or ErrCapacityFull", i, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if wins.Load() != 1 {
		t.Fatalf("winners = %d, want exactly 1", wins.Load())
	}

	view, err := s.GetView(ctx, created.ID, polls.Viewer{UserID: ownerID})
	if err != nil {
		t.Fatalf("GetView: %v", err)
	}
	if view.Claims[optionID].Count != 1 {
		t.Fatalf("claim count = %d, want exactly 1", view.Claims[optionID].Count)
	}
}

func raceName(i int) string {
	return "Racer" + string(rune('A'+i))
}

package polls_test

// Ports main:src/server/polls/__tests__/roster.workers.test.ts case-for-case against
// BuildRosterCSV: header + one row per claim + a zero-claim row per untaken slot, the UTF-8 BOM,
// RFC 4180 quoting, formula-injection defusing, empty capacity for unlimited slots, NOT_FOUND —
// plus the route-level "not a sign-up sheet" rule the old roster route enforced.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/refsdal/whenweall/internal/polls"
	"github.com/refsdal/whenweall/internal/testdb"
)

// rosterLines strips the BOM and splits the CSV into its non-empty CRLF-terminated lines.
func rosterLines(t *testing.T, csv string) []string {
	t.Helper()
	if !strings.HasPrefix(csv, "\uFEFF") {
		t.Fatalf("csv does not start with a UTF-8 BOM: %q", csv[:min(len(csv), 12)])
	}
	var out []string
	for _, l := range strings.Split(strings.TrimPrefix(csv, "\uFEFF"), "\r\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func TestBuildRosterCSV(t *testing.T) {
	ctx := context.Background()

	t.Run("emits a header row, one row per claim, and a zero-claim row for an unclaimed slot", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(2), nil}, 0)
		if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Alice", Email: strPtr("alice@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim: %v", err)
		}

		csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
		if err != nil {
			t.Fatalf("BuildRosterCSV: %v", err)
		}
		lines := rosterLines(t, csv)
		if lines[0] != "slot,capacity,claimed,participant,email" {
			t.Errorf("header = %q", lines[0])
		}
		if !strings.Contains(csv, "Slot 0,2,1,Alice,alice@example.com") {
			t.Errorf("missing Alice's claim row: %q", csv)
		}
		// Slot 1 (unlimited, unclaimed): one row, empty capacity, zero claimed, empty participant/email.
		found := false
		for _, l := range lines {
			if l == "Slot 1,,0,," {
				found = true
			}
		}
		if !found {
			t.Errorf("missing zero-claim row %q in %q", "Slot 1,,0,,", lines)
		}
	})

	t.Run("quotes fields containing commas, quotes, or newlines per RFC 4180", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created, err := s.Create(ctx, orgID, ownerID, polls.CreatePollInput{
			Type: polls.PollTypeSignup, Title: "Sheet", Timezone: "Europe/Oslo",
			Options: []polls.OptionInput{withCapacity(textOption(`Bring "snacks", please`), nil)},
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: "Bob, Jr.", Email: strPtr("bob@example.com")}, polls.Viewer{}); err != nil {
			t.Fatalf("Claim: %v", err)
		}

		csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
		if err != nil {
			t.Fatalf("BuildRosterCSV: %v", err)
		}
		for _, want := range []string{`"Bring ""snacks"", please"`, `"Bob, Jr."`} {
			if !strings.Contains(csv, want) {
				t.Errorf("csv missing %s: %q", want, csv)
			}
		}
	})

	for _, name := range []string{"=1+1", "+1+1", "-1+1", "@SUM(A1)"} {
		t.Run("prefixes a formula-looking name ("+name+") with a single quote", func(t *testing.T) {
			d := testdb.New(t)
			s := polls.NewService(d)
			orgID, ownerID := seedOrgAndUser(t, d)
			created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{nil}, 0)
			if _, err := s.Claim(ctx, created.ID, created.Options[0].ID, polls.ClaimInput{Name: name}, polls.Viewer{}); err != nil {
				t.Fatalf("Claim: %v", err)
			}

			csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
			if err != nil {
				t.Fatalf("BuildRosterCSV: %v", err)
			}
			if !strings.Contains(csv, ",'"+name) {
				t.Errorf("csv did not defuse %q: %q", name, csv)
			}
		})
	}

	t.Run("leaves capacity empty for unlimited slots and prints the number for capped slots", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createSignupPoll(t, ctx, s, orgID, ownerID, []*int{intPtr(3), nil}, 0)

		csv, err := s.BuildRosterCSV(ctx, created.ID, "en")
		if err != nil {
			t.Fatalf("BuildRosterCSV: %v", err)
		}
		lines := rosterLines(t, csv)
		var capped, unlimited bool
		for _, l := range lines {
			capped = capped || strings.HasPrefix(l, "Slot 0,3,")
			unlimited = unlimited || strings.HasPrefix(l, "Slot 1,,")
		}
		if !capped || !unlimited {
			t.Errorf("capacity columns wrong (capped=%v unlimited=%v): %q", capped, unlimited, lines)
		}
	})

	t.Run("ErrNotFound for a missing poll", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		if _, err := s.BuildRosterCSV(ctx, "missing12345", "en"); !errors.Is(err, polls.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("ErrNotSignup for a scheduling poll (the old route's 400 'Not a sign-up sheet')", func(t *testing.T) {
		d := testdb.New(t)
		s := polls.NewService(d)
		orgID, ownerID := seedOrgAndUser(t, d)
		created := createTestPoll(t, ctx, s, orgID, ownerID)
		if _, err := s.BuildRosterCSV(ctx, created.ID, "en"); !errors.Is(err, polls.ErrNotSignup) {
			t.Errorf("err = %v, want ErrNotSignup", err)
		}
	})
}

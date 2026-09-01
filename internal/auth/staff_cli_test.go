package auth

import (
	"context"
	"strings"
	"testing"
)

// Tests for MakeStaff's use as the create-staff-user bootstrap command's implementation — see
// cmd/whenweall/main.go's createStaffUserCmd. TestStaffFlagAndRequireStaff (auth_test.go) already
// covers that MakeStaff actually grants staff access end to end; these tests cover the two
// properties the CLI subcommand depends on: repeat runs are safe, and an unknown email fails with
// a message an operator can act on.

// TestMakeStaffIdempotent asserts that running create-staff-user twice against the same email —
// the expected recovery path if an operator re-runs the bootstrap command, or runs it against an
// already-staffed account — succeeds both times and leaves exactly one staff_users row behind,
// rather than erroring on the second call's duplicate insert.
func TestMakeStaffIdempotent(t *testing.T) {
	ts := newTestService(t)
	email := "repeat-staffer@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

	if err := ts.svc.MakeStaff(context.Background(), email); err != nil {
		t.Fatalf("MakeStaff (first call): %v", err)
	}
	if err := ts.svc.MakeStaff(context.Background(), email); err != nil {
		t.Fatalf("MakeStaff (second call, should be a no-op): %v", err)
	}

	var count int
	row := ts.svc.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM staff_users su JOIN users u ON u.id = su.user_id WHERE u.email = $1`,
		email,
	)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("counting staff_users rows: %v", err)
	}
	if count != 1 {
		t.Errorf("staff_users rows for %s = %d, want 1 (MakeStaff should be idempotent)", email, count)
	}
}

// TestMakeStaffUnknownEmailErrorMentionsNoUser asserts the unknown-email error's wording, since
// the create-staff-user subcommand prints this error directly to an operator's terminal — it
// needs to read as "there's no such account" rather than as an opaque internal failure.
func TestMakeStaffUnknownEmailErrorMentionsNoUser(t *testing.T) {
	ts := newTestService(t)
	err := ts.svc.MakeStaff(context.Background(), "nobody@example.com")
	if err == nil {
		t.Fatal("MakeStaff(unknown email) = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "no user") {
		t.Errorf("MakeStaff(unknown email) error = %q, want it to mention %q", err.Error(), "no user")
	}
}

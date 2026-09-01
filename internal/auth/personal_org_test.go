package auth

import (
	"context"
	"fmt"
	"testing"

	"github.com/thecodearcher/limen"
	"github.com/thecodearcher/limen/plugins/organization"
)

// lookupUserID looks up email's user id directly (same approach as MakeStaff — there is no
// public find-by-email API on Limen), so the test can build a *limen.User to drive the
// organization API the same way Limen itself would after authenticating that user.
func lookupUserID(t *testing.T, ts *testService, email string) int64 {
	t.Helper()
	var id int64
	if err := ts.svc.db.QueryRowContext(context.Background(),
		"SELECT id FROM users WHERE email = $1", limen.NormalizeEmail(email),
	).Scan(&id); err != nil {
		t.Fatalf("lookupUserID(%q): %v", email, err)
	}
	return id
}

// countOrganizations returns how many organizations user belongs to.
func countOrganizations(t *testing.T, ts *testService, user *limen.User) int64 {
	t.Helper()
	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	return page.Total
}

// TestPersonalOrgCreatedOnSignup covers Task 3's brief step 1: after a credential signup, the new
// user has exactly one organization (the silent personal org); signing out and back in again does
// not create a second one.
func TestPersonalOrgCreatedOnSignup(t *testing.T) {
	ts := newTestService(t)
	email := "org-owner@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after signup = %d, want 1", got)
	}

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signout", map[string]any{}), "signout")
	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signin/credential", map[string]any{
		"credential": email,
		"password":   signupPassword,
	}), "signin")

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after sign out/in again = %d, want still 1", got)
	}
}

// TestPersonalOrgHookIdempotentForExistingUser directly exercises the guard the After hook relies
// on (ListOrganizations count checked before CreateOrganization) — the same logic an OAuth
// re-sign-in of an existing user hits via the "oauth-callback" route, which this test doesn't need
// to actually drive over HTTP (that would require a live OAuth provider) since
// ensurePersonalOrganizationForUser is exactly what the hook calls once it has resolved
// ctx.GetAuthResult().User.
func TestPersonalOrgHookIdempotentForExistingUser(t *testing.T) {
	ts := newTestService(t)
	email := "repeat-signer@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after signup = %d, want 1", got)
	}

	// Simulate a second "brand new session" authentication event for the same, already-orged
	// user (what the After hook itself would do on a real oauth-callback re-sign-in).
	ts.svc.ensurePersonalOrganizationForUser(context.Background(), user)

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after a second ensure call = %d, want still 1 (not idempotent)", got)
	}
}

// TestRequireOrgMemberSucceedsForPersonalOrg is the happy-path RequireOrgMember test deferred from
// the previous plan's review: after signup creates the personal org, RequireOrgMember succeeds for
// that user against that org's id.
func TestRequireOrgMemberSucceedsForPersonalOrg(t *testing.T) {
	ts := newTestService(t)
	email := "member-check@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	orgID := fmt.Sprint(page.Items[0].ID)

	sess := &Session{UserID: fmt.Sprint(userID)}
	ctx := context.WithValue(context.Background(), sessionCtxKey{}, sess)

	gotSess, err := ts.svc.RequireOrgMember(ctx, orgID)
	if err != nil {
		t.Fatalf("RequireOrgMember(member, their personal org) error = %v, want nil", err)
	}
	if gotSess != sess {
		t.Errorf("RequireOrgMember returned session = %+v, want the same *Session passed in via context", gotSess)
	}
}

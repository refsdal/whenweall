package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
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

// triggerSessionResolution hits an authenticated route so Middleware/resolveSession actually runs
// with the session cookie present — the invariant is enforced lazily in resolveSession
// (ensurePersonalOrgOnce), not at signup/signin response time itself: the signup/signin request's
// own pass through Middleware happens before that response's Set-Cookie has reached the client, so
// it takes one more round trip (here, /me) for resolveSession to ever see the cookie.
func triggerSessionResolution(t *testing.T, ts *testService) {
	t.Helper()
	resp := ts.get(t, "/api/v1/auth/me")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /me: status %d: %s", resp.StatusCode, body)
	}
}

// TestPersonalOrgCreatedOnSignup covers Task 3's brief step 1: after a credential signup and the
// first authenticated request, the new user has exactly one organization (the silent personal
// org); signing out and back in again does not create a second one.
func TestPersonalOrgCreatedOnSignup(t *testing.T) {
	ts := newTestService(t)
	email := "org-owner@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	triggerSessionResolution(t, ts)

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
	triggerSessionResolution(t, ts)

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after sign out/in again = %d, want still 1", got)
	}
}

// TestPersonalOrgEnsureIsIdempotentAtTheDBLevel directly exercises the guard
// ensurePersonalOrganizationForUser relies on (ListOrganizations count re-checked, under an
// advisory lock, before CreateOrganization) — independent of the in-process once-cache
// (personalOrgEnsured), which TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne below covers
// separately. This is what makes a redirect-mode OAuth re-sign-in of an existing user (which this
// suite can't drive end-to-end without a live OAuth provider) a no-op: it hits exactly this
// function on every request, cache or no cache.
func TestPersonalOrgEnsureIsIdempotentAtTheDBLevel(t *testing.T) {
	ts := newTestService(t)
	email := "repeat-signer@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	triggerSessionResolution(t, ts)

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after signup = %d, want 1", got)
	}

	// Call the core check-then-create directly a second time, bypassing the once-cache
	// entirely, to prove the DB-level guard itself (not just the cache) is idempotent.
	if err := ts.svc.ensurePersonalOrganizationForUser(context.Background(), user); err != nil {
		t.Fatalf("ensurePersonalOrganizationForUser (second call): %v", err)
	}

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after a second ensure call = %d, want still 1 (not idempotent)", got)
	}
}

// TestPersonalOrgRecreatedAfterCacheClearAndOrgDeleted proves the lazy path actually re-derives
// the invariant from the database rather than only ever trusting the cache: after the personal org
// is created and the user is marked done in personalOrgEnsured, deleting the org out from under
// them and clearing that one cache entry (simulating this same process having forgotten, or a
// fresh replica that never saw this user before) makes the very next authenticated request recreate
// exactly one organization.
func TestPersonalOrgRecreatedAfterCacheClearAndOrgDeleted(t *testing.T) {
	ts := newTestService(t)
	email := "cache-cleared@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	triggerSessionResolution(t, ts)

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}

	page, err := ts.svc.orgs.ListOrganizations(context.Background(), user, &organization.ListOrganizationsFilter{}, &limen.QueryOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("len(page.Items) = %d, want 1", len(page.Items))
	}
	orgID := page.Items[0].ID

	if _, err := ts.svc.db.ExecContext(context.Background(),
		"DELETE FROM organization_members WHERE organization_id = $1", orgID,
	); err != nil {
		t.Fatalf("delete organization_members: %v", err)
	}
	if _, err := ts.svc.db.ExecContext(context.Background(),
		"DELETE FROM organizations WHERE id = $1", orgID,
	); err != nil {
		t.Fatalf("delete organizations: %v", err)
	}
	if got := countOrganizations(t, ts, user); got != 0 {
		t.Fatalf("organizations after manual deletion = %d, want 0", got)
	}

	// Clear this user's once-cache entry directly (white-box: same package) rather than adding a
	// dedicated exported test hook — this stands in for "a fresh replica that never marked this
	// user done".
	ts.svc.personalOrgEnsured.Delete(fmt.Sprint(userID))

	triggerSessionResolution(t, ts)

	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after re-triggering with the cache cleared = %d, want 1", got)
	}
}

// TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne exercises the advisory lock directly: N
// goroutines all make a fresh org-less user's first authenticated request at the same time (none
// of them has this user in personalOrgEnsured yet), and exactly one organization must result.
func TestPersonalOrgConcurrentFirstRequestsCreateExactlyOne(t *testing.T) {
	ts := newTestService(t)
	email := "concurrent-first@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			resp, err := ts.client.Get(ts.url("/api/v1/auth/me"))
			if err != nil {
				errs <- fmt.Errorf("GET /me: %w", err)
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				errs <- fmt.Errorf("GET /me: status %d: %s", resp.StatusCode, body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent /me request failed: %v", err)
	}

	userID := lookupUserID(t, ts, email)
	user := &limen.User{ID: userID, Email: email}
	if got := countOrganizations(t, ts, user); got != 1 {
		t.Fatalf("organizations after %d concurrent first requests = %d, want 1", n, got)
	}
}

// TestRequireOrgMemberSucceedsForPersonalOrg is the happy-path RequireOrgMember test deferred from
// the previous plan's review: after signup and the first authenticated request creates the
// personal org, RequireOrgMember succeeds for that user against that org's id.
func TestRequireOrgMemberSucceedsForPersonalOrg(t *testing.T) {
	ts := newTestService(t)
	email := "member-check@example.com"

	requireStatus2xx(t, ts.postJSON(t, "/api/v1/auth/signup/credential", map[string]any{
		"email":    email,
		"password": signupPassword,
	}), "signup")
	triggerSessionResolution(t, ts)

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
